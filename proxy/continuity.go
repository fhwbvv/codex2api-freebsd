package proxy

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/codex2api/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Continuity 会话连续性信息
type Continuity struct {
	Key    string // 会话 key
	Source string // 来源标识
}

// Request 请求结构（用于连续性解析）
type Request struct {
	Payload []byte      // 请求体
	Headers http.Header // 请求头
}

// Options 选项结构（用于传递元数据）
type Options struct {
	Metadata map[string]any // 元数据
}

// Metadata 常量定义
const (
	ExecutionSessionMetadataKey = "execution_session"
	SelectedAuthMetadataKey     = "selected_auth"
)

// metadataString 从元数据中提取字符串值
func metadataString(meta map[string]any, key string) string {
	if len(meta) == 0 {
		return ""
	}
	raw, ok := meta[key]
	if !ok || raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case []byte:
		return strings.TrimSpace(string(v))
	default:
		return ""
	}
}

// principalString 将任意值转换为字符串
func principalString(raw any) string {
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", raw))
	}
}

// getGinContext 尝试从 context 中获取 gin.Context
func getGinContext(ctx context.Context) *gin.Context {
	if ctx == nil {
		return nil
	}
	// gin.Context 通常存储在 key "gin-context"
	if c, ok := ctx.Value("gin-context").(*gin.Context); ok {
		return c
	}
	// *gin.Context 本身不实现 context.Context，不能直接类型断言
	// 必须通过存储在 context 中的 key 获取
	return nil
}

// ResolveContinuity 解析会话连续性
// 优先级（从高到低）：
// 1. prompt_cache_key (请求体)
// 2. execution_session (元数据)
// 3. idempotency-key (请求头)
// 4. client_principal (gin context 中的 apiKey)
// 5. auth_id (账号 ID)
func ResolveContinuity(ctx context.Context, account *auth.Account, req Request, opts Options) Continuity {
	// 1. 最高优先级：请求体中的 prompt_cache_key
	if promptCacheKey := strings.TrimSpace(gjson.GetBytes(req.Payload, "prompt_cache_key").String()); promptCacheKey != "" {
		return Continuity{Key: promptCacheKey, Source: "prompt_cache_key"}
	}

	// 2. 元数据中的 execution_session
	if executionSession := metadataString(opts.Metadata, ExecutionSessionMetadataKey); executionSession != "" {
		return Continuity{Key: executionSession, Source: "execution_session"}
	}

	// 3. 请求头中的 Idempotency-Key
	if req.Headers != nil {
		if v := strings.TrimSpace(req.Headers.Get("Idempotency-Key")); v != "" {
			return Continuity{Key: v, Source: "idempotency_key"}
		}
	}

	// 4. gin context 中的 apiKey（client_principal）
	// 优先从 Authorization header 读取
	apiKey := ""
	if ginCtx := getGinContext(ctx); ginCtx != nil {
		// 尝试从 Authorization header 读取
		authHeader := ginCtx.GetHeader("Authorization")
		if authHeader != "" {
			apiKey = strings.TrimPrefix(authHeader, "Bearer ")
			apiKey = strings.TrimSpace(apiKey)
		}
		// 回退：尝试从 gin context 中获取（如果中间件设置了）
		if apiKey == "" {
			if v, exists := ginCtx.Get("apiKey"); exists && v != nil {
				apiKey = principalString(v)
			}
		}
	}
	if apiKey != "" {
		return Continuity{
			Key:    uuid.NewSHA1(uuid.NameSpaceOID, []byte("codex2api:prompt-cache:"+apiKey)).String(),
			Source: "client_principal",
		}
	}

	// 5. 最低优先级：基于 auth.ID 生成确定性 UUID
	if account != nil {
		account.Mu().RLock()
		authID := strings.TrimSpace(account.Email)
		if authID == "" {
			// 只有当 DBID > 0 时才使用 ID 作为 fallback
			if account.DBID > 0 {
				authID = fmt.Sprintf("%d", account.DBID)
			}
		}
		account.Mu().RUnlock()

		if authID != "" {
			return Continuity{
				Key:    uuid.NewSHA1(uuid.NameSpaceOID, []byte("codex2api:prompt-cache:auth:"+authID)).String(),
				Source: "auth_id",
			}
		}
	}

	return Continuity{}
}

// ApplyContinuityBody 将会话连续性应用到请求体
// 将 continuity.Key 注入到 prompt_cache_key 字段
func ApplyContinuityBody(body []byte, c Continuity) []byte {
	if c.Key == "" {
		return body
	}
	body, _ = sjson.SetBytes(body, "prompt_cache_key", c.Key)
	return body
}

// ApplyContinuityHeaders 将会话连续性应用到请求头
// 设置 session_id 头用于保持会话连续性
func ApplyContinuityHeaders(headers http.Header, c Continuity) {
	if headers == nil || c.Key == "" {
		return
	}
	headers.Set("session_id", c.Key)
}

// GetSessionID 从连续性信息获取 session_id（向后兼容）
func (c Continuity) GetSessionID() string {
	return c.Key
}

// IsEmpty 检查连续性是否为空
func (c Continuity) IsEmpty() bool {
	return c.Key == ""
}
