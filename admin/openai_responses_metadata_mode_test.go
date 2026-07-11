package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codex2api/auth"
	"github.com/codex2api/database"
	"github.com/gin-gonic/gin"
)

func TestOpenAIResponsesCodexClientMetadataModeLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newTestAdminDB(t)
	store := auth.NewStore(db, nil, nil)
	handler := &Handler{db: db, store: store}

	mode := auth.CodexClientMetadataModeAlways
	addRecorder := invokeOpenAIResponsesJSONHandler(t, http.MethodPost, "/api/admin/accounts/openai-responses", nil, addOpenAIResponsesAccountReq{
		Name:                    "responses-relay",
		BaseURL:                 "https://relay.example.com",
		APIKey:                  "relay-token",
		Models:                  []string{"gpt-5.5"},
		CodexClientMetadataMode: &mode,
	}, handler.AddOpenAIResponsesAccount)
	if addRecorder.Code != http.StatusOK {
		t.Fatalf("add status = %d, want %d: %s", addRecorder.Code, http.StatusOK, addRecorder.Body.String())
	}
	var added struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(addRecorder.Body.Bytes(), &added); err != nil {
		t.Fatalf("decode add response: %v", err)
	}
	assertOpenAIResponsesMetadataMode(t, db, store, added.ID, auth.CodexClientMetadataModeAlways)

	listRecorder := httptest.NewRecorder()
	listContext, _ := gin.CreateTestContext(listRecorder)
	listContext.Request = httptest.NewRequest(http.MethodGet, "/api/admin/accounts", nil)
	handler.ListAccounts(listContext)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d: %s", listRecorder.Code, http.StatusOK, listRecorder.Body.String())
	}
	var listed accountsResponse
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listed.Accounts) != 1 || listed.Accounts[0].CodexClientMetadataMode != auth.CodexClientMetadataModeAlways {
		t.Fatalf("listed accounts = %#v, want metadata mode %q", listed.Accounts, auth.CodexClientMetadataModeAlways)
	}

	updatePath := fmt.Sprintf("/api/admin/accounts/%d/openai-responses", added.ID)
	params := gin.Params{{Key: "id", Value: fmt.Sprintf("%d", added.ID)}}
	preserveRecorder := invokeOpenAIResponsesJSONHandler(t, http.MethodPut, updatePath, params, addOpenAIResponsesAccountReq{
		Name:    "responses-relay",
		BaseURL: "https://relay.example.com",
		Models:  []string{"gpt-5.5"},
	}, handler.UpdateOpenAIResponsesAccount)
	if preserveRecorder.Code != http.StatusOK {
		t.Fatalf("preserve update status = %d, want %d: %s", preserveRecorder.Code, http.StatusOK, preserveRecorder.Body.String())
	}
	assertOpenAIResponsesMetadataMode(t, db, store, added.ID, auth.CodexClientMetadataModeAlways)

	mode = auth.CodexClientMetadataModeOff
	offRecorder := invokeOpenAIResponsesJSONHandler(t, http.MethodPut, updatePath, params, addOpenAIResponsesAccountReq{
		Name:                    "responses-relay",
		BaseURL:                 "https://relay.example.com",
		Models:                  []string{"gpt-5.5"},
		CodexClientMetadataMode: &mode,
	}, handler.UpdateOpenAIResponsesAccount)
	if offRecorder.Code != http.StatusOK {
		t.Fatalf("off update status = %d, want %d: %s", offRecorder.Code, http.StatusOK, offRecorder.Body.String())
	}
	assertOpenAIResponsesMetadataMode(t, db, store, added.ID, auth.CodexClientMetadataModeOff)
}

func TestAddOpenAIResponsesCodexClientMetadataModeDefaultsAndValidation(t *testing.T) {
	t.Run("omitted defaults to auto", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		db := newTestAdminDB(t)
		store := auth.NewStore(db, nil, nil)
		handler := &Handler{db: db, store: store}

		recorder := invokeOpenAIResponsesJSONHandler(t, http.MethodPost, "/api/admin/accounts/openai-responses", nil, addOpenAIResponsesAccountReq{
			BaseURL: "https://relay.example.com",
			APIKey:  "relay-token-default",
			Models:  []string{"gpt-5.5"},
		}, handler.AddOpenAIResponsesAccount)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
		}
		var added struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &added); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		assertOpenAIResponsesMetadataMode(t, db, store, added.ID, auth.CodexClientMetadataModeAuto)
	})

	t.Run("invalid mode is rejected", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		db := newTestAdminDB(t)
		store := auth.NewStore(db, nil, nil)
		handler := &Handler{db: db, store: store}
		mode := "sometimes"

		recorder := invokeOpenAIResponsesJSONHandler(t, http.MethodPost, "/api/admin/accounts/openai-responses", nil, addOpenAIResponsesAccountReq{
			BaseURL:                 "https://relay.example.com",
			APIKey:                  "relay-token-invalid",
			Models:                  []string{"gpt-5.5"},
			CodexClientMetadataMode: &mode,
		}, handler.AddOpenAIResponsesAccount)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
		}
	})
}

func invokeOpenAIResponsesJSONHandler(t *testing.T, method, path string, params gin.Params, body any, handler func(*gin.Context)) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = params
	ctx.Request = httptest.NewRequest(method, path, bytes.NewReader(payload))
	ctx.Request.Header.Set("Content-Type", "application/json")
	handler(ctx)
	return recorder
}

func assertOpenAIResponsesMetadataMode(t *testing.T, db *database.DB, store *auth.Store, accountID int64, want string) {
	t.Helper()
	row, err := db.GetAccountByID(context.Background(), accountID)
	if err != nil {
		t.Fatalf("GetAccountByID(%d): %v", accountID, err)
	}
	if got := row.GetCredential("codex_client_metadata_mode"); got != want {
		t.Fatalf("persisted metadata mode = %q, want %q", got, want)
	}
	account := store.FindByID(accountID)
	if account == nil {
		t.Fatalf("runtime account %d not found", accountID)
	}
	if got := account.OpenAIResponsesCodexClientMetadataMode(); got != want {
		t.Fatalf("runtime metadata mode = %q, want %q", got, want)
	}
}
