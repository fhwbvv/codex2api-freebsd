# Documentation Index

## API Documentation

完整 API 文档参见 [`docs/API.md`](../docs/API.md)，涵盖所有公共 API 和管理 API。

### OpenAPI/Swagger Specification

The complete OpenAPI 3.0.3 specification is available in [`openapi.yaml`](./openapi.yaml).

### Error Codes Reference

See [`errors.md`](./docs/errors.md) for detailed error code documentation.

### API Versioning

This API uses URL path versioning (e.g., `/v1/`). The current version is v1.0.0.

### Authentication

**公共 API** (`/v1/*`) 使用 API Key 认证:

```
Authorization: Bearer sk-...
```

**管理 API** (`/api/admin/*`) 使用 Admin Secret 认证:

```
X-Admin-Key: your-admin-secret
```

或

```
Authorization: Bearer your-admin-secret
```

### Response Formats

#### Success Response

```json
{
  "id": "chatcmpl-abc123",
  "object": "chat.completion",
  "created": 1234567890,
  "model": "gpt-5.4",
  "choices": [...],
  "usage": {
    "prompt_tokens": 10,
    "completion_tokens": 20,
    "total_tokens": 30
  }
}
```

#### Error Response

```json
{
  "error": {
    "code": "invalid_request",
    "message": "Invalid request",
    "type": "invalid_request_error",
    "details": {
      "field": "model",
      "message": "Model 'invalid-model' is not supported"
    }
  }
}
```

### Rate Limiting

Rate limits are returned in response headers:

- `X-RateLimit-Limit`: Maximum requests allowed
- `X-RateLimit-Remaining`: Remaining requests
- `X-RateLimit-Reset`: Unix timestamp when the limit resets

### Public API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/models` | GET | List available models |
| `/v1/chat/completions` | POST | Create chat completion |
| `/v1/responses` | POST | Create response (Codex native) |
| `/health` | GET | Health check |

### Admin API Endpoints

> 以下为速查表，完整请求/响应格式参见 [`docs/API.md`](../docs/API.md)。

**Token 上传 & 账号管理:**

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/admin/accounts` | GET | 获取账号列表 |
| `/api/admin/accounts` | POST | 添加 RT 账号（支持批量） |
| `/api/admin/accounts/at` | POST | 添加 AT-only 账号（支持批量） |
| `/api/admin/accounts/import` | POST | 文件批量导入（TXT/JSON/AT-TXT） |
| `/api/admin/accounts/:id` | DELETE | 删除账号 |
| `/api/admin/accounts/:id/refresh` | POST | 手动刷新 AT |
| `/api/admin/accounts/:id/test` | GET | 测试账号连接 |
| `/api/admin/accounts/:id/usage` | GET | 查看账号用量 |
| `/api/admin/accounts/batch-test` | POST | 批量测试连接（SSE） |
| `/api/admin/accounts/export` | GET | 导出账号 |
| `/api/admin/accounts/migrate` | POST | 从远程实例迁移账号（SSE） |
| `/api/admin/accounts/event-trend` | GET | 账号增删趋势 |
| `/api/admin/accounts/clean-banned` | POST | 清理 401 账号 |
| `/api/admin/accounts/clean-rate-limited` | POST | 清理 429 账号 |
| `/api/admin/accounts/clean-error` | POST | 清理错误账号 |

**OAuth 授权:**

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/admin/oauth/generate-auth-url` | POST | 生成 OAuth 授权 URL |
| `/api/admin/oauth/exchange-code` | POST | 授权码兑换 Token |

**API Key 管理:**

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/admin/keys` | GET | 获取密钥列表 |
| `/api/admin/keys` | POST | 创建新密钥 |
| `/api/admin/keys/:id` | DELETE | 删除密钥 |

**系统设置 & 运维:**

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/admin/stats` | GET | 仪表盘统计 |
| `/api/admin/health` | GET | 健康检查 |
| `/api/admin/ops/overview` | GET | 运维监控概览 |
| `/api/admin/settings` | GET | 获取系统设置 |
| `/api/admin/settings` | PUT | 更新系统设置 |
| `/api/admin/models` | GET | 获取支持模型列表 |

**用量统计:**

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/admin/usage/stats` | GET | 使用统计 |
| `/api/admin/usage/logs` | GET | 使用日志（分页） |
| `/api/admin/usage/chart-data` | GET | 图表聚合数据 |
| `/api/admin/usage/logs` | DELETE | 清空日志 |

**代理池管理:**

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/admin/proxies` | GET | 获取代理列表 |
| `/api/admin/proxies` | POST | 添加代理（支持批量） |
| `/api/admin/proxies/:id` | DELETE | 删除代理 |
| `/api/admin/proxies/:id` | PATCH | 更新代理 |
| `/api/admin/proxies/batch-delete` | POST | 批量删除 |
| `/api/admin/proxies/test` | POST | 测试代理连通性 |

### Model Support

Supported models include:
- `gpt-5.4`
- `gpt-5.4-mini`
- `gpt-5`
- `gpt-5-codex`
- `gpt-5-codex-mini`
- `gpt-5.1`, `gpt-5.1-codex`, etc.

See the OpenAPI spec for the complete list.
