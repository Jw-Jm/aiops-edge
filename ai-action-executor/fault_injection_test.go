package main

import (
	"net/http"
	"testing"
	"time"
)

func TestLocalValidationDispatchGateBlocksOnlyTargetAction(t *testing.T) {
	s := newTestServer(ModeManual)
	s.dispatchGate = make(chan struct{})
	s.dispatchGateActionID = "act-1"
	s.readCurrentStateFn = func(ctx ActionExecutionContext) (string, string, bool, error) {
		return ctx.TargetUID, ctx.ResourceVersion, false, nil
	}

	done := make(chan *testResponse, 1)
	go func() {
		rec, req := signedPOST("/v1/executor/execute", execBody())
		s.handleExecute(rec, req)
		done <- &testResponse{code: rec.Code}
	}()

	select {
	case <-done:
		t.Fatal("dispatch gate must hold the target action")
	case <-time.After(50 * time.Millisecond):
	}
	close(s.dispatchGate)

	select {
	case result := <-done:
		if result.code != http.StatusOK {
			t.Fatalf("released action status=%d, want 200", result.code)
		}
	case <-time.After(time.Second):
		t.Fatal("released dispatch gate did not complete")
	}
}

func TestLocalValidationDropsResponseOnlyAfterPatch(t *testing.T) {
	s := newTestServer(ModeApproved)
	s.k8sEnabled = true
	s.dropResponseAfterApply = true
	s.dropResponseActionID = "act-1"
	s.readCurrentStateFn = func(ctx ActionExecutionContext) (string, string, bool, error) {
		return ctx.TargetUID, ctx.ResourceVersion, false, nil
	}
	patches := 0
	s.patchTargetFn = func(ActionExecutionContext, string) error {
		patches++
		return nil
	}

	defer func() {
		if recovered := recover(); recovered != http.ErrAbortHandler {
			t.Fatalf("response-loss injection panic=%v, want http.ErrAbortHandler", recovered)
		}
		if patches != 1 {
			t.Fatalf("patches=%d, want exactly one applied patch", patches)
		}
	}()
	rec, req := signedPOST("/v1/executor/execute", execBody())
	s.handleExecute(rec, req)
}

type testResponse struct{ code int }
