package leaderelection

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// leaseStore 提供可控的 Lease 语义：holder + renewTime（手动控制过期）。
type leaseStore struct {
	mu          sync.Mutex
	holder      string
	renewTime   time.Time
	leaseSec    int32
	updateFail  bool // 模拟续租失败（如网络分区/权限）
	createCalls int
}

func (s *leaseStore) expire() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.renewTime = time.Now().Add(-time.Hour) // 已过期，可被抢占
}

func (s *leaseStore) server(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/apis/coordination.k8s.io/v1/namespaces/default/leases/"), "/")
		name := parts[len(parts)-1]
		switch r.Method {
		case http.MethodGet:
			if s.holder == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"metadata": map[string]any{"name": name, "resourceVersion": "100"},
				"spec": map[string]any{
					"holderIdentity":       s.holder,
					"leaseDurationSeconds": s.leaseSec,
					"renewTime":            s.renewTime.Format(time.RFC3339),
				},
			})
		case http.MethodPost:
			var l kubeLease
			_ = json.NewDecoder(r.Body).Decode(&l)
			s.holder = l.Spec.HolderIdentity
			s.renewTime = time.Now()
			s.createCalls++
			w.WriteHeader(http.StatusCreated)
		case http.MethodPut:
			if s.updateFail {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			var l kubeLease
			_ = json.NewDecoder(r.Body).Decode(&l)
			s.holder = l.Spec.HolderIdentity
			s.renewTime = time.Now()
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
}

func optsFor(id string) Options {
	return Options{
		Namespace:     "default",
		Name:          "aiops-events",
		Identity:      id,
		LeaseDuration: 15 * time.Second,
		RenewDeadline: 2 * time.Second,
		RetryPeriod:   50 * time.Millisecond,
	}
}

// TestElectorOnlyOneLeader 场景1+3：多实例竞争，仅一个 leader；leader 退出后 follower 接管。
func TestElectorOnlyOneLeaderThenHandoff(t *testing.T) {
	store := &leaseStore{holder: "", leaseSec: 15, renewTime: time.Now()}
	srv := store.server(t)
	defer srv.Close()

	var mu sync.Mutex
	leaders := map[string]bool{} // identity -> 是否当前 leader（回调时序）

	// 实例 A（先启动，应成为 leader）
	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	leA := NewElector(NewLeaseClient(srv.URL), optsFor("pod-a"))
	doneA := make(chan struct{})
	go func() {
		leA.Run(ctxA, func(leading bool) {
			mu.Lock()
			leaders["pod-a"] = leading
			mu.Unlock()
		})
		close(doneA)
	}()

	// 等 A 成为 leader
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return leaders["pod-a"]
	})

	// 实例 B（follower 加入，不应成为 leader）
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	leB := NewElector(NewLeaseClient(srv.URL), optsFor("pod-b"))
	go leB.Run(ctxB, func(leading bool) {
		mu.Lock()
		leaders["pod-b"] = leading
		mu.Unlock()
	})

	time.Sleep(300 * time.Millisecond) // 给 B 竞争机会

	mu.Lock()
	if leaders["pod-a"] != true || leaders["pod-b"] == true {
		mu.Unlock()
		t.Fatalf("expected exactly one leader (a), got a=%v b=%v", leaders["pod-a"], leaders["pod-b"])
	}
	mu.Unlock()

	// A 退出 → B 应接管
	cancelA()
	<-doneA
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return leaders["pod-b"]
	})
}

type existingEmptyLeaseClient struct {
	resourceVersion string
	holder          string
	creates         int
	updates         int
}

func (c *existingEmptyLeaseClient) Get(context.Context, string, string) (LeaseInfo, error) {
	return LeaseInfo{
		Holder:          c.holder,
		AcquireTime:     "2026-08-20T09:00:00Z",
		RenewTime:       time.Now(),
		ResourceVersion: c.resourceVersion,
	}, nil
}

func (c *existingEmptyLeaseClient) Create(context.Context, string, string, string) error {
	c.creates++
	return nil
}

func (c *existingEmptyLeaseClient) Update(context.Context, string, string, string) error {
	c.updates++
	c.holder = "pod-a"
	return nil
}

func (c *existingEmptyLeaseClient) Release(context.Context, string, string) error { return nil }

func TestElectorUpdatesExistingEmptyLease(t *testing.T) {
	client := &existingEmptyLeaseClient{resourceVersion: "100"}
	e := NewElector(client, optsFor("pod-a"))

	if !e.tryAcquire(context.Background()) {
		t.Fatal("expected elector to acquire an existing empty lease")
	}
	if client.creates != 0 {
		t.Fatalf("must not POST an existing lease, create calls=%d", client.creates)
	}
	if client.updates != 1 {
		t.Fatalf("expected one update of the existing lease, update calls=%d", client.updates)
	}
}

// TestElectorRenewFailStopsLeadership 场景5：leader 续租失败 → 立即停止 watch（fail-safe）。
func TestElectorRenewFailStopsLeadership(t *testing.T) {
	store := &leaseStore{holder: "", leaseSec: 15, renewTime: time.Now()}
	srv := store.server(t)
	defer srv.Close()

	var mu sync.Mutex
	leading := false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	le := NewElector(NewLeaseClient(srv.URL), optsFor("pod-a"))
	go le.Run(ctx, func(l bool) {
		mu.Lock()
		leading = l
		mu.Unlock()
	})

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return leading
	})

	// 触发续租失败：后续 Update 返回 500。
	store.mu.Lock()
	store.updateFail = true
	store.mu.Unlock()

	// fail-safe：renew 失败后必须停止 leadership（回调 false），不等 lease 过期。
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return !leading
	})
}

// TestElectorReacquireAfterLeaseExpired 场景4补充：lease 过期后 follower 可抢占。
func TestElectorReacquireAfterLeaseExpired(t *testing.T) {
	store := &leaseStore{holder: "pod-old", leaseSec: 15, renewTime: time.Now()}
	store.expire() // 旧 holder 的 lease 已过期
	srv := store.server(t)
	defer srv.Close()

	var mu sync.Mutex
	leading := false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	le := NewElector(NewLeaseClient(srv.URL), optsFor("pod-new"))
	go le.Run(ctx, func(l bool) {
		mu.Lock()
		leading = l
		mu.Unlock()
	})

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return leading
	})

	mu.Lock()
	got := leading
	mu.Unlock()
	if !got {
		t.Fatal("expected new instance to acquire expired lease")
	}
}

// TestElectorCallbackStrictlyAlternates 场景6：onLeadership 回调必须严格交替
// (true,false,true,false,...)，不得出现连续两个 true——保证任何时刻至多一个
// cluster-wide watch 活跃（无重叠 writer 区间）。
func TestElectorCallbackStrictlyAlternates(t *testing.T) {
	store := &leaseStore{holder: "", leaseSec: 15, renewTime: time.Now()}
	srv := store.server(t)
	defer srv.Close()

	var mu sync.Mutex
	var seq []bool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	le := NewElector(NewLeaseClient(srv.URL), optsFor("pod-a"))
	go le.Run(ctx, func(l bool) {
		mu.Lock()
		seq = append(seq, l)
		mu.Unlock()
	})

	// 触发多次 acquire→renew-fail→reacquire 循环，产生多段 leadership。
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seq) >= 1
	})
	store.mu.Lock()
	store.updateFail = true
	store.mu.Unlock()
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seq) >= 2
	})
	// 恢复续租，允许重新 acquire。
	store.mu.Lock()
	store.updateFail = false
	store.mu.Unlock()
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seq) >= 3
	})

	mu.Lock()
	defer mu.Unlock()
	for i, v := range seq {
		if i > 0 && seq[i-1] == v {
			t.Fatalf("callback not strictly alternating at %d: %v", i, seq)
		}
	}
}

// waitFor 轮询直到 cond 为 true 或超时。
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
