package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// APIKeyScopeCounter 是「某 Key × 某分组/账号」的累计用量计数器(issue #439 v2)。
//
// 与滑动窗口限额不同,累计额度不会随时间自动回落——用完必须手动重置,语义对齐
// api_keys.quota_limit / quota_used / reset_count / last_reset_at。
// 上限本身仍配在 limits.scope_limits 里(QuotaCost),这张表只存已用量与重置状态,
// 避免同一份配置存两处产生漂移。
type APIKeyScopeCounter struct {
	APIKeyID     int64        `json:"api_key_id"`
	ScopeType    string       `json:"scope_type"`
	ScopeID      int64        `json:"scope_id"`
	UsedCost     float64      `json:"used_cost"`
	UsedTokens   int64        `json:"used_tokens"`
	UsedRequests int64        `json:"used_requests"`
	ResetCount   int          `json:"reset_count"`
	LastResetAt  sql.NullTime `json:"last_reset_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

// APIKeyScopeCounterKey 唯一定位一条计数器。
type APIKeyScopeCounterKey struct {
	ScopeType string
	ScopeID   int64
}

// ListAPIKeyScopeCounters 返回某 Key 的全部累计计数器,按 (scope_type, scope_id) 索引。
func (db *DB) ListAPIKeyScopeCounters(ctx context.Context, apiKeyID int64) (map[APIKeyScopeCounterKey]APIKeyScopeCounter, error) {
	out := make(map[APIKeyScopeCounterKey]APIKeyScopeCounter)
	if db == nil || apiKeyID <= 0 {
		return out, nil
	}
	rows, err := db.conn.QueryContext(ctx, `
		SELECT api_key_id, scope_type, scope_id,
		       COALESCE(used_cost, 0), COALESCE(used_tokens, 0), COALESCE(used_requests, 0),
		       COALESCE(reset_count, 0), last_reset_at, updated_at
		FROM api_key_scope_counters
		WHERE api_key_id = $1
	`, apiKeyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var row APIKeyScopeCounter
		var lastResetRaw, updatedRaw interface{}
		if err := rows.Scan(
			&row.APIKeyID, &row.ScopeType, &row.ScopeID,
			&row.UsedCost, &row.UsedTokens, &row.UsedRequests,
			&row.ResetCount, &lastResetRaw, &updatedRaw,
		); err != nil {
			return nil, err
		}
		lastReset, err := parseDBNullTimeValue(lastResetRaw)
		if err != nil {
			return nil, err
		}
		updatedAt, err := parseDBTimeValue(updatedRaw)
		if err != nil {
			return nil, err
		}
		row.LastResetAt = lastReset
		row.UpdatedAt = updatedAt
		out[APIKeyScopeCounterKey{ScopeType: row.ScopeType, ScopeID: row.ScopeID}] = row
	}
	return out, rows.Err()
}

// ListAPIKeyScopeCountersForKeys 是 ListAPIKeyScopeCounters 的批量版本，供列表页概览
// 一次拿到多个 Key 的累计计数器。
func (db *DB) ListAPIKeyScopeCountersForKeys(ctx context.Context, apiKeyIDs []int64) (map[int64]map[APIKeyScopeCounterKey]APIKeyScopeCounter, error) {
	out := make(map[int64]map[APIKeyScopeCounterKey]APIKeyScopeCounter)
	if db == nil || len(apiKeyIDs) == 0 {
		return out, nil
	}
	placeholders := make([]string, 0, len(apiKeyIDs))
	args := make([]interface{}, 0, len(apiKeyIDs))
	for i, id := range apiKeyIDs {
		if db.isSQLite() {
			placeholders = append(placeholders, "?")
		} else {
			placeholders = append(placeholders, fmt.Sprintf("$%d", i+1))
		}
		args = append(args, id)
	}
	query := fmt.Sprintf(`
		SELECT api_key_id, scope_type, scope_id,
		       COALESCE(used_cost, 0), COALESCE(used_tokens, 0), COALESCE(used_requests, 0),
		       COALESCE(reset_count, 0)
		FROM api_key_scope_counters
		WHERE api_key_id IN (%s)
	`, strings.Join(placeholders, ","))
	rows, err := db.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var row APIKeyScopeCounter
		if err := rows.Scan(&row.APIKeyID, &row.ScopeType, &row.ScopeID,
			&row.UsedCost, &row.UsedTokens, &row.UsedRequests, &row.ResetCount); err != nil {
			return nil, err
		}
		if out[row.APIKeyID] == nil {
			out[row.APIKeyID] = make(map[APIKeyScopeCounterKey]APIKeyScopeCounter)
		}
		out[row.APIKeyID][APIKeyScopeCounterKey{ScopeType: row.ScopeType, ScopeID: row.ScopeID}] = row
	}
	return out, rows.Err()
}

// ResetAPIKeyScopeCounter 把某条累计计数器清零并记一次重置。计数器行不存在时补一条,
// 使「重置」在还没产生任何用量时也是幂等的成功操作。
func (db *DB) ResetAPIKeyScopeCounter(ctx context.Context, apiKeyID int64, scopeType string, scopeID int64) error {
	if db == nil || apiKeyID <= 0 || scopeID <= 0 {
		return sql.ErrNoRows
	}
	scopeType = strings.ToLower(strings.TrimSpace(scopeType))
	if scopeType != APIKeyScopeTypeGroup && scopeType != APIKeyScopeTypeAccount {
		return fmt.Errorf("未知的 scope_type: %q", scopeType)
	}
	query := `
		INSERT INTO api_key_scope_counters (api_key_id, scope_type, scope_id, used_cost, used_tokens, used_requests, reset_count, last_reset_at, updated_at)
		VALUES ($1, $2, $3, 0, 0, 0, 1, ` + db.nowExpr() + `, ` + db.nowExpr() + `)
		ON CONFLICT (api_key_id, scope_type, scope_id) DO UPDATE SET
			used_cost = 0,
			used_tokens = 0,
			used_requests = 0,
			reset_count = COALESCE(api_key_scope_counters.reset_count, 0) + 1,
			last_reset_at = ` + db.nowExpr() + `,
			updated_at = ` + db.nowExpr()
	_, err := db.conn.ExecContext(ctx, query, apiKeyID, scopeType, scopeID)
	return err
}

// nowExpr 返回当前时间的 SQL 表达式（两种驱动的写法不同）。
func (db *DB) nowExpr() string {
	if db.isSQLite() {
		return "CURRENT_TIMESTAMP"
	}
	return "NOW()"
}

// scopeCounterDelta 是一批日志折算出的累计增量。
type scopeCounterDelta struct {
	cost     float64
	tokens   int64
	requests int64
}

// applyAPIKeyScopeCountersWithExec 在批量落库的同一事务里累加 scope 累计计数器。
// 只为「确实配了累计额度的 Key」记账（见 scopeQuotaKeyIDs 的 60s 缓存），其余 Key 零开销。
//
// 分组维度不在 Go 侧解析成员关系，而是让 SQL join account_group_members：账号可能属于多个
// 分组，join 天然把一笔消耗记到它当前所属的每个分组上，也不必把内存里的分组关系带进 DB 层。
func (db *DB) applyAPIKeyScopeCountersWithExec(ctx context.Context, execer sqlExecer, batch []usageLogEntry) error {
	if db == nil || execer == nil || len(batch) == 0 {
		return nil
	}
	quotaKeys := db.scopeQuotaKeyIDs(ctx)
	if len(quotaKeys) == 0 {
		return nil
	}

	type counterKey struct {
		apiKeyID  int64
		accountID int64
	}
	deltas := make(map[counterKey]*scopeCounterDelta)
	for _, entry := range batch {
		if entry.APIKeyID <= 0 || entry.AccountID <= 0 || entry.StatusCode == 499 {
			continue
		}
		if _, ok := quotaKeys[entry.APIKeyID]; !ok {
			continue
		}
		key := counterKey{apiKeyID: entry.APIKeyID, accountID: entry.AccountID}
		delta := deltas[key]
		if delta == nil {
			delta = &scopeCounterDelta{}
			deltas[key] = delta
		}
		delta.cost += entry.UserBilled
		delta.tokens += int64(entry.TotalTokens)
		delta.requests++
	}
	if len(deltas) == 0 {
		return nil
	}

	accountQuery := `
		INSERT INTO api_key_scope_counters (api_key_id, scope_type, scope_id, used_cost, used_tokens, used_requests, updated_at)
		VALUES ($1, '` + APIKeyScopeTypeAccount + `', $2, $3, $4, $5, ` + db.nowExpr() + `)
		ON CONFLICT (api_key_id, scope_type, scope_id) DO UPDATE SET
			used_cost = COALESCE(api_key_scope_counters.used_cost, 0) + $3,
			used_tokens = COALESCE(api_key_scope_counters.used_tokens, 0) + $4,
			used_requests = COALESCE(api_key_scope_counters.used_requests, 0) + $5,
			updated_at = ` + db.nowExpr()
	groupQuery := `
		INSERT INTO api_key_scope_counters (api_key_id, scope_type, scope_id, used_cost, used_tokens, used_requests, updated_at)
		SELECT $1, '` + APIKeyScopeTypeGroup + `', m.group_id, $3, $4, $5, ` + db.nowExpr() + `
		FROM account_group_members m
		WHERE m.account_id = $2
		ON CONFLICT (api_key_id, scope_type, scope_id) DO UPDATE SET
			used_cost = COALESCE(api_key_scope_counters.used_cost, 0) + $3,
			used_tokens = COALESCE(api_key_scope_counters.used_tokens, 0) + $4,
			used_requests = COALESCE(api_key_scope_counters.used_requests, 0) + $5,
			updated_at = ` + db.nowExpr()

	for key, delta := range deltas {
		if delta.cost <= 0 && delta.tokens <= 0 && delta.requests <= 0 {
			continue
		}
		args := []interface{}{key.apiKeyID, key.accountID, delta.cost, delta.tokens, delta.requests}
		if _, err := execer.ExecContext(ctx, accountQuery, args...); err != nil {
			return err
		}
		if _, err := execer.ExecContext(ctx, groupQuery, args...); err != nil {
			return err
		}
	}
	return nil
}

// scopeQuotaKeyIDs 返回配了累计额度的 API Key 集合，带 60s 缓存。
// 落库热路径上不能每批都全表解析 limits JSON；配置变更最迟 60s 后生效，
// 期间少记的用量在下一批就会补上（计数器是累加的，不会丢）。
func (db *DB) scopeQuotaKeyIDs(ctx context.Context) map[int64]struct{} {
	db.scopeQuotaMu.Lock()
	if db.scopeQuotaKeys != nil && time.Now().Before(db.scopeQuotaExpiresAt) {
		cached := db.scopeQuotaKeys
		db.scopeQuotaMu.Unlock()
		return cached
	}
	db.scopeQuotaMu.Unlock()

	keys, err := db.ListAPIKeys(ctx)
	if err != nil {
		return nil
	}
	fresh := make(map[int64]struct{})
	for _, key := range keys {
		for _, scope := range key.Limits.ScopeLimits {
			if scope.QuotaCost > 0 || scope.QuotaTokens > 0 || scope.QuotaRequests > 0 {
				fresh[key.ID] = struct{}{}
				break
			}
		}
	}
	db.scopeQuotaMu.Lock()
	db.scopeQuotaKeys = fresh
	db.scopeQuotaExpiresAt = time.Now().Add(time.Minute)
	db.scopeQuotaMu.Unlock()
	return fresh
}

// InvalidateScopeQuotaKeyCache 让下一次记账立刻重新解析哪些 Key 配了累计额度。
// 管理端保存 Key 后调用，避免新配的累计额度要等一个缓存周期才开始记账。
func (db *DB) InvalidateScopeQuotaKeyCache() {
	if db == nil {
		return
	}
	db.scopeQuotaMu.Lock()
	db.scopeQuotaKeys = nil
	db.scopeQuotaExpiresAt = time.Time{}
	db.scopeQuotaMu.Unlock()
}
