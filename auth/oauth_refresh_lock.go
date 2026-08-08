package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"
)

const (
	oauthRefreshLeaseNamespace = "oauth-refresh"
	oauthRefreshLeaseTTL       = 5 * time.Minute
	oauthRefreshLeaseHold      = 4 * time.Minute
	oauthRefreshLeaseWait      = 5*time.Minute + 5*time.Second
	oauthRefreshLeasePoll      = 100 * time.Millisecond
)

var oauthRefreshLeaseOwnerSequence atomic.Uint64

type oauthRefreshLocalLock struct {
	ch   chan struct{}
	refs int
}

type oauthRefreshLease struct {
	store       *Store
	fingerprint string
	local       *oauthRefreshLocalLock
	owner       string
	distributed bool
	ctx         context.Context
	cancel      context.CancelFunc
	released    atomic.Bool
}

func oauthRefreshTokenFingerprint(refreshToken string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(refreshToken)))
	return hex.EncodeToString(sum[:])
}

func (s *Store) acquireOAuthRefreshLease(ctx context.Context, refreshToken string) (*oauthRefreshLease, error) {
	if s == nil {
		return nil, fmt.Errorf("账号存储未配置")
	}
	fingerprint := oauthRefreshTokenFingerprint(refreshToken)
	if fingerprint == oauthRefreshTokenFingerprint("") {
		return nil, fmt.Errorf("refresh_token 为空")
	}

	local, err := s.acquireOAuthRefreshLocalLock(ctx, fingerprint)
	if err != nil {
		return nil, err
	}
	lease := &oauthRefreshLease{
		store:       s,
		fingerprint: fingerprint,
		local:       local,
		owner:       fmt.Sprintf("%p-%d-%d", s, time.Now().UnixNano(), oauthRefreshLeaseOwnerSequence.Add(1)),
	}
	if s.tokenCache == nil {
		lease.ctx, lease.cancel = context.WithTimeout(ctx, oauthRefreshLeaseHold)
		return lease, nil
	}

	deadline := time.Now().Add(oauthRefreshLeaseWait)
	for {
		acquired, err := s.tokenCache.AcquireLease(
			ctx,
			oauthRefreshLeaseNamespace,
			fingerprint,
			lease.owner,
			oauthRefreshLeaseTTL,
		)
		if err != nil {
			// Redis 短暂不可用时保留进程内串行化，避免把一次正常刷新直接打成失败。
			log.Printf("获取 OAuth 跨实例刷新 lease 失败，降级为进程内锁: %v", err)
			lease.ctx, lease.cancel = context.WithTimeout(ctx, oauthRefreshLeaseHold)
			return lease, nil
		}
		if acquired {
			lease.distributed = true
			lease.ctx, lease.cancel = context.WithTimeout(ctx, oauthRefreshLeaseHold)
			return lease, nil
		}
		if time.Now().After(deadline) {
			lease.Release()
			return nil, fmt.Errorf("等待同一 OAuth 凭据的刷新任务超时")
		}

		timer := time.NewTimer(oauthRefreshLeasePoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			lease.Release()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *Store) acquireOAuthRefreshLocalLock(ctx context.Context, fingerprint string) (*oauthRefreshLocalLock, error) {
	s.oauthRefreshLocksMu.Lock()
	local := s.oauthRefreshLocks[fingerprint]
	if local == nil {
		local = &oauthRefreshLocalLock{ch: make(chan struct{}, 1)}
		s.oauthRefreshLocks[fingerprint] = local
	}
	local.refs++
	s.oauthRefreshLocksMu.Unlock()

	select {
	case local.ch <- struct{}{}:
		return local, nil
	case <-ctx.Done():
		s.releaseOAuthRefreshLocalLockRef(fingerprint, local)
		return nil, ctx.Err()
	}
}

func (s *Store) releaseOAuthRefreshLocalLockRef(fingerprint string, local *oauthRefreshLocalLock) {
	s.oauthRefreshLocksMu.Lock()
	local.refs--
	if local.refs == 0 && s.oauthRefreshLocks[fingerprint] == local {
		delete(s.oauthRefreshLocks, fingerprint)
	}
	s.oauthRefreshLocksMu.Unlock()
}

func (lease *oauthRefreshLease) Context() context.Context {
	if lease == nil || lease.ctx == nil {
		return context.Background()
	}
	return lease.ctx
}

func (lease *oauthRefreshLease) Release() {
	if lease == nil || lease.store == nil || lease.local == nil || lease.released.Swap(true) {
		return
	}
	if lease.distributed && lease.store.tokenCache != nil {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := lease.store.tokenCache.ReleaseLease(
			releaseCtx,
			oauthRefreshLeaseNamespace,
			lease.fingerprint,
			lease.owner,
		); err != nil {
			log.Printf("释放 OAuth 跨实例刷新 lease 失败: %v", err)
		}
		cancel()
	}
	if lease.cancel != nil {
		lease.cancel()
	}

	<-lease.local.ch
	lease.store.releaseOAuthRefreshLocalLockRef(lease.fingerprint, lease.local)
}

func (s *Store) reloadOAuthCredentialsAfterLock(
	ctx context.Context,
	acc *Account,
	lockedRefreshToken string,
	lockedAccessToken string,
) (changed bool, usable bool, err error) {
	if s == nil || s.db == nil || acc == nil || acc.DBID <= 0 {
		return false, false, nil
	}
	row, err := s.db.GetAccountByID(ctx, acc.DBID)
	if err != nil {
		return false, false, err
	}
	refreshToken := strings.TrimSpace(row.GetCredential("refresh_token"))
	accessToken := strings.TrimSpace(row.GetCredential("access_token"))
	refreshChanged := refreshToken != "" && refreshToken != strings.TrimSpace(lockedRefreshToken)
	accessChanged := accessToken != "" && accessToken != strings.TrimSpace(lockedAccessToken)
	if !refreshChanged && !accessChanged {
		return false, false, nil
	}

	sessionToken := strings.TrimSpace(row.GetCredential("session_token"))
	expiresAt := parseOAuthCredentialExpiry(row.GetCredential("expires_at"))

	acc.mu.Lock()
	if refreshToken != "" {
		acc.RefreshToken = refreshToken
	}
	if accessToken != "" {
		acc.AccessToken = accessToken
	}
	if sessionToken != "" {
		acc.SessionToken = sessionToken
	}
	if !expiresAt.IsZero() {
		acc.ExpiresAt = expiresAt
	}
	if accountID := strings.TrimSpace(row.GetCredential("account_id")); accountID != "" {
		acc.AccountID = accountID
	}
	if email := strings.TrimSpace(row.GetCredential("email")); email != "" {
		acc.Email = email
	}
	if planType := strings.TrimSpace(row.GetCredential("plan_type")); planType != "" {
		acc.PlanType = planType
	}
	effectiveAccessToken := acc.AccessToken
	effectiveExpiresAt := acc.ExpiresAt
	acc.mu.Unlock()

	usable = effectiveAccessToken != "" &&
		(effectiveExpiresAt.IsZero() || time.Until(effectiveExpiresAt) > 5*time.Minute)
	return true, usable, nil
}

func parseOAuthCredentialExpiry(value string) time.Time {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func (s *Store) finishReloadedOAuthRefresh(
	ctx context.Context,
	acc *Account,
	activeCooldown bool,
	expiredCooldown bool,
	cooldownUntil time.Time,
	cooldownReason string,
) {
	acc.mu.Lock()
	if activeCooldown {
		acc.Status = StatusCooldown
		acc.CooldownUtil = cooldownUntil
		acc.CooldownReason = cooldownReason
	} else {
		acc.Status = StatusReady
		acc.CooldownUtil = time.Time{}
		acc.CooldownReason = ""
	}
	acc.ErrorMsg = ""
	acc.recomputeSchedulerLocked(atomic.LoadInt64(&s.maxConcurrency))
	accessToken := acc.AccessToken
	expiresAt := acc.ExpiresAt
	dbID := acc.DBID
	acc.mu.Unlock()
	s.fastSchedulerUpdate(acc)

	if s.tokenCache != nil && accessToken != "" {
		ttl := time.Until(expiresAt) - 5*time.Minute
		if expiresAt.IsZero() {
			ttl = 30 * time.Minute
		}
		if ttl > 0 {
			_ = s.tokenCache.SetAccessToken(ctx, dbID, accessToken, ttl)
		}
	}
	if expiredCooldown {
		s.deleteCachedAccountCooldown(dbID)
		if s.db != nil {
			_ = s.db.ClearCooldown(ctx, dbID)
		}
	} else if !activeCooldown && s.db != nil {
		_ = s.db.ClearError(ctx, dbID)
	}
}
