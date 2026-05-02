package database

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type PromptFilterLog struct {
	ID              int64     `json:"id"`
	CreatedAt       time.Time `json:"created_at"`
	Source          string    `json:"source"`
	Endpoint        string    `json:"endpoint"`
	Model           string    `json:"model"`
	Action          string    `json:"action"`
	Mode            string    `json:"mode"`
	Score           int       `json:"score"`
	Threshold       int       `json:"threshold"`
	MatchedPatterns string    `json:"matched_patterns"`
	TextPreview     string    `json:"text_preview"`
	APIKeyID        int64     `json:"api_key_id"`
	APIKeyName      string    `json:"api_key_name"`
	APIKeyMasked    string    `json:"api_key_masked"`
	ClientIP        string    `json:"client_ip"`
	ErrorCode       string    `json:"error_code"`
}

type PromptFilterLogInput struct {
	Source          string
	Endpoint        string
	Model           string
	Action          string
	Mode            string
	Score           int
	Threshold       int
	MatchedPatterns string
	TextPreview     string
	APIKeyID        int64
	APIKeyName      string
	APIKeyMasked    string
	ClientIP        string
	ErrorCode       string
}

type PromptFilterLogQuery struct {
	Page     int
	PageSize int
	Limit    int
	Source   string
	Action   string
	Endpoint string
	Model    string
	APIKeyID int64
	Query    string
}

func (db *DB) InsertPromptFilterLog(ctx context.Context, input *PromptFilterLogInput) error {
	if db == nil || input == nil {
		return nil
	}
	_, err := db.conn.ExecContext(ctx, `
		INSERT INTO prompt_filter_logs (
			source, endpoint, model, action, mode, score, threshold_value, matched_patterns, text_preview,
			api_key_id, api_key_name, api_key_masked, client_ip, error_code
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`, input.Source, input.Endpoint, input.Model, input.Action, input.Mode, input.Score, input.Threshold,
		input.MatchedPatterns, input.TextPreview, input.APIKeyID, input.APIKeyName, input.APIKeyMasked, input.ClientIP, input.ErrorCode)
	return err
}

func (db *DB) ListPromptFilterLogs(ctx context.Context, limit int) ([]*PromptFilterLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	result, _, err := db.ListPromptFilterLogsPage(ctx, PromptFilterLogQuery{Page: 1, PageSize: limit})
	return result, err
}

func (db *DB) ListPromptFilterLogsPage(ctx context.Context, query PromptFilterLogQuery) ([]*PromptFilterLog, int, error) {
	pageSize := query.PageSize
	if pageSize <= 0 {
		pageSize = query.Limit
	}
	if pageSize <= 0 || pageSize > 500 {
		pageSize = 100
	}
	page := query.Page
	if page <= 0 {
		page = 1
	}

	where, args := promptFilterLogWhere(query)
	countSQL := `SELECT COUNT(*) FROM prompt_filter_logs` + where
	var total int
	if err := db.conn.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := db.conn.QueryContext(ctx, `
		SELECT id, created_at, COALESCE(source, ''), COALESCE(endpoint, ''), COALESCE(model, ''),
		       COALESCE(action, ''), COALESCE(mode, ''), COALESCE(score, 0), COALESCE(threshold_value, 0),
		       COALESCE(matched_patterns, '[]'), COALESCE(text_preview, ''), COALESCE(api_key_id, 0),
		       COALESCE(api_key_name, ''), COALESCE(api_key_masked, ''), COALESCE(client_ip, ''), COALESCE(error_code, '')
		FROM prompt_filter_logs
		`+where+`
		ORDER BY id DESC
		LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args))+`
	`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	logs := make([]*PromptFilterLog, 0)
	for rows.Next() {
		item := &PromptFilterLog{}
		var createdAtRaw interface{}
		if err := rows.Scan(&item.ID, &createdAtRaw, &item.Source, &item.Endpoint, &item.Model, &item.Action, &item.Mode,
			&item.Score, &item.Threshold, &item.MatchedPatterns, &item.TextPreview, &item.APIKeyID, &item.APIKeyName,
			&item.APIKeyMasked, &item.ClientIP, &item.ErrorCode); err != nil {
			return nil, 0, err
		}
		createdAt, err := parseDBTimeValue(createdAtRaw)
		if err != nil {
			return nil, 0, err
		}
		item.CreatedAt = createdAt
		logs = append(logs, item)
	}
	return logs, total, rows.Err()
}

func promptFilterLogWhere(query PromptFilterLogQuery) (string, []any) {
	clauses := make([]string, 0, 8)
	args := make([]any, 0, 8)
	addExact := func(column, value string) {
		value = strings.TrimSpace(value)
		if value == "" || value == "all" {
			return
		}
		args = append(args, value)
		clauses = append(clauses, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	addExact("source", query.Source)
	addExact("action", query.Action)
	addExact("endpoint", query.Endpoint)
	addExact("model", query.Model)
	if query.APIKeyID > 0 {
		args = append(args, query.APIKeyID)
		clauses = append(clauses, fmt.Sprintf("api_key_id = $%d", len(args)))
	}
	if q := strings.TrimSpace(query.Query); q != "" {
		args = append(args, "%"+strings.ToLower(q)+"%")
		idx := len(args)
		clauses = append(clauses, fmt.Sprintf(`(
			LOWER(COALESCE(text_preview, '')) LIKE $%d OR
			LOWER(COALESCE(matched_patterns, '')) LIKE $%d OR
			LOWER(COALESCE(error_code, '')) LIKE $%d OR
			LOWER(COALESCE(api_key_name, '')) LIKE $%d OR
			LOWER(COALESCE(api_key_masked, '')) LIKE $%d
		)`, idx, idx, idx, idx, idx))
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func (db *DB) ClearPromptFilterLogs(ctx context.Context) error {
	if db == nil {
		return nil
	}
	if db.isSQLite() {
		if _, err := db.conn.ExecContext(ctx, `DELETE FROM prompt_filter_logs`); err != nil {
			return err
		}
		_, err := db.conn.ExecContext(ctx, `DELETE FROM sqlite_sequence WHERE name = 'prompt_filter_logs'`)
		return err
	}
	_, err := db.conn.ExecContext(ctx, `TRUNCATE TABLE prompt_filter_logs RESTART IDENTITY`)
	return err
}
