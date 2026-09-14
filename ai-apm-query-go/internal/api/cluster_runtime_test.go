package api

import "testing"

const podsFixture = `{
  "items": [
    {"metadata":{"name":"p1","namespace":"observability"},"status":{"phase":"Running","containerStatuses":[{"name":"c","ready":true,"restartCount":0}]}},
    {"metadata":{"name":"p2","namespace":"observability"},"status":{"phase":"Running","containerStatuses":[{"name":"c","ready":false,"restartCount":7},{"name":"sidecar","ready":true,"restartCount":3}]}},
    {"metadata":{"name":"p3","namespace":"default"},"status":{"phase":"Pending","containerStatuses":[]}},
    {"metadata":{"name":"p4","namespace":"default"},"status":{"phase":"Failed","containerStatuses":[{"name":"c","ready":false,"restartCount":0}]}},
    {"metadata":{"name":"p5","namespace":"kube-system"},"status":{"phase":"Succeeded","containerStatuses":[{"name":"c","ready":false,"restartCount":1}]}}
  ]
}`

func TestParsePodRuntimeSeparatesPhaseReadinessAndRestarts(t *testing.T) {
	pods, restarts, ok := parsePodRuntime([]byte(podsFixture))
	if !ok {
		t.Fatal("fixture must parse")
	}
	if pods.Total != 5 {
		t.Fatalf("total pods must be 5, got %d", pods.Total)
	}
	// 阶段分布必须分别表达，不能合并成"异常"
	if pods.Running != 2 || pods.Pending != 1 || pods.Failed != 1 || pods.Succeeded != 1 {
		t.Fatalf("phase distribution wrong: %+v", pods)
	}
	// 只有 Running 且带容器状态的 Pod 进入就绪分母：
	// p3 Pending 无容器状态（尚未调度 ≠ 不就绪），p4 Failed / p5 Succeeded 为终态。
	if pods.Ready != 1 || pods.NotReady != 1 {
		t.Fatalf("readiness must count only running pods: %+v", pods)
	}
	// 终态 Pod 的历史 restartCount 不是当前事实，不得计入。
	if restarts.TotalPodsWithRestarts != 1 {
		t.Fatalf("expected only p2 to count restarts, got %d", restarts.TotalPodsWithRestarts)
	}
	// 单 Pod 多容器重启次数必须累加（7+3=10）
	if restarts.MaxPerPod != 10 {
		t.Fatalf("max restarts per pod must sum containers, got %d", restarts.MaxPerPod)
	}
	if restarts.TotalRestarts != 10 {
		t.Fatalf("total restarts must sum non-terminal containers, got %d", restarts.TotalRestarts)
	}
	if len(restarts.Top) == 0 || restarts.Top[0].Name != "p2" || restarts.Top[0].Restarts != 10 {
		t.Fatalf("top restarts must be ordered descending: %+v", restarts.Top)
	}
	if restarts.Source == "" {
		t.Fatal("restart fact must declare its source for reconciliation")
	}
}

func TestParsePodRuntimeRejectsMalformedInput(t *testing.T) {
	if _, _, ok := parsePodRuntime([]byte("not json")); ok {
		t.Fatal("malformed payload must not produce facts")
	}
}

func TestParsePodRuntimeEmptyClusterDoesNotFabricateHealth(t *testing.T) {
	pods, restarts, ok := parsePodRuntime([]byte(`{"items":[]}`))
	if !ok {
		t.Fatal("empty list is still a valid reading")
	}
	if pods.Total != 0 || pods.Ready != 0 || pods.NotReady != 0 {
		t.Fatalf("empty cluster must stay zero counts, not health: %+v", pods)
	}
	if restarts.TotalRestarts != 0 || len(restarts.Top) != 0 {
		t.Fatalf("no pods means no restart facts: %+v", restarts)
	}
}

func TestSortRestartTopIsDeterministic(t *testing.T) {
	entries := []restartTopEntry{
		{Namespace: "b", Name: "x", Restarts: 2},
		{Namespace: "a", Name: "y", Restarts: 2},
		{Namespace: "a", Name: "a", Restarts: 5},
	}
	sortRestartTop(entries)
	want := []string{"a/a", "a/y", "b/x"}
	for i, w := range want {
		got := entries[i].Namespace + "/" + entries[i].Name
		if got != w {
			t.Fatalf("position %d: got %s want %s (full %+v)", i, got, w, entries)
		}
	}
}
