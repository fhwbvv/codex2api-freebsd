package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/nacl/box"
)

func newTestAgentKey(t *testing.T) (ed25519.PublicKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成 Ed25519 key 失败: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("PKCS8 编码失败: %v", err)
	}
	return pub, base64.StdEncoding.EncodeToString(der)
}

func TestValidateCodexAgentIdentityPrivateKey(t *testing.T) {
	_, pkcs8 := newTestAgentKey(t)
	if err := ValidateCodexAgentIdentityPrivateKey(pkcs8); err != nil {
		t.Fatalf("有效 PKCS8 Ed25519 私钥应通过: %v", err)
	}
	if err := ValidateCodexAgentIdentityPrivateKey("not-base64!!"); err == nil {
		t.Fatal("非法 base64 应报错")
	}
	if err := ValidateCodexAgentIdentityPrivateKey(base64.StdEncoding.EncodeToString([]byte("garbage"))); err == nil {
		t.Fatal("非 PKCS8 应报错")
	}
}

func TestBuildCodexAgentAssertionSignature(t *testing.T) {
	pub, pkcs8 := newTestAgentKey(t)
	acc := &Account{CodexAuthMode: CodexAuthModeAgentIdentity, AgentRuntimeID: "agent-RT123", AgentPrivateKey: pkcs8, AgentTaskID: "TASK-xyz"}
	if !acc.IsCodexAgentIdentity() {
		t.Fatal("应识别为 agent identity 账号")
	}
	assertion, err := acc.BuildCodexAgentAssertion(time.Now())
	if err != nil {
		t.Fatalf("构建 assertion 失败: %v", err)
	}
	if !strings.HasPrefix(assertion, "AgentAssertion ") {
		t.Fatalf("assertion 前缀错误: %q", assertion)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(assertion, "AgentAssertion "))
	if err != nil {
		t.Fatalf("envelope 不是 base64url: %v", err)
	}
	var env map[string]string
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("envelope 不是 JSON: %v", err)
	}
	if env["agent_runtime_id"] != "agent-RT123" || env["task_id"] != "TASK-xyz" {
		t.Fatalf("envelope 字段错误: %+v", env)
	}
	payload := env["agent_runtime_id"] + ":" + env["task_id"] + ":" + env["timestamp"]
	sig, err := base64.StdEncoding.DecodeString(env["signature"])
	if err != nil {
		t.Fatalf("签名不是 base64: %v", err)
	}
	if !ed25519.Verify(pub, []byte(payload), sig) {
		t.Fatal("assertion 签名无法用公钥验证")
	}
}

func TestBuildAgentAssertionRequiresTaskID(t *testing.T) {
	_, pkcs8 := newTestAgentKey(t)
	acc := &Account{CodexAuthMode: CodexAuthModeAgentIdentity, AgentRuntimeID: "agent-RT123", AgentPrivateKey: pkcs8}
	if _, err := acc.BuildCodexAgentAssertion(time.Now()); err == nil {
		t.Fatal("缺少 task_id 时应报错")
	}
}

// sealTaskIDForKey 用私钥派生的 Curve25519 公钥模拟上游 SealAnonymous 加密 task_id。
func sealTaskIDForKey(t *testing.T, pkcs8, taskID string) string {
	t.Helper()
	priv, err := parseAgentIdentityPrivateKey(pkcs8)
	if err != nil {
		t.Fatalf("解析私钥失败: %v", err)
	}
	seed := priv.Seed()
	digest := sha512.Sum512(seed)
	var curvePriv [32]byte
	copy(curvePriv[:], digest[:32])
	curvePriv[0] &= 248
	curvePriv[31] &= 127
	curvePriv[31] |= 64
	curvePubBytes, err := curve25519.X25519(curvePriv[:], curve25519.Basepoint)
	if err != nil {
		t.Fatalf("派生公钥失败: %v", err)
	}
	var curvePub [32]byte
	copy(curvePub[:], curvePubBytes)
	sealed, err := box.SealAnonymous(nil, []byte(taskID), &curvePub, rand.Reader)
	if err != nil {
		t.Fatalf("SealAnonymous 失败: %v", err)
	}
	return base64.StdEncoding.EncodeToString(sealed)
}

func TestDecryptAgentTaskIDRoundTrip(t *testing.T) {
	_, pkcs8 := newTestAgentKey(t)
	priv, _ := parseAgentIdentityPrivateKey(pkcs8)
	key := agentIdentityRuntimeKey{runtimeID: "agent-RT123", privateKey: priv}
	encrypted := sealTaskIDForKey(t, pkcs8, "DECRYPTED-TASK-42")
	got, err := decryptAgentTaskID(key, encrypted)
	if err != nil {
		t.Fatalf("解密失败: %v", err)
	}
	if got != "DECRYPTED-TASK-42" {
		t.Fatalf("解密结果错误: %q", got)
	}
}

func TestRegisterAgentIdentityTaskDecryptsEncryptedResponse(t *testing.T) {
	_, pkcs8 := newTestAgentKey(t)
	encrypted := sealTaskIDForKey(t, pkcs8, "TASK-FROM-SERVER")

	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		if !strings.Contains(r.URL.Path, "/v1/agent/agent-RT123/task/register") {
			t.Errorf("注册路径错误: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"encrypted_task_id": encrypted})
	}))
	defer srv.Close()

	prev := agentIdentityAuthAPIBase
	agentIdentityAuthAPIBase = srv.URL
	defer func() { agentIdentityAuthAPIBase = prev }()

	priv, _ := parseAgentIdentityPrivateKey(pkcs8)
	key := agentIdentityRuntimeKey{runtimeID: "agent-RT123", privateKey: priv}
	task, err := registerAgentIdentityTask(context.Background(), key, "")
	if err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	if task != "TASK-FROM-SERVER" {
		t.Fatalf("task id 错误: %q", task)
	}
	// 注册请求体应带 timestamp + signature
	if gotBody["timestamp"] == "" || gotBody["signature"] == "" {
		t.Fatalf("注册请求缺少 timestamp/signature: %+v", gotBody)
	}
}

func TestIsAgentIdentityTaskInvalidResponse(t *testing.T) {
	if !IsAgentIdentityTaskInvalidResponse(401, []byte(`{"error":{"code":"invalid_task_id"}}`)) {
		t.Fatal("invalid_task_id 应判为 task 失效")
	}
	if !IsAgentIdentityTaskInvalidResponse(401, []byte(`{"message":"task not found"}`)) {
		t.Fatal("task not found 应判为 task 失效")
	}
	if IsAgentIdentityTaskInvalidResponse(500, []byte(`{"error":{"code":"invalid_task_id"}}`)) {
		t.Fatal("非 401 不应判为 task 失效")
	}
	if IsAgentIdentityTaskInvalidResponse(401, []byte(`{"error":"rate_limited"}`)) {
		t.Fatal("其它 401 不应判为 task 失效")
	}
}
