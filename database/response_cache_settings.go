package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
)

const (
	DefaultResponseCacheLocalMaxBytes       = int64(64 << 20)
	DefaultResponseCacheLocalMaxEntryBytes  = int64(8 << 20)
	DefaultResponseCacheReconstructMaxBytes = int64(64 << 20)
	DefaultResponseCacheConfigGeneration    = int64(1)

	MinResponseCacheLocalMaxBytes       = int64(8 << 20)
	MaxResponseCacheLocalMaxBytes       = int64(4 << 30)
	MinResponseCacheLocalMaxEntryBytes  = int64(1 << 20)
	MaxResponseCacheLocalMaxEntryBytes  = int64(256 << 20)
	MinResponseCacheReconstructMaxBytes = int64(8 << 20)
	MaxResponseCacheReconstructMaxBytes = int64(512 << 20)
)

var (
	ErrInvalidResponseCacheSettings    = errors.New("invalid response cache settings")
	ErrResponseCacheGenerationOverflow = errors.New("response cache settings generation overflow")
)

type ResponseCacheSettings struct {
	LocalMaxBytes       int64
	LocalMaxEntryBytes  int64
	ReconstructMaxBytes int64
	Generation          int64
}

type ResponseCacheSettingsUpdate struct {
	LocalMaxBytes       *int64
	LocalMaxEntryBytes  *int64
	ReconstructMaxBytes *int64
}

func DefaultResponseCacheSettings() ResponseCacheSettings {
	return ResponseCacheSettings{
		LocalMaxBytes:       DefaultResponseCacheLocalMaxBytes,
		LocalMaxEntryBytes:  DefaultResponseCacheLocalMaxEntryBytes,
		ReconstructMaxBytes: DefaultResponseCacheReconstructMaxBytes,
		Generation:          DefaultResponseCacheConfigGeneration,
	}
}

func ValidateResponseCacheSettings(settings ResponseCacheSettings) error {
	switch {
	case settings.LocalMaxBytes < MinResponseCacheLocalMaxBytes ||
		settings.LocalMaxBytes > MaxResponseCacheLocalMaxBytes:
		return fmt.Errorf(
			"%w: response_cache_local_max_bytes must be between %d and %d",
			ErrInvalidResponseCacheSettings,
			MinResponseCacheLocalMaxBytes,
			MaxResponseCacheLocalMaxBytes,
		)
	case settings.LocalMaxEntryBytes < MinResponseCacheLocalMaxEntryBytes ||
		settings.LocalMaxEntryBytes > MaxResponseCacheLocalMaxEntryBytes:
		return fmt.Errorf(
			"%w: response_cache_local_max_entry_bytes must be between %d and %d",
			ErrInvalidResponseCacheSettings,
			MinResponseCacheLocalMaxEntryBytes,
			MaxResponseCacheLocalMaxEntryBytes,
		)
	case settings.ReconstructMaxBytes < MinResponseCacheReconstructMaxBytes ||
		settings.ReconstructMaxBytes > MaxResponseCacheReconstructMaxBytes:
		return fmt.Errorf(
			"%w: response_cache_reconstruct_max_bytes must be between %d and %d",
			ErrInvalidResponseCacheSettings,
			MinResponseCacheReconstructMaxBytes,
			MaxResponseCacheReconstructMaxBytes,
		)
	case settings.LocalMaxEntryBytes > settings.LocalMaxBytes:
		return fmt.Errorf(
			"%w: response_cache_local_max_entry_bytes must not exceed response_cache_local_max_bytes",
			ErrInvalidResponseCacheSettings,
		)
	case settings.Generation <= 0:
		return fmt.Errorf(
			"%w: response_cache_config_generation must be positive",
			ErrInvalidResponseCacheSettings,
		)
	default:
		return nil
	}
}

func (db *DB) GetResponseCacheSettings(ctx context.Context) (ResponseCacheSettings, error) {
	settings := DefaultResponseCacheSettings()
	err := scanResponseCacheSettings(db.conn.QueryRowContext(ctx, responseCacheSettingsSelectQuery(false)), &settings)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, nil
	}
	if err != nil {
		return ResponseCacheSettings{}, err
	}
	if err := ValidateResponseCacheSettings(settings); err != nil {
		return ResponseCacheSettings{}, err
	}
	return settings, nil
}

func (db *DB) UpdateResponseCacheSettings(ctx context.Context, update ResponseCacheSettingsUpdate) (ResponseCacheSettings, error) {
	var committed ResponseCacheSettings
	err := db.withSQLiteWriteLock(ctx, func() error {
		tx, err := db.conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO system_settings (id) VALUES (1)
			ON CONFLICT (id) DO NOTHING
		`); err != nil {
			return err
		}

		current := DefaultResponseCacheSettings()
		if err := scanResponseCacheSettings(
			tx.QueryRowContext(ctx, responseCacheSettingsSelectQuery(!db.isSQLite())),
			&current,
		); err != nil {
			return err
		}
		next := current
		if update.LocalMaxBytes != nil {
			next.LocalMaxBytes = *update.LocalMaxBytes
		}
		if update.LocalMaxEntryBytes != nil {
			next.LocalMaxEntryBytes = *update.LocalMaxEntryBytes
		}
		if update.ReconstructMaxBytes != nil {
			next.ReconstructMaxBytes = *update.ReconstructMaxBytes
		}
		if err := ValidateResponseCacheSettings(next); err != nil {
			return err
		}

		changed := next.LocalMaxBytes != current.LocalMaxBytes ||
			next.LocalMaxEntryBytes != current.LocalMaxEntryBytes ||
			next.ReconstructMaxBytes != current.ReconstructMaxBytes
		if changed {
			if current.Generation == math.MaxInt64 {
				return ErrResponseCacheGenerationOverflow
			}
			next.Generation = current.Generation + 1
			if _, err := tx.ExecContext(ctx, `
				UPDATE system_settings
				SET response_cache_local_max_bytes = $1,
				    response_cache_local_max_entry_bytes = $2,
				    response_cache_reconstruct_max_bytes = $3,
				    response_cache_config_generation = $4
				WHERE id = 1
			`, next.LocalMaxBytes, next.LocalMaxEntryBytes, next.ReconstructMaxBytes, next.Generation); err != nil {
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		committed = next
		return nil
	})
	if err != nil {
		return ResponseCacheSettings{}, err
	}
	return committed, nil
}

func responseCacheSettingsSelectQuery(forUpdate bool) string {
	query := `
		SELECT COALESCE(response_cache_local_max_bytes, 67108864),
		       COALESCE(response_cache_local_max_entry_bytes, 8388608),
		       COALESCE(response_cache_reconstruct_max_bytes, 67108864),
		       COALESCE(response_cache_config_generation, 1)
		FROM system_settings
		WHERE id = 1
	`
	if forUpdate {
		query += " FOR UPDATE"
	}
	return query
}

type responseCacheSettingsScanner interface {
	Scan(dest ...any) error
}

func scanResponseCacheSettings(row responseCacheSettingsScanner, settings *ResponseCacheSettings) error {
	return row.Scan(
		&settings.LocalMaxBytes,
		&settings.LocalMaxEntryBytes,
		&settings.ReconstructMaxBytes,
		&settings.Generation,
	)
}
