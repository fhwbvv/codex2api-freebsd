package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// 存量升级路径:旧版 prompt_conversation_locks 表没有 identity_kind 列。
// 全新建表的测试覆盖不到这条路径——它一开始就带新列。本测试构造一张旧表、
// 灌入一行旧锁,再触发建表逻辑,验证:迁移不报错、旧行回落到 'newapi' 语义、
// 且随后可正常写入 codex_session 降级身份的新锁。
func TestPromptConversationLockIdentityKindMigrationFromLegacyTable(t *testing.T) {
	db, err := New("sqlite", filepath.Join(t.TempDir(), "lock-migration.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	// New() 启动时已建好新表(含 identity_kind)。为模拟存量旧库,先删除它,
	// 再建一张缺少 identity_kind 的旧 schema——这正是升级前磁盘上的样子。
	if _, err := db.conn.ExecContext(ctx, `DROP TABLE IF EXISTS prompt_conversation_locks`); err != nil {
		t.Fatalf("drop pre-created table: %v", err)
	}

	// 旧 schema:与当前 DDL 相同,唯独缺少 identity_kind 列。
	legacyDDL := `CREATE TABLE prompt_conversation_locks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		lock_key VARCHAR(64) NOT NULL UNIQUE,
		status VARCHAR(24) NOT NULL DEFAULT 'active',
		platform VARCHAR(100) NOT NULL DEFAULT '',
		newapi_user_id VARCHAR(255) NOT NULL DEFAULT '',
		session_fingerprint VARCHAR(32) NOT NULL DEFAULT '',
		session_hash VARCHAR(64) NOT NULL DEFAULT '',
		incident_id VARCHAR(64) NOT NULL DEFAULT '',
		decision_id VARCHAR(128) NOT NULL DEFAULT '',
		request_id VARCHAR(255) NOT NULL DEFAULT '',
		reason_code VARCHAR(100) NOT NULL DEFAULT '',
		endpoint VARCHAR(255) NOT NULL DEFAULT '',
		model VARCHAR(128) NOT NULL DEFAULT '',
		trigger_count BIGINT NOT NULL DEFAULT 1,
		unlock_count BIGINT NOT NULL DEFAULT 0,
		locked_at TIMESTAMP NOT NULL,
		unlocked_at TIMESTAMP NULL,
		unlock_reason TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMP NOT NULL,
		updated_at TIMESTAMP NOT NULL
	)`
	if _, err := db.conn.ExecContext(ctx, legacyDDL); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	legacyKey := "aa" + repeatByte("0", 62) // 64 hex
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	if _, err := db.conn.ExecContext(ctx, `INSERT INTO prompt_conversation_locks
		(lock_key, status, platform, newapi_user_id, session_fingerprint, session_hash, decision_id, locked_at, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		legacyKey, "active", "gateway-a", "42", "0123456789abcdef0123456789abcdef",
		"deadbeef", "dec_legacy", now, now, now); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	// 触发建表/迁移逻辑并读回旧行。
	if err := db.ensurePromptConversationLocksTable(ctx); err != nil {
		t.Fatalf("migrate legacy table: %v", err)
	}
	lock, err := db.GetActivePromptConversationLock(ctx, legacyKey)
	if err != nil {
		t.Fatalf("read legacy lock after migration: %v", err)
	}
	if lock.IdentityKind != PromptConversationLockIdentityNewAPI {
		t.Fatalf("legacy row identity_kind = %q, want %q", lock.IdentityKind, PromptConversationLockIdentityNewAPI)
	}
	if lock.NewAPIUserID != "42" || lock.Status != PromptConversationLockStatusActive {
		t.Fatalf("legacy row corrupted after migration: %#v", lock)
	}

	// 迁移后可写入降级身份的新锁。
	item, activated, err := db.LockPromptConversation(ctx, PromptConversationLockInput{
		LockKey:            "bb" + repeatByte("0", 62),
		IdentityKind:       PromptConversationLockIdentityCodexSession,
		Platform:           "codex-local",
		NewAPIUserID:       "apikey:101",
		SessionFingerprint: "fedcba9876543210fedcba9876543210",
		SessionHash:        "cafebabe",
		DecisionID:         "local-block:xyz",
		ReasonCode:         "terminal_policy_match",
		LockedAt:           time.Now().UTC(),
	})
	if err != nil || !activated || item == nil {
		t.Fatalf("write codex_session lock after migration: item=%#v activated=%t err=%v", item, activated, err)
	}
	if item.IdentityKind != PromptConversationLockIdentityCodexSession {
		t.Fatalf("new lock identity_kind = %q, want codex_session", item.IdentityKind)
	}

	// 迁移是幂等的:再次触发不应报错或破坏数据。
	if err := db.ensurePromptConversationLocksTable(ctx); err != nil {
		t.Fatalf("second ensure (idempotency): %v", err)
	}
	if again, err := db.GetActivePromptConversationLock(ctx, legacyKey); err != nil || again.IdentityKind != PromptConversationLockIdentityNewAPI {
		t.Fatalf("legacy row after second ensure: lock=%#v err=%v", again, err)
	}
}

func repeatByte(s string, n int) string {
	out := make([]byte, 0, n)
	for range n {
		out = append(out, s[0])
	}
	return string(out)
}
