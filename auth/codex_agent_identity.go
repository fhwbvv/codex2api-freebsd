package auth

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/nacl/box"
)

// Codex Agent Identity（auth_mode=agentIdentity）授权：
// 账号不保存 OAuth access/refresh token，而是持有 Ed25519 私钥（PKCS#8 base64）与
// agent_runtime_id。每次上游请求用私钥对 "runtime_id:task_id:timestamp" 签名，放进
// Authorization: AgentAssertion <base64url(envelope)> 头。task_id 首次由 task 注册端点
// 获得（可能是加密返回，用 X25519/NaCl box 从私钥 seed 派生密钥解密），之后缓存并落库。
//
// 逻辑参考 sub2api 的 agent identity 实现，适配本项目的 Account/Store 架构。

const CodexAuthModeAgentIdentity = "agentIdentity"

const (
	agentIdentityAuthAPIBaseURL          = "https://auth.openai.com/api/accounts"
	agentIdentityTaskRegistrationTimeout = 30 * time.Second
)

// agentIdentityAuthAPIBase 可在测试中覆盖。
var agentIdentityAuthAPIBase = agentIdentityAuthAPIBaseURL

// agentIdentityTaskLocks 按账号串行化 task 注册，避免并发重复注册。
var agentIdentityTaskLocks sync.Map // map[int64]*sync.Mutex

// IsCodexAgentIdentity 判断账号是否为 Agent Identity 授权账号。
func (a *Account) IsCodexAgentIdentity() bool {
	if a == nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.isCodexAgentIdentityLocked()
}

func (a *Account) isCodexAgentIdentityLocked() bool {
	return strings.EqualFold(strings.TrimSpace(a.CodexAuthMode), CodexAuthModeAgentIdentity) &&
		strings.TrimSpace(a.AgentRuntimeID) != "" &&
		strings.TrimSpace(a.AgentPrivateKey) != ""
}

// parseAgentIdentityPrivateKey 解析 PKCS#8 base64 的 Ed25519 私钥（不记录密钥内容）。
func parseAgentIdentityPrivateKey(raw string) (ed25519.PrivateKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("agent identity 私钥缺失")
	}
	der, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, errors.New("agent identity 私钥不是合法的 base64")
	}
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, errors.New("agent identity 私钥不是合法的 PKCS#8")
	}
	privateKey, ok := key.(ed25519.PrivateKey)
	if !ok || len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("agent identity 私钥不是 Ed25519")
	}
	return privateKey, nil
}

// ValidateCodexAgentIdentityPrivateKey 只校验私钥形态，不返回/记录密钥。
func ValidateCodexAgentIdentityPrivateKey(encoded string) error {
	_, err := parseAgentIdentityPrivateKey(encoded)
	return err
}

// agentIdentityRuntimeKey 是构建签名所需的运行时凭据快照。
type agentIdentityRuntimeKey struct {
	runtimeID  string
	taskID     string
	privateKey ed25519.PrivateKey
}

func (a *Account) agentIdentityRuntimeKeyLocked() (agentIdentityRuntimeKey, error) {
	privateKey, err := parseAgentIdentityPrivateKey(a.AgentPrivateKey)
	if err != nil {
		return agentIdentityRuntimeKey{}, err
	}
	runtimeID := strings.TrimSpace(a.AgentRuntimeID)
	if runtimeID == "" {
		return agentIdentityRuntimeKey{}, errors.New("agent identity runtime id 缺失")
	}
	return agentIdentityRuntimeKey{
		runtimeID:  runtimeID,
		taskID:     strings.TrimSpace(a.AgentTaskID),
		privateKey: privateKey,
	}, nil
}

// BuildCodexAgentAssertion 生成一次请求用的 Authorization 值（AgentAssertion <...>）。
// 纯本地签名，不发网络请求；要求 task_id 已就绪（由 EnsureCodexAgentIdentityTask 保证）。
func (a *Account) BuildCodexAgentAssertion(now time.Time) (string, error) {
	if a == nil {
		return "", errors.New("account is nil")
	}
	a.mu.RLock()
	key, err := a.agentIdentityRuntimeKeyLocked()
	a.mu.RUnlock()
	if err != nil {
		return "", err
	}
	return buildAgentAssertion(key, now)
}

func buildAgentAssertion(key agentIdentityRuntimeKey, now time.Time) (string, error) {
	if key.runtimeID == "" || key.taskID == "" {
		return "", errors.New("agent identity runtime 或 task id 缺失")
	}
	timestamp := now.UTC().Format(time.RFC3339)
	payload := []byte(key.runtimeID + ":" + key.taskID + ":" + timestamp)
	signature, err := key.privateKey.Sign(nil, payload, crypto.Hash(0))
	if err != nil {
		return "", errors.New("agent assertion 签名失败")
	}
	envelope := map[string]string{
		"agent_runtime_id": key.runtimeID,
		"task_id":          key.taskID,
		"timestamp":        timestamp,
		"signature":        base64.StdEncoding.EncodeToString(signature),
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", errors.New("agent assertion 序列化失败")
	}
	return "AgentAssertion " + base64.RawURLEncoding.EncodeToString(encoded), nil
}

func signAgentTaskRegistration(key agentIdentityRuntimeKey, timestamp time.Time) (string, string, error) {
	if key.runtimeID == "" {
		return "", "", errors.New("agent identity runtime id 缺失")
	}
	formatted := timestamp.UTC().Format(time.RFC3339)
	signature, err := key.privateKey.Sign(nil, []byte(key.runtimeID+":"+formatted), crypto.Hash(0))
	if err != nil {
		return "", "", errors.New("agent task 注册签名失败")
	}
	return formatted, base64.StdEncoding.EncodeToString(signature), nil
}

// decryptAgentTaskID 解密注册端点返回的加密 task_id（NaCl anonymous box，
// 收方公私钥由 Ed25519 seed 经 SHA-512 派生为 Curve25519 密钥）。
func decryptAgentTaskID(key agentIdentityRuntimeKey, encoded string) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return "", errors.New("加密 task id 不是合法的 base64")
	}
	seed := key.privateKey.Seed()
	digest := sha512.Sum512(seed)
	var curvePrivate [32]byte
	copy(curvePrivate[:], digest[:32])
	curvePrivate[0] &= 248
	curvePrivate[31] &= 127
	curvePrivate[31] |= 64
	curvePublicBytes, err := curve25519.X25519(curvePrivate[:], curve25519.Basepoint)
	if err != nil {
		return "", errors.New("派生 agent identity 解密公钥失败")
	}
	var curvePublic [32]byte
	copy(curvePublic[:], curvePublicBytes)
	plaintext, ok := box.OpenAnonymous(nil, ciphertext, &curvePublic, &curvePrivate)
	if !ok {
		return "", errors.New("解密加密 task id 失败")
	}
	taskID := strings.TrimSpace(string(plaintext))
	if taskID == "" {
		return "", errors.New("解密出的 task id 为空")
	}
	return taskID, nil
}

type agentIdentityTaskRegistrationResponse struct {
	TaskID               string `json:"task_id"`
	TaskIDCamel          string `json:"taskId"`
	EncryptedTaskID      string `json:"encrypted_task_id"`
	EncryptedTaskIDCamel string `json:"encryptedTaskId"`
}

// registerAgentIdentityTask 向 OpenAI 注册一个新 task 并返回 task_id。
func registerAgentIdentityTask(ctx context.Context, key agentIdentityRuntimeKey, proxyURL string) (string, error) {
	timestamp, signature, err := signAgentTaskRegistration(key, time.Now())
	if err != nil {
		return "", err
	}
	client, err := grokHTTPClient(proxyURL) // 复用带代理配置的通用客户端构造器
	if err != nil {
		return "", err
	}
	client.Timeout = agentIdentityTaskRegistrationTimeout
	body, err := json.Marshal(map[string]string{"timestamp": timestamp, "signature": signature})
	if err != nil {
		return "", errors.New("序列化 agent task 注册请求失败")
	}
	url := strings.TrimRight(strings.TrimSpace(agentIdentityAuthAPIBase), "/") + "/v1/agent/" + key.runtimeID + "/task/register"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return "", errors.New("构建 agent task 注册请求失败")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("agent task 注册请求失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("agent task 注册返回状态 %d", resp.StatusCode)
	}
	var result agentIdentityTaskRegistrationResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&result); err != nil {
		return "", errors.New("agent task 注册响应无效")
	}
	if taskID := strings.TrimSpace(result.TaskID); taskID != "" {
		return taskID, nil
	}
	if taskID := strings.TrimSpace(result.TaskIDCamel); taskID != "" {
		return taskID, nil
	}
	encrypted := strings.TrimSpace(result.EncryptedTaskID)
	if encrypted == "" {
		encrypted = strings.TrimSpace(result.EncryptedTaskIDCamel)
	}
	if encrypted == "" {
		return "", errors.New("agent task 注册响应未包含 task id")
	}
	return decryptAgentTaskID(key, encrypted)
}

// EnsureCodexAgentIdentityTask 保证账号持有可用 task_id：缺失（或需强制刷新）时注册，
// 成功后落库(credentials.task_id)并缓存到运行时账号。按账号串行化，避免并发重复注册。
// forceRefresh=true 时忽略现有 task_id 重新注册（用于 401 task 失效恢复）。
func (s *Store) EnsureCodexAgentIdentityTask(ctx context.Context, account *Account, forceRefresh bool) error {
	if account == nil || !account.IsCodexAgentIdentity() {
		return nil
	}
	if !forceRefresh {
		account.mu.RLock()
		hasTask := strings.TrimSpace(account.AgentTaskID) != ""
		account.mu.RUnlock()
		if hasTask {
			return nil
		}
	}

	lockKey := account.ID()
	candidate := &sync.Mutex{}
	actual, _ := agentIdentityTaskLocks.LoadOrStore(lockKey, candidate)
	mu, _ := actual.(*sync.Mutex)
	if mu == nil {
		mu = candidate
	}
	mu.Lock()
	defer mu.Unlock()

	// 拿到锁后复查：可能已有其它请求完成注册
	account.mu.RLock()
	key, keyErr := account.agentIdentityRuntimeKeyLocked()
	existingTask := strings.TrimSpace(account.AgentTaskID)
	proxyURL := account.ProxyURL
	account.mu.RUnlock()
	if keyErr != nil {
		return keyErr
	}
	if !forceRefresh && existingTask != "" {
		return nil
	}

	taskID, err := registerAgentIdentityTask(ctx, key, proxyURL)
	if err != nil {
		return err
	}

	account.mu.Lock()
	account.AgentTaskID = taskID
	account.mu.Unlock()

	if s != nil && s.db != nil && account.DBID > 0 {
		if err := s.db.UpdateCredentials(ctx, account.DBID, map[string]interface{}{"task_id": taskID}); err != nil {
			return fmt.Errorf("持久化 agent task id 失败: %w", err)
		}
	}
	return nil
}

// IsAgentIdentityTaskInvalidResponse 判断上游 401 是否为 task 失效（可通过重注册恢复）。
func IsAgentIdentityTaskInvalidResponse(statusCode int, body []byte) bool {
	if statusCode != http.StatusUnauthorized {
		return false
	}
	lower := strings.ToLower(string(body))
	compact := strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "").Replace(lower)
	for _, marker := range []string{
		`"code":"invalid_task_id"`,
		`"code":"task_not_found"`,
		`"code":"task_expired"`,
		`"error":"invalid_task_id"`,
	} {
		if strings.Contains(compact, marker) {
			return true
		}
	}
	for _, marker := range []string{
		"invalid task_id", "invalid task id", "task_id is invalid",
		"task id is invalid", "task not found", "task expired",
		"unknown task_id", "unknown task id",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
