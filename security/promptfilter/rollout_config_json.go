package promptfilter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Keep the Go representation of local API-key IDs as int64 while exposing a
// string-only JSON contract. This avoids JavaScript precision loss and remains
// compatible with older hand-written numeric arrays.
func (cfg GuardRolloutConfig) MarshalJSON() ([]byte, error) {
	type rolloutAlias GuardRolloutConfig
	newAPIUsers := make([]string, 0, len(cfg.NewAPIUserAllowlist))
	for _, value := range cfg.NewAPIUserAllowlist {
		normalized, err := normalizeRolloutNewAPIUserID(value)
		if err != nil {
			return nil, fmt.Errorf("newapi_user_allowlist: %w", err)
		}
		if normalized != "" {
			newAPIUsers = append(newAPIUsers, normalized)
		}
	}
	apiKeys := make([]string, 0, len(cfg.APIKeyAllowlist))
	for _, value := range cfg.APIKeyAllowlist {
		if value <= 0 {
			return nil, fmt.Errorf("api_key_allowlist: ID must be a positive integer")
		}
		apiKeys = append(apiKeys, strconv.FormatInt(value, 10))
	}
	return json.Marshal(struct {
		rolloutAlias
		NewAPIUserAllowlist []string `json:"newapi_user_allowlist"`
		APIKeyAllowlist     []string `json:"api_key_allowlist"`
	}{
		rolloutAlias:        rolloutAlias(cfg),
		NewAPIUserAllowlist: newAPIUsers,
		APIKeyAllowlist:     apiKeys,
	})
}

func (cfg *GuardRolloutConfig) UnmarshalJSON(data []byte) error {
	type rolloutAlias GuardRolloutConfig
	alias := rolloutAlias(*cfg)
	wire := struct {
		*rolloutAlias
		NewAPIUserAllowlist []json.RawMessage `json:"newapi_user_allowlist"`
		APIKeyAllowlist     []json.RawMessage `json:"api_key_allowlist"`
	}{rolloutAlias: &alias}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	newAPIUsers := make([]string, 0, len(wire.NewAPIUserAllowlist))
	for index, raw := range wire.NewAPIUserAllowlist {
		value, err := decodeRolloutJSONID(raw, false)
		if err != nil {
			return fmt.Errorf("newapi_user_allowlist[%d]: %w", index, err)
		}
		if value != "" {
			newAPIUsers = append(newAPIUsers, value)
		}
	}
	apiKeys := make([]int64, 0, len(wire.APIKeyAllowlist))
	for index, raw := range wire.APIKeyAllowlist {
		value, err := decodeRolloutJSONID(raw, true)
		if err != nil {
			return fmt.Errorf("api_key_allowlist[%d]: %w", index, err)
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed <= 0 {
			return fmt.Errorf("api_key_allowlist[%d]: ID must fit in a positive int64", index)
		}
		apiKeys = append(apiKeys, parsed)
	}
	alias.NewAPIUserAllowlist = newAPIUsers
	alias.APIKeyAllowlist = apiKeys
	*cfg = GuardRolloutConfig(alias)
	return nil
}

func decodeRolloutJSONID(raw json.RawMessage, apiKey bool) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "", fmt.Errorf("empty JSON value")
	}
	var value string
	if raw[0] == '"' {
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", err
		}
		value = strings.TrimSpace(value)
	} else {
		value = string(raw)
	}
	if decimal, ok := canonicalRolloutPositiveDecimal(value); ok {
		return decimal, nil
	}
	if apiKey {
		return "", fmt.Errorf("expected a positive decimal integer encoded as a string or JSON integer")
	}
	if raw[0] != '"' {
		return "", fmt.Errorf("expected a string or positive JSON integer")
	}
	if value == "" {
		return "", nil
	}
	if looksLikeRolloutNonIntegerNumber(value) {
		return "", fmt.Errorf("floating-point and scientific notation are not allowed")
	}
	return value, nil
}

func normalizeRolloutNewAPIUserID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if decimal, ok := canonicalRolloutPositiveDecimal(value); ok {
		return decimal, nil
	}
	if value == "" {
		return "", nil
	}
	if looksLikeRolloutNonIntegerNumber(value) {
		return "", fmt.Errorf("floating-point and scientific notation are not allowed")
	}
	return value, nil
}

func canonicalRolloutPositiveDecimal(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	value = strings.TrimLeft(value, "0")
	if value == "" {
		return "", false
	}
	return value, true
}

func looksLikeRolloutNonIntegerNumber(value string) bool {
	if len(value) > 1 && (value[0] == '+' || value[0] == '-') {
		digitsOnly := true
		for _, r := range value[1:] {
			if r < '0' || r > '9' {
				digitsOnly = false
				break
			}
		}
		if digitsOnly {
			return true
		}
	}
	if !strings.ContainsAny(value, ".eE") {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && r != '+' && r != '-' && r != '.' && r != 'e' && r != 'E' {
			return false
		}
	}
	return true
}
