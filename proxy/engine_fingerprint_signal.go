package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

type EngineFingerprintSignal struct {
	Type     string   `json:"type"`
	Match    []string `json:"match"`
	Required bool     `json:"required"`
}

const (
	FingerprintSignalHeaderExact  = "header_exact"
	FingerprintSignalHeaderPrefix = "header_prefix"
	FingerprintSignalBodyPath     = "body_path"
)

var DefaultEngineFingerprintSignals = []EngineFingerprintSignal{
	{Type: FingerprintSignalHeaderPrefix, Match: []string{"x-codex-"}, Required: true},
	{Type: FingerprintSignalHeaderExact, Match: []string{"session-id", "session_id"}, Required: false},
	{Type: FingerprintSignalHeaderExact, Match: []string{"thread-id", "thread_id"}, Required: false},
	{Type: FingerprintSignalBodyPath, Match: []string{"client_metadata.x-codex-window-id", "client_metadata.x-codex-installation-id"}, Required: false},
}

func EvaluateEngineFingerprint(headers http.Header, body []byte, signals []EngineFingerprintSignal) bool {
	if len(signals) == 0 {
		signals = DefaultEngineFingerprintSignals
	}
	for _, signal := range signals {
		if !signal.Required {
			continue
		}
		if !matchEngineFingerprintSignal(headers, body, signal) {
			return false
		}
	}
	return true
}

func ParseEngineFingerprintSignals(raw string) ([]EngineFingerprintSignal, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return cloneEngineFingerprintSignals(DefaultEngineFingerprintSignals), true
	}
	if err := ValidateEngineFingerprintSignalsJSON(raw); err != nil {
		return nil, false
	}
	var signals []EngineFingerprintSignal
	if err := json.Unmarshal([]byte(raw), &signals); err != nil {
		return nil, false
	}
	return signals, true
}

func ValidateEngineFingerprintSignalsJSON(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var signals []EngineFingerprintSignal
	if err := json.Unmarshal([]byte(raw), &signals); err != nil {
		return fmt.Errorf("invalid engine fingerprint signals JSON: %w", err)
	}
	for i, signal := range signals {
		if err := validateEngineFingerprintSignal(signal); err != nil {
			return fmt.Errorf("invalid engine fingerprint signal at index %d: %w", i, err)
		}
	}
	return nil
}

func DefaultEngineFingerprintSignalsJSON() string {
	data, err := json.MarshalIndent(DefaultEngineFingerprintSignals, "", "  ")
	if err != nil {
		return "[]"
	}
	return string(data)
}

func validateEngineFingerprintSignal(signal EngineFingerprintSignal) error {
	switch strings.TrimSpace(signal.Type) {
	case FingerprintSignalHeaderExact, FingerprintSignalHeaderPrefix, FingerprintSignalBodyPath:
	default:
		return fmt.Errorf("unknown type %q", signal.Type)
	}
	if len(signal.Match) == 0 {
		return errors.New("match must not be empty")
	}
	for _, match := range signal.Match {
		if strings.TrimSpace(match) != "" {
			return nil
		}
	}
	return errors.New("match must contain at least one non-empty value")
}

func matchEngineFingerprintSignal(headers http.Header, body []byte, signal EngineFingerprintSignal) bool {
	signalType := strings.TrimSpace(signal.Type)
	for _, match := range signal.Match {
		match = strings.TrimSpace(match)
		if match == "" {
			continue
		}
		switch signalType {
		case FingerprintSignalHeaderExact:
			if matchHeaderExact(headers, match) {
				return true
			}
		case FingerprintSignalHeaderPrefix:
			if matchHeaderPrefix(headers, match) {
				return true
			}
		case FingerprintSignalBodyPath:
			if gjson.GetBytes(body, match).Exists() {
				return true
			}
		}
	}
	return false
}

func matchHeaderExact(headers http.Header, name string) bool {
	if headers == nil {
		return false
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	for headerName, values := range headers {
		if strings.ToLower(strings.TrimSpace(headerName)) != name {
			continue
		}
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				return true
			}
		}
	}
	return false
}

func matchHeaderPrefix(headers http.Header, prefix string) bool {
	if headers == nil {
		return false
	}
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if prefix == "" {
		return false
	}
	for name, values := range headers {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), prefix) {
			continue
		}
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				return true
			}
		}
	}
	return false
}

func cloneEngineFingerprintSignals(signals []EngineFingerprintSignal) []EngineFingerprintSignal {
	cloned := make([]EngineFingerprintSignal, 0, len(signals))
	for _, signal := range signals {
		matches := append([]string(nil), signal.Match...)
		cloned = append(cloned, EngineFingerprintSignal{Type: signal.Type, Match: matches, Required: signal.Required})
	}
	return cloned
}
