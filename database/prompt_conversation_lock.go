package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	PromptConversationLockStatusActive   = "active"
	PromptConversationLockStatusUnlocked = "unlocked"
	PromptConversationLockCacheNamespace = "prompt-conversation-lock"
)

type PromptConversationLock struct {
	ID                 int64      `json:"id"`
	LockKey            string     `json:"lock_key"`
	Status             string     `json:"status"`
	Platform           string     `json:"platform"`
	NewAPIUserID       string     `json:"newapi_user_id"`
	SessionFingerprint string     `json:"session_fingerprint"`
	SessionHash        string     `json:"session_hash"`
	IncidentID         string     `json:"incident_id,omitempty"`
	DecisionID         string     `json:"decision_id"`
	RequestID          string     `json:"request_id,omitempty"`
	ReasonCode         string     `json:"reason_code"`
	Endpoint           string     `json:"endpoint,omitempty"`
	Model              string     `json:"model,omitempty"`
	TriggerCount       int64      `json:"trigger_count"`
	UnlockCount        int64      `json:"unlock_count"`
	LockedAt           time.Time  `json:"locked_at"`
	UnlockedAt         *time.Time `json:"unlocked_at,omitempty"`
	UnlockReason       string     `json:"unlock_reason,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type PromptConversationLockInput struct {
	LockKey            string
	Platform           string
	NewAPIUserID       string
	SessionFingerprint string
	SessionHash        string
	IncidentID         string
	DecisionID         string
	RequestID          string
	ReasonCode         string
	Endpoint           string
	Model              string
	LockedAt           time.Time
}

var promptConversationLockSchemaMu sync.Mutex

func (db *DB) ensurePromptConversationLocksTable(ctx context.Context) error {
	if db == nil {
		return errors.New("database unavailable")
	}
	promptConversationLockSchemaMu.Lock()
	defer promptConversationLockSchemaMu.Unlock()
	idType := "BIGSERIAL PRIMARY KEY"
	timeType := "TIMESTAMPTZ"
	if db.isSQLite() {
		idType = "INTEGER PRIMARY KEY AUTOINCREMENT"
		timeType = "TIMESTAMP"
	}
	ddl := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS prompt_conversation_locks (
		id %s,
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
		locked_at %s NOT NULL,
		unlocked_at %s NULL,
		unlock_reason TEXT NOT NULL DEFAULT '',
		created_at %s NOT NULL,
		updated_at %s NOT NULL
	)`, idType, timeType, timeType, timeType, timeType)
	for _, statement := range []string{
		ddl,
		`CREATE INDEX IF NOT EXISTS idx_prompt_conversation_locks_status ON prompt_conversation_locks(status, updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_prompt_conversation_locks_session ON prompt_conversation_locks(session_hash, status)`,
	} {
		if _, err := db.conn.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

const promptConversationLockSelect = `SELECT id, lock_key, status, platform, newapi_user_id,
	session_fingerprint, session_hash, incident_id, decision_id, request_id, reason_code,
	endpoint, model, trigger_count, unlock_count, locked_at, unlocked_at, unlock_reason,
	created_at, updated_at FROM prompt_conversation_locks`

func scanPromptConversationLock(scanner interface{ Scan(...any) error }) (*PromptConversationLock, error) {
	item := &PromptConversationLock{}
	var lockedAt, unlockedAt, createdAt, updatedAt any
	if err := scanner.Scan(
		&item.ID, &item.LockKey, &item.Status, &item.Platform, &item.NewAPIUserID,
		&item.SessionFingerprint, &item.SessionHash, &item.IncidentID, &item.DecisionID,
		&item.RequestID, &item.ReasonCode, &item.Endpoint, &item.Model, &item.TriggerCount,
		&item.UnlockCount, &lockedAt, &unlockedAt, &item.UnlockReason, &createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}
	var err error
	if item.LockedAt, err = parsePromptRiskTimeValue(lockedAt); err != nil {
		return nil, err
	}
	if item.CreatedAt, err = parsePromptRiskTimeValue(createdAt); err != nil {
		return nil, err
	}
	if item.UpdatedAt, err = parsePromptRiskTimeValue(updatedAt); err != nil {
		return nil, err
	}
	if unlockedAt != nil {
		if parsed, parseErr := parsePromptRiskTimeValue(unlockedAt); parseErr == nil {
			item.UnlockedAt = &parsed
		}
	}
	return item, nil
}

func normalizePromptConversationLockInput(input PromptConversationLockInput) (PromptConversationLockInput, error) {
	input.LockKey = strings.ToLower(strings.TrimSpace(input.LockKey))
	input.Platform = strings.ToLower(truncateCandidateRunes(strings.TrimSpace(input.Platform), 100))
	input.NewAPIUserID = truncateCandidateRunes(strings.TrimSpace(input.NewAPIUserID), 255)
	input.SessionFingerprint = strings.ToLower(strings.TrimSpace(input.SessionFingerprint))
	input.SessionHash = strings.ToLower(strings.TrimSpace(input.SessionHash))
	input.IncidentID = truncateCandidateRunes(strings.TrimSpace(input.IncidentID), 64)
	input.DecisionID = truncateCandidateRunes(strings.TrimSpace(input.DecisionID), 128)
	input.RequestID = truncateCandidateRunes(strings.TrimSpace(input.RequestID), 255)
	input.ReasonCode = truncateCandidateRunes(strings.TrimSpace(input.ReasonCode), 100)
	input.Endpoint = truncateCandidateRunes(strings.TrimSpace(input.Endpoint), 255)
	input.Model = truncateCandidateRunes(strings.TrimSpace(input.Model), 128)
	if len(input.LockKey) != 64 || input.Platform == "" || input.NewAPIUserID == "" || len(input.SessionFingerprint) != 32 || input.DecisionID == "" {
		return PromptConversationLockInput{}, errors.New("invalid prompt conversation lock identity")
	}
	if input.LockedAt.IsZero() {
		input.LockedAt = time.Now().UTC()
	} else {
		input.LockedAt = input.LockedAt.UTC()
	}
	return input, nil
}

// LockPromptConversation activates a lock for a new verified upstream CYB
// decision. Replaying the same decision is idempotent and cannot re-lock a
// conversation that an administrator has already unlocked.
func (db *DB) LockPromptConversation(ctx context.Context, raw PromptConversationLockInput) (*PromptConversationLock, bool, error) {
	input, err := normalizePromptConversationLockInput(raw)
	if err != nil {
		return nil, false, err
	}
	if err := db.ensurePromptConversationLocksTable(ctx); err != nil {
		return nil, false, err
	}
	now := time.Now().UTC()
	query := `INSERT INTO prompt_conversation_locks (
		lock_key, status, platform, newapi_user_id, session_fingerprint, session_hash,
		incident_id, decision_id, request_id, reason_code, endpoint, model, trigger_count,
		unlock_count, locked_at, unlocked_at, unlock_reason, created_at, updated_at
	) VALUES ($1,'active',$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,1,0,$12,NULL,'',$13,$13)
	ON CONFLICT(lock_key) DO UPDATE SET
		status='active', platform=excluded.platform, newapi_user_id=excluded.newapi_user_id,
		session_fingerprint=excluded.session_fingerprint, session_hash=excluded.session_hash,
		incident_id=excluded.incident_id, decision_id=excluded.decision_id,
		request_id=excluded.request_id, reason_code=excluded.reason_code,
		endpoint=excluded.endpoint, model=excluded.model,
		trigger_count=prompt_conversation_locks.trigger_count+1,
		locked_at=excluded.locked_at, unlocked_at=NULL, unlock_reason='', updated_at=excluded.updated_at
	WHERE prompt_conversation_locks.decision_id<>excluded.decision_id
	RETURNING id, lock_key, status, platform, newapi_user_id, session_fingerprint, session_hash,
		incident_id, decision_id, request_id, reason_code, endpoint, model, trigger_count,
		unlock_count, locked_at, unlocked_at, unlock_reason, created_at, updated_at`
	item, scanErr := scanPromptConversationLock(db.conn.QueryRowContext(ctx, query,
		input.LockKey, input.Platform, input.NewAPIUserID, input.SessionFingerprint, input.SessionHash,
		input.IncidentID, input.DecisionID, input.RequestID, input.ReasonCode, input.Endpoint,
		input.Model, input.LockedAt, now,
	))
	if scanErr == nil {
		return item, true, nil
	}
	if !errors.Is(scanErr, sql.ErrNoRows) {
		return nil, false, scanErr
	}
	item, err = db.GetPromptConversationLock(ctx, input.LockKey)
	return item, false, err
}

func (db *DB) GetPromptConversationLock(ctx context.Context, lockKey string) (*PromptConversationLock, error) {
	if err := db.ensurePromptConversationLocksTable(ctx); err != nil {
		return nil, err
	}
	return scanPromptConversationLock(db.conn.QueryRowContext(ctx, promptConversationLockSelect+` WHERE lock_key=$1`, strings.ToLower(strings.TrimSpace(lockKey))))
}

func (db *DB) GetActivePromptConversationLock(ctx context.Context, lockKey string) (*PromptConversationLock, error) {
	if err := db.ensurePromptConversationLocksTable(ctx); err != nil {
		return nil, err
	}
	return scanPromptConversationLock(db.conn.QueryRowContext(ctx, promptConversationLockSelect+` WHERE lock_key=$1 AND status='active'`, strings.ToLower(strings.TrimSpace(lockKey))))
}

func (db *DB) GetActivePromptConversationLockBySessionHash(ctx context.Context, sessionHash string) (*PromptConversationLock, error) {
	if err := db.ensurePromptConversationLocksTable(ctx); err != nil {
		return nil, err
	}
	return scanPromptConversationLock(db.conn.QueryRowContext(ctx, promptConversationLockSelect+` WHERE session_hash=$1 AND status='active' ORDER BY updated_at DESC LIMIT 1`, strings.ToLower(strings.TrimSpace(sessionHash))))
}

func (db *DB) UnlockPromptConversation(ctx context.Context, lockKey, reason string) (*PromptConversationLock, error) {
	if err := db.ensurePromptConversationLocksTable(ctx); err != nil {
		return nil, err
	}
	reason = truncateCandidateRunes(strings.TrimSpace(reason), 1000)
	if reason == "" {
		reason = "管理员主动解锁"
	}
	now := time.Now().UTC()
	result, err := db.conn.ExecContext(ctx, `UPDATE prompt_conversation_locks SET
		status='unlocked', unlock_count=unlock_count+1, unlocked_at=$2,
		unlock_reason=$3, updated_at=$2 WHERE lock_key=$1 AND status='active'`,
		strings.ToLower(strings.TrimSpace(lockKey)), now, reason)
	if err != nil {
		return nil, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if rows == 0 {
		return nil, sql.ErrNoRows
	}
	return db.GetPromptConversationLock(ctx, lockKey)
}
