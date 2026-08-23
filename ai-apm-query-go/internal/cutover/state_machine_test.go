package cutover

import (
	"strings"
	"testing"
)

// advanceTo 推进状态机直到达到 target 阶段（含），返回错误。
func advanceTo(t *testing.T, s *State, target Phase) {
	t.Helper()
	for s.Phase < target {
		if err := s.advance(); err != nil {
			t.Fatalf("advance to %v (at %v): %v", target, s.Phase, err)
		}
	}
}

func TestPhaseOrderLocked(t *testing.T) {
	want := []Phase{
		PhasePrecheck, PhaseActivateNewWriter, PhaseActivateNewReader,
		PhaseFreshDataVerify, PhaseScopeVerify, PhaseSemanticVerify,
		PhaseStopOldWriter, PhaseStopOldReader, PhaseRemoveAdapters,
		PhaseRemoveFallback, PhaseFinalVerify, PhaseDone,
	}
	s := New()
	if s.Phase != PhasePrecheck {
		t.Fatalf("initial phase = %v, want PRECHECK", s.Phase)
	}
	// 模拟真实 cutover：fresh-data 在 StopOld 前 PASS；old 停止发生在对应阶段。
	s.MarkFreshDataVerified()
	for i, p := range want {
		if s.Phase != p {
			t.Fatalf("phase[%d] = %v, want %v", i, s.Phase, p)
		}
		if err := s.advance(); err != nil {
			t.Fatalf("advance from %v: %v", p, err)
		}
		if s.Phase == PhaseStopOldWriter {
			s.StopOldWriter()
		}
		if s.Phase == PhaseStopOldReader {
			s.StopOldReader()
		}
	}
}

func TestFreshDataGateBlocksStopOld(t *testing.T) {
	s := New()
	advanceTo(t, s, PhaseSemanticVerify) // 停在 StopOldWriter 前一步，fresh-data 未 verify
	if err := s.advance(); err == nil {
		t.Fatal("expected advance to STOP_OLD_WRITER to be blocked before fresh-data verified")
	} else if !strings.Contains(err.Error(), "fresh-data verification PASS") {
		t.Fatalf("wrong error: %v", err)
	}
	// 标记 fresh-data verified 后推进到 StopOldWriter 成功。
	s.MarkFreshDataVerified()
	if err := s.advance(); err != nil {
		t.Fatalf("advance after fresh-data verified: %v", err)
	}
	if s.Phase != PhaseStopOldWriter {
		t.Fatalf("phase = %v, want STOP_OLD_WRITER", s.Phase)
	}
}

func TestAbortMatrix(t *testing.T) {
	cases := []struct {
		phase    Phase
		decision Decision
	}{
		{PhasePrecheck, DecisionAbort},
		{PhaseActivateNewWriter, DecisionAbort},
		{PhaseActivateNewReader, DecisionRollback},
		{PhaseFreshDataVerify, DecisionAbort},
		{PhaseScopeVerify, DecisionHardAbort},
		{PhaseSemanticVerify, DecisionHardAbort},
		{PhaseStopOldWriter, DecisionStopForbidden},
		{PhaseStopOldReader, DecisionStopForbidden},
		{PhaseRemoveAdapters, DecisionHardAbort},
		{PhaseRemoveFallback, DecisionHardAbort},
		{PhaseFinalVerify, DecisionHardAbort},
	}
	s := New()
	for _, c := range cases {
		if got := s.Fail(c.phase); got != c.decision {
			t.Errorf("Fail(%v) = %v, want %v", c.phase, got, c.decision)
		}
	}
}

func TestRemoveAdaptersRequiresBothStopped(t *testing.T) {
	s := New()
	s.MarkFreshDataVerified()
	// 推进到 StopOldReader 阶段并只停 old writer。
	advanceTo(t, s, PhaseStopOldWriter)
	s.StopOldWriter()
	if err := s.advance(); err != nil {
		t.Fatal(err) // StopOldWriter → StopOldReader 应成功
	}
	if s.Phase != PhaseStopOldReader {
		t.Fatalf("phase = %v, want STOP_OLD_READER", s.Phase)
	}
	// 未 stop old reader → advance 到 REMOVE_ADAPTERS 被拦。
	if err := s.advance(); err == nil {
		t.Fatal("expected advance to REMOVE_ADAPTERS blocked before old reader stopped")
	}
	s.StopOldReader()
	if err := s.advance(); err != nil {
		t.Fatalf("advance to REMOVE_ADAPTERS after both stopped: %v", err)
	}
}

func TestGate6SatisfiedOnlyWhenDone(t *testing.T) {
	s := New()
	s.MarkFreshDataVerified()
	for s.Phase < PhaseDone {
		if err := s.advance(); err != nil {
			t.Fatal(err)
		}
		if s.Phase == PhaseStopOldWriter {
			s.StopOldWriter()
		}
		if s.Phase == PhaseStopOldReader {
			s.StopOldReader()
		}
	}
	if s.Phase != PhaseDone {
		t.Fatalf("phase = %v, want DONE", s.Phase)
	}
	if !s.Gate6Satisfied() {
		t.Fatal("expected Gate6 satisfied at DONE with new active + old stopped")
	}
}
