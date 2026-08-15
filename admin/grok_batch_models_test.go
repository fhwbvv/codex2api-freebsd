package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/codex2api/auth"
	"github.com/gin-gonic/gin"
)

func TestBatchUpdateGrokModelsReplacesWhitelistAndSyncsRuntime(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	ctx := context.Background()
	grokID1, err := db.InsertAccountWithUpstream(ctx, "grok-1", "xai", auth.UpstreamGrok, map[string]interface{}{
		"upstream_type": auth.UpstreamGrok,
		"api_key":       "xai-1",
		"models":        []string{"grok-4.6"},
	}, "")
	if err != nil {
		t.Fatalf("InsertAccountWithUpstream 1: %v", err)
	}
	grokID2, err := db.InsertAccountWithUpstream(ctx, "grok-2", "xai", auth.UpstreamGrok, map[string]interface{}{
		"upstream_type": auth.UpstreamGrok,
		"api_key":       "xai-2",
		"models":        []string{"grok-4.6", "grok-4"},
	}, "")
	if err != nil {
		t.Fatalf("InsertAccountWithUpstream 2: %v", err)
	}
	codexID, err := db.InsertAccount(ctx, "codex-1", "rt_codex", "")
	if err != nil {
		t.Fatalf("InsertAccount codex: %v", err)
	}

	runtime1 := &auth.Account{DBID: grokID1, UpstreamType: auth.UpstreamGrok, APIKey: "xai-1", Models: []string{"grok-4.6"}}
	runtime2 := &auth.Account{DBID: grokID2, UpstreamType: auth.UpstreamGrok, APIKey: "xai-2", Models: []string{"grok-4.6", "grok-4"}}
	store := auth.NewStore(nil, nil, nil)
	store.AddAccount(runtime1)
	store.AddAccount(runtime2)
	handler := &Handler{db: db, store: store}

	body := fmt.Sprintf(`{"ids":[%d,%d,%d,%d,%d],"models":["grok-4.5"," grok-4.5 "]}`,
		grokID1, grokID2, grokID1, codexID, grokID2+1000)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/accounts/grok/batch-models", strings.NewReader(body))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	handler.BatchUpdateGrokModels(ginCtx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var payload struct {
		Success int64    `json:"success"`
		Failed  int64    `json:"failed"`
		Models  []string `json:"models"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Success != 2 || payload.Failed != 2 {
		t.Fatalf("payload = %#v, want success=2 failed=2", payload)
	}
	if !reflect.DeepEqual(payload.Models, []string{"grok-4.5"}) {
		t.Fatalf("models = %#v, want [grok-4.5]", payload.Models)
	}

	for _, id := range []int64{grokID1, grokID2} {
		row, err := db.GetAccountByID(ctx, id)
		if err != nil {
			t.Fatalf("GetAccountByID(%d): %v", id, err)
		}
		if got := row.GetCredentialStringSlice("models"); !reflect.DeepEqual(got, []string{"grok-4.5"}) {
			t.Fatalf("account %d models = %#v, want [grok-4.5]", id, got)
		}
	}
	codexRow, err := db.GetAccountByID(ctx, codexID)
	if err != nil {
		t.Fatalf("GetAccountByID(codex): %v", err)
	}
	if got := codexRow.GetCredentialStringSlice("models"); len(got) != 0 {
		t.Fatalf("codex models = %#v, want empty", got)
	}
	if !reflect.DeepEqual(runtime1.Models, []string{"grok-4.5"}) || !reflect.DeepEqual(runtime2.Models, []string{"grok-4.5"}) {
		t.Fatalf("runtime models = %#v / %#v, want [grok-4.5]", runtime1.Models, runtime2.Models)
	}
}

func TestBatchUpdateGrokModelsClearsWhitelist(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	ctx := context.Background()
	id, err := db.InsertAccountWithUpstream(ctx, "grok-clear", "xai", auth.UpstreamGrok, map[string]interface{}{
		"upstream_type": auth.UpstreamGrok,
		"api_key":       "xai-clear",
		"models":        []string{"grok-4.6"},
	}, "")
	if err != nil {
		t.Fatalf("InsertAccountWithUpstream: %v", err)
	}
	runtime := &auth.Account{DBID: id, UpstreamType: auth.UpstreamGrok, APIKey: "xai-clear", Models: []string{"grok-4.6"}}
	store := auth.NewStore(nil, nil, nil)
	store.AddAccount(runtime)
	handler := &Handler{db: db, store: store}

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/admin/accounts/grok/batch-models",
		strings.NewReader(fmt.Sprintf(`{"ids":[%d],"models":[]}`, id)),
	)
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	handler.BatchUpdateGrokModels(ginCtx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	row, err := db.GetAccountByID(ctx, id)
	if err != nil {
		t.Fatalf("GetAccountByID: %v", err)
	}
	if got := row.GetCredentialStringSlice("models"); len(got) != 0 {
		t.Fatalf("persisted models = %#v, want empty", got)
	}
	if len(runtime.Models) != 0 {
		t.Fatalf("runtime models = %#v, want empty", runtime.Models)
	}
}

func TestBatchUpdateGrokModelsRejectsInvalidInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &Handler{}

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "empty ids", body: `{"ids":[],"models":["grok-4.5"]}`, want: "请提供要更新的账号 ID 列表"},
		{name: "invalid model", body: `{"ids":[1],"models":["not a model!!!"]}`, want: "模型名称无效: not a model!!!"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ginCtx, _ := gin.CreateTestContext(recorder)
			ginCtx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/accounts/grok/batch-models", strings.NewReader(tc.body))
			ginCtx.Request.Header.Set("Content-Type", "application/json")
			handler.BatchUpdateGrokModels(ginCtx)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d, body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
			assertErrorMessage(t, recorder, tc.want)
		})
	}
}
