package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// AccountDailyUsage 是某账号某一天的官方结算用量快照。
//
// 上游 daily-workspace-usage-counts 只保留 7 天，过期即永久丢失，因此这里按天
// 落库累积长期历史。同一天的数值在结算前会变（当天不返回 token 明细），所以写入
// 是覆盖式 upsert，快照任务每轮回补整个保留窗口。
type AccountDailyUsage struct {
	AccountID int64  `json:"account_id"`
	Day       string `json:"day"`

	Credits float64 `json:"credits"`
	Users   int     `json:"users"`
	Threads int     `json:"threads"`
	Turns   int     `json:"turns"`

	UncachedInputTokens int64 `json:"uncached_input_tokens"`
	CachedInputTokens   int64 `json:"cached_input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	TotalTokens         int64 `json:"total_tokens"`

	// Settled 为 false 说明抓取时这天还没结算 token 明细（通常是当天）。
	// 下一轮快照回补同一天时会翻成 true。
	Settled bool `json:"settled"`

	// ClientsJSON / ModelsJSON 原样保存上游的拆分数组，前端直接渲染。
	ClientsJSON string    `json:"clients_json"`
	ModelsJSON  string    `json:"models_json"`
	SyncedAt    time.Time `json:"synced_at"`
}

// AccountDailyUsageInput 是一次 upsert 的入参。
type AccountDailyUsageInput struct {
	AccountID           int64
	Day                 string
	Credits             float64
	Users               int
	Threads             int
	Turns               int
	UncachedInputTokens int64
	CachedInputTokens   int64
	OutputTokens        int64
	TotalTokens         int64
	Settled             bool
	ClientsJSON         string
	ModelsJSON          string
}

var accountDailyUsageSchemaMu sync.Mutex

func (db *DB) ensureAccountDailyUsageTable(ctx context.Context) error {
	if db == nil {
		return errors.New("database unavailable")
	}
	accountDailyUsageSchemaMu.Lock()
	defer accountDailyUsageSchemaMu.Unlock()
	timeType := "TIMESTAMPTZ"
	boolType := "BOOLEAN"
	if db.isSQLite() {
		timeType = "TIMESTAMP"
		boolType = "INTEGER"
	}
	ddl := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS account_daily_usage (
		account_id BIGINT NOT NULL,
		day VARCHAR(10) NOT NULL,
		credits DOUBLE PRECISION NOT NULL DEFAULT 0,
		users INT NOT NULL DEFAULT 0,
		threads INT NOT NULL DEFAULT 0,
		turns INT NOT NULL DEFAULT 0,
		uncached_input_tokens BIGINT NOT NULL DEFAULT 0,
		cached_input_tokens BIGINT NOT NULL DEFAULT 0,
		output_tokens BIGINT NOT NULL DEFAULT 0,
		total_tokens BIGINT NOT NULL DEFAULT 0,
		settled %s NOT NULL DEFAULT FALSE,
		clients_json TEXT NOT NULL DEFAULT '[]',
		models_json TEXT NOT NULL DEFAULT '[]',
		synced_at %s NOT NULL,
		PRIMARY KEY (account_id, day)
	)`, boolType, timeType)
	if db.isSQLite() {
		ddl = strings.Replace(ddl, "DEFAULT FALSE", "DEFAULT 0", 1)
	}
	for _, statement := range []string{
		ddl,
		`CREATE INDEX IF NOT EXISTS idx_account_daily_usage_day ON account_daily_usage(day)`,
	} {
		if _, err := db.conn.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

// UpsertAccountDailyUsage 覆盖式写入一天的快照。上游同一天的数值会随结算变化，
// 所以固定覆盖而不是累加。
func (db *DB) UpsertAccountDailyUsage(ctx context.Context, input AccountDailyUsageInput) error {
	if err := db.ensureAccountDailyUsageTable(ctx); err != nil {
		return err
	}
	day := strings.TrimSpace(input.Day)
	if input.AccountID <= 0 || day == "" {
		return errors.New("daily usage requires account id and day")
	}
	clients := strings.TrimSpace(input.ClientsJSON)
	if clients == "" {
		clients = "[]"
	}
	models := strings.TrimSpace(input.ModelsJSON)
	if models == "" {
		models = "[]"
	}
	_, err := db.conn.ExecContext(ctx, `INSERT INTO account_daily_usage (
		account_id, day, credits, users, threads, turns,
		uncached_input_tokens, cached_input_tokens, output_tokens, total_tokens,
		settled, clients_json, models_json, synced_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
	ON CONFLICT (account_id, day) DO UPDATE SET
		credits=EXCLUDED.credits, users=EXCLUDED.users, threads=EXCLUDED.threads,
		turns=EXCLUDED.turns, uncached_input_tokens=EXCLUDED.uncached_input_tokens,
		cached_input_tokens=EXCLUDED.cached_input_tokens, output_tokens=EXCLUDED.output_tokens,
		total_tokens=EXCLUDED.total_tokens, settled=EXCLUDED.settled,
		clients_json=EXCLUDED.clients_json, models_json=EXCLUDED.models_json,
		synced_at=EXCLUDED.synced_at`,
		input.AccountID, day, input.Credits, input.Users, input.Threads, input.Turns,
		input.UncachedInputTokens, input.CachedInputTokens, input.OutputTokens, input.TotalTokens,
		input.Settled, clients, models, time.Now().UTC(),
	)
	return err
}

const accountDailyUsageSelect = `SELECT account_id, day, credits, users, threads, turns,
	uncached_input_tokens, cached_input_tokens, output_tokens, total_tokens,
	settled, clients_json, models_json, synced_at FROM account_daily_usage`

// ListAccountDailyUsage 返回某账号最近 days 天的快照，按日期升序。
func (db *DB) ListAccountDailyUsage(ctx context.Context, accountID int64, days int) ([]*AccountDailyUsage, error) {
	if err := db.ensureAccountDailyUsageTable(ctx); err != nil {
		return nil, err
	}
	if accountID <= 0 {
		return nil, errors.New("daily usage requires account id")
	}
	if days <= 0 {
		days = 30
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := db.conn.QueryContext(ctx,
		accountDailyUsageSelect+` WHERE account_id=$1 AND day>=$2 ORDER BY day ASC`,
		accountID, cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]*AccountDailyUsage, 0, days)
	for rows.Next() {
		item, scanErr := scanAccountDailyUsage(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// SumAccountDailyUsage 汇总某账号最近 days 天的 credits 与 token 总量，
// 供列表列与统计卡使用（不带客户端/模型拆分，避免解析 JSON）。
func (db *DB) SumAccountDailyUsage(ctx context.Context, accountIDs []int64, days int) (map[int64]AccountDailyUsageTotal, error) {
	if err := db.ensureAccountDailyUsageTable(ctx); err != nil {
		return nil, err
	}
	out := make(map[int64]AccountDailyUsageTotal, len(accountIDs))
	if len(accountIDs) == 0 {
		return out, nil
	}
	if days <= 0 {
		days = 7
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	placeholders := make([]string, 0, len(accountIDs))
	args := make([]any, 0, len(accountIDs)+1)
	args = append(args, cutoff)
	for i, id := range accountIDs {
		placeholders = append(placeholders, fmt.Sprintf("$%d", i+2))
		args = append(args, id)
	}
	query := fmt.Sprintf(`SELECT account_id, COALESCE(SUM(credits),0), COALESCE(SUM(total_tokens),0), COALESCE(SUM(turns),0)
		FROM account_daily_usage WHERE day>=$1 AND account_id IN (%s) GROUP BY account_id`,
		strings.Join(placeholders, ","))
	rows, err := db.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var total AccountDailyUsageTotal
		if err := rows.Scan(&id, &total.Credits, &total.TotalTokens, &total.Turns); err != nil {
			return nil, err
		}
		out[id] = total
	}
	return out, rows.Err()
}

// AccountDailyUsageTotal 是一个账号在窗口内的汇总。
type AccountDailyUsageTotal struct {
	Credits     float64 `json:"credits"`
	TotalTokens int64   `json:"total_tokens"`
	Turns       int64   `json:"turns"`
}

// PruneAccountDailyUsage 删除早于保留期的快照，避免表无限增长。
func (db *DB) PruneAccountDailyUsage(ctx context.Context, keepDays int) error {
	if err := db.ensureAccountDailyUsageTable(ctx); err != nil {
		return err
	}
	if keepDays <= 0 {
		return nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -keepDays).Format("2006-01-02")
	_, err := db.conn.ExecContext(ctx, `DELETE FROM account_daily_usage WHERE day<$1`, cutoff)
	return err
}

func scanAccountDailyUsage(scanner interface{ Scan(...any) error }) (*AccountDailyUsage, error) {
	item := &AccountDailyUsage{}
	var syncedAt any
	if err := scanner.Scan(
		&item.AccountID, &item.Day, &item.Credits, &item.Users, &item.Threads, &item.Turns,
		&item.UncachedInputTokens, &item.CachedInputTokens, &item.OutputTokens, &item.TotalTokens,
		&item.Settled, &item.ClientsJSON, &item.ModelsJSON, &syncedAt,
	); err != nil {
		return nil, err
	}
	parsed, err := parsePromptRiskTimeValue(syncedAt)
	if err != nil {
		return nil, err
	}
	item.SyncedAt = parsed
	return item, nil
}
