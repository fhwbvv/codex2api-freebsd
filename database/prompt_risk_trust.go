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
	PromptRiskTrustStatusActive    = "active"
	PromptRiskTrustStatusSuspended = "suspended"
	PromptRiskTrustStatusRevoked   = "revoked"
	PromptRiskTrustStatusExpired   = "expired"
	PromptRiskTrustSourceManual    = "manual"
	PromptRiskTrustSourceAutomatic = "automatic"

	PromptRiskTrustEventGranted       = "granted"
	PromptRiskTrustEventAutoGranted   = "auto_granted"
	PromptRiskTrustEventReactivated   = "reactivated"
	PromptRiskTrustEventSuspended     = "suspended"
	PromptRiskTrustEventAutoSuspended = "auto_suspended"
	PromptRiskTrustEventRevoked       = "revoked"
	PromptRiskTrustEventExpired       = "expired"
	PromptRiskTrustEventBypassUsed    = "bypass_used"
	PromptRiskTrustEventModelReviewed = "model_reviewed"
	PromptRiskTrustEventEvaluated     = "evaluated"
)

type PromptRiskTrustPolicy struct {
	ID                int64      `json:"id"`
	SubjectType       string     `json:"subject_type"`
	SubjectKey        string     `json:"subject_key"`
	Status            string     `json:"status"`
	Source            string     `json:"source"`
	Reason            string     `json:"reason,omitempty"`
	RiskThreshold     int        `json:"risk_threshold"`
	ValidUntil        time.Time  `json:"valid_until"`
	LastEvaluatedAt   *time.Time `json:"last_evaluated_at,omitempty"`
	LastRiskScore     int        `json:"last_risk_score"`
	LastRiskLevel     string     `json:"last_risk_level,omitempty"`
	BypassCount       int64      `json:"bypass_count"`
	LastBypassAt      *time.Time `json:"last_bypass_at,omitempty"`
	ModelReviewCount  int64      `json:"model_review_count"`
	LastModelReviewAt *time.Time `json:"last_model_review_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type PromptRiskTrustEvent struct {
	ID            int64     `json:"id"`
	PolicyID      int64     `json:"policy_id"`
	SubjectType   string    `json:"subject_type"`
	SubjectKey    string    `json:"subject_key"`
	EventType     string    `json:"event_type"`
	Reason        string    `json:"reason,omitempty"`
	RiskScore     int       `json:"risk_score"`
	RiskLevel     string    `json:"risk_level,omitempty"`
	RequestIDHash string    `json:"request_id_hash,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type PromptRiskTrustPolicyInput struct {
	SubjectType       string
	SubjectKey        string
	Source            string
	Reason            string
	RiskThreshold     int
	ValidUntil        time.Time
	LastModelReviewAt *time.Time
}

type PromptRiskTrustAdaptiveOptions struct {
	MinCleanReviews           int
	MinObservationHours       int
	TrustDurationHours        int
	ReactivationCleanReviews  int
	ReactivationCooldownHours int
	RiskThreshold             int
}

type PromptRiskTrustPolicyQuery struct {
	Page     int
	PageSize int
	Status   string
	Query    string
}

var promptRiskTrustSchemaMu sync.Mutex

func (db *DB) ensurePromptRiskTrustTables(ctx context.Context) error {
	if db == nil {
		return errors.New("database unavailable")
	}
	promptRiskTrustSchemaMu.Lock()
	defer promptRiskTrustSchemaMu.Unlock()
	policyDDL := `CREATE TABLE IF NOT EXISTS prompt_risk_trust_policies (
		id BIGSERIAL PRIMARY KEY,
		subject_type VARCHAR(40) NOT NULL,
		subject_key VARCHAR(128) NOT NULL UNIQUE,
		status VARCHAR(24) NOT NULL DEFAULT 'active',
		source VARCHAR(24) NOT NULL DEFAULT 'manual',
		reason TEXT NOT NULL DEFAULT '',
		risk_threshold INT NOT NULL DEFAULT 35,
		valid_until TIMESTAMP NOT NULL,
		last_evaluated_at TIMESTAMP NULL,
		last_risk_score INT NOT NULL DEFAULT 0,
		last_risk_level VARCHAR(24) NOT NULL DEFAULT 'low',
		bypass_count BIGINT NOT NULL DEFAULT 0,
		last_bypass_at TIMESTAMP NULL,
		model_review_count BIGINT NOT NULL DEFAULT 0,
		last_model_review_at TIMESTAMP NULL,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`
	eventDDL := `CREATE TABLE IF NOT EXISTS prompt_risk_trust_events (
		id BIGSERIAL PRIMARY KEY,
		policy_id BIGINT NOT NULL,
		subject_type VARCHAR(40) NOT NULL,
		subject_key VARCHAR(128) NOT NULL,
		event_type VARCHAR(40) NOT NULL,
		reason TEXT NOT NULL DEFAULT '',
		risk_score INT NOT NULL DEFAULT 0,
		risk_level VARCHAR(24) NOT NULL DEFAULT '',
		request_id_hash VARCHAR(128) NOT NULL DEFAULT '',
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`
	if db.isSQLite() {
		policyDDL = `CREATE TABLE IF NOT EXISTS prompt_risk_trust_policies (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			subject_type TEXT NOT NULL,
			subject_key TEXT NOT NULL UNIQUE,
			status TEXT NOT NULL DEFAULT 'active',
			source TEXT NOT NULL DEFAULT 'manual',
			reason TEXT NOT NULL DEFAULT '',
			risk_threshold INTEGER NOT NULL DEFAULT 35,
			valid_until TIMESTAMP NOT NULL,
			last_evaluated_at TIMESTAMP NULL,
			last_risk_score INTEGER NOT NULL DEFAULT 0,
			last_risk_level TEXT NOT NULL DEFAULT 'low',
			bypass_count INTEGER NOT NULL DEFAULT 0,
			last_bypass_at TIMESTAMP NULL,
			model_review_count INTEGER NOT NULL DEFAULT 0,
			last_model_review_at TIMESTAMP NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`
		eventDDL = `CREATE TABLE IF NOT EXISTS prompt_risk_trust_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			policy_id INTEGER NOT NULL,
			subject_type TEXT NOT NULL,
			subject_key TEXT NOT NULL,
			event_type TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			risk_score INTEGER NOT NULL DEFAULT 0,
			risk_level TEXT NOT NULL DEFAULT '',
			request_id_hash TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`
	}
	for _, stmt := range []string{
		policyDDL,
		eventDDL,
		`CREATE INDEX IF NOT EXISTS idx_prompt_risk_trust_status_until ON prompt_risk_trust_policies(status, valid_until)`,
		`CREATE INDEX IF NOT EXISTS idx_prompt_risk_trust_events_policy ON prompt_risk_trust_events(policy_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_prompt_risk_trust_events_subject ON prompt_risk_trust_events(subject_type, subject_key, created_at)`,
	} {
		if _, err := db.conn.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if db.isSQLite() {
		for _, column := range []struct{ name, definition string }{
			{"source", "TEXT NOT NULL DEFAULT 'manual'"},
			{"model_review_count", "INTEGER NOT NULL DEFAULT 0"},
			{"last_model_review_at", "TIMESTAMP NULL"},
		} {
			if err := db.ensureSQLiteColumn(ctx, "prompt_risk_trust_policies", column.name, column.definition); err != nil {
				return err
			}
		}
	} else {
		for _, stmt := range []string{
			`ALTER TABLE prompt_risk_trust_policies ADD COLUMN IF NOT EXISTS source VARCHAR(24) NOT NULL DEFAULT 'manual'`,
			`ALTER TABLE prompt_risk_trust_policies ADD COLUMN IF NOT EXISTS model_review_count BIGINT NOT NULL DEFAULT 0`,
			`ALTER TABLE prompt_risk_trust_policies ADD COLUMN IF NOT EXISTS last_model_review_at TIMESTAMP NULL`,
		} {
			if _, err := db.conn.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
	}
	return nil
}

func normalizePromptRiskTrustInput(input PromptRiskTrustPolicyInput) (PromptRiskTrustPolicyInput, error) {
	input.SubjectType = strings.TrimSpace(input.SubjectType)
	input.SubjectKey = strings.TrimSpace(input.SubjectKey)
	input.Source = strings.ToLower(strings.TrimSpace(input.Source))
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Source == "" {
		input.Source = PromptRiskTrustSourceManual
	}
	if input.Source != PromptRiskTrustSourceManual && input.Source != PromptRiskTrustSourceAutomatic {
		return input, errors.New("adaptive trust source is invalid")
	}
	if input.SubjectType != PromptRiskSubjectNewAPIUser || input.SubjectKey == "" {
		return input, errors.New("adaptive trust requires a signed NewAPI person profile")
	}
	if input.RiskThreshold <= 0 {
		input.RiskThreshold = 35
	}
	if input.RiskThreshold < 15 || input.RiskThreshold > 79 {
		return input, errors.New("risk threshold must be between 15 and 79")
	}
	if input.ValidUntil.IsZero() || !input.ValidUntil.After(time.Now().UTC()) {
		return input, errors.New("valid_until must be in the future")
	}
	if input.ValidUntil.After(time.Now().UTC().Add(30 * 24 * time.Hour)) {
		return input, errors.New("adaptive trust cannot exceed 30 days")
	}
	if input.Reason == "" {
		return input, errors.New("reason is required")
	}
	return input, nil
}

func (db *DB) UpsertPromptRiskTrustPolicy(ctx context.Context, raw PromptRiskTrustPolicyInput) (*PromptRiskTrustPolicy, error) {
	if err := db.ensurePromptRiskTrustTables(ctx); err != nil {
		return nil, err
	}
	input, err := normalizePromptRiskTrustInput(raw)
	if err != nil {
		return nil, err
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	previous := ""
	_ = tx.QueryRowContext(ctx, `SELECT status FROM prompt_risk_trust_policies WHERE subject_key=$1`, input.SubjectKey).Scan(&previous)
	_, err = tx.ExecContext(ctx, `INSERT INTO prompt_risk_trust_policies (
		subject_type, subject_key, status, source, reason, risk_threshold, valid_until, last_model_review_at, updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,CURRENT_TIMESTAMP)
	ON CONFLICT(subject_key) DO UPDATE SET
		subject_type=EXCLUDED.subject_type, status='active', source=EXCLUDED.source, reason=EXCLUDED.reason,
		risk_threshold=EXCLUDED.risk_threshold, valid_until=EXCLUDED.valid_until,
		last_evaluated_at=NULL, last_model_review_at=COALESCE(EXCLUDED.last_model_review_at, prompt_risk_trust_policies.last_model_review_at),
		updated_at=CURRENT_TIMESTAMP`, input.SubjectType, input.SubjectKey,
		PromptRiskTrustStatusActive, input.Source, input.Reason, input.RiskThreshold, input.ValidUntil.UTC(), input.LastModelReviewAt)
	if err != nil {
		return nil, err
	}
	var policyID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM prompt_risk_trust_policies WHERE subject_key=$1`, input.SubjectKey).Scan(&policyID); err != nil {
		return nil, err
	}
	eventType := PromptRiskTrustEventGranted
	if input.Source == PromptRiskTrustSourceAutomatic && previous == "" {
		eventType = PromptRiskTrustEventAutoGranted
	}
	if previous != "" {
		eventType = PromptRiskTrustEventReactivated
	}
	if err := insertPromptRiskTrustEvent(ctx, tx, policyID, input.SubjectType, input.SubjectKey, eventType, input.Reason, 0, "", ""); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return db.GetPromptRiskTrustPolicy(ctx, input.SubjectType, input.SubjectKey)
}

func insertPromptRiskTrustEvent(ctx context.Context, exec promptRiskEventExecutor, policyID int64, subjectType, subjectKey, eventType, reason string, riskScore int, riskLevel, requestIDHash string) error {
	_, err := exec.ExecContext(ctx, `INSERT INTO prompt_risk_trust_events (
		policy_id, subject_type, subject_key, event_type, reason, risk_score, risk_level, request_id_hash, created_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,CURRENT_TIMESTAMP)`, policyID, subjectType, subjectKey, eventType,
		strings.TrimSpace(reason), promptRiskClamp(riskScore), strings.TrimSpace(riskLevel), strings.TrimSpace(requestIDHash))
	return err
}

func scanPromptRiskTrustPolicy(scanner interface{ Scan(...any) error }) (*PromptRiskTrustPolicy, error) {
	item := &PromptRiskTrustPolicy{}
	var validUntil, lastEvaluated, lastBypass, lastModelReview, createdAt, updatedAt any
	if err := scanner.Scan(&item.ID, &item.SubjectType, &item.SubjectKey, &item.Status, &item.Source, &item.Reason,
		&item.RiskThreshold, &validUntil, &lastEvaluated, &item.LastRiskScore, &item.LastRiskLevel,
		&item.BypassCount, &lastBypass, &item.ModelReviewCount, &lastModelReview, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	var err error
	if item.ValidUntil, err = parsePromptRiskTimeValue(validUntil); err != nil {
		return nil, err
	}
	if lastEvaluated != nil {
		value, parseErr := parsePromptRiskTimeValue(lastEvaluated)
		if parseErr != nil {
			return nil, parseErr
		}
		item.LastEvaluatedAt = &value
	}
	if lastBypass != nil {
		value, parseErr := parsePromptRiskTimeValue(lastBypass)
		if parseErr != nil {
			return nil, parseErr
		}
		item.LastBypassAt = &value
	}
	if lastModelReview != nil {
		value, parseErr := parsePromptRiskTimeValue(lastModelReview)
		if parseErr != nil {
			return nil, parseErr
		}
		item.LastModelReviewAt = &value
	}
	if item.CreatedAt, err = parsePromptRiskTimeValue(createdAt); err != nil {
		return nil, err
	}
	if item.UpdatedAt, err = parsePromptRiskTimeValue(updatedAt); err != nil {
		return nil, err
	}
	return item, nil
}

const promptRiskTrustSelect = `SELECT id, subject_type, subject_key, status, source, reason, risk_threshold, valid_until,
	last_evaluated_at, last_risk_score, last_risk_level, bypass_count, last_bypass_at, model_review_count, last_model_review_at, created_at, updated_at
	FROM prompt_risk_trust_policies`

func (db *DB) GetPromptRiskTrustPolicy(ctx context.Context, subjectType, subjectKey string) (*PromptRiskTrustPolicy, error) {
	if err := db.ensurePromptRiskTrustTables(ctx); err != nil {
		return nil, err
	}
	return scanPromptRiskTrustPolicy(db.conn.QueryRowContext(ctx, promptRiskTrustSelect+` WHERE subject_type=$1 AND subject_key=$2`, strings.TrimSpace(subjectType), strings.TrimSpace(subjectKey)))
}

func (db *DB) ListPromptRiskTrustPolicies(ctx context.Context, query PromptRiskTrustPolicyQuery) ([]*PromptRiskTrustPolicy, int, error) {
	if err := db.ensurePromptRiskTrustTables(ctx); err != nil {
		return nil, 0, err
	}
	if query.Page <= 0 {
		query.Page = 1
	}
	if query.PageSize <= 0 || query.PageSize > 200 {
		query.PageSize = 20
	}
	clauses := []string{"1=1"}
	args := []any{}
	if status := strings.TrimSpace(query.Status); status != "" && status != "all" {
		args = append(args, status)
		clauses = append(clauses, fmt.Sprintf("status=$%d", len(args)))
	}
	if q := strings.TrimSpace(query.Query); q != "" {
		args = append(args, "%"+strings.ToLower(q)+"%")
		clauses = append(clauses, fmt.Sprintf("(LOWER(subject_key) LIKE $%d OR LOWER(reason) LIKE $%d)", len(args), len(args)))
	}
	where := " WHERE " + strings.Join(clauses, " AND ")
	var total int
	if err := db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM prompt_risk_trust_policies`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, query.PageSize, (query.Page-1)*query.PageSize)
	rows, err := db.conn.QueryContext(ctx, promptRiskTrustSelect+where+fmt.Sprintf(" ORDER BY updated_at DESC, id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]*PromptRiskTrustPolicy, 0, query.PageSize)
	for rows.Next() {
		item, scanErr := scanPromptRiskTrustPolicy(rows)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (db *DB) ListActivePromptRiskTrustPolicies(ctx context.Context) ([]*PromptRiskTrustPolicy, error) {
	return db.ListAllPromptRiskTrustPolicies(ctx, PromptRiskTrustStatusActive)
}

func (db *DB) ListAllPromptRiskTrustPolicies(ctx context.Context, status string) ([]*PromptRiskTrustPolicy, error) {
	result := make([]*PromptRiskTrustPolicy, 0)
	for page := 1; ; page++ {
		items, total, err := db.ListPromptRiskTrustPolicies(ctx, PromptRiskTrustPolicyQuery{Page: page, PageSize: 200, Status: status})
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		if len(result) >= total || len(items) == 0 {
			return result, nil
		}
	}
}

func (db *DB) transitionPromptRiskTrustPolicy(ctx context.Context, subjectType, subjectKey, status, eventType, reason string, riskScore int, riskLevel string) (*PromptRiskTrustPolicy, error) {
	if err := db.ensurePromptRiskTrustTables(ctx); err != nil {
		return nil, err
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id int64
	var current string
	if err := tx.QueryRowContext(ctx, `SELECT id, status FROM prompt_risk_trust_policies WHERE subject_type=$1 AND subject_key=$2`, subjectType, subjectKey).Scan(&id, &current); err != nil {
		return nil, err
	}
	if current != status {
		result, err := tx.ExecContext(ctx, `UPDATE prompt_risk_trust_policies SET status=$1, reason=$2, last_risk_score=$3, last_risk_level=$4, last_evaluated_at=CURRENT_TIMESTAMP, updated_at=CURRENT_TIMESTAMP WHERE id=$5 AND status=$6`, status, reason, promptRiskClamp(riskScore), riskLevel, id, current)
		if err != nil {
			return nil, err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if updated == 0 {
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return db.GetPromptRiskTrustPolicy(ctx, subjectType, subjectKey)
		}
		if err := insertPromptRiskTrustEvent(ctx, tx, id, subjectType, subjectKey, eventType, reason, riskScore, riskLevel, ""); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return db.GetPromptRiskTrustPolicy(ctx, subjectType, subjectKey)
}

func (db *DB) RevokePromptRiskTrustPolicy(ctx context.Context, subjectType, subjectKey, reason string) (*PromptRiskTrustPolicy, error) {
	if strings.TrimSpace(reason) == "" {
		reason = "管理员撤销自适应可信策略"
	}
	return db.transitionPromptRiskTrustPolicy(ctx, subjectType, subjectKey, PromptRiskTrustStatusRevoked, PromptRiskTrustEventRevoked, reason, 0, "")
}

func (db *DB) SuspendPromptRiskTrustPolicy(ctx context.Context, subjectType, subjectKey, reason string, riskScore int, riskLevel string) (*PromptRiskTrustPolicy, error) {
	if strings.TrimSpace(reason) == "" {
		reason = "风险画像达到重新审核阈值"
	}
	return db.transitionPromptRiskTrustPolicy(ctx, subjectType, subjectKey, PromptRiskTrustStatusSuspended, PromptRiskTrustEventAutoSuspended, reason, riskScore, riskLevel)
}

func (db *DB) ReconcilePromptRiskTrustPolicies(ctx context.Context) ([]*PromptRiskTrustPolicy, error) {
	items, err := db.ListActivePromptRiskTrustPolicies(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for _, item := range items {
		if !item.ValidUntil.After(now) {
			_, _ = db.transitionPromptRiskTrustPolicy(ctx, item.SubjectType, item.SubjectKey, PromptRiskTrustStatusExpired, PromptRiskTrustEventExpired, "自适应可信期限已到", item.LastRiskScore, item.LastRiskLevel)
			continue
		}
		profile, profileErr := db.GetPromptRiskProfile(ctx, item.SubjectType, item.SubjectKey)
		if profileErr != nil && !errors.Is(profileErr, sql.ErrNoRows) {
			return nil, profileErr
		}
		score, level := 0, PromptRiskLevelLow
		if profile != nil {
			score, level = profile.RiskScore, profile.RiskLevel
		}
		if score >= item.RiskThreshold || level == PromptRiskLevelHigh || level == PromptRiskLevelCritical {
			_, _ = db.SuspendPromptRiskTrustPolicy(ctx, item.SubjectType, item.SubjectKey, "风险画像达到重新审核阈值", score, level)
			continue
		}
		_, err = db.conn.ExecContext(ctx, `UPDATE prompt_risk_trust_policies SET last_evaluated_at=CURRENT_TIMESTAMP, last_risk_score=$1, last_risk_level=$2 WHERE id=$3`, score, level, item.ID)
		if err != nil {
			return nil, err
		}
	}
	return db.ListActivePromptRiskTrustPolicies(ctx)
}

type promptRiskTrustEvidenceSummary struct {
	SubjectType   string
	SubjectKey    string
	CleanCount    int
	PositiveCount int
	FirstCleanAt  *time.Time
	LastCleanAt   *time.Time
}

// PromptRiskAdaptiveReviewBasis is the durable evidence used to decide
// whether a signed person may enter adaptive model-review sampling.
type PromptRiskAdaptiveReviewBasis struct {
	CleanReviewCount      int        `json:"clean_review_count"`
	PositiveEvidenceCount int        `json:"positive_evidence_count"`
	FirstCleanAt          *time.Time `json:"first_clean_at,omitempty"`
	LastCleanAt           *time.Time `json:"last_clean_at,omitempty"`
}

func (db *DB) GetPromptRiskAdaptiveReviewBasis(ctx context.Context, subjectType, subjectKey string, since time.Time) (PromptRiskAdaptiveReviewBasis, error) {
	if err := db.ensurePromptRiskEventsTable(ctx); err != nil {
		return PromptRiskAdaptiveReviewBasis{}, err
	}
	var result PromptRiskAdaptiveReviewBasis
	var firstClean, lastClean any
	err := db.conn.QueryRowContext(ctx, `SELECT
		COUNT(DISTINCT CASE WHEN event_kind='review_cleared' THEN CASE WHEN request_correlation_id<>'' THEN request_correlation_id ELSE source_type || ':' || source_id END END),
		COALESCE(SUM(CASE WHEN request_risk_score>0 AND event_kind IN ('review_flagged_monitor','local_block_strike','upstream_cy_confirmed_miss','upstream_cy_local_detected','upstream_cy_upstream_only') THEN 1 ELSE 0 END), 0),
		MIN(CASE WHEN event_kind='review_cleared' THEN created_at ELSE NULL END),
		MAX(CASE WHEN event_kind='review_cleared' THEN created_at ELSE NULL END)
	FROM prompt_risk_events
	WHERE subject_type=$1 AND subject_key=$2 AND is_person=TRUE AND created_at >= $3`,
		strings.TrimSpace(subjectType), strings.TrimSpace(subjectKey), since.UTC()).Scan(
		&result.CleanReviewCount, &result.PositiveEvidenceCount, &firstClean, &lastClean)
	if err != nil {
		return result, err
	}
	if firstClean != nil {
		value, err := parsePromptRiskTimeValue(firstClean)
		if err != nil {
			return result, err
		}
		result.FirstCleanAt = &value
	}
	if lastClean != nil {
		value, err := parsePromptRiskTimeValue(lastClean)
		if err != nil {
			return result, err
		}
		result.LastCleanAt = &value
	}
	return result, nil
}

func normalizePromptRiskTrustAdaptiveOptions(options PromptRiskTrustAdaptiveOptions) PromptRiskTrustAdaptiveOptions {
	if options.MinCleanReviews <= 0 {
		options.MinCleanReviews = 10
	}
	if options.MinObservationHours <= 0 {
		options.MinObservationHours = 24
	}
	if options.TrustDurationHours <= 0 {
		options.TrustDurationHours = 7 * 24
	}
	if options.ReactivationCleanReviews <= 0 {
		options.ReactivationCleanReviews = 5
	}
	if options.ReactivationCooldownHours <= 0 {
		options.ReactivationCooldownHours = 24
	}
	if options.RiskThreshold < 15 || options.RiskThreshold > 79 {
		options.RiskThreshold = 35
	}
	return options
}

func (db *DB) promptRiskTrustEvidenceSummaries(ctx context.Context, since time.Time) ([]promptRiskTrustEvidenceSummary, error) {
	if err := db.ensurePromptRiskEventsTable(ctx); err != nil {
		return nil, err
	}
	rows, err := db.conn.QueryContext(ctx, `SELECT subject_type, subject_key,
		COUNT(DISTINCT CASE WHEN event_kind='review_cleared' THEN CASE WHEN request_correlation_id<>'' THEN request_correlation_id ELSE source_type || ':' || source_id END END),
		COALESCE(SUM(CASE WHEN request_risk_score>0 AND event_kind IN ('review_flagged_monitor','local_block_strike','upstream_cy_confirmed_miss','upstream_cy_local_detected','upstream_cy_upstream_only') THEN 1 ELSE 0 END), 0),
		MIN(CASE WHEN event_kind='review_cleared' THEN created_at ELSE NULL END),
		MAX(CASE WHEN event_kind='review_cleared' THEN created_at ELSE NULL END)
	FROM prompt_risk_events
	WHERE subject_type=$1 AND is_person=TRUE AND created_at >= $2
	GROUP BY subject_type, subject_key`, PromptRiskSubjectNewAPIUser, since.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]promptRiskTrustEvidenceSummary, 0)
	for rows.Next() {
		var item promptRiskTrustEvidenceSummary
		var firstClean, lastClean any
		if err := rows.Scan(&item.SubjectType, &item.SubjectKey, &item.CleanCount, &item.PositiveCount, &firstClean, &lastClean); err != nil {
			return nil, err
		}
		if firstClean != nil {
			value, err := parsePromptRiskTimeValue(firstClean)
			if err != nil {
				return nil, err
			}
			item.FirstCleanAt = &value
		}
		if lastClean != nil {
			value, err := parsePromptRiskTimeValue(lastClean)
			if err != nil {
				return nil, err
			}
			item.LastCleanAt = &value
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (db *DB) promptRiskTrustEvidenceSummarySince(ctx context.Context, subjectKey string, since time.Time) (promptRiskTrustEvidenceSummary, error) {
	if err := db.ensurePromptRiskEventsTable(ctx); err != nil {
		return promptRiskTrustEvidenceSummary{}, err
	}
	item := promptRiskTrustEvidenceSummary{SubjectType: PromptRiskSubjectNewAPIUser, SubjectKey: subjectKey}
	var firstClean, lastClean any
	err := db.conn.QueryRowContext(ctx, `SELECT
		COUNT(DISTINCT CASE WHEN event_kind='review_cleared' THEN CASE WHEN request_correlation_id<>'' THEN request_correlation_id ELSE source_type || ':' || source_id END END),
		COALESCE(SUM(CASE WHEN request_risk_score>0 AND event_kind IN ('review_flagged_monitor','local_block_strike','upstream_cy_confirmed_miss','upstream_cy_local_detected','upstream_cy_upstream_only') THEN 1 ELSE 0 END), 0),
		MIN(CASE WHEN event_kind='review_cleared' THEN created_at ELSE NULL END),
		MAX(CASE WHEN event_kind='review_cleared' THEN created_at ELSE NULL END)
	FROM prompt_risk_events WHERE subject_type=$1 AND subject_key=$2 AND is_person=TRUE AND created_at >= $3`,
		PromptRiskSubjectNewAPIUser, subjectKey, since.UTC()).Scan(&item.CleanCount, &item.PositiveCount, &firstClean, &lastClean)
	if err != nil {
		return item, err
	}
	if firstClean != nil {
		value, err := parsePromptRiskTimeValue(firstClean)
		if err != nil {
			return item, err
		}
		item.FirstCleanAt = &value
	}
	if lastClean != nil {
		value, err := parsePromptRiskTimeValue(lastClean)
		if err != nil {
			return item, err
		}
		item.LastCleanAt = &value
	}
	return item, nil
}

// ReconcileAdaptivePromptRiskTrustPolicies creates temporary automatic trust
// only for signed people with enough clean model-reviewed history. A manually
// revoked or manually expired policy is never silently re-enabled.
func (db *DB) ReconcileAdaptivePromptRiskTrustPolicies(ctx context.Context, raw PromptRiskTrustAdaptiveOptions) ([]*PromptRiskTrustPolicy, error) {
	options := normalizePromptRiskTrustAdaptiveOptions(raw)
	if _, err := db.ReconcilePromptRiskTrustPolicies(ctx); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	summaries, err := db.promptRiskTrustEvidenceSummaries(ctx, now.Add(-time.Duration(options.TrustDurationHours)*time.Hour))
	if err != nil {
		return nil, err
	}
	for _, evidence := range summaries {
		policy, policyErr := db.GetPromptRiskTrustPolicy(ctx, evidence.SubjectType, evidence.SubjectKey)
		if policyErr != nil && !errors.Is(policyErr, sql.ErrNoRows) {
			return nil, policyErr
		}
		profile, profileErr := db.GetPromptRiskProfile(ctx, evidence.SubjectType, evidence.SubjectKey)
		if profileErr != nil && !errors.Is(profileErr, sql.ErrNoRows) {
			return nil, profileErr
		}
		if profile == nil || !profile.IsPerson {
			continue
		}
		if policy != nil && policy.Status == PromptRiskTrustStatusActive {
			continue
		}
		if policy != nil && policy.Source != PromptRiskTrustSourceAutomatic {
			continue
		}
		eligible := evidence.CleanCount >= options.MinCleanReviews && evidence.PositiveCount == 0 && evidence.FirstCleanAt != nil &&
			evidence.FirstCleanAt.Before(now.Add(-time.Duration(options.MinObservationHours)*time.Hour)) &&
			profile.RiskScore < 15 && profile.RiskLevel == PromptRiskLevelLow
		if policy != nil && policy.Status == PromptRiskTrustStatusSuspended {
			if now.Before(policy.UpdatedAt.Add(time.Duration(options.ReactivationCooldownHours) * time.Hour)) {
				continue
			}
			recent, recentErr := db.promptRiskTrustEvidenceSummarySince(ctx, evidence.SubjectKey, policy.UpdatedAt)
			if recentErr != nil {
				return nil, recentErr
			}
			eligible = recent.CleanCount >= options.ReactivationCleanReviews && recent.PositiveCount == 0 && profile.RiskScore < 15
			if recent.LastCleanAt != nil {
				evidence.LastCleanAt = recent.LastCleanAt
			}
		}
		if !eligible {
			continue
		}
		reason := "稳定低风险画像自动降低同步模型复核频率"
		_, err = db.UpsertPromptRiskTrustPolicy(ctx, PromptRiskTrustPolicyInput{
			SubjectType: evidence.SubjectType, SubjectKey: evidence.SubjectKey, Source: PromptRiskTrustSourceAutomatic,
			Reason: reason, RiskThreshold: options.RiskThreshold,
			ValidUntil: now.Add(time.Duration(options.TrustDurationHours) * time.Hour), LastModelReviewAt: evidence.LastCleanAt,
		})
		if err != nil {
			return nil, err
		}
	}
	return db.ListActivePromptRiskTrustPolicies(ctx)
}

func (db *DB) RecordPromptRiskTrustBypass(ctx context.Context, policyID int64, subjectType, subjectKey, requestIDHash string) error {
	if err := db.ensurePromptRiskTrustTables(ctx); err != nil {
		return err
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE prompt_risk_trust_policies SET bypass_count=bypass_count+1, last_bypass_at=CURRENT_TIMESTAMP, updated_at=CURRENT_TIMESTAMP WHERE id=$1 AND status='active'`, policyID)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 0 {
		return tx.Commit()
	}
	if err := insertPromptRiskTrustEvent(ctx, tx, policyID, subjectType, subjectKey, PromptRiskTrustEventBypassUsed, "跳过同步模型复核，本地高危规则仍生效", 0, PromptRiskLevelLow, requestIDHash); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *DB) RecordPromptRiskTrustModelReview(ctx context.Context, policyID int64, subjectType, subjectKey, requestIDHash string) error {
	if err := db.ensurePromptRiskTrustTables(ctx); err != nil {
		return err
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE prompt_risk_trust_policies SET model_review_count=model_review_count+1, last_model_review_at=CURRENT_TIMESTAMP WHERE id=$1 AND status='active'`, policyID)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 0 {
		return tx.Commit()
	}
	if err := insertPromptRiskTrustEvent(ctx, tx, policyID, subjectType, subjectKey, PromptRiskTrustEventModelReviewed, "周期抽检模型复核通过", 0, PromptRiskLevelLow, requestIDHash); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *DB) ListPromptRiskTrustEvents(ctx context.Context, subjectType, subjectKey string, limit int) ([]*PromptRiskTrustEvent, error) {
	if err := db.ensurePromptRiskTrustTables(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.conn.QueryContext(ctx, `SELECT id, policy_id, subject_type, subject_key, event_type, reason, risk_score, risk_level, request_id_hash, created_at FROM prompt_risk_trust_events WHERE subject_type=$1 AND subject_key=$2 ORDER BY created_at DESC, id DESC LIMIT $3`, subjectType, subjectKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*PromptRiskTrustEvent, 0, limit)
	for rows.Next() {
		item := &PromptRiskTrustEvent{}
		var created any
		if err := rows.Scan(&item.ID, &item.PolicyID, &item.SubjectType, &item.SubjectKey, &item.EventType, &item.Reason, &item.RiskScore, &item.RiskLevel, &item.RequestIDHash, &created); err != nil {
			return nil, err
		}
		item.CreatedAt, err = parsePromptRiskTimeValue(created)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ListPromptRiskTrustEventsPage returns the independent, durable decision and
// operations history for one adaptive-review subject.
func (db *DB) ListPromptRiskTrustEventsPage(ctx context.Context, subjectType, subjectKey string, page, pageSize int) ([]*PromptRiskTrustEvent, int, error) {
	if err := db.ensurePromptRiskTrustTables(ctx); err != nil {
		return nil, 0, err
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 20
	}
	subjectType = strings.TrimSpace(subjectType)
	subjectKey = strings.TrimSpace(subjectKey)
	var total int
	if err := db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM prompt_risk_trust_events WHERE subject_type=$1 AND subject_key=$2`, subjectType, subjectKey).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := db.conn.QueryContext(ctx, `SELECT id, policy_id, subject_type, subject_key, event_type, reason, risk_score, risk_level, request_id_hash, created_at
		FROM prompt_risk_trust_events WHERE subject_type=$1 AND subject_key=$2
		ORDER BY created_at DESC, id DESC LIMIT $3 OFFSET $4`, subjectType, subjectKey, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]*PromptRiskTrustEvent, 0, pageSize)
	for rows.Next() {
		item := &PromptRiskTrustEvent{}
		var created any
		if err := rows.Scan(&item.ID, &item.PolicyID, &item.SubjectType, &item.SubjectKey, &item.EventType, &item.Reason, &item.RiskScore, &item.RiskLevel, &item.RequestIDHash, &created); err != nil {
			return nil, 0, err
		}
		if item.CreatedAt, err = parsePromptRiskTimeValue(created); err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}
