package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// selCollector IPMI SEL 采集器：周期性执行 ipmitool（或回退 ipmi-sel）解析 SEL 事件并写 CH。
// H1 去重：内存维护最近 maxRecentSELIDs 个 (node, record-id)，同一记录只写一次，
// 避免每周期 `sel list last 20` 全量重插造成重复写。
type selCollector struct {
	cfg      *Config
	writer   *EventWriter
	hostname string

	dedupMu   sync.Mutex
	dedup     map[string]struct{} // 最近 (node/record-id) 集合
	dedupRing []string            // FIFO 顺序，用于淘汰最旧记录
}

// maxRecentSELIDs 内存去重集合上限（SEL record id 每 BMC 独立编号，按 node 前缀区分）。
const maxRecentSELIDs = 500

func NewSELCollector(cfg *Config, writer *EventWriter) *selCollector {
	hn, err := os.Hostname()
	if err != nil || hn == "" {
		hn = "localhost"
	}
	return &selCollector{
		cfg:       cfg,
		writer:    writer,
		hostname:  hn,
		dedup:     make(map[string]struct{}),
		dedupRing: make([]string, 0, maxRecentSELIDs),
	}
}

// seen 记录 (node, record-id) 并返回是否已见过（重复则跳过写入）。
// 集合上限 maxRecentSELIDs，FIFO 淘汰最旧，避免内存无界增长。
func (s *selCollector) seen(key string) bool {
	if key == "" {
		return false
	}
	s.dedupMu.Lock()
	defer s.dedupMu.Unlock()
	if _, ok := s.dedup[key]; ok {
		return true
	}
	s.dedup[key] = struct{}{}
	s.dedupRing = append(s.dedupRing, key)
	if len(s.dedupRing) > maxRecentSELIDs {
		old := s.dedupRing[0]
		s.dedupRing = s.dedupRing[1:]
		delete(s.dedup, old)
	}
	return false
}

// Run 周期循环采集（间隔 SEL_INTERVAL_SECONDS），单次失败仅记日志不退出。
func (s *selCollector) Run(ctx context.Context) {
	interval := time.Duration(s.cfg.SELInterval) * time.Second
	log.Printf("SEL collector: started (interval=%s, localOnly=%v, nodes=%v, cmd=%s)",
		interval, s.cfg.SELLocalOnly, s.cfg.SELNodes, s.cfg.IPMICmd)
	for {
		s.collectOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// collectOnce 采集一次 SEL。
// SEL_LOCAL_ONLY=true（默认）时用本机 hostname 作为 node 采集本机 BMC；
// 否则对 SEL_NODES 列表逐台经 IPMI LAN 采集。
func (s *selCollector) collectOnce(ctx context.Context) {
	nodes := s.targetNodes()
	for _, node := range nodes {
		if ctx.Err() != nil {
			return
		}
		entries := s.readSEL(node)
		kept := 0
		skippedDup := 0
		for _, en := range entries {
			// H1：SEL record id 每 BMC 独立编号，按 (node, record-id) 去重，同一记录只写一次
			if s.seen(node + "/" + en.Name) {
				skippedDup++
				continue
			}
			s.writer.Add(en)
			kept++
		}
		if len(entries) > 0 {
			log.Printf("SEL: node=%s collected %d entries (dedup-skipped %d)", node, kept, skippedDup)
		}
	}
}

func (s *selCollector) targetNodes() []string {
	if s.cfg.SELLocalOnly {
		return []string{s.hostname}
	}
	nodes := s.cfg.SELNodes
	if len(nodes) == 0 {
		nodes = []string{s.hostname}
	}
	return nodes
}

// readSEL 对指定 node 执行 SEL 读取命令并解析为标准 Event 结构。
func (s *selCollector) readSEL(node string) []*Event {
	output, cmdName, err := s.execSEL(node)
	if err != nil {
		log.Printf("SEL: node=%s %s failed: %v", node, cmdName, err)
		return nil
	}
	lines := splitLines(output)
	if cmdName == "ipmi-sel" {
		return parseIPMISEL(lines, node)
	}
	return parseIPMITool(lines, node)
}

// execSEL 优先使用配置的 ipmitool；二进制不存在时回退 ipmi-sel。返回输出与所用命令名。
func (s *selCollector) execSEL(node string) (string, string, error) {
	if s.cfg.IPMICmd != "ipmi-sel" && commandExists(s.cfg.IPMICmd) {
		return s.execIPMITool(node)
	}
	return s.execIPMISEL(node)
}

// execIPMITool 执行 `ipmitool sel list last 20`。本地 node 走本机 BMC；
// 远程 node 经 -I lanplus -H/-U/-P 访问 IPMI LAN。
func (s *selCollector) execIPMITool(node string) (string, string, error) {
	var args []string
	if !s.isLocal(node) {
		args = append(args, "-I", "lanplus", "-H", node)
		if s.cfg.IPMIUser != "" {
			args = append(args, "-U", s.cfg.IPMIUser)
		}
		if s.cfg.IPMIPass != "" {
			args = append(args, "-P", s.cfg.IPMIPass)
		}
	}
	args = append(args, "sel", "list", "last", "20")
	out, err := exec.Command(s.cfg.IPMICmd, args...).Output()
	if err != nil {
		return "", s.cfg.IPMICmd, wrapExecError(err)
	}
	return string(out), s.cfg.IPMICmd, nil
}

// execIPMISEL 回退到 freeipmi 的 ipmi-sel（--output-event-record）。
func (s *selCollector) execIPMISEL(node string) (string, string, error) {
	args := []string{"--output-event-record"}
	if !s.isLocal(node) {
		args = append(args, "-h", node)
		if s.cfg.IPMIUser != "" {
			args = append(args, "-u", s.cfg.IPMIUser)
		}
		if s.cfg.IPMIPass != "" {
			args = append(args, "-p", s.cfg.IPMIPass)
		}
	}
	out, err := exec.Command("ipmi-sel", args...).Output()
	if err != nil {
		return "", "ipmi-sel", wrapExecError(err)
	}
	return string(out), "ipmi-sel", nil
}

func (s *selCollector) isLocal(node string) bool {
	return s.cfg.SELLocalOnly || node == s.hostname
}

func wrapExecError(err error) error {
	if ee, ok := err.(*exec.ExitError); ok {
		return fmt.Errorf("exit status %d: %s", ee.ExitCode(), strings.TrimSpace(string(ee.Stderr)))
	}
	return err
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func splitLines(s string) []string {
	var lines []string
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}

// parseIPMITool 解析 `ipmitool sel list` 输出：
//
//	<id> | MM/DD/YYYY | HH:MM:SS | <sensor> | <event> | <message>
func parseIPMITool(lines []string, node string) []*Event {
	var events []*Event
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 4 {
			continue
		}
		id := strings.TrimSpace(parts[0])
		date := strings.TrimSpace(parts[1])
		tm := strings.TrimSpace(parts[2])
		rest := make([]string, 0, len(parts)-3)
		for _, p := range parts[3:] {
			p = strings.TrimSpace(p)
			if p != "" {
				rest = append(rest, p)
			}
		}
		if len(rest) == 0 {
			continue
		}
		ts, err := time.ParseInLocation("01/02/2006 15:04:05", date+" "+tm, time.Local)
		if err != nil {
			continue // 无法解析的行（表头/空行等）跳过
		}
		sensor := rest[0]
		eventDesc := ""
		if len(rest) >= 2 {
			eventDesc = rest[1]
		}
		msg := strings.Join(rest, " | ")
		events = append(events, &Event{
			Ts:              ts,
			Kind:            "SEL",
			Name:            "sel-" + id,
			Reason:          eventDesc,
			Type:            "Error",
			Message:         msg,
			InvolvedObject:  sensor,
			SourceComponent: "ipmi-sel",
			Source:          "ipmi-sel",
			Node:            node,
		})
	}
	return events
}

// parseIPMISEL 解析 freeipmi `ipmi-sel --output-event-record` 输出：
//
//	<id> | <date> | <time> | <name> | <type> | <event>
//
// 日期兼容 MM/DD/YYYY、YYYY-MM-DD、Mon-DD-YYYY 等格式。
func parseIPMISEL(lines []string, node string) []*Event {
	dateLayouts := []string{"01/02/2006", "2006-01-02", "Jan-02-2006", "January-02-2006"}
	var events []*Event
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 4 {
			continue
		}
		id := strings.TrimSpace(parts[0])
		date := strings.TrimSpace(parts[1])
		tm := strings.TrimSpace(parts[2])
		rest := make([]string, 0, len(parts)-3)
		for _, p := range parts[3:] {
			p = strings.TrimSpace(p)
			if p != "" {
				rest = append(rest, p)
			}
		}
		if len(rest) == 0 {
			continue
		}
		var ts time.Time
		for _, layout := range dateLayouts {
			if t, err := time.ParseInLocation(layout+" 15:04:05", date+" "+tm, time.Local); err == nil {
				ts = t
				break
			}
		}
		if ts.IsZero() {
			continue
		}
		events = append(events, &Event{
			Ts:              ts,
			Kind:            "SEL",
			Name:            "sel-" + id,
			Reason:          strings.Join(rest, " | "),
			Type:            "Error",
			Message:         strings.Join(rest, " | "),
			InvolvedObject:  rest[0],
			SourceComponent: "ipmi-sel",
			Source:          "ipmi-sel",
			Node:            node,
		})
	}
	return events
}
