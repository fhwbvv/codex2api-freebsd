package proxy

import (
	"errors"
	"testing"
)

func TestClassifyTransportFailureWsBusyAcquire(t *testing.T) {
	err := errors.New("acquire websocket connection timed out after 30s waiting for busy session")
	if got := classifyTransportFailure(err); got != upstreamErrorKindWsBusyAcquire {
		t.Fatalf("classifyTransportFailure = %q, want %q", got, upstreamErrorKindWsBusyAcquire)
	}
}

// "timed out" 文案（如容量等待超时）应归为 timeout 而不是笼统的 transport。
func TestClassifyTransportFailureTimedOutPhrase(t *testing.T) {
	err := errors.New("acquire websocket connection timed out after 30s waiting for account connection capacity")
	if got := classifyTransportFailure(err); got != "timeout" {
		t.Fatalf("classifyTransportFailure = %q, want timeout", got)
	}
}

func TestShouldPenalizeTransportKind(t *testing.T) {
	if shouldPenalizeTransportKind(upstreamErrorKindWsBusyAcquire) {
		t.Fatal("busy acquire timeout must not penalize account health (issue #413)")
	}
	if shouldPenalizeTransportKind("") {
		t.Fatal("empty kind must not penalize")
	}
	if !shouldPenalizeTransportKind("transport") {
		t.Fatal("generic transport failure should penalize")
	}
	if !shouldPenalizeTransportKind("timeout") {
		t.Fatal("timeout failure should penalize")
	}
}
