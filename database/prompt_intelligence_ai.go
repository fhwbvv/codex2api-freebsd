package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

const (
	PromptRuleCandidateSourceAIAnalysis         = "ai_analysis"
	PromptRuleCandidateSourceAIIdentityUpdate   = "ai_identity_update"
	PromptRuleCandidateSourceAIIdentityRollback = "ai_identity_rollback"
)

type PromptRuleCandidateAIAnalysisSummary struct {
	Count                int
	Latest               *PromptRuleCandidateEvidence
	LatestIdentityChange *PromptRuleCandidateEvidence
}

// ListLatestPromptRuleCandidateAIAnalyses returns the latest durable AI
// attribution for each requested candidate together with its analysis count.
// The result is derived from evidence rows, so it survives restarts and is
// shared by every admin replica.
func (db *DB) ListLatestPromptRuleCandidateAIAnalyses(ctx context.Context, candidateIDs []int64) (map[int64]PromptRuleCandidateAIAnalysisSummary, error) {
	result := make(map[int64]PromptRuleCandidateAIAnalysisSummary)
	analysesByCandidate := make(map[int64]map[int64]*PromptRuleCandidateEvidence)
	if db == nil || len(candidateIDs) == 0 {
		return result, nil
	}
	unique := make([]int64, 0, len(candidateIDs))
	seen := make(map[int64]struct{}, len(candidateIDs))
	for _, candidateID := range candidateIDs {
		if candidateID <= 0 {
			continue
		}
		if _, exists := seen[candidateID]; exists {
			continue
		}
		seen[candidateID] = struct{}{}
		unique = append(unique, candidateID)
	}
	if len(unique) == 0 {
		return result, nil
	}
	sourcePlaceholders := dbPlaceholders(db.isSQLite(), 1, 3)
	placeholders := dbPlaceholders(db.isSQLite(), 4, len(unique))
	args := make([]any, 0, len(unique)+3)
	args = append(args, PromptRuleCandidateSourceAIAnalysis, PromptRuleCandidateSourceAIIdentityUpdate, PromptRuleCandidateSourceAIIdentityRollback)
	for _, candidateID := range unique {
		args = append(args, candidateID)
	}
	rows, err := db.conn.QueryContext(ctx, `
		SELECT evidence.id, evidence.candidate_id, evidence.source_kind, evidence.source_ref,
		       evidence.source_ref_hash, evidence.sample_preview, evidence.metadata_json,
		       evidence.request_protocol, evidence.request_provider, evidence.model,
		       evidence.api_key_id, evidence.api_key_name,
		       COALESCE(evidence.prompt_policy_incident_id, ''), evidence.observed_at,
		       evidence.created_at
		FROM prompt_rule_candidate_evidence evidence
		WHERE evidence.source_kind IN (`+strings.Join(sourcePlaceholders, ",")+`)
		  AND evidence.candidate_id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY evidence.candidate_id, evidence.id
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		item := &PromptRuleCandidateEvidence{}
		var observedRaw, createdRaw any
		if err := rows.Scan(&item.ID, &item.CandidateID, &item.SourceKind, &item.SourceRef,
			&item.SourceRefHash, &item.SamplePreview, &item.MetadataJSON, &item.Protocol,
			&item.Provider, &item.Model, &item.APIKeyID, &item.APIKeyName,
			&item.PromptPolicyIncidentID, &observedRaw, &createdRaw); err != nil {
			return nil, err
		}
		if item.ObservedAt, err = parseDBTimeValue(observedRaw); err != nil {
			return nil, err
		}
		if item.CreatedAt, err = parseDBTimeValue(createdRaw); err != nil {
			return nil, err
		}
		summary := result[item.CandidateID]
		if item.SourceKind == PromptRuleCandidateSourceAIAnalysis {
			summary.Count++
			summary.Latest = item
			if analysesByCandidate[item.CandidateID] == nil {
				analysesByCandidate[item.CandidateID] = make(map[int64]*PromptRuleCandidateEvidence)
			}
			analysesByCandidate[item.CandidateID][item.ID] = item
		} else {
			summary.LatestIdentityChange = item
		}
		result[item.CandidateID] = summary
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for candidateID, summary := range result {
		if summary.LatestIdentityChange == nil {
			continue
		}
		var revision struct {
			AnalysisEvidenceID int64 `json:"analysis_evidence_id"`
		}
		if json.Unmarshal([]byte(summary.LatestIdentityChange.MetadataJSON), &revision) == nil {
			if linked := analysesByCandidate[candidateID][revision.AnalysisEvidenceID]; linked != nil {
				summary.Latest = linked
				result[candidateID] = summary
			}
		}
	}
	return result, nil
}

// ReconcilePromptRuleCandidateIdentityStatuses closes historical identity
// attribution chains that were persisted before candidate lifecycle updates
// became transactional. The newest identity update/rollback evidence is the
// source of truth; original evidence and runtime rules are not changed.
func (db *DB) ReconcilePromptRuleCandidateIdentityStatuses(ctx context.Context) error {
	if db == nil {
		return errors.New("database is nil")
	}
	return db.withSQLiteWriteLock(ctx, func() error {
		tx, err := db.conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(ctx, `
			UPDATE prompt_rule_candidates
			SET status='published', published_at=COALESCE(published_at, CURRENT_TIMESTAMP),
				dismissed_at=NULL, updated_at=CURRENT_TIMESTAMP
			WHERE kind='evidence' AND status='pending'
			  AND (SELECT evidence.source_kind FROM prompt_rule_candidate_evidence evidence
			       WHERE evidence.candidate_id=prompt_rule_candidates.id
			         AND evidence.source_kind IN ($1,$2)
			       ORDER BY evidence.id DESC LIMIT 1)=$1
		`, PromptRuleCandidateSourceAIIdentityUpdate, PromptRuleCandidateSourceAIIdentityRollback); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `
			UPDATE prompt_rule_candidates
			SET status='pending', published_at=NULL, dismissed_at=NULL, updated_at=CURRENT_TIMESTAMP
			WHERE kind='evidence' AND status='published'
			  AND (SELECT evidence.source_kind FROM prompt_rule_candidate_evidence evidence
			       WHERE evidence.candidate_id=prompt_rule_candidates.id
			         AND evidence.source_kind IN ($1,$2)
			       ORDER BY evidence.id DESC LIMIT 1)=$2
		`, PromptRuleCandidateSourceAIIdentityUpdate, PromptRuleCandidateSourceAIIdentityRollback); err != nil {
			return err
		}
		return tx.Commit()
	})
}

// AddPromptRuleCandidateEvidence attaches one deduplicated audit event to an
// existing candidate without changing its kind, source, or proposed rule.
func (db *DB) AddPromptRuleCandidateEvidence(ctx context.Context, candidateID int64, raw PromptRuleCandidateEvidenceInput) (*PromptRuleCandidateEvidence, bool, error) {
	if db == nil {
		return nil, false, errors.New("database is nil")
	}
	if candidateID <= 0 {
		return nil, false, errors.New("candidate ID is invalid")
	}
	evidence, err := normalizePromptRuleCandidateEvidenceInput(raw)
	if err != nil {
		return nil, false, err
	}
	var evidenceID int64
	var added bool
	err = db.withSQLiteWriteLock(ctx, func() error {
		tx, beginErr := db.conn.BeginTx(ctx, nil)
		if beginErr != nil {
			return beginErr
		}
		defer tx.Rollback()
		var exists int
		if scanErr := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM prompt_rule_candidates WHERE id=$1`, candidateID).Scan(&exists); scanErr != nil {
			return scanErr
		}
		if exists == 0 {
			return sql.ErrNoRows
		}
		result, execErr := tx.ExecContext(ctx, `
			INSERT INTO prompt_rule_candidate_evidence (
				candidate_id, source_kind, source_ref, source_ref_hash, sample_preview, metadata_json,
				request_protocol, request_provider, model, api_key_id, api_key_name, prompt_policy_incident_id,
				observed_at, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,CURRENT_TIMESTAMP)
			ON CONFLICT(candidate_id, source_kind, source_ref_hash) DO NOTHING
		`, candidateID, evidence.SourceKind, evidence.SourceRef, evidence.SourceRefHash, evidence.SamplePreview,
			evidence.MetadataJSON, evidence.Protocol, evidence.Provider, evidence.Model, evidence.APIKeyID,
			evidence.APIKeyName, evidence.PromptPolicyIncidentID, evidence.ObservedAt)
		if execErr != nil {
			return execErr
		}
		affected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			return affectedErr
		}
		added = affected > 0
		if added {
			result, execErr = tx.ExecContext(ctx, `
				UPDATE prompt_rule_candidates SET evidence_count=evidence_count+1,
					updated_at=CURRENT_TIMESTAMP,
					last_seen_at=CASE WHEN $1 > last_seen_at THEN $1 ELSE last_seen_at END
				WHERE id=$2
			`, evidence.ObservedAt, candidateID)
			if execErr != nil {
				return execErr
			}
			if affectedErr := requireAffectedRow(result); affectedErr != nil {
				return affectedErr
			}
		}
		if scanErr := tx.QueryRowContext(ctx, `
			SELECT id FROM prompt_rule_candidate_evidence
			WHERE candidate_id=$1 AND source_kind=$2 AND source_ref_hash=$3
		`, candidateID, evidence.SourceKind, evidence.SourceRefHash).Scan(&evidenceID); scanErr != nil {
			return scanErr
		}
		return tx.Commit()
	})
	if err != nil {
		return nil, false, err
	}
	item, err := db.GetPromptRuleCandidateEvidence(ctx, evidenceID)
	return item, added, err
}

func (db *DB) GetPromptRuleCandidateEvidence(ctx context.Context, evidenceID int64) (*PromptRuleCandidateEvidence, error) {
	item := &PromptRuleCandidateEvidence{}
	var observedRaw, createdRaw any
	err := db.conn.QueryRowContext(ctx, `
		SELECT id, candidate_id, source_kind, source_ref, source_ref_hash, sample_preview, metadata_json,
		       request_protocol, request_provider, model, api_key_id, api_key_name,
		       COALESCE(prompt_policy_incident_id, ''), observed_at, created_at
		FROM prompt_rule_candidate_evidence WHERE id=$1
	`, evidenceID).Scan(&item.ID, &item.CandidateID, &item.SourceKind, &item.SourceRef, &item.SourceRefHash,
		&item.SamplePreview, &item.MetadataJSON, &item.Protocol, &item.Provider, &item.Model,
		&item.APIKeyID, &item.APIKeyName, &item.PromptPolicyIncidentID, &observedRaw, &createdRaw)
	if err != nil {
		return nil, err
	}
	if item.ObservedAt, err = parseDBTimeValue(observedRaw); err != nil {
		return nil, err
	}
	if item.CreatedAt, err = parseDBTimeValue(createdRaw); err != nil {
		return nil, err
	}
	return item, nil
}

// CompareAndSwapPromptFilterAdvancedConfigWithEvidence changes only the
// persisted advanced Prompt configuration and commits its identity audit event
// in the same transaction. This prevents an applied identity change from
// existing without a durable revision record.
func (db *DB) CompareAndSwapPromptFilterAdvancedConfigWithEvidence(
	ctx context.Context,
	candidateID int64,
	expectedJSON, replacementJSON string,
	targetCandidateStatus string,
	rawEvidence PromptRuleCandidateEvidenceInput,
) (bool, *PromptRuleCandidateEvidence, error) {
	expectedJSON = strings.TrimSpace(expectedJSON)
	replacementJSON = strings.TrimSpace(replacementJSON)
	if expectedJSON == "" {
		expectedJSON = "{}"
	}
	if replacementJSON == "" || !json.Valid([]byte(expectedJSON)) || !json.Valid([]byte(replacementJSON)) {
		return false, nil, errors.New("advanced config compare-and-swap JSON is invalid")
	}
	if candidateID <= 0 {
		return false, nil, errors.New("candidate ID is invalid")
	}
	if targetCandidateStatus != PromptRuleCandidateStatusPending && targetCandidateStatus != PromptRuleCandidateStatusPublished {
		return false, nil, errors.New("candidate target status is invalid")
	}
	expectedCandidateStatus := PromptRuleCandidateStatusPending
	if targetCandidateStatus == PromptRuleCandidateStatusPending {
		expectedCandidateStatus = PromptRuleCandidateStatusPublished
	}
	evidence, err := normalizePromptRuleCandidateEvidenceInput(rawEvidence)
	if err != nil {
		return false, nil, err
	}
	var swapped bool
	var evidenceID int64
	err = db.withSQLiteWriteLock(ctx, func() error {
		tx, beginErr := db.conn.BeginTx(ctx, nil)
		if beginErr != nil {
			return beginErr
		}
		defer tx.Rollback()
		if _, insertErr := tx.ExecContext(ctx, `INSERT INTO system_settings (id, prompt_filter_advanced_config) VALUES (1, '{}') ON CONFLICT(id) DO NOTHING`); insertErr != nil {
			return insertErr
		}
		settingsQuery := `SELECT COALESCE(NULLIF(TRIM(prompt_filter_advanced_config), ''), '{}') FROM system_settings WHERE id=1`
		if !db.isSQLite() {
			settingsQuery += ` FOR UPDATE`
		}
		var currentJSON string
		if scanErr := tx.QueryRowContext(ctx, settingsQuery).Scan(&currentJSON); scanErr != nil {
			return scanErr
		}
		if strings.TrimSpace(currentJSON) != expectedJSON {
			return nil
		}
		result, execErr := tx.ExecContext(ctx, `
			UPDATE system_settings SET prompt_filter_advanced_config=$1
			WHERE id=1 AND COALESCE(NULLIF(TRIM(prompt_filter_advanced_config), ''), '{}')=$2
		`, replacementJSON, expectedJSON)
		if execErr != nil {
			return execErr
		}
		affected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			return affectedErr
		}
		if affected == 0 {
			return nil
		}
		result, execErr = tx.ExecContext(ctx, `
			INSERT INTO prompt_rule_candidate_evidence (
				candidate_id, source_kind, source_ref, source_ref_hash, sample_preview, metadata_json,
				request_protocol, request_provider, model, api_key_id, api_key_name, prompt_policy_incident_id,
				observed_at, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,CURRENT_TIMESTAMP)
			ON CONFLICT(candidate_id, source_kind, source_ref_hash) DO NOTHING
		`, candidateID, evidence.SourceKind, evidence.SourceRef, evidence.SourceRefHash, evidence.SamplePreview,
			evidence.MetadataJSON, evidence.Protocol, evidence.Provider, evidence.Model, evidence.APIKeyID,
			evidence.APIKeyName, evidence.PromptPolicyIncidentID, evidence.ObservedAt)
		if execErr != nil {
			return execErr
		}
		evidenceAffected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			return affectedErr
		}
		if evidenceAffected == 0 {
			return errors.New("identity revision evidence already exists")
		}
		if scanErr := tx.QueryRowContext(ctx, `
			SELECT id FROM prompt_rule_candidate_evidence
			WHERE candidate_id=$1 AND source_kind=$2 AND source_ref_hash=$3
		`, candidateID, evidence.SourceKind, evidence.SourceRefHash).Scan(&evidenceID); scanErr != nil {
			return scanErr
		}
		result, execErr = tx.ExecContext(ctx, `
			UPDATE prompt_rule_candidates SET evidence_count=evidence_count+1,
				status=$3,
				published_at=CASE WHEN $3='published' THEN COALESCE(published_at, CURRENT_TIMESTAMP) ELSE NULL END,
				dismissed_at=NULL,
				updated_at=CURRENT_TIMESTAMP,
				last_seen_at=CASE WHEN $1 > last_seen_at THEN $1 ELSE last_seen_at END
			WHERE id=$2 AND kind='evidence' AND status=$4
		`, evidence.ObservedAt, candidateID, targetCandidateStatus, expectedCandidateStatus)
		if execErr != nil {
			return execErr
		}
		if affectedErr := requireAffectedRow(result); affectedErr != nil {
			return affectedErr
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return commitErr
		}
		swapped = true
		return nil
	})
	if err != nil || !swapped {
		return swapped, nil, err
	}
	item, err := db.GetPromptRuleCandidateEvidence(ctx, evidenceID)
	return true, item, err
}
