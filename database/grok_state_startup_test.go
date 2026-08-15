package database

import (
	"context"
	"testing"
	"time"
)

type grokStateContextKey struct{}

func TestGrokStateStartupContextKeepsValuesButNotSchemaCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.WithValue(context.Background(), grokStateContextKey{}, "startup"))
	cancelParent()

	ctx, cancel := grokStateStartupContext(parent)
	defer cancel()

	if got := ctx.Value(grokStateContextKey{}); got != "startup" {
		t.Fatalf("startup context value = %v, want startup", got)
	}
	select {
	case <-ctx.Done():
		t.Fatalf("Grok state context inherited the canceled schema deadline: %v", ctx.Err())
	default:
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("Grok state context has no bounded deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 4*time.Minute || remaining > grokStateBackfillInitTimeout {
		t.Fatalf("Grok state context remaining deadline = %v, want (4m, %v]", remaining, grokStateBackfillInitTimeout)
	}
}
