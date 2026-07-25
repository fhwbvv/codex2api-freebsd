package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/codex2api/security/promptfilter"
	"github.com/gin-gonic/gin"
)

const promptRequestSecurityContextKey = "prompt_filter_request_security_context"

// promptRequestSecurityContext owns request-local prompt security state. It is
// deliberately separate from the verified NewAPI identity keys because an HTTP
// request has one body/config snapshot while a WebSocket connection can carry
// multiple logical request frames under one verified connection identity.
type promptRequestSecurityContext struct {
	configOwner        *Handler
	config             promptfilter.Config
	configReady        bool
	digestBody         []byte
	digest             [sha256.Size]byte
	digestHex          string
	digestReady        bool
	digestComputations uint8
}

func promptRequestSecurityState(c *gin.Context) *promptRequestSecurityContext {
	if c == nil {
		return nil
	}
	if value, ok := c.Get(promptRequestSecurityContextKey); ok {
		if state, valid := value.(*promptRequestSecurityContext); valid && state != nil {
			return state
		}
	}
	state := &promptRequestSecurityContext{}
	c.Set(promptRequestSecurityContextKey, state)
	return state
}

// resetPromptRequestSecurityFrame starts a fresh per-frame config/digest scope
// without touching connection-level NewAPI identity verification.
func resetPromptRequestSecurityFrame(c *gin.Context) {
	if c != nil {
		c.Set(promptRequestSecurityContextKey, &promptRequestSecurityContext{})
	}
}

func (h *Handler) promptFilterConfigForRequest(c *gin.Context) promptfilter.Config {
	if h == nil || h.store == nil {
		return promptfilter.DefaultConfig()
	}
	state := promptRequestSecurityState(c)
	if state == nil {
		return h.store.GetPromptFilterConfigSnapshot()
	}
	if state.configReady && state.configOwner == h {
		return state.config
	}
	state.config = h.store.GetPromptFilterConfigSnapshot()
	state.configOwner = h
	state.configReady = true
	return state.config
}

// capturePromptRequestIngress retains the already-owned request buffer by
// reference only when signed NewAPI verification can need the pre-mapping body.
// Callers must treat the buffer as immutable; body rewrites use a new slice.
func (h *Handler) capturePromptRequestIngress(c *gin.Context, body []byte) {
	if h == nil || h.store == nil || c == nil {
		return
	}
	cfg := h.promptFilterConfigForRequest(c)
	if !cfg.Advanced.NewAPI.Enabled || strings.TrimSpace(c.GetHeader("X-NewAPI-Signature")) == "" {
		return
	}
	setIngressRequestBodyIfAbsent(c, body)
}

func sameRequestBodyBuffer(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	if len(left) == 0 {
		return true
	}
	return &left[0] == &right[0]
}

func promptRequestBodyDigest(c *gin.Context, body []byte) ([sha256.Size]byte, string) {
	state := promptRequestSecurityState(c)
	if state != nil && state.digestReady && sameRequestBodyBuffer(state.digestBody, body) {
		return state.digest, state.digestHex
	}
	digest := sha256.Sum256(body)
	digestHex := hex.EncodeToString(digest[:])
	if state != nil {
		state.digestBody = body
		state.digest = digest
		state.digestHex = digestHex
		state.digestReady = true
		state.digestComputations++
	}
	return digest, digestHex
}

func promptRequestDigestComputationCount(c *gin.Context) int {
	state := promptRequestSecurityState(c)
	if state == nil {
		return 0
	}
	return int(state.digestComputations)
}
