package api

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// 28.2 #19：Runtime/Lease/Outbox/Tool/Recovery/Replay/SSE/LLM/Alert control-plane
// metrics（标准库 Prometheus 文本格式，零依赖；经 /metrics 供 VM 抓取，不泄露 secret）。
// ─────────────────────────────────────────────────────────────────────────────

// cpMetrics 是 control-plane 运行指标的原子计数器。
type cpMetrics struct {
	mu sync.Mutex
	// Lease
	leaseClaim, leaseRenew, leaseRelease, leaseFencing int64
	// Runtime Commit
	commitTotal, commitIdempotent int64
	// Outbox dispatch
	outboxDispatch, outboxStaleReclaim        int64
	actionOutboxDispatch, actionOutboxUnknown int64
	actionOutboxPending                       int64
	actionOutboxOldestAgeSeconds              float64
	// Tool
	toolStarted, toolConverged int64
	// Replay
	replayConsumed, replayRejected int64
	// Recovery
	recoveryScans int64
	// SSE
	sseSubscriptions, sseRetentionRejected int64
	// LLM config
	llmConfigReads int64
	// Alert
	alertEvalLoops, alertLeaderElected int64
	// correlation：最近 correlation_id 是否透传（不记录其值，避免泄漏）
	correlationPropagated int64
}

var cp = &cpMetrics{}

func (m *cpMetrics) inc(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch name {
	case "lease_claim":
		m.leaseClaim++
	case "lease_renew":
		m.leaseRenew++
	case "lease_release":
		m.leaseRelease++
	case "lease_fencing":
		m.leaseFencing++
	case "commit":
		m.commitTotal++
	case "commit_idempotent":
		m.commitIdempotent++
	case "outbox_dispatch":
		m.outboxDispatch++
	case "outbox_stale_reclaim":
		m.outboxStaleReclaim++
	case "action_outbox_dispatch":
		m.actionOutboxDispatch++
	case "action_outbox_unknown":
		m.actionOutboxUnknown++
	case "tool_started":
		m.toolStarted++
	case "tool_converged":
		m.toolConverged++
	case "replay_consumed":
		m.replayConsumed++
	case "replay_rejected":
		m.replayRejected++
	case "recovery_scan":
		m.recoveryScans++
	case "sse_subscription":
		m.sseSubscriptions++
	case "sse_retention_rejected":
		m.sseRetentionRejected++
	case "llm_config_read":
		m.llmConfigReads++
	case "alert_eval_loop":
		m.alertEvalLoops++
	case "alert_leader_elected":
		m.alertLeaderElected++
	case "correlation_propagated":
		m.correlationPropagated++
	}
}

func (m *cpMetrics) setActionQueue(depth int, oldestAge time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.actionOutboxPending = int64(depth)
	if oldestAge > 0 {
		m.actionOutboxOldestAgeSeconds = oldestAge.Seconds()
	} else {
		m.actionOutboxOldestAgeSeconds = 0
	}
}

// writeControlPlaneMetrics 追加 control-plane 指标到 /metrics 输出。
func (m *cpMetrics) writeControlPlaneMetrics(w http.ResponseWriter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// 用标签按 metric 分组，不泄露 correlation_id 值（只计数是否透传）。
	fmt.Fprintf(w, "# HELP aio_control_plane_lease_claim_total Run execution lease claim attempts.\n")
	fmt.Fprintf(w, "# TYPE aio_control_plane_lease_claim_total counter\n")
	fmt.Fprintf(w, "aio_control_plane_lease_claim_total %d\n", m.leaseClaim)
	fmt.Fprintf(w, "# HELP aio_control_plane_lease_fencing_total Lease fencing rejections (epoch/token mismatch).\n")
	fmt.Fprintf(w, "# TYPE aio_control_plane_lease_fencing_total counter\n")
	fmt.Fprintf(w, "aio_control_plane_lease_fencing_total %d\n", m.leaseFencing)
	fmt.Fprintf(w, "# HELP aio_control_plane_commit_total Runtime Commit total.\n")
	fmt.Fprintf(w, "# TYPE aio_control_plane_commit_total counter\n")
	fmt.Fprintf(w, "aio_control_plane_commit_total %d\n", m.commitTotal)
	fmt.Fprintf(w, "# HELP aio_control_plane_commit_idempotent_total Runtime Commit idempotent replays.\n")
	fmt.Fprintf(w, "# TYPE aio_control_plane_commit_idempotent_total counter\n")
	fmt.Fprintf(w, "aio_control_plane_commit_idempotent_total %d\n", m.commitIdempotent)
	fmt.Fprintf(w, "# HELP aio_control_plane_outbox_dispatch_total RunInvocation outbox dispatch attempts.\n")
	fmt.Fprintf(w, "# TYPE aio_control_plane_outbox_dispatch_total counter\n")
	fmt.Fprintf(w, "aio_control_plane_outbox_dispatch_total %d\n", m.outboxDispatch)
	fmt.Fprintf(w, "# HELP aio_control_plane_outbox_stale_reclaim_total Stale outbox claim reclaims.\n")
	fmt.Fprintf(w, "# TYPE aio_control_plane_outbox_stale_reclaim_total counter\n")
	fmt.Fprintf(w, "aio_control_plane_outbox_stale_reclaim_total %d\n", m.outboxStaleReclaim)
	fmt.Fprintf(w, "# HELP aio_control_plane_action_outbox_dispatch_total Action outbox dispatch attempts.\n")
	fmt.Fprintf(w, "# TYPE aio_control_plane_action_outbox_dispatch_total counter\n")
	fmt.Fprintf(w, "aio_control_plane_action_outbox_dispatch_total %d\n", m.actionOutboxDispatch)
	fmt.Fprintf(w, "# HELP aio_control_plane_action_outbox_unknown_total Action executions awaiting reconciliation.\n")
	fmt.Fprintf(w, "# TYPE aio_control_plane_action_outbox_unknown_total counter\n")
	fmt.Fprintf(w, "aio_control_plane_action_outbox_unknown_total %d\n", m.actionOutboxUnknown)
	fmt.Fprintf(w, "# HELP aio_control_plane_action_outbox_pending Action outbox pending depth.\n")
	fmt.Fprintf(w, "# TYPE aio_control_plane_action_outbox_pending gauge\n")
	fmt.Fprintf(w, "aio_control_plane_action_outbox_pending %d\n", m.actionOutboxPending)
	fmt.Fprintf(w, "# HELP aio_control_plane_action_outbox_oldest_age_seconds Action outbox oldest pending age.\n")
	fmt.Fprintf(w, "# TYPE aio_control_plane_action_outbox_oldest_age_seconds gauge\n")
	fmt.Fprintf(w, "aio_control_plane_action_outbox_oldest_age_seconds %.3f\n", m.actionOutboxOldestAgeSeconds)
	fmt.Fprintf(w, "# HELP aio_control_plane_tool_started_total ToolRun started.\n")
	fmt.Fprintf(w, "# TYPE aio_control_plane_tool_started_total counter\n")
	fmt.Fprintf(w, "aio_control_plane_tool_started_total %d\n", m.toolStarted)
	fmt.Fprintf(w, "# HELP aio_control_plane_tool_converged_total ToolRun converged (timeout/failed_unknown).\n")
	fmt.Fprintf(w, "# TYPE aio_control_plane_tool_converged_total counter\n")
	fmt.Fprintf(w, "aio_control_plane_tool_converged_total %d\n", m.toolConverged)
	fmt.Fprintf(w, "# HELP aio_control_plane_replay_consumed_total Shared replay nonce consumed.\n")
	fmt.Fprintf(w, "# TYPE aio_control_plane_replay_consumed_total counter\n")
	fmt.Fprintf(w, "aio_control_plane_replay_consumed_total %d\n", m.replayConsumed)
	fmt.Fprintf(w, "# HELP aio_control_plane_replay_rejected_total Shared replay rejections (context_replayed).\n")
	fmt.Fprintf(w, "# TYPE aio_control_plane_replay_rejected_total counter\n")
	fmt.Fprintf(w, "aio_control_plane_replay_rejected_total %d\n", m.replayRejected)
	fmt.Fprintf(w, "# HELP aio_control_plane_recovery_scan_total Recovery scanner global scans.\n")
	fmt.Fprintf(w, "# TYPE aio_control_plane_recovery_scan_total counter\n")
	fmt.Fprintf(w, "aio_control_plane_recovery_scan_total %d\n", m.recoveryScans)
	fmt.Fprintf(w, "# HELP aio_control_plane_sse_subscription_total SSE subscriptions.\n")
	fmt.Fprintf(w, "# TYPE aio_control_plane_sse_subscription_total counter\n")
	fmt.Fprintf(w, "aio_control_plane_sse_subscription_total %d\n", m.sseSubscriptions)
	fmt.Fprintf(w, "# HELP aio_control_plane_llm_config_read_total LLM internal config reads (no secret).\n")
	fmt.Fprintf(w, "# TYPE aio_control_plane_llm_config_read_total counter\n")
	fmt.Fprintf(w, "aio_control_plane_llm_config_read_total %d\n", m.llmConfigReads)
	fmt.Fprintf(w, "# HELP aio_control_plane_alert_leader_elected_total Alert leader elections.\n")
	fmt.Fprintf(w, "# TYPE aio_control_plane_alert_leader_elected_total counter\n")
	fmt.Fprintf(w, "aio_control_plane_alert_leader_elected_total %d\n", m.alertLeaderElected)
	fmt.Fprintf(w, "# HELP aio_control_plane_correlation_propagated_total Correlation IDs propagated (value not exposed).\n")
	fmt.Fprintf(w, "# TYPE aio_control_plane_correlation_propagated_total counter\n")
	fmt.Fprintf(w, "aio_control_plane_correlation_propagated_total %d\n", m.correlationPropagated)
}
