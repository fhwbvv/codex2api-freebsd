# 可选 NewAPI 身份与执行适配器

NewAPI 是一个可选的外部适配器，不是 Prompt Filter、本地拦截、外部模型审核或风险画像的运行前提。未使用 NewAPI 的部署可以忽略本文。

Codex2API 只接受与实际请求使用的 Codex2API Key 一对一绑定的 NewAPI 签名身份：

- 请求使用已绑定 Key 时，只使用该绑定的启用状态与绑定密钥验证身份。
- 未绑定 Key 不接受 NewAPI 签名身份。
- 旧版进程级密钥配置不再参与验签。

绑定不控制 Prompt 拦截模式或审核档位。所有请求统一服从 Codex2API GuardPipeline；NewAPI 发送的 `mode`、`profile` 仅可作为兼容元数据接收，不会覆盖全局安全策略。

提示词过滤不依赖 NewAPI 审计接入。违规累计、账号限制和 IP 限制由 NewAPI 的独立开关控制，默认全部关闭。

## 双方配置

在管理页为调用方实际使用的 Codex2API Key 创建绑定，系统会生成并保存至少 32 字节的绑定密钥。绑定密钥不得写入日志或业务响应正文。

NewAPI：

```env
CODEX2API_POLICY_ENABLED=false
CODEX2API_POLICY_IDENTITY_FORWARD_ENABLED=true
CODEX2API_POLICY_BINDINGS=[{"platform_id":"primary-newapi","target":"http://127.0.0.1:18095","codex_key_fingerprint":"<Codex2API Key 的 SHA-256 指纹>","secret":"<该 Key 的绑定密钥，至少 32 个字符>","enabled":true}]
CODEX2API_POLICY_AUDIT_ENABLED=true
CODEX2API_POLICY_STRIKE_ENABLED=false
CODEX2API_POLICY_ACCOUNT_BAN_ENABLED=false
CODEX2API_POLICY_IP_BLOCK_ENABLED=false
CODEX2API_POLICY_BAN_AFTER=2
CODEX2API_POLICY_WINDOW_SECONDS=86400
```

首次部署建议保持 `CODEX2API_POLICY_ENABLED=false`，完成连通性和签名测试后再按需要开启；处罚子开关继续保持关闭。

`CODEX2API_POLICY_BINDINGS` 的每一项必须同时匹配实际出站目标地址和实际使用的 Codex2API Key 指纹。原始 Codex2API Key 不得写入该配置；指纹为原始 Key 的 SHA-256 小写十六进制值。管理页面保存的绑定配置与该环境变量结构相同，环境变量存在时以环境变量为准。

## 请求签名协议

NewAPI 生成唯一请求 ID 和 Unix 秒时间戳，构造签名原文：

```text
v1\n<timestamp>\n<request_id>\n<user_id>\n<client_ip>\n<http_method>\n<request_path>\n<body_sha256>
```

使用当前 Codex2API Key 的绑定密钥计算 HMAC-SHA256，并以小写十六进制写入以下请求头：

```text
X-NewAPI-User-ID
X-NewAPI-Client-IP
X-NewAPI-Request-ID
X-NewAPI-Timestamp
X-NewAPI-Method
X-NewAPI-Path
X-NewAPI-Body-SHA256
X-NewAPI-Signature-Version: 1
X-NewAPI-Signature
```

原始端点、协议和模型提供商放入 `X-NewAPI-Policy-Meta`，并使用独立的 `policy-meta-v1` HMAC 签名。为兼容已有 NewAPI，Codex2API 仍可解析其中的模式与档位字段，但不会将其用作拦截配置。扩展元数据不会改变 V1 身份签名格式。

## 外部适配器实现建议

1. 在 OpenAI 兼容渠道创建上游请求头时签名，不接受客户端提交的同名头。
2. 必须设置目标地址允许列表，只向 Codex2API 主机发送身份头。
3. 收到签名 HTTP 400 策略决策或 WebSocket 策略事件时，必须先校验决策签名和事件签名，再保存用户、IP、请求 ID、模型、接口和受限长度的 Prompt 证据。
4. 审计、违规累计、账号封禁、IP 限制必须是相互独立且默认关闭处罚的配置；不能因为启用审计就自动封禁。
5. 管理员和超级管理员应保留自动处罚保护，避免误报造成管理后台失联。
6. 审计页面必须使用管理员鉴权；Prompt 证据应限制长度并设置数据保留周期。

外部项目的文件布局和持久化实现不属于本仓库契约。兼容实现只需遵守上述签名、目标限制、默认关闭处罚和隐私边界。
