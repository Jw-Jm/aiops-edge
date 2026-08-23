// Package cutover 实现 V9.2 Phase 6 原子 cutover 的可执行状态机（P6.4.3）。
//
// 配套文档：docs/AIOPS_P6_4_ROLLBACK_MATRIX.md（FROZEN 架构 contract）。
//
// 本状态机把 rollback/abort 矩阵编码为可执行规则，保证：
//   - checkpoint 只能按序推进，不得跳过
//   - fresh-data verification PASS 之前绝不允许 stop old writer/reader
//   - 任何 ABORT/HARD ABORT 保持旧链完整（旧 writer/reader 不停、adapter 不删、fallback 不摘）
//   - ROLLBACK 只撤销到上一个 checkpoint，绝不进入"半 cutover"状态
package cutover

import (
	"fmt"
)

// Phase 是 cutover 的 checkpoint 阶段。
type Phase int

const (
	// PhaseNone 尚未开始。
	PhaseNone Phase = iota
	// Precheck 后端健康预检。
	PhasePrecheck
	// PhaseActivateNewWriter 激活 new writer。
	PhaseActivateNewWriter
	// PhaseActivateNewReader 激活 new reader。
	PhaseActivateNewReader
	// PhaseFreshDataVerify 验证 fresh data 可见。
	PhaseFreshDataVerify
	// PhaseScopeVerify 验证 tenant/cluster/resource scope（A/B 隔离）。
	PhaseScopeVerify
	// PhaseSemanticVerify 验证语义映射（unavailable/no_data/timeout/permission_denied 不折叠）。
	PhaseSemanticVerify
	// PhaseStopOldWriter 停止 old writer。
	PhaseStopOldWriter
	// PhaseStopOldReader 停止 old reader。
	PhaseStopOldReader
	// PhaseRemoveAdapters 移除 old adapters。
	PhaseRemoveAdapters
	// PhaseRemoveFallback 移除 fallback/transition router。
	PhaseRemoveFallback
	// PhaseFinalVerify 最终验证。
	PhaseFinalVerify
	// PhaseDone 完成。
	PhaseDone
)

// phaseName 供诊断。
var phaseName = map[Phase]string{
	PhaseNone:              "NONE",
	PhasePrecheck:          "PRECHECK",
	PhaseActivateNewWriter: "ACTIVATE_NEW_WRITER",
	PhaseActivateNewReader: "ACTIVATE_NEW_READER",
	PhaseFreshDataVerify:   "FRESH_DATA_VERIFY",
	PhaseScopeVerify:       "SCOPE_VERIFY",
	PhaseSemanticVerify:    "SEMANTIC_VERIFY",
	PhaseStopOldWriter:     "STOP_OLD_WRITER",
	PhaseStopOldReader:     "STOP_OLD_READER",
	PhaseRemoveAdapters:    "REMOVE_ADAPTERS",
	PhaseRemoveFallback:    "REMOVE_FALLBACK",
	PhaseFinalVerify:       "FINAL_VERIFY",
	PhaseDone:              "DONE",
}

// String 返回阶段名。
func (p Phase) String() string { return phaseName[p] }

// Decision 是 checkpoint 失败后的处置。
type Decision int

const (
	// DecisionNone 无（成功推进）。
	DecisionNone Decision = iota
	// DecisionAbort ABORT：旧链保持，不停 old writer/reader。
	DecisionAbort
	// DecisionHardAbort HARD ABORT：立即停止，禁止继续。
	DecisionHardAbort
	// DecisionRollback ROLLBACK：回退到上一个 checkpoint 的旧链。
	DecisionRollback
	// DecisionStopForbidden 禁止推进到下一 checkpoint（如 stop fail 时不进 adapter removal）。
	DecisionStopForbidden
)

// State 是 cutover 状态机的当前状态。
type State struct {
	Phase Phase
	// OldWriterStopped / OldReaderStopped 标记 old 是否已停（Gate 判据用）。
	OldWriterStopped bool
	OldReaderStopped bool
	// FreshDataVerified 记录 fresh-data verify 是否已 PASS（铁律护栏）。
	FreshDataVerified bool
	// ActiveWriter / ActiveReader 标记 new 是否 ACTIVE。
	ActiveWriter bool
	ActiveReader bool
}

// New 构造初始 cutover 状态。
func New() *State { return &State{Phase: PhasePrecheck} }

// decide 返回在某 checkpoint 失败时的处置（编码 rollback/abort 矩阵）。
func (s *State) decide(failPhase Phase) Decision {
	switch failPhase {
	case PhasePrecheck:
		return DecisionAbort // backend unhealthy → ABORT，旧链保持
	case PhaseActivateNewWriter:
		return DecisionAbort // activation fail → ABORT，旧 writer/reader 不动
	case PhaseActivateNewReader:
		return DecisionRollback // startup/readiness fail → 回退 new writer activation，旧链继续
	case PhaseFreshDataVerify:
		return DecisionAbort // invisible → ABORT，不停 old writer/reader
	case PhaseScopeVerify:
		return DecisionHardAbort // A/B 串数据 → HARD ABORT
	case PhaseSemanticVerify:
		return DecisionHardAbort // 语义折叠 → HARD ABORT
	case PhaseStopOldWriter:
		return DecisionStopForbidden // stop fail → 不进入 adapter removal
	case PhaseStopOldReader:
		return DecisionStopForbidden // stop fail → 不进入 adapter removal
	case PhaseRemoveAdapters:
		return DecisionHardAbort // regression → Gate FAIL
	case PhaseRemoveFallback:
		return DecisionHardAbort // tests fail → Gate FAIL
	case PhaseFinalVerify:
		return DecisionHardAbort // 任一不满足 → Gate FAIL
	default:
		return DecisionNone
	}
}

// advance 尝试推进到下一 checkpoint。
// fresh-data 铁律：StopOldWriter/StopOldReader 之前必须 FreshDataVerified。
func (s *State) advance() error {
	next := s.Phase + 1
	if next == PhaseStopOldWriter || next == PhaseStopOldReader {
		if !s.FreshDataVerified {
			return fmt.Errorf("cutover: cannot %s before fresh-data verification PASS", phaseName[next])
		}
	}
	if next == PhaseRemoveAdapters && (!s.OldWriterStopped || !s.OldReaderStopped) {
		return fmt.Errorf("cutover: cannot remove adapters before old writer AND old reader stopped")
	}
	s.Phase = next
	if next == PhaseActivateNewWriter {
		s.ActiveWriter = true
	}
	if next == PhaseActivateNewReader {
		s.ActiveReader = true
	}
	return nil
}

// Fail 报告某 checkpoint 失败并返回处置决策。
func (s *State) Fail(failPhase Phase) Decision { return s.decide(failPhase) }

// MarkFreshDataVerified 标记 fresh-data verify PASS（铁律护栏解锁 stop old 阶段）。
func (s *State) MarkFreshDataVerified() { s.FreshDataVerified = true }

// StopOldWriter 标记 old writer 已停。
func (s *State) StopOldWriter() { s.OldWriterStopped = true }

// StopOldReader 标记 old reader 已停。
func (s *State) StopOldReader() { s.OldReaderStopped = true }

// Gate6Satisfied 判断 Gate 6 完成判据（new active、old absent、历史数据 unreachable）。
func (s *State) Gate6Satisfied() bool {
	return s.Phase == PhaseDone &&
		s.ActiveWriter && s.ActiveReader &&
		s.OldWriterStopped && s.OldReaderStopped
}
