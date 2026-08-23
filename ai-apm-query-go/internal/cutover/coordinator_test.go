package cutover

import (
	"errors"
	"testing"
)

// fakeRuntime 实现 RuntimeWriter + RuntimeReader（受控真实切换）。
type fakeRuntime struct {
	writingNew  bool
	readingNew  bool
	legacyWrite bool
	legacyRead  bool
	// failToEnter 模拟"切换但未真正生效"（reconcile 检查）。
	failWriterEnter bool
	failReaderEnter bool
}

func (f *fakeRuntime) ActivateNew() bool {
	if f.failWriterEnter || f.failReaderEnter {
		return false
	}
	f.writingNew = true
	f.readingNew = true
	return true
}
func (f *fakeRuntime) IsWritingNew() bool { return f.writingNew }
func (f *fakeRuntime) IsReadingNew() bool { return f.readingNew }
func (f *fakeRuntime) StopLegacy()        { f.legacyWrite = false; f.legacyRead = false }

func TestCoordinatorFullCutoverSequence(t *testing.T) {
	rt := &fakeRuntime{}
	c := NewCoordinator("cut-1", rt, rt)

	// 1. 激活 new writer → runtime 实际 new。
	if err := c.ActivateNewWriter(); err != nil {
		t.Fatalf("activate new writer: %v", err)
	}
	if !rt.writingNew {
		t.Fatal("writer not actually writing new after ActivateNewWriter")
	}
	// 2. 激活 new reader。
	if err := c.ActivateNewReader(); err != nil {
		t.Fatalf("activate new reader: %v", err)
	}
	if !rt.readingNew {
		t.Fatal("reader not actually reading new after ActivateNewReader")
	}
	// 3. fresh-data verify（真实 telemetry PASS）。
	if err := c.VerifyFreshData(true); err != nil {
		t.Fatalf("verify fresh data: %v", err)
	}
	// 4. 停 old writer / old reader。
	if err := c.StopOldWriter(); err != nil {
		t.Fatalf("stop old writer: %v", err)
	}
	if err := c.StopOldReader(); err != nil {
		t.Fatalf("stop old reader: %v", err)
	}
	st := c.Status()
	if !st.NewWriterActive || !st.NewReaderActive || !st.FreshDataVerified ||
		!st.OldWriterStopped || !st.OldReaderStopped {
		t.Fatalf("status mismatch: %+v", st)
	}
}

func TestCoordinatorNoBypassStopOldBeforeFreshData(t *testing.T) {
	rt := &fakeRuntime{}
	c := NewCoordinator("cut-2", rt, rt)
	if err := c.ActivateNewWriter(); err != nil {
		t.Fatal(err)
	}
	if err := c.ActivateNewReader(); err != nil {
		t.Fatal(err)
	}
	// 未 fresh-data verify，直接尝试 StopOldWriter → 必须拒绝（经 State 铁律，无旁路）。
	if err := c.StopOldWriter(); err == nil {
		t.Fatal("StopOldWriter must be rejected before fresh-data verified (no bypass)")
	}
	if c.Status().OldWriterStopped {
		t.Fatal("old writer must not be stopped before fresh-data verified")
	}
}

func TestCoordinatorWriterActivationReconcile(t *testing.T) {
	rt := &fakeRuntime{failWriterEnter: true}
	c := NewCoordinator("cut-3", rt, rt)
	// writer 切换后未真正进入 new → ActivateNewWriter 必须失败（runtime reconcile）。
	if err := c.ActivateNewWriter(); err == nil {
		t.Fatal("expected ActivateNewWriter to fail when writer not actually new")
	}
}

func TestCoordinatorFreshDataFailureAborts(t *testing.T) {
	rt := &fakeRuntime{}
	c := NewCoordinator("cut-4", rt, rt)
	if err := c.ActivateNewWriter(); err != nil {
		t.Fatal(err)
	}
	if err := c.ActivateNewReader(); err != nil {
		t.Fatal(err)
	}
	// fresh data 不可见 → ABORT，且 old 不能停。
	err := c.VerifyFreshData(false)
	if err == nil {
		t.Fatal("expected abort when fresh data not verified")
	}
	var ce *Error
	if errors.As(err, &ce) && ce.Decision != DecisionAbort {
		t.Fatalf("expected DecisionAbort, got %v", ce.Decision)
	}
	if c.Status().FreshDataVerified || c.Status().OldWriterStopped {
		t.Fatal("old path must remain unchanged after fresh-data ABORT")
	}
}

func TestCoordinatorObservableStatusFields(t *testing.T) {
	rt := &fakeRuntime{}
	c := NewCoordinator("cut-obs", rt, rt)
	if err := c.ActivateNewWriter(); err != nil {
		t.Fatal(err)
	}
	st := c.Status()
	if st.CutoverID != "cut-obs" || st.CurrentState != "ACTIVATE_NEW_WRITER" {
		t.Fatalf("status fields wrong: %+v", st)
	}
	if st.StartedAt.IsZero() || st.LastTransitionAt.IsZero() {
		t.Fatal("timestamps not set")
	}
}
