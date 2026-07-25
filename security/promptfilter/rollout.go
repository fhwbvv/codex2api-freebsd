package promptfilter

import (
	"crypto/sha256"
	"encoding/binary"
	"strconv"
	"strings"
)

const (
	RolloutIdentityNewAPIUser = "newapi_user"
	RolloutIdentityAPIKey     = "api_key"
)

// RolloutIdentity is trusted request-scoped input supplied by the proxy after
// NewAPI HMAC verification or local API-key authentication. It is never copied
// into Decision or persisted in audit metadata.
type RolloutIdentity struct {
	Source string
	Value  string
}

// RolloutDecision contains non-identifying cohort metadata suitable for logs.
type RolloutDecision struct {
	Source   string `json:"source"`
	Bucket   int    `json:"bucket"`
	Selected bool   `json:"selected"`
	Fallback string `json:"fallback"`
}

func resolveGuardRollout(globalMode string, cfg GuardRolloutConfig, identity RolloutIdentity, envelope RequestEnvelope) (string, *RolloutDecision) {
	// A rollout may only reduce enforce. It must never promote off, shadow, or
	// warn traffic into enforce, even when a whitelist or 100% cohort is set.
	if globalMode != GuardModeEnforce || !cfg.Enabled {
		return globalMode, nil
	}
	cfg = normalizeGuardRolloutConfig(cfg, DefaultGuardConfig().Rollout)
	fallback := cfg.FallbackMode
	record := &RolloutDecision{Source: identity.Source, Bucket: -1, Fallback: fallback}
	if !guardRolloutScopeMatches(cfg.Protocols, string(envelope.Protocol)) || !guardRolloutScopeMatches(cfg.Providers, string(envelope.ModelFamily)) {
		return fallback, record
	}
	identity.Source = strings.TrimSpace(identity.Source)
	identity.Value = strings.TrimSpace(identity.Value)
	if identity.Value == "" || (identity.Source != RolloutIdentityNewAPIUser && identity.Source != RolloutIdentityAPIKey) {
		record.Source = ""
		return fallback, record
	}
	record.Source = identity.Source
	record.Bucket = guardRolloutBucket(identity)
	if guardRolloutAllowlisted(cfg, identity) || cfg.Percent == 100 || record.Bucket < cfg.Percent {
		record.Selected = true
		return GuardModeEnforce, record
	}
	return fallback, record
}

func guardRolloutScopeMatches(configured []string, actual string) bool {
	if len(configured) == 0 {
		return true
	}
	actual = strings.ToLower(strings.TrimSpace(actual))
	for _, value := range configured {
		if value == actual {
			return true
		}
	}
	return false
}

func guardRolloutAllowlisted(cfg GuardRolloutConfig, identity RolloutIdentity) bool {
	switch identity.Source {
	case RolloutIdentityNewAPIUser:
		for _, userID := range cfg.NewAPIUserAllowlist {
			if identity.Value == userID {
				return true
			}
		}
	case RolloutIdentityAPIKey:
		apiKeyID, err := strconv.ParseInt(identity.Value, 10, 64)
		if err != nil || apiKeyID <= 0 {
			return false
		}
		for _, allowedID := range cfg.APIKeyAllowlist {
			if apiKeyID == allowedID {
				return true
			}
		}
	}
	return false
}

func guardRolloutBucket(identity RolloutIdentity) int {
	digest := sha256.Sum256([]byte(identity.Source + "\x00" + identity.Value))
	return int(binary.BigEndian.Uint64(digest[:8]) % 100)
}
