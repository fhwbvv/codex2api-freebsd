package database

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestResetAPIKeyQuotaPreservesHistoricalUsage(t *testing.T) {
	db, err := New("sqlite", filepath.Join(t.TempDir(), "quota.db"))
	if err != nil {
		t.Fatalf("New(sqlite): %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	id, err := db.InsertAPIKeyWithOptions(ctx, APIKeyInput{
		Name:       "limited",
		Key:        "sk-reset-single-1234567890",
		QuotaLimit: 10,
		QuotaUsed:  7.5,
	})
	if err != nil {
		t.Fatalf("InsertAPIKeyWithOptions: %v", err)
	}
	if _, err := db.conn.ExecContext(ctx, `UPDATE api_keys SET total_used = 21.5 WHERE id = $1`, id); err != nil {
		t.Fatalf("seed total_used: %v", err)
	}

	target, err := db.ResetAPIKeyQuota(ctx, id)
	if err != nil {
		t.Fatalf("ResetAPIKeyQuota: %v", err)
	}
	if target.ID != id || target.Key != "sk-reset-single-1234567890" {
		t.Fatalf("reset target = %#v", target)
	}
	row, err := db.GetAPIKeyByID(ctx, id)
	if err != nil {
		t.Fatalf("GetAPIKeyByID: %v", err)
	}
	if row.QuotaUsed != 0 || row.TotalUsed != 21.5 {
		t.Fatalf("usage after reset = quota %v total %v, want 0 and 21.5", row.QuotaUsed, row.TotalUsed)
	}
	if row.ResetCount != 1 || !row.LastResetAt.Valid {
		t.Fatalf("reset metadata = count %d time valid %v, want 1 and true", row.ResetCount, row.LastResetAt.Valid)
	}
}

func TestResetAllAPIKeyQuotasResetsEveryKey(t *testing.T) {
	db, err := New("sqlite", filepath.Join(t.TempDir(), "quota-all.db"))
	if err != nil {
		t.Fatalf("New(sqlite): %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	limitedIDs := make([]int64, 0, 2)
	for index, used := range []float64{2.5, 8} {
		id, err := db.InsertAPIKeyWithOptions(ctx, APIKeyInput{
			Name:       "limited",
			Key:        fmt.Sprintf("sk-reset-all-limited-%d-1234567890", index),
			QuotaLimit: 10,
			QuotaUsed:  used,
		})
		if err != nil {
			t.Fatalf("InsertAPIKeyWithOptions limited: %v", err)
		}
		limitedIDs = append(limitedIDs, id)
	}
	unlimitedID, err := db.InsertAPIKeyWithOptions(ctx, APIKeyInput{
		Name:      "unlimited",
		Key:       "sk-reset-all-unlimited-1234567890",
		QuotaUsed: 4,
	})
	if err != nil {
		t.Fatalf("InsertAPIKeyWithOptions unlimited: %v", err)
	}

	targets, err := db.ResetAllAPIKeyQuotas(ctx)
	if err != nil {
		t.Fatalf("ResetAllAPIKeyQuotas: %v", err)
	}
	if len(targets) != 3 {
		t.Fatalf("reset targets = %d, want 3", len(targets))
	}
	for _, id := range limitedIDs {
		row, err := db.GetAPIKeyByID(ctx, id)
		if err != nil {
			t.Fatalf("GetAPIKeyByID(%d): %v", id, err)
		}
		if row.QuotaUsed != 0 || row.ResetCount != 1 || !row.LastResetAt.Valid {
			t.Fatalf("limited row after reset = %#v", row)
		}
	}
	unlimited, err := db.GetAPIKeyByID(ctx, unlimitedID)
	if err != nil {
		t.Fatalf("GetAPIKeyByID(unlimited): %v", err)
	}
	if unlimited.QuotaUsed != 0 || unlimited.ResetCount != 1 || !unlimited.LastResetAt.Valid {
		t.Fatalf("unlimited row was not reset: %#v", unlimited)
	}
}

func TestResetAPIKeyQuotaRestartsOnlyFiveHourAndSevenDayWindows(t *testing.T) {
	db, err := New("sqlite", filepath.Join(t.TempDir(), "quota-windows.db"))
	if err != nil {
		t.Fatalf("New(sqlite): %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	id, err := db.InsertAPIKeyWithOptions(ctx, APIKeyInput{
		Name: "window-only",
		Key:  "sk-reset-window-only-1234567890",
		Limits: APIKeyLimits{
			CostLimit5h: 1,
			CostLimit7d: 2,
		},
	})
	if err != nil {
		t.Fatalf("InsertAPIKeyWithOptions: %v", err)
	}
	oldAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	insertAPIKeyQuotaWindowUsage(t, db, id, oldAt, 100, 0.75)

	before, err := db.GetAPIKeyWindowUsage(ctx, id, 5*time.Hour)
	if err != nil || before.UserBilled != 0.75 || before.Tokens != 100 {
		t.Fatalf("5h usage before reset = %#v, err=%v", before, err)
	}
	if _, err := db.ResetAPIKeyQuota(ctx, id); err != nil {
		t.Fatalf("ResetAPIKeyQuota: %v", err)
	}

	for _, window := range []time.Duration{5 * time.Hour, 7 * 24 * time.Hour} {
		usage, err := db.GetAPIKeyWindowUsage(ctx, id, window)
		if err != nil {
			t.Fatalf("GetAPIKeyWindowUsage(%v): %v", window, err)
		}
		if usage.UserBilled != 0 || usage.Tokens != 0 || usage.Requests != 0 {
			t.Fatalf("usage after reset for %v = %#v, want zero", window, usage)
		}
		costs, err := db.GetAllAPIKeysWindowCost(ctx, window)
		if err != nil {
			t.Fatalf("GetAllAPIKeysWindowCost(%v): %v", window, err)
		}
		if costs[id] != 0 {
			t.Fatalf("batch cost after reset for %v = %v, want zero", window, costs[id])
		}
	}

	usage30d, err := db.GetAPIKeyWindowUsage(ctx, id, 30*24*time.Hour)
	if err != nil || usage30d.UserBilled != 0.75 || usage30d.Tokens != 100 {
		t.Fatalf("30d usage should retain history: %#v, err=%v", usage30d, err)
	}
	daily, err := db.GetAPIKeyUsageSince(ctx, id, time.Now().Add(-24*time.Hour))
	if err != nil || daily.UserBilled != 0.75 || daily.Tokens != 100 {
		t.Fatalf("daily usage should retain history: %#v, err=%v", daily, err)
	}

	row, err := db.GetAPIKeyByID(ctx, id)
	if err != nil {
		t.Fatalf("GetAPIKeyByID: %v", err)
	}
	insertAPIKeyQuotaWindowUsage(t, db, id, row.LastResetAt.Time.Add(time.Second), 25, 0.2)
	usage5h, err := db.GetAPIKeyWindowUsage(ctx, id, 5*time.Hour)
	if err != nil || usage5h.UserBilled != 0.2 || usage5h.Tokens != 25 || usage5h.Requests != 1 {
		t.Fatalf("5h usage after new request = %#v, err=%v", usage5h, err)
	}
}

func insertAPIKeyQuotaWindowUsage(t *testing.T, db *DB, apiKeyID int64, at time.Time, tokens int64, billed float64) {
	t.Helper()
	_, err := db.conn.Exec(`
		INSERT INTO usage_logs (api_key_id, status_code, total_tokens, user_billed, created_at)
		VALUES ($1, 200, $2, $3, $4)
	`, apiKeyID, tokens, billed, sqliteTimeParam(at))
	if err != nil {
		t.Fatalf("insert usage log: %v", err)
	}
}
