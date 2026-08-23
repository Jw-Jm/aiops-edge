package main

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	"ai-event-collector/internal/leaderelection"
)

// runWatchWithLeaderElection 使集群级 K8s watch 只在 Lease holder（leader）上运行。
// 实现 V9.2 §71 "single leader / single deployment"：DaemonSet 多副本下仅 leader 执行
// cluster-wide watch，follower 只做 SEL（天然按节点隔离）。
//
// fail-safe：每个 leadership 使用独立的可取消 context；Lease 丢失（renew 失败/超时）
// 时 onLeadership(false) 立即取消 watch context 停止 watch/write，绝不"WARN 后继续当
// leader 写"——否则网络分区时仍可能形成双 writer。
func runWatchWithLeaderElection(cfg *Config, kw *k8sWatcher, parent context.Context) {
	namespace := cfg.LeaseNamespace
	if namespace == "" {
		namespace = "default"
	}
	identity := podName()
	client := kw.leaseClient() // 复用 in-cluster K8s REST 基础（token/CA/host）
	le := leaderelection.NewElector(client, leaderelection.Options{
		Namespace:     namespace,
		Name:          cfg.LeaseName,
		Identity:      identity,
		LeaseDuration: 15 * time.Second,
		RenewDeadline: 5 * time.Second,
		RetryPeriod:   2 * time.Second,
	})

	log.Printf("leader election: running (identity=%s lease=%s/%s)", identity, namespace, cfg.LeaseName)
	var (
		watchMu     sync.Mutex
		watchCancel context.CancelFunc
	)
	le.Run(parent, func(leading bool) {
		if leading {
			watchMu.Lock()
			if watchCancel != nil { // 防重入：旧 leadership 未清理则先取消
				watchCancel()
			}
			wctx, cancel := context.WithCancel(parent)
			watchCancel = cancel
			watchMu.Unlock()
			log.Printf("leader election: acquired leadership, starting cluster-wide K8s watch")
			go kw.Run(wctx)
		} else {
			watchMu.Lock()
			if watchCancel != nil {
				watchCancel()
				watchCancel = nil
			}
			watchMu.Unlock()
			log.Printf("leader election: leadership lost, K8s watch stopped (follower)")
		}
	})
}

// podName 返回当前 Pod 名（holderIdentity 建议用 Pod 名，便于 kubectl get lease 观测）。
// 非 in-cluster（无 HOSTNAME）时回退为进程名。
func podName() string {
	if h := os.Getenv("HOSTNAME"); h != "" {
		return h
	}
	if n := os.Getenv("POD_NAME"); n != "" {
		return n
	}
	host, _ := os.Hostname()
	return host
}
