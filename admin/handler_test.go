package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codex2api/auth"
	"github.com/codex2api/database"
	"github.com/gin-gonic/gin"
)

func TestRefreshAccountRejectsInvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := &Handler{
		refreshAccount: func(context.Context, int64) error {
			t.Fatal("refresh should not be called for invalid id")
			return nil
		},
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "bad-id"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/accounts/bad-id/refresh", nil)

	handler.RefreshAccount(ctx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}

	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["error"]; got != "无效的账号 ID" {
		t.Fatalf("error = %q, want %q", got, "无效的账号 ID")
	}
}

func TestRefreshAccountRunsSingleRefresh(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var called bool
	var gotID int64
	handler := &Handler{
		refreshAccount: func(_ context.Context, id int64) error {
			called = true
			gotID = id
			return nil
		},
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "42"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/accounts/42/refresh", nil)

	handler.RefreshAccount(ctx)

	if !called {
		t.Fatal("expected refresh to be called")
	}
	if gotID != 42 {
		t.Fatalf("refresh id = %d, want %d", gotID, 42)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["message"]; got != "账号刷新成功" {
		t.Fatalf("message = %q, want %q", got, "账号刷新成功")
	}
}

func TestRefreshAccountReturnsNotFoundForMissingAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := &Handler{
		refreshAccount: func(context.Context, int64) error {
			return errors.New("账号 7 不存在")
		},
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "7"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/accounts/7/refresh", nil)

	handler.RefreshAccount(ctx)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}

	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["error"]; got != "账号 7 不存在" {
		t.Fatalf("error = %q, want %q", got, "账号 7 不存在")
	}
}

func TestRefreshAccountReturnsRefreshFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := &Handler{
		refreshAccount: func(context.Context, int64) error {
			return errors.New("upstream unavailable")
		},
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "9"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/accounts/9/refresh", nil)

	handler.RefreshAccount(ctx)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}

	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["error"]; got != "刷新失败: upstream unavailable" {
		t.Fatalf("error = %q, want %q", got, "刷新失败: upstream unavailable")
	}
}

func TestGetAccountAuthJSONRejectsInvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := &Handler{}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "bad-id"}}
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/accounts/bad-id/auth-json", nil)

	handler.GetAccountAuthJSON(ctx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	assertErrorMessage(t, recorder, "无效的账号 ID")
}

func TestGetAccountAuthJSONReturnsCodexAuthFile(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	accountID := insertTestAccount(t, db)
	if err := db.UpdateCredentials(context.Background(), accountID, map[string]interface{}{
		"id_token":     "id_test",
		"access_token": "access_test",
		"account_id":   "account_test",
	}); err != nil {
		t.Fatalf("seed credentials: %v", err)
	}
	handler := &Handler{db: db}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", accountID)}}
	ctx.Request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/admin/accounts/%d/auth-json", accountID), nil)

	handler.GetAccountAuthJSON(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Disposition"); got != `attachment; filename="auth.json"` {
		t.Fatalf("Content-Disposition = %q, want auth.json attachment", got)
	}

	var payload struct {
		AuthMode     string  `json:"auth_mode"`
		OpenAIAPIKey *string `json:"OPENAI_API_KEY"`
		Tokens       struct {
			IDToken      string `json:"id_token"`
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			AccountID    string `json:"account_id"`
		} `json:"tokens"`
		LastRefresh string `json:"last_refresh"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.AuthMode != "chatgpt" {
		t.Fatalf("auth_mode = %q, want chatgpt", payload.AuthMode)
	}
	if payload.OpenAIAPIKey != nil {
		t.Fatalf("OPENAI_API_KEY = %q, want null", *payload.OpenAIAPIKey)
	}
	if payload.Tokens.IDToken != "id_test" || payload.Tokens.AccessToken != "access_test" || payload.Tokens.RefreshToken != "rt_test" || payload.Tokens.AccountID != "account_test" {
		t.Fatalf("tokens = %+v, want seeded credentials", payload.Tokens)
	}
	if payload.LastRefresh == "" {
		t.Fatal("last_refresh is empty")
	}
}

func TestGetAccountAuthJSONRejectsIncompleteTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	accountID := insertTestAccount(t, db)
	handler := &Handler{db: db}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", accountID)}}
	ctx.Request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/admin/accounts/%d/auth-json", accountID), nil)

	handler.GetAccountAuthJSON(ctx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	assertErrorMessage(t, recorder, "账号缺少 access_token 或 id_token，请先刷新账号后再生成 auth.json")
}

func TestGetUsageLogsRejectsInvalidAPIKeyID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := &Handler{}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/usage/logs?start=2026-01-01T00:00:00Z&end=2026-01-02T00:00:00Z&page=1&api_key_id=bad", nil)

	handler.GetUsageLogs(ctx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}

	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["error"]; got != "api_key_id 参数无效，需要正整数" {
		t.Fatalf("error = %q, want %q", got, "api_key_id 参数无效，需要正整数")
	}
}

func TestUpdateAccountSchedulerRejectsInvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := &Handler{}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "bad-id"}}
	ctx.Request = httptest.NewRequest(http.MethodPatch, "/api/admin/accounts/bad-id/scheduler", http.NoBody)

	handler.UpdateAccountScheduler(ctx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	assertErrorMessage(t, recorder, "无效的账号 ID")
}

func TestUpdateAccountSchedulerRejectsInvalidBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	handler := &Handler{db: db}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "1"}}
	ctx.Request = httptest.NewRequest(http.MethodPatch, "/api/admin/accounts/1/scheduler", strings.NewReader(`{"score_bias_override":"abc"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateAccountScheduler(ctx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	assertErrorMessage(t, recorder, "score_bias_override 必须是整数或 null")
}

func TestUpdateAccountSchedulerRejectsInvalidAllowedAPIKeyIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	accountID := insertTestAccount(t, db)
	handler := &Handler{db: db}

	testCases := []struct {
		name    string
		body    string
		message string
	}{
		{
			name:    "invalid type",
			body:    `{"allowed_api_key_ids":"abc"}`,
			message: "allowed_api_key_ids 必须是整数数组或 null",
		},
		{
			name:    "non positive",
			body:    `{"allowed_api_key_ids":[0]}`,
			message: "allowed_api_key_ids 中的值必须是正整数",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", accountID)}}
			ctx.Request = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/admin/accounts/%d/scheduler", accountID), strings.NewReader(tc.body))
			ctx.Request.Header.Set("Content-Type", "application/json")

			handler.UpdateAccountScheduler(ctx)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
			}
			assertErrorMessage(t, recorder, tc.message)
		})
	}
}

func TestUpdateAccountSchedulerRejectsOutOfRangeValues(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	accountID := insertTestAccount(t, db)
	handler := &Handler{db: db}

	testCases := []struct {
		name    string
		body    string
		message string
	}{
		{
			name:    "score bias out of range",
			body:    `{"score_bias_override":201}`,
			message: "score_bias_override 超出范围，必须在 -200..200 之间",
		},
		{
			name:    "base concurrency out of range",
			body:    `{"base_concurrency_override":0}`,
			message: "base_concurrency_override 超出范围，必须在 1..50 之间",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", accountID)}}
			ctx.Request = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/admin/accounts/%d/scheduler", accountID), strings.NewReader(tc.body))
			ctx.Request.Header.Set("Content-Type", "application/json")

			handler.UpdateAccountScheduler(ctx)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
			}
			assertErrorMessage(t, recorder, tc.message)
		})
	}
}

func TestUpdateAccountSchedulerPersistsOverrides(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	accountID := insertTestAccount(t, db)
	handler := &Handler{db: db}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", accountID)}}
	ctx.Request = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/admin/accounts/%d/scheduler", accountID), strings.NewReader(`{"score_bias_override":88,"base_concurrency_override":7}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateAccountScheduler(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	rows, err := db.ListActive(context.Background())
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if !rows[0].ScoreBiasOverride.Valid || rows[0].ScoreBiasOverride.Int64 != 88 {
		t.Fatalf("score_bias_override = %+v, want 88", rows[0].ScoreBiasOverride)
	}
	if !rows[0].BaseConcurrencyOverride.Valid || rows[0].BaseConcurrencyOverride.Int64 != 7 {
		t.Fatalf("base_concurrency_override = %+v, want 7", rows[0].BaseConcurrencyOverride)
	}
}

func TestUpdateAccountSchedulerPersistsAllowedAPIKeyIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	accountID := insertTestAccount(t, db)
	keyID1 := insertTestAPIKey(t, db, "Team A")
	keyID2 := insertTestAPIKey(t, db, "Team B")
	handler := &Handler{db: db}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", accountID)}}
	ctx.Request = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/admin/accounts/%d/scheduler", accountID), strings.NewReader(fmt.Sprintf(`{"score_bias_override":88,"base_concurrency_override":7,"allowed_api_key_ids":[%d,%d,%d]}`, keyID2, keyID1, keyID2)))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateAccountScheduler(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	rows, err := db.ListActive(context.Background())
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if got := rows[0].GetCredentialInt64Slice("allowed_api_key_ids"); len(got) != 2 || got[0] != keyID1 || got[1] != keyID2 {
		t.Fatalf("allowed_api_key_ids = %v, want [%d %d]", got, keyID1, keyID2)
	}
}

func TestUpdateAccountSchedulerResetsToAutoOnNull(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	accountID := insertTestAccount(t, db)
	ctx := context.Background()
	if err := db.UpdateAccountSchedulerConfig(ctx, accountID, sql.NullInt64{Int64: 20, Valid: true}, sql.NullInt64{Int64: 4, Valid: true}, database.OptionalInt64Slice{}); err != nil {
		t.Fatalf("seed scheduler config: %v", err)
	}

	handler := &Handler{db: db}
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", accountID)}}
	ginCtx.Request = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/admin/accounts/%d/scheduler", accountID), strings.NewReader(`{"score_bias_override":null,"base_concurrency_override":null}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateAccountScheduler(ginCtx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	rows, err := db.ListActive(context.Background())
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].ScoreBiasOverride.Valid {
		t.Fatalf("score_bias_override = %+v, want null", rows[0].ScoreBiasOverride)
	}
	if rows[0].BaseConcurrencyOverride.Valid {
		t.Fatalf("base_concurrency_override = %+v, want null", rows[0].BaseConcurrencyOverride)
	}
}

func TestUpdateAccountSchedulerClearsAllowedAPIKeyIDsOnNull(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	accountID := insertTestAccount(t, db)
	keyID := insertTestAPIKey(t, db, "Team A")
	if err := db.UpdateCredentials(context.Background(), accountID, map[string]interface{}{
		"allowed_api_key_ids": []int64{keyID},
	}); err != nil {
		t.Fatalf("seed allowed api keys: %v", err)
	}

	handler := &Handler{db: db}
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", accountID)}}
	ginCtx.Request = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/admin/accounts/%d/scheduler", accountID), strings.NewReader(`{"score_bias_override":null,"base_concurrency_override":null,"allowed_api_key_ids":null}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateAccountScheduler(ginCtx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	rows, err := db.ListActive(context.Background())
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if got := rows[0].GetCredentialInt64Slice("allowed_api_key_ids"); len(got) != 0 {
		t.Fatalf("allowed_api_key_ids = %v, want empty", got)
	}
}

func TestUpdateAccountSchedulerKeepsAllowedAPIKeyIDsWhenFieldOmitted(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	accountID := insertTestAccount(t, db)
	keyID := insertTestAPIKey(t, db, "Team A")
	if err := db.UpdateCredentials(context.Background(), accountID, map[string]interface{}{
		"allowed_api_key_ids": []int64{keyID},
	}); err != nil {
		t.Fatalf("seed allowed api keys: %v", err)
	}

	handler := &Handler{db: db}
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", accountID)}}
	ginCtx.Request = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/admin/accounts/%d/scheduler", accountID), strings.NewReader(`{"score_bias_override":12,"base_concurrency_override":3}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateAccountScheduler(ginCtx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	rows, err := db.ListActive(context.Background())
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if got := rows[0].GetCredentialInt64Slice("allowed_api_key_ids"); len(got) != 1 || got[0] != keyID {
		t.Fatalf("allowed_api_key_ids = %v, want [%d]", got, keyID)
	}
}

func TestUpdateAccountSchedulerRejectsMissingAllowedAPIKeyID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	accountID := insertTestAccount(t, db)
	handler := &Handler{db: db}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", accountID)}}
	ctx.Request = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/admin/accounts/%d/scheduler", accountID), strings.NewReader(`{"allowed_api_key_ids":[999]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateAccountScheduler(ctx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	assertErrorMessage(t, recorder, "allowed_api_key_ids 包含不存在的 API Key ID: 999")
}

func TestUpdateAccountSchedulerUpdatesRuntimeOverrides(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	accountID := insertTestAccount(t, db)
	keyID1 := insertTestAPIKey(t, db, "Team A")
	keyID2 := insertTestAPIKey(t, db, "Team B")
	runtimeAccount := &auth.Account{
		DBID:        accountID,
		AccessToken: "token",
		Status:      auth.StatusReady,
		PlanType:    "pro",
	}
	store := &auth.Store{}
	store.AddAccount(runtimeAccount)

	handler := &Handler{db: db, store: store}
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", accountID)}}
	ginCtx.Request = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/admin/accounts/%d/scheduler", accountID), strings.NewReader(fmt.Sprintf(`{"score_bias_override":33,"base_concurrency_override":5,"allowed_api_key_ids":[%d,%d]}`, keyID2, keyID1)))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateAccountScheduler(ginCtx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	scoreBias, ok := runtimeAccount.GetScoreBiasOverride()
	if !ok || scoreBias != 33 {
		t.Fatalf("runtime score_bias_override = (%d, %t), want (33, true)", scoreBias, ok)
	}
	baseConcurrency, ok := runtimeAccount.GetBaseConcurrencyOverride()
	if !ok || baseConcurrency != 5 {
		t.Fatalf("runtime base_concurrency_override = (%d, %t), want (5, true)", baseConcurrency, ok)
	}
	if got := runtimeAccount.GetAllowedAPIKeyIDs(); len(got) != 2 || got[0] != keyID1 || got[1] != keyID2 {
		t.Fatalf("runtime allowed_api_key_ids = %v, want [%d %d]", got, keyID1, keyID2)
	}
}

func newTestAdminDB(t *testing.T) *database.DB {
	t.Helper()

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "admin-handler-test.sqlite")
	db, err := database.New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("new test db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_ = os.Remove(dbPath)
	})
	return db
}

func insertTestAccount(t *testing.T, db *database.DB) int64 {
	t.Helper()

	id, err := db.InsertAccount(context.Background(), "test-account", "rt_test", "")
	if err != nil {
		t.Fatalf("insert account: %v", err)
	}
	return id
}

func insertTestAPIKey(t *testing.T, db *database.DB, name string) int64 {
	t.Helper()

	id, err := db.InsertAPIKey(context.Background(), name, fmt.Sprintf("sk-test-%s-1234567890", strings.ToLower(strings.ReplaceAll(name, " ", "-"))))
	if err != nil {
		t.Fatalf("insert api key: %v", err)
	}
	return id
}

func assertErrorMessage(t *testing.T, recorder *httptest.ResponseRecorder, want string) {
	t.Helper()

	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload["error"]; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}
