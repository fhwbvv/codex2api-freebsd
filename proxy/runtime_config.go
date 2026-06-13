package proxy

import (
	"strings"
	"sync/atomic"
	"time"

	"github.com/codex2api/database"
)

const (
	ClientCompatModePreserve = "preserve"
	ClientCompatModeAuto     = "auto"
	ClientCompatModeForce    = "force"

	StreamFlushPolicyImmediate = "immediate"
	StreamFlushPolicyCoalesce  = "coalesce"

	FirstTokenModeStrict = "strict"
	FirstTokenModeLoose  = "loose"

	BillingTierPolicyActual    = "actual"
	BillingTierPolicyRequested = "requested"

	defaultClientCompatMode      = ClientCompatModePreserve
	defaultCodexMinCLIVersion    = "0.118.0"
	defaultStreamFlushPolicy     = StreamFlushPolicyImmediate
	defaultStreamFlushIntervalMS = 20
	minStreamFlushIntervalMS     = 1
	maxStreamFlushIntervalMS     = 1000
	defaultFirstTokenMode        = FirstTokenModeStrict
	defaultFirstTokenTimeoutSec  = 0
	maxFirstTokenTimeoutSec      = 600
	defaultBillingTierPolicy     = BillingTierPolicyActual
	defaultCodexWSHideErrors     = true
	defaultCodexWSSilentRetry    = true
	defaultCodexWSSilentRetries  = 2
	maxCodexWSSilentRetries      = 10
)

type RuntimeSettings struct {
	ClientCompatMode      string
	CodexMinCLIVersion    string
	StreamFlushPolicy     string
	StreamFlushIntervalMS int
	FirstTokenMode        string
	FirstTokenTimeoutSec  int
	BillingTierPolicy     string
	CodexForceWebsocket   bool // 强制 Codex 上游走 WebSocket（默认 false）
	CodexWSHideErrors     bool // 隐藏 Codex WS 上游原始错误（默认 true）
	CodexWSSilentRetry    bool // 首包前 Codex WS 上游错误静默换号重试（默认 true）
	CodexWSSilentRetries  int  // Codex WS 静默换号最大重试次数（默认 2）
}

var runtimeSettings atomic.Value // stores RuntimeSettings

func init() {
	runtimeSettings.Store(DefaultRuntimeSettings())
}

func DefaultRuntimeSettings() RuntimeSettings {
	return RuntimeSettings{
		ClientCompatMode:      defaultClientCompatMode,
		CodexMinCLIVersion:    defaultCodexMinCLIVersion,
		StreamFlushPolicy:     defaultStreamFlushPolicy,
		StreamFlushIntervalMS: defaultStreamFlushIntervalMS,
		FirstTokenMode:        defaultFirstTokenMode,
		FirstTokenTimeoutSec:  defaultFirstTokenTimeoutSec,
		BillingTierPolicy:     defaultBillingTierPolicy,
		CodexWSHideErrors:     defaultCodexWSHideErrors,
		CodexWSSilentRetry:    defaultCodexWSSilentRetry,
		CodexWSSilentRetries:  defaultCodexWSSilentRetries,
	}
}

func NormalizeClientCompatMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", ClientCompatModePreserve:
		return ClientCompatModePreserve
	case ClientCompatModeAuto:
		return ClientCompatModeAuto
	case ClientCompatModeForce:
		return ClientCompatModeForce
	default:
		return ClientCompatModePreserve
	}
}

func NormalizeStreamFlushPolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "", StreamFlushPolicyImmediate:
		return StreamFlushPolicyImmediate
	case StreamFlushPolicyCoalesce:
		return StreamFlushPolicyCoalesce
	default:
		return StreamFlushPolicyImmediate
	}
}

func NormalizeFirstTokenMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", FirstTokenModeStrict:
		return FirstTokenModeStrict
	case FirstTokenModeLoose:
		return FirstTokenModeLoose
	default:
		return FirstTokenModeStrict
	}
}

func NormalizeBillingTierPolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "", BillingTierPolicyActual:
		return BillingTierPolicyActual
	case BillingTierPolicyRequested:
		return BillingTierPolicyRequested
	default:
		return BillingTierPolicyActual
	}
}

func NormalizeRuntimeSettings(settings RuntimeSettings) RuntimeSettings {
	defaults := DefaultRuntimeSettings()
	settings.ClientCompatMode = NormalizeClientCompatMode(settings.ClientCompatMode)
	settings.StreamFlushPolicy = NormalizeStreamFlushPolicy(settings.StreamFlushPolicy)
	settings.FirstTokenMode = NormalizeFirstTokenMode(settings.FirstTokenMode)
	settings.BillingTierPolicy = NormalizeBillingTierPolicy(settings.BillingTierPolicy)
	if strings.TrimSpace(settings.CodexMinCLIVersion) == "" {
		settings.CodexMinCLIVersion = defaults.CodexMinCLIVersion
	} else {
		settings.CodexMinCLIVersion = strings.TrimSpace(settings.CodexMinCLIVersion)
	}
	if settings.StreamFlushIntervalMS < minStreamFlushIntervalMS {
		settings.StreamFlushIntervalMS = defaults.StreamFlushIntervalMS
	}
	if settings.StreamFlushIntervalMS > maxStreamFlushIntervalMS {
		settings.StreamFlushIntervalMS = maxStreamFlushIntervalMS
	}
	if settings.FirstTokenTimeoutSec < 0 {
		settings.FirstTokenTimeoutSec = defaultFirstTokenTimeoutSec
	}
	if settings.FirstTokenTimeoutSec > maxFirstTokenTimeoutSec {
		settings.FirstTokenTimeoutSec = maxFirstTokenTimeoutSec
	}
	if settings.CodexWSSilentRetries < 0 {
		settings.CodexWSSilentRetries = 0
	}
	if settings.CodexWSSilentRetries > maxCodexWSSilentRetries {
		settings.CodexWSSilentRetries = maxCodexWSSilentRetries
	}
	return settings
}

func ApplyRuntimeSettingsFromSystem(settings *database.SystemSettings) RuntimeSettings {
	next := DefaultRuntimeSettings()
	if settings != nil {
		next.ClientCompatMode = settings.ClientCompatMode
		next.CodexMinCLIVersion = settings.CodexMinCLIVersion
		next.StreamFlushPolicy = settings.StreamFlushPolicy
		next.StreamFlushIntervalMS = settings.StreamFlushIntervalMS
		next.FirstTokenMode = settings.FirstTokenMode
		next.FirstTokenTimeoutSec = settings.FirstTokenTimeoutSeconds
		next.BillingTierPolicy = settings.BillingTierPolicy
		next.CodexForceWebsocket = settings.CodexForceWebsocket
		next.CodexWSHideErrors = settings.CodexWSHideUpstreamErrors
		next.CodexWSSilentRetry = settings.CodexWSSilentRetryEnabled
		next.CodexWSSilentRetries = settings.CodexWSSilentMaxRetries
	}
	next = NormalizeRuntimeSettings(next)
	runtimeSettings.Store(next)
	return next
}

func ApplyRuntimeSettings(settings RuntimeSettings) RuntimeSettings {
	settings = NormalizeRuntimeSettings(settings)
	runtimeSettings.Store(settings)
	return settings
}

func CurrentRuntimeSettings() RuntimeSettings {
	if v, ok := runtimeSettings.Load().(RuntimeSettings); ok {
		return NormalizeRuntimeSettings(v)
	}
	return DefaultRuntimeSettings()
}

func currentStreamFlushInterval() time.Duration {
	ms := CurrentRuntimeSettings().StreamFlushIntervalMS
	if ms < minStreamFlushIntervalMS {
		ms = defaultStreamFlushIntervalMS
	}
	return time.Duration(ms) * time.Millisecond
}

func currentFirstTokenTimeout() time.Duration {
	seconds := CurrentRuntimeSettings().FirstTokenTimeoutSec
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func currentFirstTokenMode() string {
	return CurrentRuntimeSettings().FirstTokenMode
}
