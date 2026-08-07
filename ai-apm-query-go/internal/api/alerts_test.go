package api

import "testing"

func TestTransitionStatus_FiringToAck(t *testing.T) {
	ev := &AlertEvent{Status: "firing"}
	if !transitionStatus(ev, "acknowledged", "admin") {
		t.Fatal("expected firing->acknowledged allowed")
	}
	if ev.Status != "acknowledged" || ev.AcknowledgedBy != "admin" {
		t.Fatalf("status=%s by=%s", ev.Status, ev.AcknowledgedBy)
	}
}

func TestTransitionStatus_AckToResolved(t *testing.T) {
	ev := &AlertEvent{Status: "acknowledged"}
	if !transitionStatus(ev, "resolved", "admin") {
		t.Fatal("expected acknowledged->resolved allowed")
	}
	if ev.Status != "resolved" || ev.ResolvedBy != "admin" {
		t.Fatalf("status=%s by=%s", ev.Status, ev.ResolvedBy)
	}
}

func TestTransitionStatus_FiringToResolved(t *testing.T) {
	ev := &AlertEvent{Status: "firing"}
	if !transitionStatus(ev, "resolved", "admin") {
		t.Fatal("expected firing->resolved allowed")
	}
}

func TestTransitionStatus_Illegal(t *testing.T) {
	ev := &AlertEvent{Status: "resolved"}
	if transitionStatus(ev, "acknowledged", "admin") {
		t.Fatal("resolved->acknowledged should be illegal")
	}
	if transitionStatus(ev, "firing", "admin") {
		t.Fatal("resolved->firing should be illegal")
	}
}
