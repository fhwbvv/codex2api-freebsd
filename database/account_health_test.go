package database

import (
	"context"
	"testing"
	"time"
)

func TestAccountHealthBucketsExcludeInternalCapabilityProbe(t *testing.T) {
	db := newGrokStateTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	insert := func(statusCode int, reason string) {
		t.Helper()
		if _, err := db.conn.ExecContext(ctx, `INSERT INTO usage_logs
			(account_id, status_code, internal_reason, created_at)
			VALUES (1, $1, $2, $3)`, statusCode, reason, sqliteTimeParam(now.Add(-time.Minute))); err != nil {
			t.Fatalf("insert usage log: %v", err)
		}
	}
	insert(200, "")
	insert(200, "grok_capability_probe")
	insert(500, "grok_capability_probe")

	buckets, err := db.GetAccountsHealthBuckets(ctx, now, 20, 10*time.Minute)
	if err != nil {
		t.Fatalf("GetAccountsHealthBuckets: %v", err)
	}
	var success, failed int
	for _, bucket := range buckets[1] {
		success += bucket.Success
		failed += bucket.Failed
	}
	if success != 1 || failed != 0 {
		t.Fatalf("health buckets = success %d failed %d, want 1/0 with probe rows excluded", success, failed)
	}
}
