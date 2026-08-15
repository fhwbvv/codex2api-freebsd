package auth

import (
	"testing"
	"time"
)

func TestGrokRoutingStatePrefersFreshNativeCapability(t *testing.T) {
	account := &Account{UpstreamType: UpstreamGrok, AccessToken: "at", BaseURL: "https://account.example/v1", CredentialGeneration: 3}
	now := time.Now()
	account.SetGrokRoutingState(GrokRoutingState{
		CredentialGeneration: 3,
		Models:               []GrokModelRoute{{ModelID: "grok-4.5", BaseURL: "https://catalog.example/v1", APIBackend: GrokProtocolResponses}},
		Capabilities:         []GrokProtocolCapability{{ModelID: "grok-4.5", Origin: "https://catalog.example/v1", Protocol: GrokProtocolMessages, Status: GrokCapabilityOK, ObservedAt: now, ExpiresAt: now.Add(time.Hour)}},
	})

	route, ok := account.GetGrokModelRoute("GROK-4.5", GrokProtocolMessages, now)
	if !ok {
		t.Fatal("route not found")
	}
	if route.Protocol != GrokProtocolMessages || route.BaseURL != "https://catalog.example/v1" || !route.Native {
		t.Fatalf("route = %#v", route)
	}

	route, ok = account.GetGrokModelRoute("grok-4.5", GrokProtocolChatCompletions, now)
	if !ok || route.Protocol != GrokProtocolResponses || route.Native {
		t.Fatalf("catalog fallback route = %#v, ok=%v", route, ok)
	}
}

func TestGrokRoutingStateCopiesMutableInputs(t *testing.T) {
	headers := map[string]string{"x-safe": "one"}
	models := []GrokModelRoute{{ModelID: "grok-4.5", APIBackend: GrokProtocolResponses, ExtraHeaders: headers}}
	account := &Account{UpstreamType: UpstreamGrok, APIKey: "xai"}
	account.SetGrokRoutingState(GrokRoutingState{Models: models})
	models[0].ModelID = "changed"
	headers["x-safe"] = "changed"

	route, ok := account.GetGrokModelRoute("grok-4.5", GrokProtocolResponses, time.Now())
	if !ok || route.ExtraHeaders["x-safe"] != "one" {
		t.Fatalf("routing state retained caller-owned storage: %#v, ok=%v", route, ok)
	}
	route.ExtraHeaders["x-safe"] = "mutated"
	route2, _ := account.GetGrokModelRoute("grok-4.5", GrokProtocolResponses, time.Now())
	if route2.ExtraHeaders["x-safe"] != "one" {
		t.Fatalf("returned route leaked internal map: %#v", route2)
	}
}

func TestGrokRoutingRejectsWrongOriginAndExpiredStaleCatalog(t *testing.T) {
	now := time.Now()
	account := &Account{UpstreamType: UpstreamGrok, APIKey: "xai", CredentialGeneration: 2}
	account.SetGrokRoutingState(GrokRoutingState{
		CredentialGeneration: 2, CatalogKnown: true,
		Models: []GrokModelRoute{{ModelID: "grok-4.5", APIBaseURL: "https://api.x.ai/v1", APIBackend: GrokProtocolResponses}},
		Capabilities: []GrokProtocolCapability{{
			ModelID: "grok-4.5", Origin: "https://other.example/v1", Protocol: GrokProtocolMessages,
			Status: GrokCapabilityOK, ObservedAt: now, ExpiresAt: now.Add(time.Hour),
		}},
		ObservedAt: now.Add(-30 * time.Minute), ExpiresAt: now.Add(-25 * time.Minute),
	})
	route, ok := account.GetGrokModelRoute("grok-4.5", GrokProtocolMessages, now)
	if !ok || route.Native || route.Protocol != GrokProtocolResponses {
		t.Fatalf("wrong-origin capability selected: %#v, ok=%v", route, ok)
	}
	if _, ok := account.GetGrokModelRoute("grok-4.5", GrokProtocolResponses, now.Add(31*time.Minute)); ok {
		t.Fatal("catalog remained routable beyond observed_at + 1h")
	}
}

func TestGrokRoutingUsesSingleStaleDeadlineFromSuccessfulObservation(t *testing.T) {
	observed := time.Now().Add(-59 * time.Minute)
	account := &Account{UpstreamType: UpstreamGrok, APIKey: "xai", CredentialGeneration: 1}
	account.SetGrokRoutingState(GrokRoutingState{
		CredentialGeneration: 1, CatalogKnown: true,
		ObservedAt: observed, ExpiresAt: observed.Add(5 * time.Minute),
		Models: []GrokModelRoute{{ModelID: "grok-4.5", APIBackend: GrokProtocolResponses}},
	})
	if _, ok := account.GetGrokModelRoute("grok-4.5", GrokProtocolResponses, observed.Add(time.Hour-time.Nanosecond)); !ok {
		t.Fatal("catalog was not routable inside stale-if-error window")
	}
	if _, ok := account.GetGrokModelRoute("grok-4.5", GrokProtocolResponses, observed.Add(time.Hour)); ok {
		t.Fatal("catalog was routable at the one-hour stale deadline")
	}
}

func TestGrokAuthoritativeEmptyCatalogIsKnown(t *testing.T) {
	account := &Account{UpstreamType: UpstreamGrok, APIKey: "xai", CredentialGeneration: 1}
	account.SetGrokRoutingState(GrokRoutingState{CredentialGeneration: 1, CatalogKnown: true})
	if !account.HasGrokModelCatalog() {
		t.Fatal("authoritative empty catalog was treated as missing")
	}
	if got := account.GrokCatalogModels(); len(got) != 0 {
		t.Fatalf("empty catalog models = %#v", got)
	}
}

func TestGrokRoutingEnforcesCatalogVisibilityOnDirectDispatch(t *testing.T) {
	unsupported := false
	now := time.Now()

	apiKeyAccount := &Account{UpstreamType: UpstreamGrok, APIKey: "xai", CredentialGeneration: 1}
	apiKeyAccount.SetGrokRoutingState(GrokRoutingState{
		CredentialGeneration: 1,
		CatalogKnown:         true,
		Models: []GrokModelRoute{
			{ModelID: "hidden-model", Hidden: true, APIBackend: GrokProtocolResponses},
			{ModelID: "oauth-only-model", SupportedInAPI: &unsupported, APIBackend: GrokProtocolResponses},
		},
	})
	for _, model := range []string{"hidden-model", "oauth-only-model"} {
		if route, ok := apiKeyAccount.GetGrokModelRoute(model, GrokProtocolResponses, now); ok {
			t.Fatalf("API-key direct dispatch exposed %q: %#v", model, route)
		}
	}

	oauthAccount := &Account{UpstreamType: UpstreamGrok, AccessToken: "oauth", CredentialGeneration: 1}
	oauthAccount.SetGrokRoutingState(GrokRoutingState{
		CredentialGeneration: 1,
		CatalogKnown:         true,
		Models: []GrokModelRoute{
			{ModelID: "hidden-model", Hidden: true, APIBackend: GrokProtocolResponses},
			{ModelID: "oauth-model", SupportedInAPI: &unsupported, APIBackend: GrokProtocolResponses},
		},
	})
	if route, ok := oauthAccount.GetGrokModelRoute("hidden-model", GrokProtocolResponses, now); ok {
		t.Fatalf("OAuth direct dispatch exposed hidden model: %#v", route)
	}
	if _, ok := oauthAccount.GetGrokModelRoute("oauth-model", GrokProtocolResponses, now); !ok {
		t.Fatal("OAuth dispatch incorrectly rejected supportedInApi=false model")
	}
}
