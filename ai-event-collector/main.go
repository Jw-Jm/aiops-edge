package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

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

	// 健康端点 :8080/health（供探针）
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
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
