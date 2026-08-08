package database

import (
	"context"
	"testing"
	"time"
)

type usageStatsRollupContextKey struct{}

func TestUsageStatsRollupStartupContextKeepsValuesButNotSchemaCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.WithValue(context.Background(), usageStatsRollupContextKey{}, "startup"))
	cancelParent()

	ctx, cancel := usageStatsRollupStartupContext(parent)
	defer cancel()

	if got := ctx.Value(usageStatsRollupContextKey{}); got != "startup" {
		t.Fatalf("startup context value = %v, want startup", got)
	}
	select {
	case <-ctx.Done():
		t.Fatalf("rollup context inherited the canceled schema deadline: %v", ctx.Err())
	default:
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("rollup context has no bounded deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 4*time.Minute || remaining > usageStatsRollupInitTimeout {
		t.Fatalf("rollup context remaining deadline = %v, want (4m, %v]", remaining, usageStatsRollupInitTimeout)
	}
}
