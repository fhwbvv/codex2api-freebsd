package database

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// BenchmarkPostgresFortyThousandAccountPage measures the two database phases
// used by the admin account page against a real PostgreSQL database:
//
//  1. the cold, non-secret 40k account projection;
//  2. the bounded current-page base/stat/usage/health queries (20 accounts).
//
// It requires an empty, disposable database in CODEX2API_TEST_POSTGRES_DSN.
// Ordinary `go test` runs do not execute benchmarks or create these fixtures.
func BenchmarkPostgresFortyThousandAccountPage(b *testing.B) {
	dsn := os.Getenv("CODEX2API_TEST_POSTGRES_DSN")
	if dsn == "" {
		b.Skip("CODEX2API_TEST_POSTGRES_DSN is not set")
	}

	db, err := New("postgres", dsn)
	if err != nil {
		b.Fatalf("open postgres: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	var accountCount, usageCount int64
	if err := db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts`).Scan(&accountCount); err != nil {
		b.Fatalf("count accounts: %v", err)
	}
	if err := db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_logs`).Scan(&usageCount); err != nil {
		b.Fatalf("count usage logs: %v", err)
	}
	if accountCount != 0 || usageCount != 0 {
		b.Fatalf("benchmark requires an empty disposable database (accounts=%d usage_logs=%d)", accountCount, usageCount)
	}

	marker := fmt.Sprintf("__codex2api_page_bench_%d_", time.Now().UnixNano())
	pattern := marker + "%"
	b.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cleanupCancel()
		_, _ = db.conn.ExecContext(cleanupCtx, `
			DELETE FROM usage_logs
			WHERE account_id IN (SELECT id FROM accounts WHERE name LIKE $1)`, pattern)
		_, _ = db.conn.ExecContext(cleanupCtx, `DELETE FROM account_group_members WHERE account_id IN (SELECT id FROM accounts WHERE name LIKE $1)`, pattern)
		_, _ = db.conn.ExecContext(cleanupCtx, `DELETE FROM accounts WHERE name LIKE $1`, pattern)
	})
	if _, err := db.conn.ExecContext(ctx, `
		INSERT INTO accounts (name, platform, type, credentials, status, enabled, locked, tags, created_at, updated_at)
		SELECT $1 || n::text,
			'openai', 'oauth',
			jsonb_build_object(
				'email', $1 || n::text || '@example.com',
				'plan_type', CASE WHEN n % 5 = 0 THEN 'team' ELSE 'plus' END,
				'refresh_token', 'configured'
			),
			'active', true, false,
			CASE WHEN n % 4 = 0 THEN '["bench"]'::jsonb ELSE '[]'::jsonb END,
			NOW() - (n % 86400) * INTERVAL '1 second', NOW()
		FROM generate_series(1, 40000) AS n`, marker); err != nil {
		b.Fatalf("insert account fixtures: %v", err)
	}
	if _, err := db.conn.ExecContext(ctx, `
		INSERT INTO usage_logs (
			account_id, endpoint, model, total_tokens, status_code, duration_ms,
			account_billed, user_billed, is_retry_attempt, created_at, channel
		)
		SELECT a.id, '/bench', 'gpt-5.4', 100,
			CASE WHEN sample % 17 = 0 THEN 429 ELSE 200 END,
			500, 0.001, 0.0012, false,
			NOW() - sample * INTERVAL '8 hours', 'codex'
		FROM accounts AS a
		CROSS JOIN generate_series(1, 20) AS sample
		WHERE a.name LIKE $1`, pattern); err != nil {
		b.Fatalf("insert usage fixtures: %v", err)
	}

	projection, err := db.ListAccountListProjection(ctx, UpstreamChannelCodex)
	if err != nil {
		b.Fatalf("load projection: %v", err)
	}
	if len(projection) != 40_000 {
		b.Fatalf("projection rows=%d, want 40000", len(projection))
	}
	pageIDs := make([]int64, 20)
	for index := range pageIDs {
		pageIDs[index] = projection[index].ID
	}

	b.Run("cold_projection_40000", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(40_000, "rows/op")
		for index := 0; index < b.N; index++ {
			rows, queryErr := db.ListAccountListProjection(context.Background(), UpstreamChannelCodex)
			if queryErr != nil || len(rows) != 40_000 {
				b.Fatalf("projection rows=%d err=%v", len(rows), queryErr)
			}
		}
	})

	fullIDs := make([]int64, len(projection))
	for index, row := range projection {
		fullIDs[index] = row.ID
	}
	b.Run("request_counts_full_pool_40000", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(40_000, "ids/op")
		for index := 0; index < b.N; index++ {
			counts, queryErr := db.GetAccountRequestCountsByIDs(context.Background(), fullIDs)
			if queryErr != nil {
				b.Fatal(queryErr)
			}
			if len(counts) != 40_000 {
				b.Fatalf("counts=%d, want 40000", len(counts))
			}
		}
	})

	b.Run("page_20_four_queries", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(4, "sql/op")
		for index := 0; index < b.N; index++ {
			queryCtx := context.Background()
			if _, queryErr := db.ListActiveByIDs(queryCtx, pageIDs); queryErr != nil {
				b.Fatal(queryErr)
			}
			if _, queryErr := db.GetAccountRequestCountsByIDs(queryCtx, pageIDs); queryErr != nil {
				b.Fatal(queryErr)
			}
			now := time.Now()
			if _, _, queryErr := db.GetAccountUsageWindowsByIDs(queryCtx, pageIDs, now.Add(-5*time.Hour), now.AddDate(0, 0, -7)); queryErr != nil {
				b.Fatal(queryErr)
			}
			if _, queryErr := db.GetAccountsHealthBucketsByIDs(queryCtx, pageIDs, now, 20, 10*time.Minute); queryErr != nil {
				b.Fatal(queryErr)
			}
		}
	})
}
