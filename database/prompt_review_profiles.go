package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// PromptReviewProfile is a named, server-side review provider profile. API
// keys are deliberately kept out of the admin response; callers should use
// PromptReviewProfileMetadata for list/detail views.
type PromptReviewProfile struct {
	ID            string
	Name          string
	BaseURL       string
	Model         string
	RequestMode   string
	AdapterJSON   string
	APIKeys       string
	TimeoutSecond int
	Active        bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (db *DB) ensurePromptReviewProfiles(ctx context.Context) error {
	if db == nil || db.conn == nil {
		return errors.New("database is not initialized")
	}
	_, err := db.conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS prompt_review_profiles (
			id VARCHAR(64) PRIMARY KEY,
			name VARCHAR(120) NOT NULL,
			base_url VARCHAR(512) NOT NULL DEFAULT '',
			model VARCHAR(128) NOT NULL DEFAULT '',
			request_mode VARCHAR(32) NOT NULL DEFAULT 'moderations',
			adapter_json TEXT NOT NULL DEFAULT '{}',
			api_keys TEXT NOT NULL DEFAULT '',
			timeout_seconds INTEGER NOT NULL DEFAULT 10,
			active BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	return err
}

func (db *DB) ListPromptReviewProfiles(ctx context.Context) ([]PromptReviewProfile, error) {
	if err := db.ensurePromptReviewProfiles(ctx); err != nil {
		return nil, err
	}
	rows, err := db.conn.QueryContext(ctx, `
		SELECT id, name, base_url, model, request_mode, adapter_json, api_keys,
		       timeout_seconds, active, created_at, updated_at
		FROM prompt_review_profiles ORDER BY active DESC, updated_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	profiles := make([]PromptReviewProfile, 0)
	for rows.Next() {
		var profile PromptReviewProfile
		if err := rows.Scan(&profile.ID, &profile.Name, &profile.BaseURL, &profile.Model,
			&profile.RequestMode, &profile.AdapterJSON, &profile.APIKeys,
			&profile.TimeoutSecond, &profile.Active, &profile.CreatedAt, &profile.UpdatedAt); err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	return profiles, rows.Err()
}

func (db *DB) GetPromptReviewProfile(ctx context.Context, id string) (*PromptReviewProfile, error) {
	if err := db.ensurePromptReviewProfiles(ctx); err != nil {
		return nil, err
	}
	var profile PromptReviewProfile
	err := db.conn.QueryRowContext(ctx, `
		SELECT id, name, base_url, model, request_mode, adapter_json, api_keys,
		       timeout_seconds, active, created_at, updated_at
		FROM prompt_review_profiles WHERE id = $1`, id).Scan(
		&profile.ID, &profile.Name, &profile.BaseURL, &profile.Model,
		&profile.RequestMode, &profile.AdapterJSON, &profile.APIKeys,
		&profile.TimeoutSecond, &profile.Active, &profile.CreatedAt, &profile.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

func (db *DB) UpsertPromptReviewProfile(ctx context.Context, profile PromptReviewProfile) error {
	if err := db.ensurePromptReviewProfiles(ctx); err != nil {
		return err
	}
	profile.ID = strings.TrimSpace(profile.ID)
	profile.Name = strings.TrimSpace(profile.Name)
	if profile.ID == "" || profile.Name == "" {
		return errors.New("prompt review profile id and name are required")
	}
	if profile.TimeoutSecond <= 0 {
		profile.TimeoutSecond = 10
	}
	_, err := db.conn.ExecContext(ctx, `
		INSERT INTO prompt_review_profiles
			(id, name, base_url, model, request_mode, adapter_json, api_keys, timeout_seconds, active, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)
		ON CONFLICT (id) DO UPDATE SET
			name=EXCLUDED.name, base_url=EXCLUDED.base_url, model=EXCLUDED.model,
			request_mode=EXCLUDED.request_mode, adapter_json=EXCLUDED.adapter_json,
			api_keys=EXCLUDED.api_keys, timeout_seconds=EXCLUDED.timeout_seconds,
			active=EXCLUDED.active, updated_at=CURRENT_TIMESTAMP`,
		profile.ID, profile.Name, profile.BaseURL, profile.Model, profile.RequestMode,
		profile.AdapterJSON, profile.APIKeys, profile.TimeoutSecond, profile.Active)
	return err
}

func (db *DB) SetPromptReviewProfileActive(ctx context.Context, id string) error {
	if err := db.ensurePromptReviewProfiles(ctx); err != nil {
		return err
	}
	return db.withWriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE prompt_review_profiles SET active=FALSE, updated_at=CURRENT_TIMESTAMP`)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE prompt_review_profiles SET active=TRUE, updated_at=CURRENT_TIMESTAMP WHERE id=$1`, id)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows != 1 {
			return sql.ErrNoRows
		}
		return nil
	})
}

func (db *DB) DeletePromptReviewProfile(ctx context.Context, id string) error {
	if err := db.ensurePromptReviewProfiles(ctx); err != nil {
		return err
	}
	_, err := db.conn.ExecContext(ctx, `DELETE FROM prompt_review_profiles WHERE id=$1`, id)
	return err
}
