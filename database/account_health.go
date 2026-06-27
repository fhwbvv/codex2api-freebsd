package database

import (
	"context"
	"time"
)

// AccountHealthBucket 是单个时间窗口内某账号的请求成败计数。
type AccountHealthBucket struct {
	Success int `json:"success"`
	Failed  int `json:"failed"`
}

// GetAccountsHealthBuckets 返回每个账号最近 blockCount 个时间桶（由旧到新）的请求
// 成败计数，每个桶跨度 bucketDuration，整体覆盖 [now-blockCount*dur, now]。用于账号
// 管理页的「健康状态」条。
//
// 状态码 2xx 记为成功，其余记为失败；499（客户端主动取消）忽略，与图表聚合
// (getChartAggregation) 的口径保持一致。窗口很短（默认 ~3.3h），逐行拉取后在 Go
// 里分桶，避免 SQLite / Postgres 的时间分桶 SQL 差异。
func (db *DB) GetAccountsHealthBuckets(ctx context.Context, now time.Time, blockCount int, bucketDuration time.Duration) (map[int64][]AccountHealthBucket, error) {
	if blockCount < 1 {
		blockCount = 20
	}
	if bucketDuration <= 0 {
		bucketDuration = 10 * time.Minute
	}

	windowStart := now.Add(-time.Duration(blockCount) * bucketDuration)
	startArg, endArg := db.timeRangeArgs(windowStart, now)

	rows, err := db.conn.QueryContext(ctx, `
		SELECT account_id, created_at, status_code
		FROM usage_logs
		WHERE created_at >= $1 AND created_at <= $2
		  AND status_code <> 499
		  AND account_id > 0
	`, startArg, endArg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[int64][]AccountHealthBucket)

	for rows.Next() {
		var accountID int64
		var createdRaw interface{}
		var statusCode int
		if err := rows.Scan(&accountID, &createdRaw, &statusCode); err != nil {
			return nil, err
		}
		if accountID <= 0 {
			continue
		}
		createdAt, err := parseDBTimeValue(createdRaw)
		if err != nil || createdAt.IsZero() {
			continue
		}

		idx := int(createdAt.Sub(windowStart) / bucketDuration)
		if idx < 0 {
			idx = 0
		}
		if idx >= blockCount {
			idx = blockCount - 1
		}

		buckets, ok := result[accountID]
		if !ok {
			buckets = make([]AccountHealthBucket, blockCount)
			result[accountID] = buckets
		}
		if statusCode >= 200 && statusCode < 300 {
			buckets[idx].Success++
		} else {
			buckets[idx].Failed++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
