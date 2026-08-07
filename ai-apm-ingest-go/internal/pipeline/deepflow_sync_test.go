package pipeline

import (
	"testing"
	"time"
)

func TestParseSyncInterval_Default(t *testing.T) {
	if got := parseSyncInterval(""); got != 60*time.Second {
		t.Fatalf("default interval = %v, want 60s", got)
	}
}

func TestParseSyncInterval_ValidSeconds(t *testing.T) {
	if got := parseSyncInterval("10"); got != 10*time.Second {
		t.Fatalf("got %v, want 10s", got)
	}
}

func TestParseSyncInterval_ValidDuration(t *testing.T) {
	if got := parseSyncInterval("15s"); got != 15*time.Second {
		t.Fatalf("got %v, want 15s", got)
	}
}

func TestParseSyncInterval_ClampedOutOfRange(t *testing.T) {
	if got := parseSyncInterval("2h"); got != 60*time.Second { // >3600s 回退默认
		t.Fatalf("too-large interval = %v, want default", got)
	}
	if got := parseSyncInterval("1s"); got != 60*time.Second { // <5s 回退默认
		t.Fatalf("too-small interval = %v, want default", got)
	}
}

func TestParseSyncInterval_AtMaxAllowed(t *testing.T) {
	if got := parseSyncInterval("1h"); got != 3600*time.Second { // 恰好等于上限，应允许
		t.Fatalf("at-max interval = %v, want 3600s", got)
	}
}

func TestParseSyncInterval_InvalidString(t *testing.T) {
	if got := parseSyncInterval("abc"); got != 60*time.Second {
		t.Fatalf("invalid interval = %v, want default", got)
	}
}

func TestClampStartTime_NoClockSkew(t *testing.T) {
	now := time.Now().UTC()
	last := now.Add(-2 * time.Minute)
	if got := clampStartTime(last, now); got.Unix() != last.Unix() {
		t.Fatalf("clamp changed valid last time: got %v, want %v", got, last)
	}
}

func TestClampStartTime_Future(t *testing.T) {
	now := time.Now().UTC()
	if got := clampStartTime(now.Add(time.Hour), now); got.After(now) {
		t.Fatalf("clamp did not pull future time back: %v", got)
	}
}

func TestClampStartTime_TooOld(t *testing.T) {
	now := time.Now().UTC()
	if got := clampStartTime(now.Add(-2*time.Hour), now); got.Before(now.Add(-15*time.Minute)) {
		t.Fatalf("clamp allowed too-old start: %v", got)
	}
}
