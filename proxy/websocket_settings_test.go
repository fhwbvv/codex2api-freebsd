package proxy

import (
	"testing"

	"github.com/codex2api/auth"
	"github.com/codex2api/config"
	"github.com/codex2api/database"
)

func TestShouldUseWebsocketHonorsRuntimeForceWithoutStaticConfig(t *testing.T) {
	previous := CurrentRuntimeSettings()
	t.Cleanup(func() { ApplyRuntimeSettings(previous) })

	settings := DefaultRuntimeSettings()
	settings.CodexForceWebsocket = true
	ApplyRuntimeSettings(settings)

	handler := NewHandler(nil, nil, nil, nil)
	if !handler.shouldUseWebsocketForHTTP() {
		t.Fatal("shouldUseWebsocketForHTTP() = false, want true when runtime force websocket is enabled")
	}
}

func TestShouldUseWebsocketHonorsStoreForceBeforeHTTPConfig(t *testing.T) {
	previous := CurrentRuntimeSettings()
	t.Cleanup(func() { ApplyRuntimeSettings(previous) })
	ApplyRuntimeSettings(DefaultRuntimeSettings())

	store := auth.NewStore(nil, nil, &database.SystemSettings{
		MaxConcurrency:          2,
		TestConcurrency:         1,
		TestModel:               "gpt-5.4",
		CodexForceWebsocket:     true,
		CodexWSSilentMaxRetries: 2,
	})
	handler := NewHandler(store, nil, &config.Config{CodexUpstreamTransport: "http"}, nil)

	if !handler.shouldUseWebsocketForHTTP() {
		t.Fatal("shouldUseWebsocketForHTTP() = false, want true when store force websocket is enabled")
	}
}
