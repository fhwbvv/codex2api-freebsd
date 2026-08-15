package database

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestOfficialPricingSyncConfigLifecycleSQLite(t *testing.T) {
	db, err := New("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("New sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	initial, err := db.GetOfficialPricingSyncConfig(ctx)
	if err != nil {
		t.Fatalf("Get initial config: %v", err)
	}
	if initial.Enabled || initial.IntervalMinutes != 1440 || !initial.IncludeOpenAI || !initial.IncludeGrok {
		t.Fatalf("unexpected initial config: %+v", initial)
	}

	updated, err := db.UpdateOfficialPricingSyncConfig(ctx, OfficialPricingSyncConfig{
		Enabled:         true,
		IntervalMinutes: 360,
		IncludeOpenAI:   true,
		IncludeGrok:     false,
	})
	if err != nil {
		t.Fatalf("Update config: %v", err)
	}
	if !updated.Enabled || updated.IntervalMinutes != 360 || !updated.IncludeOpenAI || updated.IncludeGrok {
		t.Fatalf("unexpected updated config: %+v", updated)
	}

	attempted := time.Now().UTC().Truncate(time.Millisecond)
	if err := db.RecordOfficialPricingSyncResult(ctx, attempted, errors.New("temporary upstream error"), []string{"grok warning"}); err != nil {
		t.Fatalf("Record failed result: %v", err)
	}
	failed, err := db.GetOfficialPricingSyncConfig(ctx)
	if err != nil {
		t.Fatalf("Get failed result: %v", err)
	}
	if !failed.LastAttemptAt.Valid || failed.LastSuccessAt.Valid || failed.LastError == "" || failed.LastWarning != "grok warning" {
		t.Fatalf("unexpected failed state: %+v", failed)
	}

	if err := db.RecordOfficialPricingSyncResult(ctx, attempted.Add(time.Minute), nil, nil); err != nil {
		t.Fatalf("Record successful result: %v", err)
	}
	succeeded, err := db.GetOfficialPricingSyncConfig(ctx)
	if err != nil {
		t.Fatalf("Get successful result: %v", err)
	}
	if !succeeded.LastSuccessAt.Valid || succeeded.LastError != "" || succeeded.LastWarning != "" {
		t.Fatalf("unexpected successful state: %+v", succeeded)
	}
}

func TestOfficialPricingSyncConfigRejectsEmptySources(t *testing.T) {
	db, err := New("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("New sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.UpdateOfficialPricingSyncConfig(context.Background(), OfficialPricingSyncConfig{
		IntervalMinutes: 1440,
	})
	if err == nil {
		t.Fatal("expected empty official sources to be rejected")
	}
}
