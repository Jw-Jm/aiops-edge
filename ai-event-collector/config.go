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

	// ClickHouse 连接
	CHHost     string
	CHPort     int
	CHUser     string
	CHPassword string

	// K8s 事件采集
	K8SWatchEnabled bool

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

	HTTPPort int
}

// loadConfig 从环境变量加载配置（带默认值，详见 README）。
func loadConfig() *Config {
	return &Config{
		TenantID:          getenv("TENANT_ID", "default"),
		ClusterID:         getenv("CLUSTER_ID", "default"),
		CHHost:            getenv("CLICKHOUSE_HOST", "clickhouse.observability.svc.cluster.local"),
		CHPort:            getenvInt("CLICKHOUSE_PORT", 8123),
		CHUser:            os.Getenv("CLICKHOUSE_USER"),
		CHPassword:        os.Getenv("CLICKHOUSE_PASSWORD"),
		K8SWatchEnabled:   getenvBool("K8S_WATCH_ENABLED", true),
		SELCollectEnabled: getenvBool("SEL_COLLECT_ENABLED", false),
		SELNodes:          splitCSV(os.Getenv("SEL_NODES")),
		SELLocalOnly:      getenvBool("SEL_LOCAL_ONLY", true),
		SELInterval:       getenvInt("SEL_INTERVAL_SECONDS", 120),
		IPMIUser:          os.Getenv("IPMI_USER"),
		IPMIPass:          os.Getenv("IPMI_PASS"),
		IPMICmd:           getenv("IPMI_CMD", "ipmitool"),
		BatchSize:         getenvInt("BATCH_SIZE", 500),
		FlushInterval:     getenvInt("FLUSH_INTERVAL_SECONDS", 5),
		HTTPPort:          getenvInt("HTTP_PORT", 8080),
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
