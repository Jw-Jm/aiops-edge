package query

import "testing"

func TestParseReaderMode(t *testing.T) {
	if m, err := ParseReaderMode("legacy"); err != nil || m != ModeLegacy {
		t.Fatalf("ParseReaderMode(legacy) = %q,%v", m, err)
	}
	if m, err := ParseReaderMode("new"); err != nil || m != ModeNew {
		t.Fatalf("ParseReaderMode(new) = %q,%v", m, err)
	}
	if _, err := ParseReaderMode("bogus"); err == nil {
		t.Fatal("ParseReaderMode(bogus) expected error")
	}
}

func TestSourceRouterRoutesByMode(t *testing.T) {
	rLegacy := NewSourceRouter(ModeLegacy)
	if got := rLegacy.ReaderFor("logs"); got != ReaderLegacy {
		t.Fatalf("legacy mode: logs should route to legacy, got %q", got)
	}
	rNew := NewSourceRouter(ModeNew)
	if got := rNew.ReaderFor("logs"); got != ReaderNew {
		t.Fatalf("new mode: logs should route to new (VLogs), got %q", got)
	}
	if got := rNew.ReaderFor("metrics"); got != ReaderNew {
		t.Fatalf("new mode: metrics should route to new (VM), got %q", got)
	}
	// traces 始终走 ClickHouse（trace/edge SoT 固定），无论 mode。
	if got := rNew.ReaderFor("traces"); got != ReaderLegacy {
		t.Fatalf("traces SoT is ClickHouse in both modes, got %q", got)
	}
}
