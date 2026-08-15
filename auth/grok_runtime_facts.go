package auth

import (
	"context"
	"strings"
	"time"

	"github.com/codex2api/database"
)

// GrokRuntimeFactObservation is the sanitized inference-plane evidence that
// may affect persistent Grok routing facts. It intentionally contains no
// response body, token, principal, or other credential material.
type GrokRuntimeFactObservation struct {
	ModelsETagHint         string
	Settings               *GrokRuntimeSettingsObservation
	BillingExhausted       bool
	ProviderCode           string
	HTTPStatus             int
	ExpireNativeCapability bool
	ModelID                string
	Origin                 string
	Protocol               GrokProtocol
	ObservedAt             time.Time
}

// GrokRuntimeSettingsObservation contains only the non-secret /settings
// projection used by the 426 recovery path. FieldPresence remains separate so
// absent, explicit null, false and zero values never collapse together.
type GrokRuntimeSettingsObservation struct {
	Payload       map[string]any
	FieldPresence map[string]string
}

type grokRuntimeFactSink interface {
	persistGrokRuntimeFact(context.Context, *Account, GrokRuntimeFactObservation) error
}

// ObserveGrokRuntimeFact commits one inference observation. Accounts not
// attached to a database-backed Store (including probes/temporary accounts)
// deliberately do nothing.
func (a *Account) ObserveGrokRuntimeFact(ctx context.Context, observation GrokRuntimeFactObservation) error {
	if a == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	a.mu.RLock()
	sink := a.grokRuntimeSink
	a.mu.RUnlock()
	if sink == nil {
		return nil
	}
	a.grokRuntimeFactsMu.Lock()
	defer a.grokRuntimeFactsMu.Unlock()
	return sink.persistGrokRuntimeFact(ctx, a, observation)
}

func normalizeGrokRuntimeOrigin(origin string) string {
	return strings.TrimRight(strings.TrimSpace(origin), "/")
}

func (s *Store) persistGrokRuntimeFact(ctx context.Context, account *Account, observation GrokRuntimeFactObservation) error {
	if s == nil || s.db == nil || account == nil || account.DBID <= 0 {
		return nil
	}
	observedAt := observation.ObservedAt
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	generation := account.GetCredentialGeneration()
	if generation <= 0 {
		return nil
	}
	origin := normalizeGrokRuntimeOrigin(observation.Origin)
	if origin == "" {
		origin, _ = account.GrokCredentials()
		origin = normalizeGrokRuntimeOrigin(origin)
	}

	// Each mutation is fenced independently. A refresh may advance generation
	// while this observer waits, in which case the database returns applied=false
	// and memory remains untouched.
	if observation.Settings != nil {
		payload, presence := sanitizeGrokRuntimeSettings(observation.Settings)
		fact := database.GrokAccountFact{
			AccountID: account.DBID, Kind: database.GrokFactSettings,
			CredentialGeneration: generation, Status: "ok",
			HTTPStatus: observation.HTTPStatus, Source: "inference_426_settings",
			Payload: payload, FieldPresence: presence,
			ObservedAt: observedAt, ExpiresAt: observedAt.Add(5 * time.Minute),
		}
		applied, err := s.db.UpsertGrokAccountFact(ctx, fact)
		if err != nil {
			return err
		}
		if applied {
			if err := s.ReloadGrokPersistentState(ctx, account.DBID); err != nil {
				return err
			}
		}
	}

	if hint := strings.TrimSpace(observation.ModelsETagHint); hint != "" && origin != "" {
		if len(hint) > 512 || strings.ContainsAny(hint, "\r\n\x00") {
			return nil
		}
		account.mu.RLock()
		alreadyPersisted := account.grokRuntimeModelsHintGeneration == generation &&
			strings.EqualFold(account.grokRuntimeModelsHintOrigin, origin) && account.grokRuntimeModelsHint == hint
		account.mu.RUnlock()
		if !alreadyPersisted {
			applied, err := s.db.UpdateGrokModelsETagHint(ctx, account.DBID, origin, generation, hint, observedAt)
			if err != nil {
				return err
			}
			if applied {
				if err := s.ReloadGrokPersistentState(ctx, account.DBID); err != nil {
					return err
				}
				account.mu.Lock()
				if account.CredentialGeneration == generation {
					account.grokRuntimeModelsHint = hint
					account.grokRuntimeModelsHintOrigin = origin
					account.grokRuntimeModelsHintGeneration = generation
				}
				account.mu.Unlock()
			}
		}
	}

	if observation.BillingExhausted {
		code := normalizeGrokRuntimeProviderCode(observation.ProviderCode)
		fact := database.GrokAccountFact{
			AccountID: account.DBID, Kind: database.GrokFactBilling,
			CredentialGeneration: generation, Status: "exhausted",
			HTTPStatus: observation.HTTPStatus, Source: "inference_error",
			Payload:       map[string]any{"balance_exhausted": true, "provider_code": code},
			FieldPresence: map[string]string{"balance_exhausted": "value", "provider_code": runtimePresence(code)},
			ObservedAt:    observedAt, ExpiresAt: observedAt.Add(30 * time.Second),
		}
		applied, err := s.db.UpsertGrokAccountFactAndExpireCapabilities(ctx, fact, observedAt)
		if err != nil {
			return err
		}
		if applied {
			if err := s.ReloadGrokPersistentState(ctx, account.DBID); err != nil {
				return err
			}
		}
	}

	if observation.ExpireNativeCapability && origin != "" {
		protocol := NormalizeGrokProtocol(string(observation.Protocol))
		modelID := strings.TrimSpace(observation.ModelID)
		if protocol != "" && modelID != "" {
			affected, err := s.db.ExpireGrokModelCapabilities(ctx, account.DBID, generation, modelID, origin, string(protocol), observedAt)
			if err != nil {
				return err
			}
			if affected > 0 {
				return s.ReloadGrokPersistentState(ctx, account.DBID)
			}
		}
	}
	return nil
}

var grokRuntimeSettingTypes = map[string]string{
	"allow_access":               "bool",
	"subscription_tier_display":  "string",
	"on_demand_enabled":          "bool",
	"default_model":              "string",
	"min_client_version":         "string",
	"force_update":               "bool",
	"usage_billing_redirect_url": "string",
}

func sanitizeGrokRuntimeSettings(input *GrokRuntimeSettingsObservation) (map[string]any, map[string]string) {
	payload := make(map[string]any)
	presence := make(map[string]string, len(grokRuntimeSettingTypes))
	if input == nil {
		return payload, presence
	}
	for key, expectedType := range grokRuntimeSettingTypes {
		value, valueExists := input.Payload[key]
		state := strings.ToLower(strings.TrimSpace(input.FieldPresence[key]))
		if state == "present" {
			state = "value"
		}
		if state == "" {
			switch {
			case !valueExists:
				state = "missing"
			case value == nil:
				state = "null"
			default:
				state = "value"
			}
		}
		switch state {
		case "missing":
			presence[key] = "missing"
		case "null":
			presence[key] = "null"
			payload[key] = nil
		case "invalid":
			presence[key] = "invalid"
		case "value":
			valid := false
			switch expectedType {
			case "bool":
				_, valid = value.(bool)
			case "string":
				text, ok := value.(string)
				valid = ok && len(text) <= 4096 && !strings.ContainsRune(text, '\x00')
			}
			if valid {
				presence[key] = "value"
				payload[key] = value
			} else {
				presence[key] = "invalid"
			}
		default:
			presence[key] = "invalid"
		}
	}
	return payload, presence
}

func normalizeGrokRuntimeProviderCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) > 64 {
		value = value[:64]
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func runtimePresence(value string) string {
	if value == "" {
		return "missing"
	}
	return "value"
}
