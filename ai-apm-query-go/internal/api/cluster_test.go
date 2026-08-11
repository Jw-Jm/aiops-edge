package api

import (
	"net/http"
	"testing"
)

// TestExtractClusterClause 验证多集群过滤 SQL 片段生成逻辑。
// 生产语义：cluster_id 为空或 "all" → 不过滤（查询所有集群）；
// 其他值 → 返回 " AND cluster_id='xxx'"（仅查询该集群）。
func TestExtractClusterClause(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"all", "cluster_id=all", ""},
		{"empty", "", ""},
		{"specific", "cluster_id=prod", " AND cluster_id='prod'"},
		{"id999", "cluster_id=999", " AND cluster_id='999'"},
		{"default", "cluster_id=default", " AND cluster_id='default'"},
		{"inject", "cluster_id=x' OR '1'='1", " AND cluster_id='x'' OR ''1''=''1'"},
	}
	for _, c := range cases {
		r, _ := http.NewRequest("GET", "/api/v1/services?"+c.query, nil)
		got := extractClusterClause(r)
		if got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// TestExtractClusterID 验证原始 cluster_id 值透传。
func TestExtractClusterID(t *testing.T) {
	if got := extractClusterID(mustReq("cluster_id=all")); got != "all" {
		t.Errorf("all: got %q want all", got)
	}
	if got := extractClusterID(mustReq("")); got != "all" {
		t.Errorf("empty: got %q want all", got)
	}
	if got := extractClusterID(mustReq("cluster_id=prod")); got != "prod" {
		t.Errorf("prod: got %q want prod", got)
	}
}

func mustReq(query string) *http.Request {
	r, _ := http.NewRequest("GET", "/api/v1/services?"+query, nil)
	return r
}
