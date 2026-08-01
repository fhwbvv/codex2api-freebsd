package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/codex2api/cache"
	"github.com/codex2api/database"
	"github.com/gin-gonic/gin"
)

func TestResetAPIKeyQuotaHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := database.New("sqlite", filepath.Join(t.TempDir(), "quota-handler.db"))
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	defer db.Close()

	id, err := db.InsertAPIKeyWithOptions(context.Background(), database.APIKeyInput{
		Name: "client",
		Key:  "sk-handler-reset-single-1234567890",
		Limits: database.APIKeyLimits{
			CostLimit5h: 1,
			CostLimit7d: 2,
		},
	})
	if err != nil {
		t.Fatalf("InsertAPIKeyWithOptions: %v", err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", id)}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/admin/keys/1/reset-quota", nil)
	tc := cache.NewMemory(1)
	defer tc.Close()
	cacheCtx := context.Background()
	for _, key := range []string{
		fmt.Sprintf("%d:usage:5h", id),
		fmt.Sprintf("%d:usage:7d", id),
		fmt.Sprintf("%d:usage:30d", id),
	} {
		if err := tc.SetRuntime(cacheCtx, adminAPIKeyLimitsCacheNamespace, key, json.RawMessage(`{"user_billed":4}`), time.Minute); err != nil {
			t.Fatalf("seed limit cache %s: %v", key, err)
		}
	}
	if err := tc.SetRuntime(cacheCtx, adminAPIKeyCacheNamespace, "sk-handler-reset-single-1234567890", json.RawMessage(`{"id":1}`), time.Minute); err != nil {
		t.Fatalf("seed API key cache: %v", err)
	}

	(&Handler{db: db, cache: tc}).ResetAPIKeyQuota(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", recorder.Code, recorder.Body.String())
	}
	row, err := db.GetAPIKeyByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetAPIKeyByID: %v", err)
	}
	if row.QuotaUsed != 0 || row.ResetCount != 1 || !row.LastResetAt.Valid {
		t.Fatalf("row after reset = %#v", row)
	}
	for _, key := range []string{fmt.Sprintf("%d:usage:5h", id), fmt.Sprintf("%d:usage:7d", id)} {
		if _, ok, err := tc.GetRuntime(cacheCtx, adminAPIKeyLimitsCacheNamespace, key); err != nil || ok {
			t.Fatalf("reset cache %s still present: ok=%v err=%v", key, ok, err)
		}
	}
	if _, ok, err := tc.GetRuntime(cacheCtx, adminAPIKeyLimitsCacheNamespace, fmt.Sprintf("%d:usage:30d", id)); err != nil || !ok {
		t.Fatalf("30d cache should remain: ok=%v err=%v", ok, err)
	}
	if _, ok, err := tc.GetRuntime(cacheCtx, adminAPIKeyCacheNamespace, "sk-handler-reset-single-1234567890"); err != nil || ok {
		t.Fatalf("API key auth cache still present: ok=%v err=%v", ok, err)
	}
}

func TestResetAllAPIKeyQuotasHandlerReturnsAffectedCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := database.New("sqlite", filepath.Join(t.TempDir(), "quota-all-handler.db"))
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	for index := 0; index < 2; index++ {
		_, err := db.InsertAPIKeyWithOptions(ctx, database.APIKeyInput{
			Name:       "client",
			Key:        fmt.Sprintf("sk-handler-reset-all-%d-1234567890", index),
			QuotaLimit: 5,
			QuotaUsed:  float64(index + 1),
		})
		if err != nil {
			t.Fatalf("InsertAPIKeyWithOptions: %v", err)
		}
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/admin/keys/reset-all-quotas", nil)
	(&Handler{db: db}).ResetAllAPIKeyQuotas(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		ResetCount int64 `json:"reset_count"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.ResetCount != 2 {
		t.Fatalf("reset_count = %d, want 2", payload.ResetCount)
	}
}
