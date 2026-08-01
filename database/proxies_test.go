package database

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
)

func newProxyTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := New("sqlite", filepath.Join(t.TempDir(), "codex2api.db"))
	if err != nil {
		t.Fatalf("New(sqlite) returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func findProxyRow(t *testing.T, db *DB, id int64) *ProxyRow {
	t.Helper()
	rows, err := db.ListProxies(context.Background())
	if err != nil {
		t.Fatalf("ListProxies returned error: %v", err)
	}
	for _, row := range rows {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("proxy %d not found", id)
	return nil
}

func TestGetProxyReturnsRequestedRow(t *testing.T) {
	db := newProxyTestDB(t)
	ctx := context.Background()
	id, err := db.InsertProxy(ctx, "http://proxy.example:8080", "primary")
	if err != nil {
		t.Fatalf("InsertProxy returned error: %v", err)
	}

	row, err := db.GetProxy(ctx, id)
	if err != nil {
		t.Fatalf("GetProxy returned error: %v", err)
	}
	if row.ID != id || row.URL != "http://proxy.example:8080" || row.Label != "primary" {
		t.Fatalf("GetProxy returned %#v", row)
	}
}

func TestListProxiesByIDsReturnsOnlyRequestedRows(t *testing.T) {
	db := newProxyTestDB(t)
	ctx := context.Background()
	firstID, err := db.InsertProxy(ctx, "http://first.example:8080", "")
	if err != nil {
		t.Fatalf("InsertProxy(first) returned error: %v", err)
	}
	if _, err := db.InsertProxy(ctx, "http://second.example:8080", ""); err != nil {
		t.Fatalf("InsertProxy(second) returned error: %v", err)
	}
	thirdID, err := db.InsertProxy(ctx, "http://third.example:8080", "")
	if err != nil {
		t.Fatalf("InsertProxy(third) returned error: %v", err)
	}

	rows, err := db.ListProxiesByIDs(ctx, []int64{thirdID, firstID})
	if err != nil {
		t.Fatalf("ListProxiesByIDs returned error: %v", err)
	}
	if len(rows) != 2 || rows[0].ID != firstID || rows[1].ID != thirdID {
		t.Fatalf("ListProxiesByIDs returned %#v", rows)
	}
}

func TestProxyTestStatusLifecycleAndPoolFiltering(t *testing.T) {
	db := newProxyTestDB(t)
	ctx := context.Background()

	untestedID, err := db.InsertProxy(ctx, "http://untested.example:8080", "")
	if err != nil {
		t.Fatalf("InsertProxy(untested) returned error: %v", err)
	}
	successID, err := db.InsertProxy(ctx, "http://success.example:8080", "")
	if err != nil {
		t.Fatalf("InsertProxy(success) returned error: %v", err)
	}
	errorID, err := db.InsertProxy(ctx, "http://error.example:8080", "")
	if err != nil {
		t.Fatalf("InsertProxy(error) returned error: %v", err)
	}
	disabledID, err := db.InsertProxy(ctx, "http://disabled.example:8080", "")
	if err != nil {
		t.Fatalf("InsertProxy(disabled) returned error: %v", err)
	}

	if got := findProxyRow(t, db, untestedID).TestStatus; got != ProxyTestStatusUntested {
		t.Fatalf("new proxy test_status = %q, want %q", got, ProxyTestStatusUntested)
	}
	if err := db.UpdateProxyTestResult(ctx, successID, "http://success.example:8080", ProxyTestStatusSuccess, "1.2.3.4", "US", 123); err != nil {
		t.Fatalf("UpdateProxyTestResult(success) returned error: %v", err)
	}
	if err := db.UpdateProxyTestResult(ctx, errorID, "http://error.example:8080", ProxyTestStatusError, "stale-ip", "stale-location", 999); err != nil {
		t.Fatalf("UpdateProxyTestResult(error) returned error: %v", err)
	}
	disabled := false
	if err := db.UpdateProxy(ctx, disabledID, nil, nil, &disabled); err != nil {
		t.Fatalf("UpdateProxy(disabled) returned error: %v", err)
	}

	errorRow := findProxyRow(t, db, errorID)
	if errorRow.TestStatus != ProxyTestStatusError || errorRow.TestIP != "" || errorRow.TestLocation != "" || errorRow.TestLatencyMs != 0 {
		t.Fatalf("error proxy retained stale test data: %#v", errorRow)
	}

	enabledRows, err := db.ListEnabledProxies(ctx)
	if err != nil {
		t.Fatalf("ListEnabledProxies returned error: %v", err)
	}
	gotIDs := make(map[int64]bool, len(enabledRows))
	for _, row := range enabledRows {
		gotIDs[row.ID] = true
	}
	if !gotIDs[untestedID] || !gotIDs[successID] {
		t.Fatalf("enabled pool IDs = %v, want untested and success proxies", gotIDs)
	}
	if gotIDs[errorID] || gotIDs[disabledID] {
		t.Fatalf("enabled pool IDs = %v, must exclude error and disabled proxies", gotIDs)
	}
}

func TestUpdateProxyURLResetsTestStatusOnlyWhenURLChanges(t *testing.T) {
	db := newProxyTestDB(t)
	ctx := context.Background()

	id, err := db.InsertProxy(ctx, "http://old.example:8080", "")
	if err != nil {
		t.Fatalf("InsertProxy returned error: %v", err)
	}
	if err := db.UpdateProxyTestResult(ctx, id, "http://old.example:8080", ProxyTestStatusSuccess, "1.2.3.4", "US", 123); err != nil {
		t.Fatalf("UpdateProxyTestResult returned error: %v", err)
	}

	sameURL := "http://old.example:8080"
	label := "same URL"
	if err := db.UpdateProxy(ctx, id, &sameURL, &label, nil); err != nil {
		t.Fatalf("UpdateProxy(same URL) returned error: %v", err)
	}
	if got := findProxyRow(t, db, id); got.TestStatus != ProxyTestStatusSuccess || got.TestIP != "1.2.3.4" {
		t.Fatalf("same URL reset test result: %#v", got)
	}

	newURL := "http://new.example:8080"
	if err := db.UpdateProxy(ctx, id, &newURL, nil, nil); err != nil {
		t.Fatalf("UpdateProxy(new URL) returned error: %v", err)
	}
	got := findProxyRow(t, db, id)
	if got.TestStatus != ProxyTestStatusUntested || got.TestIP != "" || got.TestLocation != "" || got.TestLatencyMs != 0 {
		t.Fatalf("changed URL did not reset test result: %#v", got)
	}
}

func TestUpdateProxyNormalizesURLWhitespace(t *testing.T) {
	db := newProxyTestDB(t)
	ctx := context.Background()

	id, err := db.InsertProxy(ctx, "http://old.example:8080", "")
	if err != nil {
		t.Fatalf("InsertProxy returned error: %v", err)
	}
	urlValue := "  http://new.example:8080  "
	if err := db.UpdateProxy(ctx, id, &urlValue, nil, nil); err != nil {
		t.Fatalf("UpdateProxy returned error: %v", err)
	}

	if got := findProxyRow(t, db, id).URL; got != "http://new.example:8080" {
		t.Fatalf("stored URL = %q, want normalized URL", got)
	}
}

func TestCleanErrorProxiesDeletesAndUnbindsAtomically(t *testing.T) {
	db := newProxyTestDB(t)
	ctx := context.Background()

	errorURL1 := "http://error-one.example:8080"
	errorURL2 := "http://error-two.example:8080"
	healthyURL := "http://healthy.example:8080"
	errorID1, err := db.InsertProxy(ctx, errorURL1, "")
	if err != nil {
		t.Fatalf("InsertProxy(error one) returned error: %v", err)
	}
	errorID2, err := db.InsertProxy(ctx, errorURL2, "")
	if err != nil {
		t.Fatalf("InsertProxy(error two) returned error: %v", err)
	}
	healthyID, err := db.InsertProxy(ctx, healthyURL, "")
	if err != nil {
		t.Fatalf("InsertProxy(healthy) returned error: %v", err)
	}
	if err := db.UpdateProxyTestResult(ctx, errorID1, errorURL1, ProxyTestStatusError, "", "", 0); err != nil {
		t.Fatalf("mark error one: %v", err)
	}
	if err := db.UpdateProxyTestResult(ctx, errorID2, errorURL2, ProxyTestStatusError, "", "", 0); err != nil {
		t.Fatalf("mark error two: %v", err)
	}
	if err := db.UpdateProxyTestResult(ctx, healthyID, healthyURL, ProxyTestStatusSuccess, "1.2.3.4", "US", 100); err != nil {
		t.Fatalf("mark healthy: %v", err)
	}

	boundErrorOne, err := db.InsertAccount(ctx, "error-one", "rt-error-one", errorURL1)
	if err != nil {
		t.Fatalf("InsertAccount(error one) returned error: %v", err)
	}
	boundErrorTwo, err := db.InsertAccount(ctx, "error-two", "rt-error-two", "  "+errorURL2+"  ")
	if err != nil {
		t.Fatalf("InsertAccount(error two) returned error: %v", err)
	}
	boundHealthy, err := db.InsertAccount(ctx, "healthy", "rt-healthy", healthyURL)
	if err != nil {
		t.Fatalf("InsertAccount(healthy) returned error: %v", err)
	}

	result, err := db.CleanErrorProxies(ctx)
	if err != nil {
		t.Fatalf("CleanErrorProxies returned error: %v", err)
	}
	if result.Deleted != 2 || result.Unbound != 2 || len(result.UnboundAccountIDs) != 2 {
		t.Fatalf("cleanup result = %#v, want deleted=2 unbound=2", result)
	}
	if len(result.DeletedProxyURLs) != 2 ||
		!slices.Contains(result.DeletedProxyURLs, errorURL1) ||
		!slices.Contains(result.DeletedProxyURLs, errorURL2) {
		t.Fatalf("DeletedProxyURLs = %v, want both deleted proxy URLs", result.DeletedProxyURLs)
	}

	rows, err := db.ListProxies(ctx)
	if err != nil {
		t.Fatalf("ListProxies returned error: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != healthyID {
		t.Fatalf("remaining proxies = %#v, want only healthy proxy %d", rows, healthyID)
	}
	for _, accountID := range []int64{boundErrorOne, boundErrorTwo} {
		row, err := db.GetAccountByID(ctx, accountID)
		if err != nil {
			t.Fatalf("GetAccountByID(%d) returned error: %v", accountID, err)
		}
		if row.ProxyURL != "" {
			t.Fatalf("account %d proxy_url = %q, want empty", accountID, row.ProxyURL)
		}
	}
	healthyAccount, err := db.GetAccountByID(ctx, boundHealthy)
	if err != nil {
		t.Fatalf("GetAccountByID(healthy) returned error: %v", err)
	}
	if healthyAccount.ProxyURL != healthyURL {
		t.Fatalf("healthy account proxy_url = %q, want %q", healthyAccount.ProxyURL, healthyURL)
	}

	emptyResult, err := db.CleanErrorProxies(ctx)
	if err != nil {
		t.Fatalf("second CleanErrorProxies returned error: %v", err)
	}
	if emptyResult.Deleted != 0 || emptyResult.Unbound != 0 || len(emptyResult.UnboundAccountIDs) != 0 {
		t.Fatalf("second cleanup result = %#v, want zero-value result", emptyResult)
	}
}

func TestCleanErrorProxiesUsesStableProxySnapshot(t *testing.T) {
	db := newProxyTestDB(t)
	ctx := context.Background()
	const (
		errorURL = "http://error.example:8080"
		lateURL  = "http://late-error.example:8080"
	)

	errorID, err := db.InsertProxy(ctx, errorURL, "")
	if err != nil {
		t.Fatalf("InsertProxy(error) returned error: %v", err)
	}
	lateID, err := db.InsertProxy(ctx, lateURL, "")
	if err != nil {
		t.Fatalf("InsertProxy(late error) returned error: %v", err)
	}
	if err := db.UpdateProxyTestResult(ctx, errorID, errorURL, ProxyTestStatusError, "", "", 0); err != nil {
		t.Fatalf("mark initial proxy error: %v", err)
	}
	if err := db.UpdateProxyTestResult(ctx, lateID, lateURL, ProxyTestStatusSuccess, "1.2.3.4", "US", 100); err != nil {
		t.Fatalf("mark late proxy healthy: %v", err)
	}
	if _, err := db.InsertAccount(ctx, "bound", "rt-bound", errorURL); err != nil {
		t.Fatalf("InsertAccount returned error: %v", err)
	}

	trigger := fmt.Sprintf(`
		CREATE TRIGGER mark_late_proxy_error_after_unbind
		AFTER UPDATE OF proxy_url ON accounts
		WHEN OLD.proxy_url = %q AND NEW.proxy_url = ''
		BEGIN
			UPDATE proxies SET test_status = 'error' WHERE id = %d;
		END
	`, errorURL, lateID)
	if _, err := db.conn.ExecContext(ctx, trigger); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	result, err := db.CleanErrorProxies(ctx)
	if err != nil {
		t.Fatalf("CleanErrorProxies returned error: %v", err)
	}
	if result.Deleted != 1 {
		t.Fatalf("Deleted = %d, want only the 1 proxy captured at cleanup start", result.Deleted)
	}
	lateRow := findProxyRow(t, db, lateID)
	if lateRow.TestStatus != ProxyTestStatusError {
		t.Fatalf("late proxy status = %q, want error and retained for next cleanup", lateRow.TestStatus)
	}
}

func TestCleanErrorProxiesReturnsOnlyActuallyUnboundAccounts(t *testing.T) {
	db := newProxyTestDB(t)
	ctx := context.Background()
	const errorURL = "http://error.example:8080"

	errorID, err := db.InsertProxy(ctx, errorURL, "")
	if err != nil {
		t.Fatalf("InsertProxy returned error: %v", err)
	}
	if err := db.UpdateProxyTestResult(ctx, errorID, errorURL, ProxyTestStatusError, "", "", 0); err != nil {
		t.Fatalf("mark proxy error: %v", err)
	}
	unboundID, err := db.InsertAccount(ctx, "unbound", "rt-unbound", errorURL)
	if err != nil {
		t.Fatalf("InsertAccount(unbound) returned error: %v", err)
	}
	protectedID, err := db.InsertAccount(ctx, "protected", "rt-protected", errorURL)
	if err != nil {
		t.Fatalf("InsertAccount(protected) returned error: %v", err)
	}

	trigger := fmt.Sprintf(`
		CREATE TRIGGER ignore_protected_proxy_unbind
		BEFORE UPDATE OF proxy_url ON accounts
		WHEN OLD.id = %d
		BEGIN
			SELECT RAISE(IGNORE);
		END
	`, protectedID)
	if _, err := db.conn.ExecContext(ctx, trigger); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	result, err := db.CleanErrorProxies(ctx)
	if err != nil {
		t.Fatalf("CleanErrorProxies returned error: %v", err)
	}
	if result.Unbound != 1 {
		t.Fatalf("Unbound = %d, want 1", result.Unbound)
	}
	if len(result.UnboundAccountIDs) != 1 || result.UnboundAccountIDs[0] != unboundID {
		t.Fatalf("UnboundAccountIDs = %v, want only actually updated account %d", result.UnboundAccountIDs, unboundID)
	}
}

func TestSQLiteProxyStatusMigrationBackfillsExistingTestData(t *testing.T) {
	db := newProxyTestDB(t)
	ctx := context.Background()

	id, err := db.InsertProxy(ctx, "http://migrated.example:8080", "")
	if err != nil {
		t.Fatalf("InsertProxy returned error: %v", err)
	}
	if _, err := db.conn.ExecContext(ctx, `
		UPDATE proxies
		SET test_status = 'untested', test_ip = '1.2.3.4', test_location = 'US', test_latency_ms = 123
		WHERE id = $1
	`, id); err != nil {
		t.Fatalf("seed pre-migration proxy result: %v", err)
	}

	if err := db.migrateSQLite(ctx); err != nil {
		t.Fatalf("migrateSQLite returned error: %v", err)
	}
	if got := findProxyRow(t, db, id).TestStatus; got != ProxyTestStatusSuccess {
		t.Fatalf("migrated test_status = %q, want %q", got, ProxyTestStatusSuccess)
	}
}
