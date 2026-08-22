package proxy

import (
	"sync/atomic"

	"github.com/codex2api/auth"
)

var grokFollowUpEffort atomic.Value

func init() {
	grokFollowUpEffort.Store(auth.DefaultGrokFollowUpEffortConfig())
}

func SetGrokFollowUpEffortConfig(cfg auth.GrokFollowUpEffortConfig) {
	grokFollowUpEffort.Store(auth.NormalizeGrokFollowUpEffortConfig(cfg))
}

func currentGrokFollowUpEffortConfig() auth.GrokFollowUpEffortConfig {
	if cfg, ok := grokFollowUpEffort.Load().(auth.GrokFollowUpEffortConfig); ok {
		return cfg
	}
	return auth.DefaultGrokFollowUpEffortConfig()
}
