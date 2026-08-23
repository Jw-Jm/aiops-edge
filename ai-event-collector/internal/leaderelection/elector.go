package leaderelection

import (
	"context"
	"log"
	"time"
)

// Options 配置 Elector 的竞争参数。
type Options struct {
	Namespace     string        // Lease 命名空间
	Name          string        // Lease 名称
	Identity      string        // 本实例 holderIdentity（建议 Pod 名）
	LeaseDuration time.Duration // 租约时长；renewTime + duration 过期后可被抢占
	RenewDeadline time.Duration // renew 超时阈值（超过即视为丢失 leadership）
	RetryPeriod   time.Duration // acquire / renew 重试间隔
}

// Elector 基于 coordination.k8s.io/v1 Lease 实现 leader election。
//
// 状态机（fail-safe）：
//
//	FOLLOWER ──acquire──▶ LEADER ──renew 成功──▶ LEADER
//	    ▲                    │ renew 失败 / 超时 / ctx 取消
//	    └────────────────────┘
//	        onLeadership(false) 必须先于重新竞争
//
// 关键语义：一旦 renew 失败（网络/权限/超时），立即调用 onLeadership(false) 停止
// cluster-wide watch/write，绝不"WARN 后继续当 leader 写"——否则分区时仍可能双 writer。
type Elector struct {
	client LeaseClient
	opts   Options
}

// NewElector 构造 Elector。
func NewElector(client LeaseClient, opts Options) *Elector {
	if opts.LeaseDuration <= 0 {
		opts.LeaseDuration = 15 * time.Second
	}
	if opts.RenewDeadline <= 0 {
		opts.RenewDeadline = 2 * time.Second
	}
	if opts.RetryPeriod <= 0 {
		opts.RetryPeriod = 1 * time.Second
	}
	return &Elector{client: client, opts: opts}
}

// Run 阻塞运行 leader election。onLeadership(true) 表示获得 leadership（应启动
// cluster-wide watch/write）；onLeadership(false) 表示丢失（必须先停止 watch/write）。
// ctx 取消时返回。
func (e *Elector) Run(ctx context.Context, onLeadership func(bool)) {
	for ctx.Err() == nil {
		// 尝试获取 leadership。
		if e.tryAcquire(ctx) {
			onLeadership(true)
			// 进入续租循环；一旦失败立即交还 leadership（fail-safe）。
			if !e.renewLoop(ctx) {
				onLeadership(false)
				// best-effort 释放 lease（清空 holder + renewTime 置过去），
				// 使 follower 可立即接管（graceful handoff），无需等租约自然过期。
				// 用独立未取消的 context 释放：renewLoop 常因 ctx 取消而退出，
				// 此时主 ctx 已不可用，不能用来发释放请求。
				relCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = e.client.Release(relCtx, e.opts.Namespace, e.opts.Name)
				cancel()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(e.opts.RetryPeriod):
		}
	}
}

// tryAcquire 尝试成为 Lease holder。仅在 Lease 不存在或已过期时抢占，避免与
// 正常续租的 holder 冲突。成功（确认自己是 holder）返回 true。
func (e *Elector) tryAcquire(ctx context.Context) bool {
	info, err := e.client.Get(ctx, e.opts.Namespace, e.opts.Name)
	if err != nil {
		log.Printf("leader election: get lease failed: %v", err)
		return false
	}
	if info.Holder != "" && !e.leaseExpired(info) {
		// Lease 被有效 holder 占用（且不是自己）。
		if info.Holder == e.opts.Identity {
			// 异常：我已持有但未进入 renewLoop（可能刚崩过重入）。直接尝试续租确认。
			return e.tryUpdate(ctx)
		}
		return false
	}
	// Lease 不存在或已过期：尝试获取。
	if info.Holder == "" {
		if err := e.client.Create(ctx, e.opts.Namespace, e.opts.Name, e.opts.Identity); err != nil {
			log.Printf("leader election: create lease failed: %v", err)
			return false
		}
	} else {
		if !e.tryUpdate(ctx) {
			return false
		}
	}
	// 复核：确认自己确实是 holder（处理并发抢占）。
	again, err := e.client.Get(ctx, e.opts.Namespace, e.opts.Name)
	if err != nil {
		return false
	}
	return again.Holder == e.opts.Identity
}

// tryUpdate 更新 lease 的 holder 为自己（Create 已存在时用 Update）。
func (e *Elector) tryUpdate(ctx context.Context) bool {
	if err := e.client.Update(ctx, e.opts.Namespace, e.opts.Name, e.opts.Identity); err != nil {
		return false
	}
	return true
}

// leaseExpired 判断 lease 是否已过期（renewTime + leaseDuration < now）。
func (e *Elector) leaseExpired(info LeaseInfo) bool {
	if info.RenewTime.IsZero() {
		return true // 无续租时间视为可抢占
	}
	return time.Since(info.RenewTime) > e.opts.LeaseDuration
}

// renewLoop 持续续租。任一续租失败即返回 false（调用方必须停止 leadership）。
func (e *Elector) renewLoop(ctx context.Context) bool {
	ticker := time.NewTicker(e.opts.RetryPeriod)
	defer ticker.Stop()
	lastRenew := time.Now()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			// 续租超时阈值检查：若距离上次成功续租已超 RenewDeadline，视为丢失。
			if time.Since(lastRenew) > e.opts.RenewDeadline {
				return false
			}
			if !e.tryUpdate(ctx) {
				return false // fail-safe：立即停止，不 WARN 后继续写
			}
			lastRenew = time.Now()
		}
	}
}
