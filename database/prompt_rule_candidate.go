package database

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

const (
	PromptRuleCandidateKindPattern  = "pattern"
	PromptRuleCandidateKindEvidence = "evidence"

	PromptRuleCandidateStatusPending    = "pending"
	PromptRuleCandidateStatusPublished  = "published"
	PromptRuleCandidateStatusDismissed  = "dismissed"
	PromptRuleCandidateStatusSuperseded = "superseded"

	PromptRuleCandidateSourcePublicIntelligence  = "public_intelligence"
	PromptRuleCandidateSourceUpstreamCyberPolicy = "upstream_cyber_policy"
	PromptRuleCandidateSourceLegacyMigration     = "legacy_auto_migration"
	PromptRuleCandidateSourceLegacyMigrationDone = "legacy_auto_migration_completed"
	PromptRuleCandidateSourceManual              = "manual"
)

const sqlitePromptRuleCandidatesDDL = `CREATE TABLE IF NOT EXISTS prompt_rule_candidates (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	fingerprint TEXT NOT NULL UNIQUE,
	kind TEXT NOT NULL DEFAULT 'pattern',
	status TEXT NOT NULL DEFAULT 'pending',
	last_source TEXT NOT NULL DEFAULT '',
	name TEXT NOT NULL DEFAULT '',
	category TEXT NOT NULL DEFAULT '',
	rule_json TEXT NOT NULL DEFAULT '{}',
	rationale TEXT NOT NULL DEFAULT '',
	source_url TEXT NOT NULL DEFAULT '',
	evidence_count INTEGER NOT NULL DEFAULT 0,
	sample_preview TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
	last_seen_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
	published_at TIMESTAMP NULL,
	dismissed_at TIMESTAMP NULL
)`

const postgresPromptRuleCandidatesDDL = `CREATE TABLE IF NOT EXISTS prompt_rule_candidates (
	id BIGSERIAL PRIMARY KEY,
	fingerprint VARCHAR(64) NOT NULL UNIQUE,
	kind VARCHAR(16) NOT NULL DEFAULT 'pattern',
	status VARCHAR(16) NOT NULL DEFAULT 'pending',
	last_source VARCHAR(64) NOT NULL DEFAULT '',
	name VARCHAR(255) NOT NULL DEFAULT '',
	category VARCHAR(100) NOT NULL DEFAULT '',
	rule_json TEXT NOT NULL DEFAULT '{}',
	rationale TEXT NOT NULL DEFAULT '',
	source_url TEXT NOT NULL DEFAULT '',
	evidence_count INT NOT NULL DEFAULT 0,
	sample_preview TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ DEFAULT NOW(),
	updated_at TIMESTAMPTZ DEFAULT NOW(),
	last_seen_at TIMESTAMPTZ DEFAULT NOW(),
	published_at TIMESTAMPTZ NULL,
	dismissed_at TIMESTAMPTZ NULL
)`

const sqlitePromptRuleCandidateEvidenceDDL = `CREATE TABLE IF NOT EXISTS prompt_rule_candidate_evidence (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	candidate_id INTEGER NOT NULL,
	source_kind TEXT NOT NULL DEFAULT '',
	source_ref TEXT NOT NULL DEFAULT '',
	source_ref_hash TEXT NOT NULL,
	sample_preview TEXT NOT NULL DEFAULT '',
	metadata_json TEXT NOT NULL DEFAULT '{}',
	request_protocol TEXT NOT NULL DEFAULT '',
	request_provider TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	api_key_id INTEGER NOT NULL DEFAULT 0,
	api_key_name TEXT NOT NULL DEFAULT '',
	prompt_policy_incident_id TEXT NULL,
	observed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
	created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(candidate_id, source_kind, source_ref_hash)
)`

const postgresPromptRuleCandidateEvidenceDDL = `CREATE TABLE IF NOT EXISTS prompt_rule_candidate_evidence (
	id BIGSERIAL PRIMARY KEY,
	candidate_id BIGINT NOT NULL,
	source_kind VARCHAR(64) NOT NULL DEFAULT '',
	source_ref TEXT NOT NULL DEFAULT '',
	source_ref_hash VARCHAR(64) NOT NULL,
	sample_preview TEXT NOT NULL DEFAULT '',
	metadata_json TEXT NOT NULL DEFAULT '{}',
	request_protocol VARCHAR(64) NOT NULL DEFAULT '',
	request_provider VARCHAR(64) NOT NULL DEFAULT '',
	model VARCHAR(100) NOT NULL DEFAULT '',
	api_key_id INT NOT NULL DEFAULT 0,
	api_key_name VARCHAR(255) NOT NULL DEFAULT '',
	prompt_policy_incident_id VARCHAR(64) NULL,
	observed_at TIMESTAMPTZ DEFAULT NOW(),
	created_at TIMESTAMPTZ DEFAULT NOW(),
	UNIQUE(candidate_id, source_kind, source_ref_hash)
)`

type PromptRuleCandidate struct {
	ID            int64      `json:"id"`
	Fingerprint   string     `json:"fingerprint"`
	Kind          string     `json:"kind"`
	Status        string     `json:"status"`
	LastSource    string     `json:"last_source"`
	Name          string     `json:"name"`
	Category      string     `json:"category"`
	RuleJSON      string     `json:"-"`
	Rationale     string     `json:"rationale,omitempty"`
	SourceURL     string     `json:"source_url,omitempty"`
	EvidenceCount int        `json:"evidence_count"`
	SamplePreview string     `json:"sample_preview,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	LastSeenAt    time.Time  `json:"last_seen_at"`
	PublishedAt   *time.Time `json:"published_at,omitempty"`
	DismissedAt   *time.Time `json:"dismissed_at,omitempty"`
}

type PromptRuleCandidateEvidence struct {
	ID                     int64     `json:"id"`
	CandidateID            int64     `json:"candidate_id"`
	SourceKind             string    `json:"source_kind"`
	SourceRef              string    `json:"source_ref,omitempty"`
	SourceRefHash          string    `json:"source_ref_hash"`
	SamplePreview          string    `json:"sample_preview,omitempty"`
	MetadataJSON           string    `json:"-"`
	Protocol               string    `json:"protocol,omitempty"`
	Provider               string    `json:"provider,omitempty"`
	Model                  string    `json:"model,omitempty"`
	APIKeyID               int64     `json:"api_key_id,omitempty"`
	APIKeyName             string    `json:"api_key_name,omitempty"`
	PromptPolicyIncidentID string    `json:"prompt_policy_incident_id,omitempty"`
	ObservedAt             time.Time `json:"observed_at"`
	CreatedAt              time.Time `json:"created_at"`
}

type PromptRuleCandidateInput struct {
	Fingerprint   string
	Kind          string
	Source        string
	Name          string
	Category      string
	RuleJSON      string
	Rationale     string
	SourceURL     string
	SamplePreview string
}

type PromptRuleCandidateEvidenceInput struct {
	SourceKind             string
	SourceRef              string
	SourceRefHash          string
	SamplePreview          string
	MetadataJSON           string
	Protocol               string
	Provider               string
	Model                  string
	APIKeyID               int64
	APIKeyName             string
	PromptPolicyIncidentID string
	ObservedAt             time.Time
}

// PromptRuleCandidateMigrationCompletion binds one durable migration marker to
// the candidate it completes. The marker is committed in the same transaction
// that removes the historical runtime rule, so merely staging a candidate can
// never be mistaken for a finished migration.
type PromptRuleCandidateMigrationCompletion struct {
	CandidateID int64
	Evidence    PromptRuleCandidateEvidenceInput
}

type PromptRuleCandidateQuery struct {
	Page     int
	PageSize int
	Status   string
	Source   string
	Query    string
}

func (db *DB) ensurePromptRuleCandidatesTable(ctx context.Context) error {
	candidateDDL := postgresPromptRuleCandidatesDDL
	evidenceDDL := postgresPromptRuleCandidateEvidenceDDL
	if db.isSQLite() {
		candidateDDL = sqlitePromptRuleCandidatesDDL
		evidenceDDL = sqlitePromptRuleCandidateEvidenceDDL
	}
	for _, statement := range []string{
		candidateDDL,
		evidenceDDL,
		`CREATE INDEX IF NOT EXISTS idx_prompt_rule_candidates_status_updated ON prompt_rule_candidates(status, updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_prompt_rule_candidates_source_last_seen ON prompt_rule_candidates(last_source, last_seen_at)`,
		`CREATE INDEX IF NOT EXISTS idx_prompt_rule_candidate_evidence_candidate ON prompt_rule_candidate_evidence(candidate_id, observed_at)`,
	} {
		if _, err := db.conn.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func normalizePromptRuleCandidateInput(input PromptRuleCandidateInput) (PromptRuleCandidateInput, error) {
	input.Fingerprint = strings.ToLower(strings.TrimSpace(input.Fingerprint))
	if !validSHA256Hex(input.Fingerprint) {
		return input, errors.New("candidate fingerprint must be a SHA-256 hex string")
	}
	input.Kind = strings.ToLower(strings.TrimSpace(input.Kind))
	if input.Kind == "" {
		input.Kind = PromptRuleCandidateKindPattern
	}
	if input.Kind != PromptRuleCandidateKindPattern && input.Kind != PromptRuleCandidateKindEvidence {
		return input, fmt.Errorf("unsupported candidate kind %q", input.Kind)
	}
	input.Source = strings.TrimSpace(input.Source)
	input.Name = strings.TrimSpace(input.Name)
	input.Category = strings.TrimSpace(input.Category)
	input.RuleJSON = strings.TrimSpace(input.RuleJSON)
	input.Rationale = strings.TrimSpace(input.Rationale)
	input.SourceURL = strings.TrimSpace(input.SourceURL)
	input.SamplePreview = truncateCandidateRunes(strings.TrimSpace(input.SamplePreview), 2000)
	if input.RuleJSON == "" {
		input.RuleJSON = "{}"
	}
	if len(input.RuleJSON) > 64*1024 || !json.Valid([]byte(input.RuleJSON)) {
		return input, errors.New("candidate rule_json must be valid JSON up to 64 KiB")
	}
	if input.Kind == PromptRuleCandidateKindPattern && (input.Name == "" || input.RuleJSON == "{}") {
		return input, errors.New("pattern candidate requires a name and rule_json")
	}
	return input, nil
}

func normalizePromptRuleCandidateEvidenceInput(input PromptRuleCandidateEvidenceInput) (PromptRuleCandidateEvidenceInput, error) {
	input.SourceKind = strings.TrimSpace(input.SourceKind)
	input.SourceRef = truncateCandidateRunes(strings.TrimSpace(input.SourceRef), 2000)
	input.SourceRefHash = strings.ToLower(strings.TrimSpace(input.SourceRefHash))
	if input.SourceRefHash == "" {
		return input, errors.New("candidate evidence requires source_ref_hash")
	}
	if !validSHA256Hex(input.SourceRefHash) {
		return input, errors.New("candidate evidence source_ref_hash must be a SHA-256 hex string")
	}
	input.SamplePreview = truncateCandidateRunes(strings.TrimSpace(input.SamplePreview), 2000)
	input.MetadataJSON = strings.TrimSpace(input.MetadataJSON)
	if input.MetadataJSON == "" {
		input.MetadataJSON = "{}"
	}
	if len(input.MetadataJSON) > 64*1024 || !json.Valid([]byte(input.MetadataJSON)) {
		return input, errors.New("candidate evidence metadata_json must be valid JSON up to 64 KiB")
	}
	input.Protocol = strings.TrimSpace(input.Protocol)
	input.Provider = strings.TrimSpace(input.Provider)
	input.Model = strings.TrimSpace(input.Model)
	input.APIKeyName = strings.TrimSpace(input.APIKeyName)
	input.PromptPolicyIncidentID = truncateCandidateRunes(strings.TrimSpace(input.PromptPolicyIncidentID), 64)
	if input.ObservedAt.IsZero() {
		input.ObservedAt = time.Now().UTC()
	}
	return input, nil
}

func validSHA256Hex(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func truncateCandidateRunes(value string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}

// StagePromptRuleCandidate atomically upserts one global candidate and one
// deduplicated evidence row. Replaying the same source reference does not
// inflate evidence_count, while a genuinely new observation does.
func (db *DB) StagePromptRuleCandidate(ctx context.Context, rawCandidate PromptRuleCandidateInput, rawEvidence PromptRuleCandidateEvidenceInput) (*PromptRuleCandidate, bool, error) {
	if db == nil {
		return nil, false, errors.New("database is nil")
	}
	candidate, err := normalizePromptRuleCandidateInput(rawCandidate)
	if err != nil {
		return nil, false, err
	}
	evidence, err := normalizePromptRuleCandidateEvidenceInput(rawEvidence)
	if err != nil {
		return nil, false, err
	}
	var candidateID int64
	var evidenceAdded bool
	err = db.withSQLiteWriteLock(ctx, func() error {
		tx, beginErr := db.conn.BeginTx(ctx, nil)
		if beginErr != nil {
			return beginErr
		}
		defer tx.Rollback()
		var evidenceID int64
		candidateID, evidenceID, evidenceAdded, err = stagePromptRuleCandidateTx(ctx, tx, candidate, evidence)
		_ = evidenceID
		if err != nil {
			return err
		}
		return tx.Commit()
	})
	if err != nil {
		return nil, false, err
	}
	item, err := db.GetPromptRuleCandidate(ctx, candidateID)
	return item, evidenceAdded, err
}

func stagePromptRuleCandidateTx(ctx context.Context, tx *sql.Tx, candidate PromptRuleCandidateInput, evidence PromptRuleCandidateEvidenceInput) (candidateID int64, evidenceID int64, evidenceAdded bool, err error) {
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO prompt_rule_candidates (
			fingerprint, kind, status, last_source, name, category, rule_json, rationale, source_url,
			evidence_count, sample_preview, created_at, updated_at, last_seen_at
		) VALUES ($1, $2, 'pending', $3, $4, $5, $6, $7, $8, 0, $9, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, $10)
		ON CONFLICT(fingerprint) DO NOTHING
	`, candidate.Fingerprint, candidate.Kind, candidate.Source, candidate.Name, candidate.Category, candidate.RuleJSON,
		candidate.Rationale, candidate.SourceURL, candidate.SamplePreview, evidence.ObservedAt); err != nil {
		return 0, 0, false, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT id FROM prompt_rule_candidates WHERE fingerprint=$1`, candidate.Fingerprint).Scan(&candidateID); err != nil {
		return 0, 0, false, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO prompt_rule_candidate_evidence (
			candidate_id, source_kind, source_ref, source_ref_hash, sample_preview, metadata_json,
			request_protocol, request_provider, model, api_key_id, api_key_name, prompt_policy_incident_id, observed_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, CURRENT_TIMESTAMP)
		ON CONFLICT(candidate_id, source_kind, source_ref_hash) DO NOTHING
	`, candidateID, evidence.SourceKind, evidence.SourceRef, evidence.SourceRefHash, evidence.SamplePreview, evidence.MetadataJSON,
		evidence.Protocol, evidence.Provider, evidence.Model, evidence.APIKeyID, evidence.APIKeyName, evidence.PromptPolicyIncidentID, evidence.ObservedAt)
	if err != nil {
		return 0, 0, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, 0, false, err
	}
	evidenceAdded = affected > 0
	if evidenceAdded {
		if _, err = tx.ExecContext(ctx, `
			UPDATE prompt_rule_candidates SET
				kind = CASE WHEN status='pending' AND $1 >= last_seen_at THEN $2 ELSE kind END,
				last_source = CASE WHEN $1 >= last_seen_at AND $3<>'' THEN $3 ELSE last_source END,
				name = CASE WHEN status='pending' AND $1 >= last_seen_at AND $4<>'' THEN $4 ELSE name END,
				category = CASE WHEN status='pending' AND $1 >= last_seen_at AND $5<>'' THEN $5 ELSE category END,
				rule_json = CASE WHEN status='pending' AND $1 >= last_seen_at AND $6<>'{}' THEN $6 ELSE rule_json END,
				rationale = CASE WHEN status='pending' AND $1 >= last_seen_at AND $7<>'' THEN $7 ELSE rationale END,
				source_url = CASE WHEN $1 >= last_seen_at AND $8<>'' THEN $8 ELSE source_url END,
				sample_preview = CASE WHEN $1 >= last_seen_at AND $9<>'' THEN $9 ELSE sample_preview END,
				evidence_count = evidence_count + 1,
				updated_at = CURRENT_TIMESTAMP,
				last_seen_at = CASE WHEN $1 > last_seen_at THEN $1 ELSE last_seen_at END
			WHERE id=$10
		`, evidence.ObservedAt, candidate.Kind, candidate.Source, candidate.Name, candidate.Category, candidate.RuleJSON,
			candidate.Rationale, candidate.SourceURL, candidate.SamplePreview, candidateID); err != nil {
			return 0, 0, false, err
		}
	}
	if err = tx.QueryRowContext(ctx, `SELECT id FROM prompt_rule_candidate_evidence WHERE candidate_id=$1 AND source_kind=$2 AND source_ref_hash=$3`, candidateID, evidence.SourceKind, evidence.SourceRefHash).Scan(&evidenceID); err != nil {
		return 0, 0, false, err
	}
	return candidateID, evidenceID, evidenceAdded, nil
}

func (db *DB) GetPromptRuleCandidate(ctx context.Context, id int64) (*PromptRuleCandidate, error) {
	return scanPromptRuleCandidate(db.conn.QueryRowContext(ctx, promptRuleCandidateSelect+` WHERE id=$1`, id))
}

func (db *DB) GetPromptRuleCandidateByFingerprint(ctx context.Context, fingerprint string) (*PromptRuleCandidate, error) {
	return scanPromptRuleCandidate(db.conn.QueryRowContext(ctx, promptRuleCandidateSelect+` WHERE fingerprint=$1`, strings.ToLower(strings.TrimSpace(fingerprint))))
}

func (db *DB) ListPromptRuleCandidates(ctx context.Context, query PromptRuleCandidateQuery) ([]*PromptRuleCandidate, int, error) {
	page := query.Page
	if page <= 0 {
		page = 1
	}
	pageSize := query.PageSize
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 50
	}
	clauses := make([]string, 0, 3)
	args := make([]any, 0, 5)
	if status := strings.ToLower(strings.TrimSpace(query.Status)); status != "" && status != "all" {
		args = append(args, status)
		clauses = append(clauses, fmt.Sprintf("status=$%d", len(args)))
	}
	if source := strings.TrimSpace(query.Source); source != "" && source != "all" {
		args = append(args, source)
		clauses = append(clauses, fmt.Sprintf("last_source=$%d", len(args)))
	}
	if value := strings.TrimSpace(query.Query); value != "" {
		args = append(args, "%"+strings.ToLower(value)+"%")
		index := len(args)
		clauses = append(clauses, fmt.Sprintf(`(
			LOWER(name) LIKE $%d OR LOWER(rule_json) LIKE $%d OR LOWER(category) LIKE $%d OR
			LOWER(rationale) LIKE $%d OR LOWER(sample_preview) LIKE $%d OR LOWER(last_source) LIKE $%d
		)`, index, index, index, index, index, index))
	}
	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}
	var total int
	if err := db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM prompt_rule_candidates`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := db.conn.QueryContext(ctx, promptRuleCandidateSelect+where+`
		ORDER BY CASE status WHEN 'pending' THEN 0 WHEN 'published' THEN 1 WHEN 'dismissed' THEN 2 ELSE 3 END, last_seen_at DESC, id DESC
		LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]*PromptRuleCandidate, 0, pageSize)
	for rows.Next() {
		item, scanErr := scanPromptRuleCandidate(rows)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (db *DB) ListPromptRuleCandidateEvidence(ctx context.Context, candidateID int64, limit int) ([]*PromptRuleCandidateEvidence, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := db.conn.QueryContext(ctx, `
		SELECT id, candidate_id, source_kind, source_ref, source_ref_hash, sample_preview, metadata_json,
		       request_protocol, request_provider, model, api_key_id, api_key_name,
		       COALESCE(prompt_policy_incident_id, ''), observed_at, created_at
		FROM prompt_rule_candidate_evidence WHERE candidate_id=$1 ORDER BY observed_at DESC, id DESC LIMIT $2
	`, candidateID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*PromptRuleCandidateEvidence, 0)
	for rows.Next() {
		item := &PromptRuleCandidateEvidence{}
		var observedRaw, createdRaw any
		if err := rows.Scan(&item.ID, &item.CandidateID, &item.SourceKind, &item.SourceRef, &item.SourceRefHash, &item.SamplePreview,
			&item.MetadataJSON, &item.Protocol, &item.Provider, &item.Model, &item.APIKeyID, &item.APIKeyName,
			&item.PromptPolicyIncidentID, &observedRaw, &createdRaw); err != nil {
			return nil, err
		}
		if item.ObservedAt, err = parseDBTimeValue(observedRaw); err != nil {
			return nil, err
		}
		if item.CreatedAt, err = parseDBTimeValue(createdRaw); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (db *DB) HasPromptRuleCandidateEvidence(ctx context.Context, candidateID int64, sourceKind, sourceRefHash string) (bool, error) {
	var count int
	err := db.conn.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM prompt_rule_candidate_evidence
		WHERE candidate_id=$1 AND source_kind=$2 AND source_ref_hash=$3
	`, candidateID, strings.TrimSpace(sourceKind), strings.ToLower(strings.TrimSpace(sourceRefHash))).Scan(&count)
	return count > 0, err
}

func (db *DB) DismissPromptRuleCandidate(ctx context.Context, id int64) (*PromptRuleCandidate, error) {
	err := db.withSQLiteWriteLock(ctx, func() error {
		tx, beginErr := db.conn.BeginTx(ctx, nil)
		if beginErr != nil {
			return beginErr
		}
		defer tx.Rollback()
		query := `SELECT status FROM prompt_rule_candidates WHERE id=$1`
		if !db.isSQLite() {
			query += ` FOR UPDATE`
		}
		var status string
		if scanErr := tx.QueryRowContext(ctx, query, id).Scan(&status); scanErr != nil {
			return scanErr
		}
		if status != PromptRuleCandidateStatusPending && status != PromptRuleCandidateStatusDismissed {
			return fmt.Errorf("%w: candidate status %q cannot be dismissed", ErrPromptRuleCandidateConflict, status)
		}
		result, execErr := tx.ExecContext(ctx, `
			UPDATE prompt_rule_candidates SET status='dismissed', dismissed_at=CURRENT_TIMESTAMP, published_at=NULL, updated_at=CURRENT_TIMESTAMP
			WHERE id=$1
		`, id)
		if execErr != nil {
			return execErr
		}
		if affectedErr := requireAffectedRow(result); affectedErr != nil {
			return affectedErr
		}
		return tx.Commit()
	})
	if err != nil {
		return nil, err
	}
	return db.GetPromptRuleCandidate(ctx, id)
}

// PublishPromptRuleCandidate atomically merges one reviewed rule into the
// persisted runtime custom-pattern set and transitions the candidate lifecycle.
// The expected same-name rule acts as a compare-and-swap guard, while unrelated
// rules written by another replica are preserved. validateMerged runs before
// any update is committed, and the committed candidate row is returned so the
// admin layer has no fallible read-after-commit step.
func (db *DB) PublishPromptRuleCandidate(
	ctx context.Context,
	id int64,
	expectedRuleJSON, candidateName, expectedCurrentRuleJSON, newRuleJSON string,
	validateMerged func(string) error,
) (*PromptRuleCandidate, string, error) {
	expectedRuleJSON = strings.TrimSpace(expectedRuleJSON)
	candidateName = strings.TrimSpace(candidateName)
	expectedCurrentRuleJSON = strings.TrimSpace(expectedCurrentRuleJSON)
	newRuleJSON = strings.TrimSpace(newRuleJSON)
	if expectedRuleJSON == "" || !json.Valid([]byte(expectedRuleJSON)) || newRuleJSON == "" || !json.Valid([]byte(newRuleJSON)) {
		return nil, "", errors.New("candidate rule JSON is invalid")
	}
	if expectedCurrentRuleJSON != "" && !json.Valid([]byte(expectedCurrentRuleJSON)) {
		return nil, "", errors.New("expected current rule JSON is invalid")
	}
	var publishedCandidate *PromptRuleCandidate
	var publishedPatternsJSON string
	err := db.withSQLiteWriteLock(ctx, func() error {
		tx, err := db.conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		// Every publish transaction locks the single runtime settings row before
		// any candidate row. This consistent order prevents two same-name
		// revisions from deadlocking while superseding one another on PostgreSQL.
		if _, err := tx.ExecContext(ctx, `INSERT INTO system_settings (id, prompt_filter_custom_patterns) VALUES (1, '[]') ON CONFLICT(id) DO NOTHING`); err != nil {
			return err
		}
		settingsQuery := `SELECT COALESCE(prompt_filter_custom_patterns, '[]') FROM system_settings WHERE id=1`
		if !db.isSQLite() {
			settingsQuery += ` FOR UPDATE`
		}
		var currentPatternsJSON string
		if err := tx.QueryRowContext(ctx, settingsQuery).Scan(&currentPatternsJSON); err != nil {
			return err
		}
		query := `SELECT kind, status, name, rule_json FROM prompt_rule_candidates WHERE id=$1`
		if !db.isSQLite() {
			query += ` FOR UPDATE`
		}
		var kind, status, storedName, storedRuleJSON string
		if err := tx.QueryRowContext(ctx, query, id).Scan(&kind, &status, &storedName, &storedRuleJSON); err != nil {
			return err
		}
		if kind != PromptRuleCandidateKindPattern {
			return errors.New("only pattern candidates can be published")
		}
		if status == PromptRuleCandidateStatusDismissed || status == PromptRuleCandidateStatusSuperseded {
			return fmt.Errorf("%w: candidate status %q cannot be published", ErrPromptRuleCandidateConflict, status)
		}
		if strings.TrimSpace(storedRuleJSON) != expectedRuleJSON || !strings.EqualFold(strings.TrimSpace(storedName), candidateName) {
			return fmt.Errorf("%w: candidate changed during review; reload before publishing", ErrPromptRuleCandidateConflict)
		}
		if status == PromptRuleCandidateStatusPublished {
			matches, matchErr := promptRuleSetContainsEquivalent(currentPatternsJSON, candidateName, newRuleJSON)
			if matchErr != nil {
				return matchErr
			}
			if !matches {
				return fmt.Errorf("%w: published candidate no longer matches the runtime rule", ErrPromptRuleCandidateConflict)
			}
			publishedPatternsJSON = currentPatternsJSON
			if validateMerged != nil {
				if err := validateMerged(publishedPatternsJSON); err != nil {
					return err
				}
			}
			publishedCandidate, err = scanPromptRuleCandidate(tx.QueryRowContext(ctx, promptRuleCandidateSelect+` WHERE id=$1`, id))
			if err != nil {
				return err
			}
			return tx.Commit()
		}
		publishedPatternsJSON, err = mergePromptRuleCandidateJSON(currentPatternsJSON, candidateName, expectedCurrentRuleJSON, newRuleJSON)
		if err != nil {
			return err
		}
		if validateMerged != nil {
			if err := validateMerged(publishedPatternsJSON); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE system_settings SET prompt_filter_custom_patterns=$1 WHERE id=1`, publishedPatternsJSON); err != nil {
			return err
		}
		if candidateName != "" {
			if _, err := tx.ExecContext(ctx, `
				UPDATE prompt_rule_candidates SET status='superseded', updated_at=CURRENT_TIMESTAMP
				WHERE id<>$1 AND kind='pattern' AND LOWER(name)=LOWER($2)
				  AND (status='published' OR (status='pending' AND id<$1))
			`, id, candidateName); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE prompt_rule_candidates SET status='published', published_at=COALESCE(published_at, CURRENT_TIMESTAMP), dismissed_at=NULL, updated_at=CURRENT_TIMESTAMP WHERE id=$1
		`, id); err != nil {
			return err
		}
		publishedCandidate, err = scanPromptRuleCandidate(tx.QueryRowContext(ctx, promptRuleCandidateSelect+` WHERE id=$1`, id))
		if err != nil {
			return err
		}
		return tx.Commit()
	})
	return publishedCandidate, publishedPatternsJSON, err
}

var ErrPromptRuleCandidateConflict = errors.New("prompt rule candidate state conflict")

func mergePromptRuleCandidateJSON(currentJSON, candidateName, expectedCurrentRuleJSON, newRuleJSON string) (string, error) {
	var newRule struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(newRuleJSON), &newRule); err != nil || !strings.EqualFold(strings.TrimSpace(newRule.Name), candidateName) {
		return "", errors.New("published rule name does not match candidate")
	}
	current, found, err := decodePromptRuleSetForName(currentJSON, candidateName)
	if err != nil {
		return "", err
	}
	if expectedCurrentRuleJSON == "" {
		if found >= 0 {
			return "", fmt.Errorf("%w: a same-name runtime rule appeared during review", ErrPromptRuleCandidateConflict)
		}
		current = append(current, json.RawMessage(append([]byte(nil), newRuleJSON...)))
	} else {
		if found < 0 || !semanticJSONEqual(current[found], []byte(expectedCurrentRuleJSON)) {
			return "", fmt.Errorf("%w: the runtime rule changed during review", ErrPromptRuleCandidateConflict)
		}
		current[found] = json.RawMessage(append([]byte(nil), newRuleJSON...))
	}
	result, err := json.Marshal(current)
	return string(result), err
}

func promptRuleSetContainsEquivalent(currentJSON, candidateName, expectedRuleJSON string) (bool, error) {
	current, found, err := decodePromptRuleSetForName(currentJSON, candidateName)
	if err != nil || found < 0 {
		return false, err
	}
	return semanticJSONEqual(current[found], []byte(expectedRuleJSON)), nil
}

func decodePromptRuleSetForName(currentJSON, candidateName string) ([]json.RawMessage, int, error) {
	var current []json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(currentJSON)), &current); err != nil {
		return nil, -1, fmt.Errorf("current custom patterns JSON is invalid: %w", err)
	}
	found := -1
	for index, raw := range current {
		var item struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &item) == nil && strings.EqualFold(strings.TrimSpace(item.Name), candidateName) {
			if found >= 0 {
				return nil, -1, fmt.Errorf("%w: duplicate current rules named %q", ErrPromptRuleCandidateConflict, candidateName)
			}
			found = index
		}
	}
	return current, found, nil
}

func semanticJSONEqual(left, right []byte) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func (db *DB) ReplacePromptFilterCustomPatterns(ctx context.Context, customPatternsJSON string) error {
	customPatternsJSON = strings.TrimSpace(customPatternsJSON)
	if customPatternsJSON == "" || !json.Valid([]byte(customPatternsJSON)) {
		return errors.New("custom patterns JSON is invalid")
	}
	return db.withSQLiteWriteLock(ctx, func() error {
		_, err := db.conn.ExecContext(ctx, `
			INSERT INTO system_settings (id, prompt_filter_custom_patterns) VALUES (1, $1)
			ON CONFLICT(id) DO UPDATE SET prompt_filter_custom_patterns=excluded.prompt_filter_custom_patterns
		`, customPatternsJSON)
		return err
	})
}

// CompareAndSwapPromptFilterCustomPatterns replaces the runtime rule snapshot
// only when it still equals the caller's reviewed value. It is used by narrow
// maintenance migrations so concurrent explicit publishes are never lost.
func (db *DB) CompareAndSwapPromptFilterCustomPatterns(ctx context.Context, expectedJSON, replacementJSON string) (bool, error) {
	return db.CompareAndSwapPromptFilterCustomPatternsWithMigrationCompletions(ctx, expectedJSON, replacementJSON, nil)
}

// CompareAndSwapPromptFilterCustomPatternsWithMigrationCompletions replaces
// the runtime rule snapshot and writes durable legacy-migration completion
// evidence in one transaction. A failed compare-and-swap writes no evidence;
// an evidence failure rolls the runtime snapshot back as well.
func (db *DB) CompareAndSwapPromptFilterCustomPatternsWithMigrationCompletions(
	ctx context.Context,
	expectedJSON, replacementJSON string,
	rawCompletions []PromptRuleCandidateMigrationCompletion,
) (bool, error) {
	expectedJSON = strings.TrimSpace(expectedJSON)
	replacementJSON = strings.TrimSpace(replacementJSON)
	if expectedJSON == "" || !json.Valid([]byte(expectedJSON)) || replacementJSON == "" || !json.Valid([]byte(replacementJSON)) {
		return false, errors.New("custom patterns compare-and-swap JSON is invalid")
	}
	completions := make([]PromptRuleCandidateMigrationCompletion, len(rawCompletions))
	for index, completion := range rawCompletions {
		if completion.CandidateID <= 0 {
			return false, errors.New("migration completion candidate ID is invalid")
		}
		evidence, err := normalizePromptRuleCandidateEvidenceInput(completion.Evidence)
		if err != nil {
			return false, err
		}
		if evidence.SourceKind != PromptRuleCandidateSourceLegacyMigrationDone {
			return false, errors.New("migration completion evidence has an invalid source kind")
		}
		completions[index] = PromptRuleCandidateMigrationCompletion{CandidateID: completion.CandidateID, Evidence: evidence}
	}
	var swapped bool
	err := db.withSQLiteWriteLock(ctx, func() error {
		tx, err := db.conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()

		settingsQuery := `SELECT COALESCE(NULLIF(TRIM(prompt_filter_custom_patterns), ''), '[]') FROM system_settings WHERE id=1`
		if !db.isSQLite() {
			settingsQuery += ` FOR UPDATE`
		}
		var currentJSON string
		if err := tx.QueryRowContext(ctx, settingsQuery).Scan(&currentJSON); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		if strings.TrimSpace(currentJSON) != expectedJSON {
			return nil
		}

		result, err := tx.ExecContext(ctx, `
			UPDATE system_settings SET prompt_filter_custom_patterns=$1
			WHERE id=1 AND COALESCE(NULLIF(TRIM(prompt_filter_custom_patterns), ''), '[]')=$2
		`, replacementJSON, expectedJSON)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return nil
		}

		for _, completion := range completions {
			evidence := completion.Evidence
			result, err := tx.ExecContext(ctx, `
				INSERT INTO prompt_rule_candidate_evidence (
					candidate_id, source_kind, source_ref, source_ref_hash, sample_preview, metadata_json,
					request_protocol, request_provider, model, api_key_id, api_key_name, observed_at, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, CURRENT_TIMESTAMP)
				ON CONFLICT(candidate_id, source_kind, source_ref_hash) DO NOTHING
			`, completion.CandidateID, evidence.SourceKind, evidence.SourceRef, evidence.SourceRefHash,
				evidence.SamplePreview, evidence.MetadataJSON, evidence.Protocol, evidence.Provider,
				evidence.Model, evidence.APIKeyID, evidence.APIKeyName, evidence.ObservedAt)
			if err != nil {
				return err
			}
			evidenceAffected, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if evidenceAffected == 0 {
				continue
			}
			result, err = tx.ExecContext(ctx, `
				UPDATE prompt_rule_candidates SET
					evidence_count=evidence_count+1,
					updated_at=CURRENT_TIMESTAMP,
					last_seen_at=CASE WHEN $1 > last_seen_at THEN $1 ELSE last_seen_at END
				WHERE id=$2
			`, evidence.ObservedAt, completion.CandidateID)
			if err != nil {
				return err
			}
			if err := requireAffectedRow(result); err != nil {
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		swapped = true
		return nil
	})
	return swapped, err
}

const promptRuleCandidateSelect = `SELECT id, fingerprint, kind, status, last_source, name, category, rule_json,
	rationale, source_url, evidence_count, sample_preview, created_at, updated_at, last_seen_at, published_at, dismissed_at
	FROM prompt_rule_candidates`

func scanPromptRuleCandidate(scanner interface{ Scan(...any) error }) (*PromptRuleCandidate, error) {
	item := &PromptRuleCandidate{}
	var createdRaw, updatedRaw, lastSeenRaw, publishedRaw, dismissedRaw any
	if err := scanner.Scan(&item.ID, &item.Fingerprint, &item.Kind, &item.Status, &item.LastSource, &item.Name, &item.Category,
		&item.RuleJSON, &item.Rationale, &item.SourceURL, &item.EvidenceCount, &item.SamplePreview,
		&createdRaw, &updatedRaw, &lastSeenRaw, &publishedRaw, &dismissedRaw); err != nil {
		return nil, err
	}
	var err error
	if item.CreatedAt, err = parseDBTimeValue(createdRaw); err != nil {
		return nil, err
	}
	if item.UpdatedAt, err = parseDBTimeValue(updatedRaw); err != nil {
		return nil, err
	}
	if item.LastSeenAt, err = parseDBTimeValue(lastSeenRaw); err != nil {
		return nil, err
	}
	published, err := parseDBNullTimeValue(publishedRaw)
	if err != nil {
		return nil, err
	}
	if published.Valid {
		value := published.Time
		item.PublishedAt = &value
	}
	dismissed, err := parseDBNullTimeValue(dismissedRaw)
	if err != nil {
		return nil, err
	}
	if dismissed.Valid {
		value := dismissed.Time
		item.DismissedAt = &value
	}
	return item, nil
}
