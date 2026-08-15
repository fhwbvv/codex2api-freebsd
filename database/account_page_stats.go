package database

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// GetAccountRequestCountsByIDs returns the same seven-day counters as
// GetAccountRequestCounts, but restricts the scan to the visible account page.
func (db *DB) GetAccountRequestCountsByIDs(ctx context.Context, ids []int64) (map[int64]*AccountRequestCount, error) {
	result := make(map[int64]*AccountRequestCount, len(ids))
	ids = positiveUniqueIDs(ids)
	if len(ids) == 0 {
		return result, nil
	}
	retryFalse := "COALESCE(is_retry_attempt, false) = false"
	retryTrue := "COALESCE(is_retry_attempt, false) = true"
	if db.isSQLite() {
		retryFalse = "COALESCE(is_retry_attempt, 0) = 0"
		retryTrue = "COALESCE(is_retry_attempt, 0) = 1"
	}
	args := make([]interface{}, 0, len(ids)+1)
	args = append(args, db.timeArg(time.Now().AddDate(0, 0, -7)))
	placeholders := make([]string, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}
	query := fmt.Sprintf(`
		SELECT account_id,
			COALESCE(SUM(CASE WHEN status_code < 400 AND %s THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status_code >= 400 AND %s THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status_code >= 400 AND %s THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status_code = 429 THEN 1 ELSE 0 END), 0)
		FROM usage_logs
		WHERE created_at >= $1 AND %s AND account_id IN (%s)
		GROUP BY account_id`, retryFalse, retryFalse, retryTrue, db.endUserUsageLogPredicate(), strings.Join(placeholders, ","))
	rows, err := db.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		value := &AccountRequestCount{}
		if err := rows.Scan(&value.AccountID, &value.SuccessCount, &value.ErrorCount, &value.RetryErrorCount, &value.RateLimitAttemptCount); err != nil {
			return nil, err
		}
		result[value.AccountID] = value
	}
	return result, rows.Err()
}

// GetAccountUsageWindowsByIDs computes the list-page usage fields without a
// global seven-day GROUP BY.
func (db *DB) GetAccountUsageWindowsByIDs(ctx context.Context, ids []int64, shortSince, longSince time.Time) (map[int64]*AccountTimeRangeUsage, map[int64]*AccountTimeRangeUsage, error) {
	shortWindow := make(map[int64]*AccountTimeRangeUsage, len(ids))
	longWindow := make(map[int64]*AccountTimeRangeUsage, len(ids))
	ids = positiveUniqueIDs(ids)
	if len(ids) == 0 {
		return shortWindow, longWindow, nil
	}
	if shortSince.Before(longSince) {
		shortSince, longSince = longSince, shortSince
	}
	args := []interface{}{db.timeArg(shortSince), db.timeArg(longSince)}
	placeholders := make([]string, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}
	query := fmt.Sprintf(`SELECT account_id,
		COALESCE(SUM(CASE WHEN created_at >= $1 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN created_at >= $1 THEN total_tokens ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN created_at >= $1 THEN account_billed ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN created_at >= $1 THEN user_billed ELSE 0 END), 0),
		COUNT(*), COALESCE(SUM(total_tokens), 0), COALESCE(SUM(account_billed), 0), COALESCE(SUM(user_billed), 0)
		FROM usage_logs
		WHERE created_at >= $2 AND status_code <> 499 AND %s AND %s AND %s AND account_id IN (%s)
		GROUP BY account_id`, db.nonRetryUsageLogPredicate(), db.currentAccountUsageGenerationPredicate(), db.endUserUsageLogPredicate(), strings.Join(placeholders, ","))
	rows, err := db.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		shortUsage := &AccountTimeRangeUsage{}
		longUsage := &AccountTimeRangeUsage{}
		if err := rows.Scan(&shortUsage.AccountID, &shortUsage.Requests, &shortUsage.Tokens, &shortUsage.AccountBilled, &shortUsage.UserBilled,
			&longUsage.Requests, &longUsage.Tokens, &longUsage.AccountBilled, &longUsage.UserBilled); err != nil {
			return nil, nil, err
		}
		longUsage.AccountID = shortUsage.AccountID
		shortWindow[shortUsage.AccountID] = shortUsage
		longWindow[longUsage.AccountID] = longUsage
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return shortWindow, longWindow, nil
}

// GetAccountUsageSinceByIDs aggregates requests/tokens/billing for the given
// accounts since the provided instant. It powers the list-page "today" column
// with the same filtering semantics as the 5h/7d usage windows.
func (db *DB) GetAccountUsageSinceByIDs(ctx context.Context, ids []int64, since time.Time) (map[int64]*AccountTimeRangeUsage, error) {
	result := make(map[int64]*AccountTimeRangeUsage, len(ids))
	ids = positiveUniqueIDs(ids)
	if len(ids) == 0 {
		return result, nil
	}
	args := []interface{}{db.timeArg(since)}
	placeholders := make([]string, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}
	query := fmt.Sprintf(`SELECT account_id,
		COUNT(*), COALESCE(SUM(total_tokens), 0), COALESCE(SUM(account_billed), 0), COALESCE(SUM(user_billed), 0)
		FROM usage_logs
		WHERE created_at >= $1 AND status_code <> 499 AND %s AND %s AND account_id IN (%s)
		GROUP BY account_id`, db.nonRetryUsageLogPredicate(), db.currentAccountUsageGenerationPredicate(), strings.Join(placeholders, ","))
	rows, err := db.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		usage := &AccountTimeRangeUsage{}
		if err := rows.Scan(&usage.AccountID, &usage.Requests, &usage.Tokens, &usage.AccountBilled, &usage.UserBilled); err != nil {
			return nil, err
		}
		result[usage.AccountID] = usage
	}
	return result, rows.Err()
}

func positiveUniqueIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}
