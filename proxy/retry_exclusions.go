package proxy

import (
	"context"
	"log"
	"time"

	"github.com/codex2api/auth"
)

type retryAccountExclusions struct {
	hard map[int64]bool
	soft map[int64]bool
}

func newRetryAccountExclusions() *retryAccountExclusions {
	return &retryAccountExclusions{
		hard: make(map[int64]bool),
		soft: make(map[int64]bool),
	}
}

func (r *retryAccountExclusions) MarkHard(accountID int64) {
	if r == nil || accountID == 0 {
		return
	}
	r.hard[accountID] = true
	delete(r.soft, accountID)
}

func (r *retryAccountExclusions) MarkSoftFirstTokenTimeout(accountID int64) {
	if r == nil || accountID == 0 {
		return
	}
	if r.hard[accountID] {
		return
	}
	r.soft[accountID] = true
}

func (r *retryAccountExclusions) ResetSoft() bool {
	if r == nil || len(r.soft) == 0 {
		return false
	}
	r.soft = make(map[int64]bool)
	return true
}

func (r *retryAccountExclusions) ForSelection() map[int64]bool {
	if r == nil || (len(r.hard) == 0 && len(r.soft) == 0) {
		return nil
	}
	exclude := make(map[int64]bool, len(r.hard)+len(r.soft))
	for id := range r.hard {
		exclude[id] = true
	}
	for id := range r.soft {
		exclude[id] = true
	}
	return exclude
}

func (h *Handler) nextRetryAccountForSession(ctx context.Context, affinityKey string, apiKeyID int64, exclusions *retryAccountExclusions, filter auth.AccountFilter) (*auth.Account, string) {
	if h == nil || h.store == nil {
		return nil, ""
	}
	for {
		exclude := exclusions.ForSelection()
		account, stickyProxyURL := h.nextAccountForSessionWithFilter(affinityKey, apiKeyID, exclude, filter)
		if account != nil {
			return account, stickyProxyURL
		}
		account, stickyProxyURL = h.store.WaitForSessionAvailableWithFilter(ctx, affinityKey, 30*time.Second, apiKeyID, exclude, filter)
		if account != nil {
			return account, stickyProxyURL
		}
		if !exclusions.ResetSoft() {
			return nil, ""
		}
		log.Printf("首字超时账号池已试完，清空本次请求软排除并进入下一轮重试")
	}
}

func isFirstTokenTimeoutOutcome(outcome streamOutcome) bool {
	return outcome.failureKind == "timeout"
}
