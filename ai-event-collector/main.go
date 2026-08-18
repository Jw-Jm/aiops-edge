package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// healthCache 缓存 ClickHouse 连通性探测结果，避免每次探针都打 CH（H4）。
type healthCache struct {
	mu        sync.Mutex
	lastCheck time.Time
	healthy   bool
	reason    string
}

// check 返回缓存（5s 内复用）或重新探测的健康状态。
func (h *healthCache) check(writer *EventWriter) (bool, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if time.Since(h.lastCheck) < 5*time.Second {
		return h.healthy, h.reason
	}
	healthy := true
	reason := ""
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	if err := writer.Ping(ctx); err != nil {
		healthy = false
		reason = fmt.Sprintf("clickhouse unreachable: %v", err)
	}
	cancel()
	if rq := writer.RetryQueueSize(); rq > 80 { // 重试队列上限 100 批，>80 视为高水位不健康
		healthy = false
		if reason != "" {
			reason += "; "
		}
		reason += fmt.Sprintf("retry queue depth %d exceeds threshold", rq)
	}
	h.lastCheck = time.Now()
	h.healthy = healthy
	h.reason = reason
	return healthy, reason
}

func main() {
	cfg := loadConfig()
	log.Printf("ai-event-collector starting (tenant=%s cluster=%s k8sWatch=%v selCollect=%v ch=%s:%d batch=%d flush=%ds)",
		cfg.TenantID, cfg.ClusterID, cfg.K8SWatchEnabled, cfg.SELCollectEnabled,
		cfg.CHHost, cfg.CHPort, cfg.BatchSize, cfg.FlushInterval)

	writer, err := NewEventWriter(cfg)
	if err != nil {
		log.Fatalf("clickhouse writer init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer writer.Close()

	if cfg.K8SWatchEnabled {
		kw, err := NewK8sWatcher(cfg, writer)
		if err != nil {
			log.Printf("K8S event watch disabled: %v", err)
		} else {
			go kw.Run(ctx)
		}
	} else {
		log.Printf("K8S event watch disabled (K8S_WATCH_ENABLED=false)")
	}

	if cfg.SELCollectEnabled {
		sc := NewSELCollector(cfg, writer)
		go sc.Run(ctx)
	} else {
		log.Printf("IPMI SEL collection disabled (SEL_COLLECT_ENABLED=false)")
	}

	// 健康端点 :8080/health（供探针）：真实反映 CH 连通性与重试队列水位（H4）。
	// 依赖异常时返回 503 + JSON body，触发重启/告警。
	hc := &healthCache{}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		healthy, reason := hc.check(writer)
		w.Header().Set("Content-Type", "application/json")
		if !healthy {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status": "unhealthy",
				"reason": reason,
			})
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
	})

	// Prometheus 文本格式指标端点（H6）：暴露 flushed 计数与重试/丢弃计数。
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		body := fmt.Sprintf(
			"# HELP ai_event_collector_events_flushed_total Total events successfully flushed to ClickHouse.\n"+
				"# TYPE ai_event_collector_events_flushed_total counter\n"+
				"ai_event_collector_events_flushed_total %d\n"+
				"# HELP ai_event_collector_retry_queue_batches Current retry queue depth (batches).\n"+
				"# TYPE ai_event_collector_retry_queue_batches gauge\n"+
				"ai_event_collector_retry_queue_batches %d\n"+
				"# HELP ai_event_collector_retry_dropped_batches_total Total batches dropped from retry queue.\n"+
				"# TYPE ai_event_collector_retry_dropped_batches_total counter\n"+
				"ai_event_collector_retry_dropped_batches_total %d\n",
			writer.FlushedTotal(), writer.RetryQueueSize(), writer.RetryDroppedTotal())
		_, _ = w.Write([]byte(body))
	})
	addr := fmt.Sprintf(":%d", cfg.HTTPPort)
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		log.Printf("health server listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("health server error: %v", err)
		}
	}()

	// 优雅退出：取消采集循环并冲刷缓冲
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("shutting down...")
	cancel()
	shutdownCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	_ = srv.Shutdown(shutdownCtx)
	log.Println("bye")
}
