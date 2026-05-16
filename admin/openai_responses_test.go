package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/codex2api/auth"
	"github.com/codex2api/database"
)

func TestFetchOpenAIResponsesModelIDsSupportsV1BaseURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q, want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Fatalf("Authorization = %q, want Bearer sk-test", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4.1"},{"id":"gpt-4.1"},{"id":"gpt-4.1-mini"}]}`))
	}))
	defer server.Close()

	models, err := fetchOpenAIResponsesModelIDs(context.Background(), server.URL+"/v1", "sk-test", "")
	if err != nil {
		t.Fatalf("fetchOpenAIResponsesModelIDs returned error: %v", err)
	}
	want := []string{"gpt-4.1", "gpt-4.1-mini"}
	if !reflect.DeepEqual(models, want) {
		t.Fatalf("models = %#v, want %#v", models, want)
	}
}

func TestConnectionTestModelForOpenAIResponsesAccountUsesFirstSupportedFallback(t *testing.T) {
	store := auth.NewStore(nil, nil, &database.SystemSettings{TestModel: "gpt-5.4"})
	handler := &Handler{store: store}
	account := &auth.Account{
		UpstreamType: auth.UpstreamOpenAIResponses,
		BaseURL:      "https://api.openai.com",
		APIKey:       "sk-test",
		Models:       []string{"gpt-4.1-mini", "gpt-4.1"},
	}

	model, err := handler.connectionTestModelForAccount(context.Background(), account, "")
	if err != nil {
		t.Fatalf("connectionTestModelForAccount returned error: %v", err)
	}
	if model != "gpt-4.1-mini" {
		t.Fatalf("model = %q, want first account model", model)
	}

	model, err = handler.connectionTestModelForAccount(context.Background(), account, "gpt-4.1")
	if err != nil {
		t.Fatalf("requested model returned error: %v", err)
	}
	if model != "gpt-4.1" {
		t.Fatalf("requested model = %q, want gpt-4.1", model)
	}
}
