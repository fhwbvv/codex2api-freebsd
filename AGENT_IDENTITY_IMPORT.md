# Codex Agent Identity 导入接口文档

向 codex2api 网关导入 Codex **Agent Identity** 账号。这类账号**不保存** OAuth
access/refresh token,只保存 Ed25519 私钥 + `agent_runtime_id`,由网关对每一次上游请求
动态签名(`Authorization: AgentAssertion <签名信封>`)。

---

## 1. 通用约定

| 项 | 值 |
|---|---|
| 网关地址 | `{BASE_URL}`(如 `http://127.0.0.1:2004`) |
| 管理鉴权头 | `X-Admin-Key: {ADMIN_KEY}` |
| 管理接口前缀 | `/api/admin` |
| Content-Type | `application/json`(入口 A/B);`multipart/form-data`(入口 C) |

- 去重按 `agent_runtime_id`:已存在的账号会被跳过,不会重复建号。
- 导入后网关会自动跑一次签名 `/responses` 探针,填充用量进度条。
- **不要对同一个 `agent_runtime_id` 短时间反复导入/删除**——上游对 task 注册有限流,
  会返回 403(临时),影响该账号首次用量采样。

---

## 2. auth.json 格式

每个账号一份 JSON:

```json
{
  "auth_mode": "agent_identity",
  "agent_identity": {
    "agent_runtime_id": "agent-XXXXXXXXXXXXXXXXXXXXXX",
    "agent_private_key": "MC4CAQAwBQYDK2VwBCIEII...",
    "account_id": "9324fdb2-2a0c-4f39-b292-839afdb3b6ec",
    "chatgpt_user_id": "user-XXXXXXXXXXXXXXXXXXXX",
    "email": "someone@example.com",
    "plan_type": "free",
    "chatgpt_account_is_fedramp": false
  }
}
```

| 字段 | 必填 | 说明 |
|---|:---:|---|
| `agent_runtime_id` | ✅ | 上游 agent 运行时 ID(`agent-` 前缀) |
| `agent_private_key` | ✅ | **PKCS#8 base64 的 Ed25519 私钥** |
| `account_id` | ✅ | ChatGPT 账号(工作区)ID |
| `chatgpt_user_id` | ✅ | ChatGPT 用户 ID(`user-` 前缀) |
| `email` | ⭕ | 账号邮箱,用作显示名 |
| `plan_type` | ⭕ | 套餐(free/plus/pro/team/k12…),缺省 `free` |
| `chatgpt_account_is_fedramp` | ⭕ | 布尔,默认 `false` |

- `auth_mode` 同时兼容 `agent_identity`(下划线)与 `agentIdentity`(驼峰)。
- 只要带 `agent_identity` 对象即可被识别,`auth_mode` 可省略。

---

## 3. 三个导入入口

### 入口 A — 单个导入

```
POST /api/admin/accounts/codex/agent-identity
```

请求体:

```json
{
  "auth_json": "<一份 auth.json 的原始 JSON 字符串>",
  "name": "可选自定义名",
  "proxy_url": "可选,如 http://user:pass@host:port"
}
```

> 注意:`auth_json` 是把整份 auth.json **作为字符串**放进去(需转义)。

curl:

```bash
curl -s -X POST "{BASE_URL}/api/admin/accounts/codex/agent-identity" \
  -H "X-Admin-Key: {ADMIN_KEY}" -H "Content-Type: application/json" \
  -d "$(jq -Rs '{auth_json: .}' path/to/one.json)"
```

响应:

```json
{ "message": "成功导入 Agent Identity 账号", "id": 123, "email": "someone@example.com" }
```

已存在:`409` `{"error":"该 Agent Identity 账号已存在"}`
字段缺失/私钥无效:`400`,`error` 说明原因。

---

### 入口 B — 批量文件导入(推荐)

```
POST /api/admin/accounts/codex/agent-identity/import
```

请求体:`files` 数组,每项是**一份 auth.json 的原始 JSON 字符串**;单批上限 **200**。

```json
{
  "files": ["<auth.json 内容1>", "<auth.json 内容2>"],
  "proxy_url": "可选"
}
```

curl(把一个目录下所有 `*.json` 打成 `files` 数组):

```bash
jq -Rs . *.json | jq -s '{files: .}' | \
curl -s -X POST "{BASE_URL}/api/admin/accounts/codex/agent-identity/import" \
  -H "X-Admin-Key: {ADMIN_KEY}" -H "Content-Type: application/json" -d @-
```

响应:

```json
{
  "total": 2,
  "imported": 1,
  "failed": 1,
  "items": [
    { "email": "a@x.com", "id": 123, "ok": true },
    { "email": "b@x.com", "ok": false, "error": "账号已存在，已跳过" }
  ]
}
```

---

### 入口 C — 通用导入(自动识别,multipart)

```
POST /api/admin/accounts/import
```

把 auth.json 当普通 JSON 文件走通用导入,网关会自动识别 `agent_identity` 并按
Agent Identity 建号(通用导入同样兼容 CLIProxyAPI / Sub2Api JSON 与文件夹扫描)。

```bash
curl -s -X POST "{BASE_URL}/api/admin/accounts/import" \
  -H "X-Admin-Key: {ADMIN_KEY}" \
  -F "format=json" \
  -F "file=@one.json" \
  -F "file=@two.json"
```

表单字段:`format=json`(必填)、`file`(可多个)、`proxy_url`(可选)、
`allow_duplicate=true`(可选,跳过去重强制新建)。
响应为 SSE 进度流或 JSON 汇总,含新增/跳过/失败计数。

---

## 4. Python 批量示例

```python
import json, glob, requests

BASE_URL = "http://127.0.0.1:2004"
ADMIN_KEY = "your-admin-key"

files = [open(p, encoding="utf-8").read() for p in glob.glob("auth_jsons/*.json")]

# 单批上限 200,超过则分批
for i in range(0, len(files), 200):
    batch = files[i:i + 200]
    r = requests.post(
        f"{BASE_URL}/api/admin/accounts/codex/agent-identity/import",
        headers={"X-Admin-Key": ADMIN_KEY, "Content-Type": "application/json"},
        json={"files": batch},
        timeout=120,
    )
    out = r.json()
    print(f"批次 {i//200+1}: 新增 {out['imported']} / 跳过或失败 {out['failed']} / 共 {out['total']}")
    for it in out["items"]:
        if not it["ok"]:
            print("  失败:", it.get("email") or "?", "—", it.get("error"))
```

---

## 5. 注意事项

- 私钥是敏感凭据,**不要打印到日志**。
- 导入的账号在账号列表带蓝色「Agent Identity」徽章;这类账号走 HTTP(不走长连接 WS),
  且不做 ChatGPT wham 探针(无 AT),用量靠 `/responses` 响应头填充。
- 大批量导入时,并发 task 注册可能撞上游 403 限流,个别账号的用量进度条会延后到
  下次探针 / 真实流量时再填,不影响账号本身可用。
