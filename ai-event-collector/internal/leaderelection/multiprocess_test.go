package leaderelection

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// P6.4.1 Multi-process Lease Competition E2E
//
// 用独立 OS 进程（非 goroutine）竞争同一 Lease，证明：
//   - 恰好一个 leader（其余 follower）
//   - leader 被杀 → 立即停止 leader write；follower 接管（cluster-wide write）
//   - 任意时刻至多一个 active cluster-wide writer（无重叠 writer 区间）
//   - 不声称 WAL 自动迁移（Lease 只解决 leader ownership，不解决 WAL handoff）
//
// 实现：Go test binary re-exec 模式。父进程启动共享 leaseStore HTTP server，派生两个
// 子进程（身份 pod-a / pod-b），各自运行 Elector，把 leadership 时间线输出到 stdout。
// 父进程解析两个子进程的 LEADER/FOLLOWER 时间线，断言无重叠、且 kill 后 follower 接管。
// ─────────────────────────────────────────────────────────────────────────────

// TestMultiprocessLeaderCompetitionE2E 父进程编排：双进程竞争 + kill/接管 + 无重叠 writer。
func TestMultiprocessLeaderCompetitionE2E(t *testing.T) {
	store := &leaseStore{holder: "", leaseSec: 2, renewTime: time.Now()}
	srv := store.server(t)
	defer srv.Close()

	// 启动进程 A。
	procA := spawnCollectorProcess(t, srv.URL, "pod-a")
	// 等 A 成为 leader。
	timelineA := waitLeader(t, procA)
	if !timelineA[0].leader {
		t.Fatal("process A did not become leader")
	}

	// 启动进程 B（follower）。
	procB := spawnCollectorProcess(t, srv.URL, "pod-b")
	time.Sleep(600 * time.Millisecond) // 给 B 竞争机会

	// 杀 leader A → B 应接管。
	killA := time.Now()
	if err := procA.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill A: %v", err)
	}
	waitFor(t, func() bool {
		tl := drainTimeline(procB)
		return len(tl) > 0 && tl[len(tl)-1].leader
	})

	// 汇总两个进程的完整时间线（B 的已 drain，A 的取已读到部分）。
	tlB := drainTimeline(procB)
	tlA := procA.readTimeline()

	// 断言：两个进程的 LEADER 区间无重叠（任意时刻至多一个 active writer）。
	// A 是被 SIGKILL 的，不会发出 FOLLOWER 事件；以其死亡时刻 killA 作为其
	// leadership 的终止点（进程已死，立即停止 leader write）。
	assertNoOverlap(t, tlA, tlB, killA)

	procA.cmd.Process.Kill()
	procB.cmd.Process.Kill()
	procA.cmd.Wait()
	procB.cmd.Wait()
}

// timelinePoint 一条 leadership 时间线记录。
type timelinePoint struct {
	t      time.Time
	leader bool
}

// collectorProcess 包装一个子进程及其 stdout 时间线。
type collectorProcess struct {
	cmd      *exec.Cmd
	stdout   *bufio.Reader
	mu       sync.Mutex
	timeline []timelinePoint
}

// TestCollectorLeaderChildProcess 是子进程入口（re-exec），通过 -test.run 单独运行。
func TestCollectorLeaderChildProcess(t *testing.T) {
	url := os.Getenv("LEADER_LEASE_URL")
	id := os.Getenv("LEADER_IDENTITY")
	if url == "" || id == "" {
		t.Skip("not a leader-elec child")
	}
	le := NewElector(NewLeaseClient(url), Options{
		Namespace: "default", Name: "aiops-events", Identity: id,
		LeaseDuration: 2 * time.Second, RenewDeadline: 1 * time.Second, RetryPeriod: 80 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	le.Run(ctx, func(leading bool) {
		// 每个 leadership 变化立即输出，父进程据此判断"active cluster-wide writer"。
		fmt.Printf("t=%d;id=%s;phase=%s\n", time.Now().UnixMilli(), id, leaderPhase(leading))
	})
}

func leaderPhase(l bool) string {
	if l {
		return "LEADER"
	}
	return "FOLLOWER"
}

// spawnCollectorProcess 派生一个独立 OS 进程运行 Elector（re-exec 本测试二进制）。
func spawnCollectorProcess(t *testing.T, url, id string) *collectorProcess {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestCollectorLeaderChildProcess")
	cmd.Env = append(os.Environ(),
		"LEADER_LEASE_URL="+url,
		"LEADER_IDENTITY="+id,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child %s: %v", id, err)
	}
	p := &collectorProcess{cmd: cmd, stdout: bufio.NewReader(stdout)}
	p.startReader()
	return p
}

// startReader 启动后台 goroutine 持续读取子进程 stdout 时间线（非阻塞主测试）。
func (c *collectorProcess) startReader() {
	go func() {
		for {
			line, err := c.stdout.ReadString('\n')
			if err != nil {
				return // EOF / 进程结束
			}
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "t=") {
				c.mu.Lock()
				c.timeline = append(c.timeline, parsePoint(line))
				c.mu.Unlock()
			}
		}
	}()
}

// readTimeline 返回当前已读取的时间线副本（非阻塞）。
func (c *collectorProcess) readTimeline() []timelinePoint {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]timelinePoint, len(c.timeline))
	copy(out, c.timeline)
	return out
}

func drainTimeline(p *collectorProcess) []timelinePoint {
	deadline := time.Now().Add(2 * time.Second)
	var tl []timelinePoint
	for time.Now().Before(deadline) {
		tl = p.readTimeline()
		if len(tl) > 0 {
			return tl
		}
		time.Sleep(20 * time.Millisecond)
	}
	return tl
}

func parsePoint(line string) timelinePoint {
	// t=<ms>;id=<id>;phase=LEADER|FOLLOWER
	ms := int64(0)
	leader := false
	for _, kv := range strings.Split(line, ";") {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}
		switch parts[0] {
		case "t":
			ms, _ = strconv.ParseInt(parts[1], 10, 64)
		case "phase":
			leader = parts[1] == "LEADER"
		}
	}
	return timelinePoint{t: time.UnixMilli(ms), leader: leader}
}

// waitLeader 等待某个子进程首次输出 LEADER。
func waitLeader(t *testing.T, p *collectorProcess) []timelinePoint {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		tl := p.readTimeline()
		if len(tl) > 0 && tl[0].leader {
			return tl
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("child never became leader")
	return nil
}

// ev 一条 leadership 状态变化事件（用于合并两个进程时间线做无重叠断言）。
type ev struct {
	ts    int64
	delta int // +1 leader / -1 follower
	id    string
}

// assertNoOverlap 断言两个进程的 LEADER 区间无重叠：把两个时间线合并排序，
// 任意时刻 active-writer 数量 ≤ 1。
//
// killA 是被 SIGKILL 的进程 A 的死亡时刻：A 不会发出 FOLLOWER 事件（进程已死），
// 以其死亡时刻作为其 leadership 终止点（进程消失即停止 leader write），否则会误报
// A 永远处于 LEADER 而 B 接管时产生伪重叠。
func assertNoOverlap(t *testing.T, tlA, tlB []timelinePoint, killA time.Time) {
	t.Helper()
	var evs []ev
	for _, p := range tlA {
		d := -1
		if p.leader {
			d = 1
		}
		evs = append(evs, ev{p.t.UnixMilli(), d, "A"})
	}
	// A 被 kill：若 A 最后仍处于 LEADER，追加一个 killA 时刻的 FOLLOWER 事件闭合其区间。
	if len(tlA) > 0 && tlA[len(tlA)-1].leader {
		evs = append(evs, ev{killA.UnixMilli(), -1, "A"})
	}
	for _, p := range tlB {
		d := -1
		if p.leader {
			d = 1
		}
		evs = append(evs, ev{p.t.UnixMilli(), d, "B"})
	}
	sortEvents(evs)
	active := 0
	for _, e := range evs {
		active += e.delta
		if active > 1 {
			t.Fatalf("overlap: %d active writers at t=%d (event %s %d)", active, e.ts, e.id, e.delta)
		}
	}
}

func sortEvents(evs []ev) {
	for i := 1; i < len(evs); i++ {
		for j := i; j > 0 && evs[j].ts < evs[j-1].ts; j-- {
			evs[j], evs[j-1] = evs[j-1], evs[j]
		}
	}
}
