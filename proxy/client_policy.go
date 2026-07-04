package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type ClientPolicyEntry struct {
	Originator            string `json:"originator,omitempty"`
	UAContains            string `json:"ua_contains,omitempty"`
	SkipEngineFingerprint bool   `json:"skip_engine_fingerprint,omitempty"`
}

func MatchAllowedClientEntry(entry ClientPolicyEntry, userAgent, originator string) bool {
	entryOriginator := strings.TrimSpace(entry.Originator)
	entryUAContains := strings.TrimSpace(entry.UAContains)
	if entryOriginator == "" && entryUAContains == "" {
		return false
	}
	if entryOriginator != "" && !strings.EqualFold(strings.TrimSpace(originator), entryOriginator) {
		return false
	}
	if entryUAContains != "" && !strings.Contains(strings.ToLower(strings.TrimSpace(userAgent)), strings.ToLower(entryUAContains)) {
		return false
	}
	return true
}

func MatchAllowedClientEntries(entries []ClientPolicyEntry, userAgent, originator string) (ClientPolicyEntry, bool) {
	for _, entry := range entries {
		if MatchAllowedClientEntry(entry, userAgent, originator) {
			return entry, true
		}
	}
	return ClientPolicyEntry{}, false
}

func MatchBlockedClientEntry(entry ClientPolicyEntry, userAgent, originator string) bool {
	entryOriginator := strings.TrimSpace(entry.Originator)
	entryUAContains := strings.TrimSpace(entry.UAContains)
	if entryOriginator == "" && entryUAContains == "" {
		return false
	}
	if entryOriginator != "" && strings.EqualFold(strings.TrimSpace(originator), entryOriginator) {
		return true
	}
	if entryUAContains != "" && strings.Contains(strings.ToLower(strings.TrimSpace(userAgent)), strings.ToLower(entryUAContains)) {
		return true
	}
	return false
}

func MatchBlockedClientEntries(entries []ClientPolicyEntry, userAgent, originator string) (ClientPolicyEntry, bool) {
	for _, entry := range entries {
		if MatchBlockedClientEntry(entry, userAgent, originator) {
			return entry, true
		}
	}
	return ClientPolicyEntry{}, false
}

func ParseClientPolicyEntries(raw string) ([]ClientPolicyEntry, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, true
	}
	if err := ValidateClientPolicyEntriesJSON(raw); err != nil {
		return nil, false
	}
	var entries []ClientPolicyEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, false
	}
	return entries, true
}

func ValidateClientPolicyEntriesJSON(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var entries []ClientPolicyEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return fmt.Errorf("invalid client policy entries JSON: %w", err)
	}
	for i, entry := range entries {
		if err := validateClientPolicyEntry(entry); err != nil {
			return fmt.Errorf("invalid client policy entry at index %d: %w", i, err)
		}
	}
	return nil
}

func validateClientPolicyEntry(entry ClientPolicyEntry) error {
	hasOriginator := strings.TrimSpace(entry.Originator) != ""
	hasUAContains := strings.TrimSpace(entry.UAContains) != ""
	if !hasOriginator && !hasUAContains {
		return errors.New("originator or ua_contains is required")
	}
	if entry.SkipEngineFingerprint && (!hasOriginator || !hasUAContains) {
		return errors.New("skip_engine_fingerprint requires both originator and ua_contains")
	}
	return nil
}
