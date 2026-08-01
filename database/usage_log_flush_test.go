package database

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lib/pq"
)

// insertUsageLogs 往缓冲里塞 count 条最简用量日志。
func insertUsageLogs(t *testing.T, db *DB, count int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < count; i++ {
		if err := db.InsertUsageLog(ctx, &UsageLogInput{
			Endpoint:    "/v1/responses",
			Model:       "gpt-5.4",
			StatusCode:  200,
			TotalTokens: 10,
		}); err != nil {
			t.Fatalf("InsertUsageLog(%d) 返回错误: %v", i, err)
		}
	}
}

// TestFlushUsageLogsDrainsBeyondOneBatch 覆盖 flush 的分批语义：flushLogs 每次只取
// usage_log_batch_size 条，FlushUsageLogs 必须循环刷完整个缓冲。
func TestFlushUsageLogsDrainsBeyondOneBatch(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "codex2api.db")
	db, err := New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("New(sqlite) 返回错误: %v", err)
	}
	// 停掉后台 flusher，让刷新时机完全由用例控制。
	close(db.logStop)
	db.logWg.Wait()
	defer db.conn.Close()

	const batchSize = 10
	const total = 35
	db.SetUsageLogConfig(UsageLogModeFull, batchSize, maxUsageLogFlushIntervalSeconds)
	insertUsageLogs(t, db, total)

	db.flushLogs()
	logs, err := db.ListRecentUsageLogs(context.Background(), total*2)
	if err != nil {
		t.Fatalf("ListRecentUsageLogs 返回错误: %v", err)
	}
	if len(logs) != batchSize {
		t.Fatalf("flushLogs 后落库 %d 条，want %d（单次只应刷一个批次）", len(logs), batchSize)
	}

	db.FlushUsageLogs()
	logs, err = db.ListRecentUsageLogs(context.Background(), total*2)
	if err != nil {
		t.Fatalf("ListRecentUsageLogs 返回错误: %v", err)
	}
	if len(logs) != total {
		t.Fatalf("FlushUsageLogs 后落库 %d 条，want %d", len(logs), total)
	}
	if stats := db.GetUsageLogRuntimeStats(); stats.BufferLength != 0 {
		t.Fatalf("BufferLength = %d，want 0", stats.BufferLength)
	}
}

// TestCloseFlushesEntireUsageLogBuffer 覆盖优雅关闭：Close 会先停掉后台 flusher，
// 此时再走「只刷一个批次 + notifyLogFlush」的路径没人消费信号，超出一个批次的日志
// 会被静默丢弃，所以收尾必须刷完整个缓冲。
func TestCloseFlushesEntireUsageLogBuffer(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "codex2api.db")
	db, err := New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("New(sqlite) 返回错误: %v", err)
	}

	const total = 35
	db.SetUsageLogConfig(UsageLogModeFull, maxUsageLogBatchSize, maxUsageLogFlushIntervalSeconds)
	insertUsageLogs(t, db, total)
	db.SetUsageLogConfig(UsageLogModeFull, 10, maxUsageLogFlushIntervalSeconds)

	if err := db.Close(); err != nil {
		t.Fatalf("Close 返回错误: %v", err)
	}

	reopened, err := New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("重新打开数据库返回错误: %v", err)
	}
	defer reopened.Close()

	logs, err := reopened.ListRecentUsageLogs(context.Background(), total*2)
	if err != nil {
		t.Fatalf("ListRecentUsageLogs 返回错误: %v", err)
	}
	if len(logs) != total {
		t.Fatalf("Close 后落库 %d 条，want %d（缓冲里的日志被丢了）", len(logs), total)
	}
}

// TestUsageLogTextClampedToColumnWidth 覆盖超长字段截断：reasoning_effort / service_tier
// 等直接来自下游请求体，超过列宽会让整条批量 INSERT 回滚，失败批次又被放回缓冲区头部，
// 一条脏数据就能永久堵死日志写入。
func TestUsageLogTextClampedToColumnWidth(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "codex2api.db")
	db, err := New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("New(sqlite) 返回错误: %v", err)
	}
	defer db.Close()

	long := strings.Repeat("x", 300)
	if err := db.InsertUsageLog(context.Background(), &UsageLogInput{
		Endpoint:             long,
		Model:                long,
		EffectiveModel:       long,
		ReasoningEffort:      long,
		ServiceTier:          long,
		RequestedServiceTier: long,
		ActualServiceTier:    long,
		Channel:              long,
		StatusCode:           200,
	}); err != nil {
		t.Fatalf("InsertUsageLog 返回错误: %v", err)
	}
	db.FlushUsageLogs()

	logs, err := db.ListRecentUsageLogs(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListRecentUsageLogs 返回错误: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("len(logs) = %d, want 1", len(logs))
	}
	got := logs[0]
	for _, tc := range []struct {
		name  string
		value string
		max   int
	}{
		{"endpoint", got.Endpoint, usageLogTextMaxLen},
		{"model", got.Model, usageLogTextMaxLen},
		{"effective_model", got.EffectiveModel, usageLogTextMaxLen},
		{"reasoning_effort", got.ReasoningEffort, usageLogTextMaxLen},
		{"service_tier", got.ServiceTier, usageLogTextMaxLen},
		{"requested_service_tier", got.RequestedServiceTier, usageLogTextMaxLen},
		{"actual_service_tier", got.ActualServiceTier, usageLogTextMaxLen},
		{"channel", got.Channel, usageLogChannelMaxLen},
	} {
		if len(tc.value) != tc.max {
			t.Fatalf("%s 长度 = %d，want %d", tc.name, len(tc.value), tc.max)
		}
	}
}

func TestClampUsageLogText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"未超长原样返回", "high", 100, "high"},
		{"刚好等于上限", "abcd", 4, "abcd"},
		{"按字符截断", "abcdef", 4, "abcd"},
		{"多字节按字符而非字节截断", "思考强度很高", 3, "思考强"},
		{"上限非正数不截断", "abc", 0, "abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampUsageLogText(tc.in, tc.max); got != tc.want {
				t.Fatalf("clampUsageLogText(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

// TestUsageLogInsertRowsStayUnderPostgresBindLimit 锁住每条 INSERT 的行数上限：
// 行数 * 每行列数必须低于 PostgreSQL 的 65535 个 bind 参数。
func TestUsageLogInsertRowsStayUnderPostgresBindLimit(t *testing.T) {
	if maxUsageLogInsertRowsPerSQL*usageLogInsertColumnCount > postgresMaxBindParams {
		t.Fatalf("单条 INSERT 参数数 = %d，超过 PostgreSQL 上限 %d",
			maxUsageLogInsertRowsPerSQL*usageLogInsertColumnCount, postgresMaxBindParams)
	}
	if maxUsageLogBatchSize*usageLogInsertColumnCount <= postgresMaxBindParams {
		t.Logf("提示：当前 batch size 上限 %d 即使不分片也不会超参数上限", maxUsageLogBatchSize)
	}
}

// dataError 构造一个 PostgreSQL 数据类错误（class 22：超长、非法字节、数值溢出这类）。
func dataError() error {
	return &pq.Error{Code: "22001", Message: "value too long for type character varying(100)"}
}

// transientError 构造一个瞬时故障（class 08：连接异常）。
func transientError() error {
	return &pq.Error{Code: "08006", Message: "connection failure"}
}

func TestIsUsageLogDataError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil 不是数据错误", nil, false},
		{"class 22 数据异常", dataError(), true},
		{"class 23 约束冲突", &pq.Error{Code: "23505"}, true},
		{"class 08 连接异常要重试", transientError(), false},
		{"class 40 死锁要重试", &pq.Error{Code: "40P01"}, false},
		{"class 53 资源不足要重试", &pq.Error{Code: "53100"}, false},
		{"包装后的数据错误仍能识别", fmt.Errorf("执行插入: %w", dataError()), true},
		{"非 pq 错误按瞬时处理", context.DeadlineExceeded, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUsageLogDataError(tc.err); got != tc.want {
				t.Fatalf("isUsageLogDataError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestSalvageDropsOnlyPoisonRows 覆盖脏数据隔离：整批因为一条写不进去的日志回滚时，
// 二分只丢那一条，其余必须照常落库（而不是整批一起放回缓冲区无限重试）。
func TestSalvageDropsOnlyPoisonRows(t *testing.T) {
	batch := make([]usageLogEntry, 16)
	for i := range batch {
		batch[i] = usageLogEntry{Model: fmt.Sprintf("m-%02d", i)}
	}
	poison := "m-05"

	var written []string
	insert := func(chunk []usageLogEntry) error {
		for _, e := range chunk {
			if e.Model == poison {
				return dataError()
			}
		}
		for _, e := range chunk {
			written = append(written, e.Model)
		}
		return nil
	}

	pending, dropped := salvageUsageLogBatchWith(batch, dataError(), insert, func(usageLogEntry, error) {})
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %d 条, want 0", len(pending))
	}
	if len(written) != len(batch)-1 {
		t.Fatalf("落库 %d 条，want %d", len(written), len(batch)-1)
	}
	for _, model := range written {
		if model == poison {
			t.Fatalf("脏数据 %q 不应落库", poison)
		}
	}
}

// TestSalvageKeepsTransientFailuresForRetry 瞬时故障不能被当成脏数据丢掉：
// 那半批要原样交回重试，且已经写进去的部分不能再交回（否则重复落库、重复计费）。
func TestSalvageKeepsTransientFailuresForRetry(t *testing.T) {
	batch := make([]usageLogEntry, 8)
	for i := range batch {
		batch[i] = usageLogEntry{Model: fmt.Sprintf("m-%02d", i)}
	}

	var written []string
	insert := func(chunk []usageLogEntry) error {
		// 后半批遇到连接故障，前半批正常写入。
		for _, e := range chunk {
			if e.Model >= "m-04" {
				return transientError()
			}
		}
		for _, e := range chunk {
			written = append(written, e.Model)
		}
		return nil
	}

	pending, dropped := salvageUsageLogBatchWith(batch, dataError(), insert, func(usageLogEntry, error) {})
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0（瞬时故障不该丢日志）", dropped)
	}
	if len(pending) != 4 {
		t.Fatalf("pending = %d 条, want 4", len(pending))
	}
	if len(written) != 4 {
		t.Fatalf("落库 %d 条，want 4", len(written))
	}
	for _, e := range pending {
		for _, model := range written {
			if e.Model == model {
				t.Fatalf("%q 已落库，不应再放回缓冲区重试", model)
			}
		}
	}
}

// TestUsageLogBufferHonorsHardLimit 覆盖缓冲硬上限：数据库长时间写不进去时缓冲不能无限涨，
// 超限丢最旧的并计数，运维能从运行状态里看见丢了多少。
func TestUsageLogBufferHonorsHardLimit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "codex2api.db")
	db, err := New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("New(sqlite) 返回错误: %v", err)
	}
	close(db.logStop)
	db.logWg.Wait()
	defer db.conn.Close()

	const overflow = 50
	db.SetUsageLogConfig(UsageLogModeFull, maxUsageLogBatchSize, maxUsageLogFlushIntervalSeconds)
	insertUsageLogs(t, db, usageLogBufferHardLimit+overflow)

	stats := db.GetUsageLogRuntimeStats()
	if stats.BufferLength != usageLogBufferHardLimit {
		t.Fatalf("BufferLength = %d，want %d", stats.BufferLength, usageLogBufferHardLimit)
	}
	if stats.DroppedTotal != overflow {
		t.Fatalf("DroppedTotal = %d，want %d", stats.DroppedTotal, overflow)
	}
	if stats.BufferLimit != usageLogBufferHardLimit {
		t.Fatalf("BufferLimit = %d，want %d", stats.BufferLimit, usageLogBufferHardLimit)
	}
}

// TestRequeueHonorsHardLimit 失败批次放回缓冲区同样要守住上限。
func TestRequeueHonorsHardLimit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "codex2api.db")
	db, err := New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("New(sqlite) 返回错误: %v", err)
	}
	close(db.logStop)
	db.logWg.Wait()
	defer db.conn.Close()

	db.SetUsageLogConfig(UsageLogModeFull, maxUsageLogBatchSize, maxUsageLogFlushIntervalSeconds)
	insertUsageLogs(t, db, usageLogBufferHardLimit)

	db.requeueUsageLogBatch(make([]usageLogEntry, 100))
	stats := db.GetUsageLogRuntimeStats()
	if stats.BufferLength != usageLogBufferHardLimit {
		t.Fatalf("requeue 后 BufferLength = %d，want %d", stats.BufferLength, usageLogBufferHardLimit)
	}
	if stats.DroppedTotal != 100 {
		t.Fatalf("DroppedTotal = %d，want 100", stats.DroppedTotal)
	}
}

// TestSalvageDropsPoisonRowAgainstPostgres 在真实 PostgreSQL 上验证脏数据隔离：
// SQLite 不校验列宽也不返回 SQLSTATE，isUsageLogDataError 这条路只有真库能走通。
// 需要一个可写的空库，用 CODEX2API_TEST_POSTGRES_DSN 指定，未设置时跳过。
func TestSalvageDropsPoisonRowAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("CODEX2API_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("未设置 CODEX2API_TEST_POSTGRES_DSN，跳过 PostgreSQL 集成用例")
	}

	db, err := New("postgres", dsn)
	if err != nil {
		t.Fatalf("New(postgres) 返回错误: %v", err)
	}
	close(db.logStop)
	db.logWg.Wait()
	defer db.conn.Close()

	ctx := context.Background()
	// 用一条 CHECK 约束造出「重试多少次都写不进去」的行：真实成因（超长、非法字节、
	// 数值溢出）同属 SQLSTATE class 22/23，走的是同一条判定分支。
	if _, err := db.conn.ExecContext(ctx,
		`ALTER TABLE usage_logs ADD CONSTRAINT tmp_salvage_poison CHECK (model <> '__poison__')`); err != nil {
		t.Fatalf("创建约束返回错误: %v", err)
	}
	defer db.conn.ExecContext(ctx, `ALTER TABLE usage_logs DROP CONSTRAINT IF EXISTS tmp_salvage_poison`)

	const marker = "/salvage-test"
	const clean = 8
	db.SetUsageLogConfig(UsageLogModeFull, maxUsageLogBatchSize, maxUsageLogFlushIntervalSeconds)
	for i := 0; i < clean; i++ {
		model := "gpt-5.4"
		if i == 3 {
			model = "__poison__"
		}
		if err := db.InsertUsageLog(ctx, &UsageLogInput{
			Endpoint:   marker,
			Model:      model,
			StatusCode: 200,
		}); err != nil {
			t.Fatalf("InsertUsageLog(%d) 返回错误: %v", i, err)
		}
	}

	db.FlushUsageLogs()

	var landed int
	if err := db.conn.QueryRowContext(ctx,
		`SELECT count(*) FROM usage_logs WHERE endpoint = $1`, marker).Scan(&landed); err != nil {
		t.Fatalf("统计落库条数返回错误: %v", err)
	}
	if landed != clean-1 {
		t.Fatalf("落库 %d 条，want %d（脏数据之外的日志必须照常写入）", landed, clean-1)
	}
	stats := db.GetUsageLogRuntimeStats()
	if stats.DroppedTotal != 1 {
		t.Fatalf("DroppedTotal = %d，want 1", stats.DroppedTotal)
	}
	if stats.BufferLength != 0 {
		t.Fatalf("BufferLength = %d，want 0（脏数据不应留在缓冲区反复重试）", stats.BufferLength)
	}
}
