package main

import (
	"os"
	"strconv"
	"strings"
)

// Config 集中管理组件全部环境变量配置。
type Config struct {
	TenantID  string
	ClusterID string

	// Unified Ingest endpoint. The collector never owns a ClickHouse
	// credential or writes the platform tables directly.
	IngestURL    string
	IngestAPIKey string

	// K8s 事件采集
	K8SWatchEnabled bool

	// Leader election（V9.2 §71 single leader）：DaemonSet 多副本时，仅 Lease holder 启动
	// 集群级 K8s watch，其余副本只做 SEL（天然按节点隔离）。默认启用。
	LeaderElectionEnabled bool
	LeaseName             string
	LeaseNamespace        string

	// IPMI SEL 采集
	SELCollectEnabled bool
	SELNodes          []string
	SELLocalOnly      bool
	SELInterval       int // seconds
	IPMIUser          string
	IPMIPass          string
	IPMICmd           string

	// 批量写入
	BatchSize     int
	FlushInterval int // seconds

	// WAL 持久化（Phase 5）：写入 CH 前先落盘，崩溃/重启后从磁盘恢复未确认批次。
	// 为空时退化为内存重试队列（向后兼容）。
	WALDir string

	HTTPPort int
}

// loadConfig 从环境变量加载配置（带默认值，详见 README）。
func loadConfig() *Config {
	return &Config{
		// Phase 5：tenant/cluster 不再默认 "default"。缺省即空，由 main.go 在启动时
		// 用 EventScope.Validate() fail-closed（必须 canonical UUID，禁止 default/slug/数值）。
		TenantID:              os.Getenv("TENANT_ID"),
		ClusterID:             os.Getenv("CLUSTER_ID"),
		IngestURL:             getenv("INGEST_URL", "http://ingest:8080"),
		IngestAPIKey:          os.Getenv("INGEST_API_KEY"),
		K8SWatchEnabled:       getenvBool("K8S_WATCH_ENABLED", true),
		LeaderElectionEnabled: getenvBool("LEADER_ELECTION_ENABLED", true),
		LeaseName:             getenv("LEASE_NAME", "aiops-event-collector-leader"),
		LeaseNamespace:        getenv("LEASE_NAMESPACE", os.Getenv("POD_NAMESPACE")),
		SELCollectEnabled:     getenvBool("SEL_COLLECT_ENABLED", false),
		SELNodes:              splitCSV(os.Getenv("SEL_NODES")),
		SELLocalOnly:          getenvBool("SEL_LOCAL_ONLY", true),
		SELInterval:           getenvInt("SEL_INTERVAL_SECONDS", 120),
		IPMIUser:              os.Getenv("IPMI_USER"),
		IPMIPass:              os.Getenv("IPMI_PASS"),
		IPMICmd:               getenv("IPMI_CMD", "ipmitool"),
		BatchSize:             getenvInt("BATCH_SIZE", 500),
		FlushInterval:         getenvInt("FLUSH_INTERVAL_SECONDS", 5),
		WALDir:                os.Getenv("WAL_DIR"),
		HTTPPort:              getenvInt("HTTP_PORT", 8080),
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func getenvBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
