package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/codex2api/auth"
	"github.com/tidwall/gjson"
)

const grokRuntimeErrorPeekLimit = 64 << 10

// grokRuntimeProviderCode extracts only a bounded machine-readable category.
// Human messages and the full provider body never enter persistent facts.
func grokRuntimeProviderCode(body []byte) string {
	for _, path := range []string{"error.code", "response.error.code", "error.type", "response.error.type", "code", "type"} {
		if value := strings.TrimSpace(gjson.GetBytes(body, path).String()); value != "" && !strings.EqualFold(value, "error") {
			value = strings.ToLower(value)
			if len(value) > 64 {
				value = value[:64]
			}
			var safe strings.Builder
			for _, r := range value {
				if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
					safe.WriteRune(r)
				}
			}
			if safe.Len() > 0 {
				return safe.String()
			}
		}
	}
	return ""
}

func persistGrokRuntimeObservation(account *auth.Account, observation auth.GrokRuntimeFactObservation) {
	if account == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := account.ObserveGrokRuntimeFact(ctx, observation); err != nil {
		// The error body is intentionally absent. A failed database write must not
		// mutate the in-memory hard gate or native routing projection.
		log.Printf("Grok 账号 %d 运行时事实持久化失败: %v", account.ID(), err)
	}
}

var grokRuntimeSettingsAliases = map[string][]string{
	"allow_access":               {"allow_access", "allowAccess"},
	"subscription_tier_display":  {"subscription_tier_display", "subscriptionTierDisplay"},
	"on_demand_enabled":          {"on_demand_enabled", "onDemandEnabled"},
	"default_model":              {"default_model", "defaultModel"},
	"min_client_version":         {"min_client_version", "minClientVersion"},
	"force_update":               {"force_update", "forceUpdate"},
	"usage_billing_redirect_url": {"usage_billing_redirect_url", "usageBillingRedirectUrl"},
}

// sanitizeGrokRuntimeSettingsBody keeps the 426 refresh path from persisting
// the complete settings response. Unknown fields are discarded before the
// auth package applies its second allowlist.
func sanitizeGrokRuntimeSettingsBody(body []byte) (*auth.GrokRuntimeSettingsObservation, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var source map[string]any
	if err := decoder.Decode(&source); err != nil {
		return nil, err
	}
	payload := make(map[string]any)
	presence := make(map[string]string, len(grokRuntimeSettingsAliases))
	for canonical, aliases := range grokRuntimeSettingsAliases {
		var value any
		found := false
		for _, alias := range aliases {
			if value, found = source[alias]; found {
				break
			}
		}
		switch {
		case !found:
			presence[canonical] = "missing"
		case value == nil:
			presence[canonical] = "null"
			payload[canonical] = nil
		case canonical == "allow_access" || canonical == "on_demand_enabled" || canonical == "force_update":
			if _, ok := value.(bool); ok {
				presence[canonical] = "value"
				payload[canonical] = value
			} else {
				presence[canonical] = "invalid"
			}
		default:
			if text, ok := value.(string); ok && len(text) <= 4096 && !strings.ContainsRune(text, '\x00') {
				presence[canonical] = "value"
				payload[canonical] = text
			} else {
				presence[canonical] = "invalid"
			}
		}
	}
	return &auth.GrokRuntimeSettingsObservation{Payload: payload, FieldPresence: presence}, nil
}

func observeGrokRuntimeSettingsFact(account *auth.Account, result GrokControlPlaneFactResult) {
	if account == nil || result.NotModified || result.StatusCode < 200 || result.StatusCode >= 300 || len(bytes.TrimSpace(result.Body)) == 0 {
		return
	}
	settings, err := sanitizeGrokRuntimeSettingsBody(result.Body)
	if err != nil {
		return
	}
	persistGrokRuntimeObservation(account, auth.GrokRuntimeFactObservation{
		Settings:   settings,
		HTTPStatus: result.StatusCode,
		ObservedAt: result.ObservedAt,
	})
}

// observeGrokExplicitBillingExhaustion converts only an explicit 402 balance
// signal into a 30-second hard billing fact. A bare/unknown 402 remains unknown.
func observeGrokExplicitBillingExhaustion(account *auth.Account, statusCode int, body []byte) {
	if account == nil || statusCode != http.StatusPaymentRequired || !IsGrokSpendingLimitError(body) {
		return
	}
	code := grokRuntimeProviderCode(body)
	if code == "" {
		code = "balance_exhausted"
	}
	persistGrokRuntimeObservation(account, auth.GrokRuntimeFactObservation{
		BillingExhausted: true,
		ProviderCode:     code,
		HTTPStatus:       statusCode,
		ObservedAt:       time.Now(),
	})
}

type grokPeekedReadCloser struct {
	io.Reader
	closer io.Closer
}

func (r *grokPeekedReadCloser) Close() error { return r.closer.Close() }

// peekGrokRuntimeErrorBody reads a small prefix for classification and restores
// an equivalent stream, including any unread tail, for the normal handler.
func peekGrokRuntimeErrorBody(resp *http.Response) []byte {
	if resp == nil || resp.Body == nil {
		return nil
	}
	original := resp.Body
	prefix, _ := io.ReadAll(io.LimitReader(original, grokRuntimeErrorPeekLimit))
	resp.Body = &grokPeekedReadCloser{
		Reader: io.MultiReader(bytes.NewReader(prefix), original),
		closer: original,
	}
	return prefix
}

func deterministicGrokNativeFailure(status int, providerCode string, body []byte) bool {
	switch status {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusUnsupportedMediaType, http.StatusNotImplemented:
		return true
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		// 400/422 normally describe request semantics and must not erase a proven
		// endpoint capability. Only explicit endpoint/model/protocol categories are
		// deterministic for the scoped native capability.
		code := strings.ToLower(strings.TrimSpace(providerCode))
		for _, marker := range []string{
			"model_not_found", "unsupported_model", "model_not_supported",
			"unsupported_endpoint", "endpoint_not_supported", "unsupported_protocol",
			"route_not_found", "method_not_allowed", "not_implemented",
		} {
			if code == marker {
				return true
			}
		}
		lower := bytes.ToLower(body)
		return bytes.Contains(lower, []byte("endpoint is not supported")) ||
			bytes.Contains(lower, []byte("protocol is not supported"))
	default:
		// 401/402/403/426/429, transport failures and 5xx are credential,
		// balance, policy, version or transient facts—not native ACL evidence.
		return false
	}
}

// observeGrokNativeProtocolFailure expires a fresh native conclusion only when
// the exact same inbound protocol was selected natively and the provider gave
// deterministic evidence. Converted backend failures never invalidate the
// inbound protocol's native capability.
//
// ExecuteGrokProtocolRequest should call this after its 426/blob retries have
// settled and before adaptGrokProtocolResponse consumes/wraps the response body.
func observeGrokNativeProtocolFailure(account *auth.Account, route GrokUpstreamRoute, inbound GrokProtocol, resp *http.Response) {
	if account == nil || resp == nil || !route.Native ||
		route.Protocol != auth.NormalizeGrokProtocol(string(inbound)) {
		return
	}
	status := resp.StatusCode
	var prefix []byte
	if status == http.StatusBadRequest || status == http.StatusUnprocessableEntity {
		prefix = peekGrokRuntimeErrorBody(resp)
	}
	code := grokRuntimeProviderCode(prefix)
	if !deterministicGrokNativeFailure(status, code, prefix) {
		return
	}
	persistGrokRuntimeObservation(account, auth.GrokRuntimeFactObservation{
		ExpireNativeCapability: true,
		ModelID:                route.Model,
		Origin:                 route.BaseURL,
		Protocol:               route.Protocol,
		HTTPStatus:             status,
		ProviderCode:           code,
		ObservedAt:             time.Now(),
	})
}
