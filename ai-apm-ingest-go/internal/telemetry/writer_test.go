package telemetry

import (
	"testing"
	"time"
)

func TestParseMode(t *testing.T) {
	if m, err := ParseMode("disabled"); err != nil || m != ModeDisabled {
		t.Fatalf("ParseMode(disabled) = %q,%v", m, err)
	}
	if m, err := ParseMode("legacy"); err != nil || m != ModeDisabled {
		t.Fatalf("ParseMode(legacy) alias should map to ModeDisabled, got %q,%v", m, err)
	}
	if m, err := ParseMode("new"); err != nil || m != ModeNew {
		t.Fatalf("ParseMode(new) = %q,%v", m, err)
	}
	if _, err := ParseMode("bogus"); err == nil {
		t.Fatal("ParseMode(bogus) expected error")
	}
}

func TestVMWriterDefaultLegacyDisabled(t *testing.T) {
	w := NewVictoriaMetricsWriter("http://vm:8428")
	if w.Enabled() {
		t.Fatal("default VM writer must be disabled (legacy)")
	}
	// legacy 下 Write 校验通过但返回 ok（不发送），仍是 disabled 状态。
	res := w.Write(map[string]string{
		"tenant_id":  "3f3c3b3a-0000-4000-8000-000000000001",
		"cluster_id": "3f3c3b3a-0000-4000-8000-000000000002",
		"__name__":   "cpu_usage",
	}, 1, t0())
	if res.ErrorCode != "" {
		t.Fatalf("legacy write should pass validation, got %q", res.ErrorCode)
	}
	if w.Enabled() {
		t.Fatal("write must not flip writer to enabled")
	}
}

func TestVMWriterSwitchableToNew(t *testing.T) {
	w := NewVictoriaMetricsWriter("http://vm:8428")
	w.SetMode(ModeNew)
	if !w.Enabled() {
		t.Fatal("SetMode(ModeNew) must enable production write")
	}
	// 显式受控切换（Phase 6 原子窗口路径），无需改源码重建。
	if _, err := ParseMode("new"); err != nil {
		t.Fatal("new mode must be a valid switch value")
	}
}

func TestVMWriterModeConstructor(t *testing.T) {
	// 隔离/受控环境以 ModeNew 构造，验证真实 enable 路径。
	w := NewVictoriaMetricsWriterMode("http://vm:8428", ModeNew)
	if !w.Enabled() {
		t.Fatal("NewVictoriaMetricsWriterMode(ModeNew) must be enabled")
	}
}

func TestVLogsWriterDefaultLegacyDisabled(t *testing.T) {
	w := NewVictoriaLogsWriter("http://vlogs:9428")
	if w.Enabled() {
		t.Fatal("default VLogs writer must be disabled (legacy)")
	}
	res := w.WriteLog(map[string]string{
		"tenant_id":  "3f3c3b3a-0000-4000-8000-000000000001",
		"cluster_id": "3f3c3b3a-0000-4000-8000-000000000002",
	}, "log", t0())
	if res.ErrorCode != "" {
		t.Fatalf("legacy write should pass validation, got %q", res.ErrorCode)
	}
	if w.Enabled() {
		t.Fatal("write must not flip writer to enabled")
	}
}

func TestVLogsWriterSwitchableToNew(t *testing.T) {
	w := NewVictoriaLogsWriter("http://vlogs:9428")
	w.SetMode(ModeNew)
	if !w.Enabled() {
		t.Fatal("SetMode(ModeNew) must enable production write")
	}
}

func t0() time.Time {
	return time.Unix(1700000000, 0).UTC()
}
