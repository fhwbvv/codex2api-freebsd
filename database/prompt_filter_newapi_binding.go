package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	PromptFilterPolicyModeInherit = "inherit"
	PromptFilterPolicyModeOff     = "off"
	PromptFilterPolicyModeShadow  = "shadow"
	PromptFilterPolicyModeWarn    = "warn"
	PromptFilterPolicyModeEnforce = "enforce"

	PromptFilterPolicyProfileInherit  = "inherit"
	PromptFilterPolicyProfileBalanced = "balanced"
	PromptFilterPolicyProfileStrict   = "strict"
	PromptFilterPolicyProfileResearch = "research"

	PromptFilterScopeInherit   = "inherit"
	PromptFilterScopeLocalOnly = "local_only"
	PromptFilterScopeOff       = "off"
)

var ErrPromptFilterNewAPIBindingConflict = errors.New("prompt filter NewAPI binding conflict")

var promptFilterPlatformCodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

const sqlitePromptFilterNewAPIBindingsDDL = `CREATE TABLE IF NOT EXISTS prompt_filter_newapi_bindings (
	api_key_id INTEGER PRIMARY KEY,
	platform_code TEXT NOT NULL UNIQUE,
	platform_name TEXT NOT NULL DEFAULT '',
	secret TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	require_signed_identity INTEGER NOT NULL DEFAULT 0,
	prompt_filter_scope TEXT NOT NULL DEFAULT 'inherit',
	policy_mode TEXT NOT NULL DEFAULT 'inherit',
	policy_profile TEXT NOT NULL DEFAULT 'inherit',
	previous_secret TEXT NOT NULL DEFAULT '',
	previous_secret_expires_at TIMESTAMP NULL,
	updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
)`

const postgresPromptFilterNewAPIBindingsDDL = `CREATE TABLE IF NOT EXISTS prompt_filter_newapi_bindings (
	api_key_id INT PRIMARY KEY,
	platform_code VARCHAR(32) NOT NULL UNIQUE,
	platform_name VARCHAR(255) NOT NULL DEFAULT '',
	secret TEXT NOT NULL,
	enabled BOOLEAN NOT NULL DEFAULT TRUE,
	require_signed_identity BOOLEAN NOT NULL DEFAULT FALSE,
	prompt_filter_scope VARCHAR(16) NOT NULL DEFAULT 'inherit',
	policy_mode VARCHAR(16) NOT NULL DEFAULT 'inherit',
	policy_profile VARCHAR(16) NOT NULL DEFAULT 'inherit',
	previous_secret TEXT NOT NULL DEFAULT '',
	previous_secret_expires_at TIMESTAMPTZ NULL,
	updated_at TIMESTAMPTZ DEFAULT NOW()
)`

// PromptFilterNewAPIBinding binds one Codex2API API key to exactly one calling
// platform. Secrets are deliberately excluded from admin JSON responses by the
// admin layer, but remain available in the in-memory auth snapshot.
type PromptFilterNewAPIBinding struct {
	APIKeyID                int64      `json:"api_key_id"`
	PlatformCode            string     `json:"platform_code"`
	PlatformName            string     `json:"platform_name"`
	Secret                  string     `json:"-"`
	Enabled                 bool       `json:"enabled"`
	RequireSignedIdentity   bool       `json:"require_signed_identity"`
	PromptFilterScope       string     `json:"prompt_filter_scope"`
	PolicyMode              string     `json:"policy_mode"`
	PolicyProfile           string     `json:"policy_profile"`
	PreviousSecret          string     `json:"-"`
	PreviousSecretExpiresAt *time.Time `json:"previous_secret_expires_at,omitempty"`
	UpdatedAt               time.Time  `json:"updated_at"`
}

func (db *DB) ensurePromptFilterNewAPIBindingsTable(ctx context.Context) error {
	ddl := postgresPromptFilterNewAPIBindingsDDL
	if db.isSQLite() {
		ddl = sqlitePromptFilterNewAPIBindingsDDL
	}
	if _, err := db.conn.ExecContext(ctx, ddl); err != nil {
		return err
	}
	if db.isSQLite() {
		if err := db.ensureSQLiteColumn(ctx, "prompt_filter_newapi_bindings", "prompt_filter_scope", "TEXT NOT NULL DEFAULT 'inherit'"); err != nil {
			return err
		}
	} else if _, err := db.conn.ExecContext(ctx, `ALTER TABLE prompt_filter_newapi_bindings ADD COLUMN IF NOT EXISTS prompt_filter_scope VARCHAR(16) NOT NULL DEFAULT 'inherit'`); err != nil {
		return err
	}
	// Binding-level policy overrides were retired. Keep the legacy columns for
	// a low-risk rolling migration, but neutralize all stored values so an old
	// shadow/off row cannot silently override the unified GuardPipeline.
	_, err := db.conn.ExecContext(ctx, `UPDATE prompt_filter_newapi_bindings
		SET policy_mode='inherit', policy_profile='inherit',
			prompt_filter_scope=CASE WHEN prompt_filter_scope IN ('inherit','local_only','off') THEN prompt_filter_scope ELSE 'inherit' END
		WHERE policy_mode<>'inherit' OR policy_profile<>'inherit' OR prompt_filter_scope NOT IN ('inherit','local_only','off')`)
	return err
}

func NormalizePromptFilterScope(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", PromptFilterScopeInherit:
		return PromptFilterScopeInherit, true
	case PromptFilterScopeLocalOnly, PromptFilterScopeOff:
		return value, true
	default:
		return "", false
	}
}

func NormalizePromptFilterPolicyMode(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", PromptFilterPolicyModeInherit:
		return PromptFilterPolicyModeInherit, true
	case PromptFilterPolicyModeOff, PromptFilterPolicyModeShadow, PromptFilterPolicyModeWarn, PromptFilterPolicyModeEnforce:
		return value, true
	default:
		return "", false
	}
}

func NormalizePromptFilterPolicyProfile(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", PromptFilterPolicyProfileInherit:
		return PromptFilterPolicyProfileInherit, true
	case PromptFilterPolicyProfileBalanced, PromptFilterPolicyProfileStrict, PromptFilterPolicyProfileResearch:
		return value, true
	default:
		return "", false
	}
}

// NormalizePromptFilterPlatformCode is the canonical validation boundary used
// by persistence, admin APIs, and signed NewAPI metadata. Invalid or overlong
// values are rejected rather than truncated into another platform identity.
func NormalizePromptFilterPlatformCode(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	return value, promptFilterPlatformCodePattern.MatchString(value)
}

func (db *DB) ListPromptFilterNewAPIBindings(ctx context.Context) ([]*PromptFilterNewAPIBinding, error) {
	rows, err := db.conn.QueryContext(ctx, `SELECT api_key_id, platform_code, platform_name, secret, enabled, require_signed_identity, prompt_filter_scope, policy_mode, policy_profile, previous_secret, previous_secret_expires_at, updated_at FROM prompt_filter_newapi_bindings ORDER BY api_key_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bindings := make([]*PromptFilterNewAPIBinding, 0)
	for rows.Next() {
		binding, scanErr := scanPromptFilterNewAPIBinding(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}

func (db *DB) GetPromptFilterNewAPIBinding(ctx context.Context, apiKeyID int64) (*PromptFilterNewAPIBinding, error) {
	return scanPromptFilterNewAPIBinding(db.conn.QueryRowContext(ctx, `SELECT api_key_id, platform_code, platform_name, secret, enabled, require_signed_identity, prompt_filter_scope, policy_mode, policy_profile, previous_secret, previous_secret_expires_at, updated_at FROM prompt_filter_newapi_bindings WHERE api_key_id = $1`, apiKeyID))
}

func (db *DB) CreatePromptFilterNewAPIBinding(ctx context.Context, binding *PromptFilterNewAPIBinding) error {
	if binding == nil {
		return errors.New("binding is nil")
	}
	if binding.APIKeyID <= 0 {
		return errors.New("api_key_id must be positive")
	}
	platformCode, ok := NormalizePromptFilterPlatformCode(binding.PlatformCode)
	if !ok {
		return errors.New("platform_code must match ^[a-z0-9][a-z0-9_-]{0,31}$")
	}
	secret := strings.TrimSpace(binding.Secret)
	if len(secret) < 32 {
		return errors.New("secret must contain at least 32 characters")
	}
	mode := PromptFilterPolicyModeInherit
	profile := PromptFilterPolicyProfileInherit
	scope, ok := NormalizePromptFilterScope(binding.PromptFilterScope)
	if !ok {
		return errors.New("prompt_filter_scope must be inherit, local_only, or off")
	}
	binding.PromptFilterScope = scope
	binding.PolicyMode = mode
	binding.PolicyProfile = profile
	return db.withSQLiteWriteLock(ctx, func() error {
		_, err := db.conn.ExecContext(ctx, `INSERT INTO prompt_filter_newapi_bindings (api_key_id, platform_code, platform_name, secret, enabled, require_signed_identity, prompt_filter_scope, policy_mode, policy_profile, previous_secret, previous_secret_expires_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, '', NULL, CURRENT_TIMESTAMP)`, binding.APIKeyID, platformCode, strings.TrimSpace(binding.PlatformName), secret, binding.Enabled, binding.RequireSignedIdentity, scope, mode, profile)
		if isPromptFilterNewAPIBindingConflict(err) {
			return fmt.Errorf("%w: %v", ErrPromptFilterNewAPIBindingConflict, err)
		}
		return err
	})
}

func (db *DB) UpdatePromptFilterNewAPIBinding(ctx context.Context, binding *PromptFilterNewAPIBinding) error {
	if binding == nil {
		return errors.New("binding is nil")
	}
	if binding.APIKeyID <= 0 {
		return errors.New("api_key_id must be positive")
	}
	platformCode, ok := NormalizePromptFilterPlatformCode(binding.PlatformCode)
	if !ok {
		return errors.New("platform_code must match ^[a-z0-9][a-z0-9_-]{0,31}$")
	}
	mode := PromptFilterPolicyModeInherit
	profile := PromptFilterPolicyProfileInherit
	scope, ok := NormalizePromptFilterScope(binding.PromptFilterScope)
	if !ok {
		return errors.New("prompt_filter_scope must be inherit, local_only, or off")
	}
	binding.PromptFilterScope = scope
	binding.PolicyMode = mode
	binding.PolicyProfile = profile
	return db.withSQLiteWriteLock(ctx, func() error {
		result, err := db.conn.ExecContext(ctx, `UPDATE prompt_filter_newapi_bindings SET platform_code=$1, platform_name=$2, enabled=$3, require_signed_identity=$4, prompt_filter_scope=$5, policy_mode=$6, policy_profile=$7, updated_at=CURRENT_TIMESTAMP WHERE api_key_id=$8`, platformCode, strings.TrimSpace(binding.PlatformName), binding.Enabled, binding.RequireSignedIdentity, scope, mode, profile, binding.APIKeyID)
		if isPromptFilterNewAPIBindingConflict(err) {
			return fmt.Errorf("%w: %v", ErrPromptFilterNewAPIBindingConflict, err)
		}
		if err != nil {
			return err
		}
		return requireAffectedRow(result)
	})
}

// ReplacePromptFilterNewAPIBindingSecret atomically rotates the current secret.
// A positive grace duration keeps the old secret valid until the stored expiry;
// zero removes it immediately.
func (db *DB) ReplacePromptFilterNewAPIBindingSecret(ctx context.Context, apiKeyID int64, secret string, grace time.Duration) error {
	var previousExpiry *time.Time
	if grace > 0 {
		expiresAt := time.Now().UTC().Add(grace)
		previousExpiry = &expiresAt
	}
	return db.ReplacePromptFilterNewAPIBindingSecretAt(ctx, apiKeyID, secret, previousExpiry)
}

// ReplacePromptFilterNewAPIBindingSecretAt persists the exact grace deadline
// supplied by the caller. This lets the database row and the immediately
// published runtime snapshot share one committed state.
func (db *DB) ReplacePromptFilterNewAPIBindingSecretAt(ctx context.Context, apiKeyID int64, secret string, previousExpiry *time.Time) error {
	secret = strings.TrimSpace(secret)
	if apiKeyID <= 0 {
		return errors.New("api_key_id must be positive")
	}
	if len(secret) < 32 {
		return errors.New("secret must contain at least 32 characters")
	}
	var previousExpiryArg interface{}
	if previousExpiry != nil {
		expiresAt := previousExpiry.UTC()
		previousExpiryArg = expiresAt
	}
	return db.withSQLiteWriteLock(ctx, func() error {
		result, err := db.conn.ExecContext(ctx, `UPDATE prompt_filter_newapi_bindings SET previous_secret=CASE WHEN $1 IS NULL THEN '' ELSE secret END, previous_secret_expires_at=$1, secret=$2, updated_at=CURRENT_TIMESTAMP WHERE api_key_id=$3`, previousExpiryArg, secret, apiKeyID)
		if err != nil {
			return err
		}
		return requireAffectedRow(result)
	})
}

func (db *DB) DeletePromptFilterNewAPIBinding(ctx context.Context, apiKeyID int64) error {
	return db.withSQLiteWriteLock(ctx, func() error {
		result, err := db.conn.ExecContext(ctx, `DELETE FROM prompt_filter_newapi_bindings WHERE api_key_id=$1`, apiKeyID)
		if err != nil {
			return err
		}
		return requireAffectedRow(result)
	})
}

func scanPromptFilterNewAPIBinding(scanner interface{ Scan(...interface{}) error }) (*PromptFilterNewAPIBinding, error) {
	binding := &PromptFilterNewAPIBinding{}
	var previousExpiryRaw, updatedAtRaw interface{}
	if err := scanner.Scan(&binding.APIKeyID, &binding.PlatformCode, &binding.PlatformName, &binding.Secret, &binding.Enabled, &binding.RequireSignedIdentity, &binding.PromptFilterScope, &binding.PolicyMode, &binding.PolicyProfile, &binding.PreviousSecret, &previousExpiryRaw, &updatedAtRaw); err != nil {
		return nil, err
	}
	if normalized, ok := NormalizePromptFilterScope(binding.PromptFilterScope); ok {
		binding.PromptFilterScope = normalized
	} else {
		binding.PromptFilterScope = PromptFilterScopeInherit
	}
	previousExpiry, err := parseDBNullTimeValue(previousExpiryRaw)
	if err != nil {
		return nil, fmt.Errorf("parse previous secret expiry: %w", err)
	}
	if previousExpiry.Valid {
		t := previousExpiry.Time
		binding.PreviousSecretExpiresAt = &t
	}
	binding.UpdatedAt, err = parseDBTimeValue(updatedAtRaw)
	if err != nil {
		return nil, fmt.Errorf("parse binding updated time: %w", err)
	}
	return binding, nil
}

func requireAffectedRow(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func isPromptFilterNewAPIBindingConflict(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate key") || strings.Contains(message, "23505")
}
