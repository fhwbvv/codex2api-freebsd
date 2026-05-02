# Codex2API

Codex2API 是一个基于 **Go + Gin + React/Vite** 的 Codex 反向代理与管理后台项目，支持：

- 标准模式：**PostgreSQL + Redis**
- 轻量模式：**SQLite + 内存缓存**

它对外提供兼容 OpenAI 风格的接口，并在内部维护一套基于 **Refresh Token 账号池** 的调度、刷新、测试、限流恢复、用量观测与后台管理能力。

---

## 目录

- [快速部署](#快速部署)
- [完整文档](#完整文档)
- [环境配置](#环境配置)
- [对外接口](#对外接口)
  - [Token 上传与账号管理](#token-上传与账号管理)
- [管理后台](#管理后台)
- [核心能力](#核心能力)
- [目录结构](#目录结构)
- [常见注意事项](#常见注意事项)
- [免责声明](#免责声明与开源协议)
- [Star History](#star-history)

---

## 快速部署

> 详细部署指南请参考：[DEPLOYMENT.md](docs/DEPLOYMENT.md)

### 部署模式总览

| 模式 | 文件 | 适用场景 |
| --- | --- | --- |
| Docker 镜像部署 | `docker-compose.yml` | **推荐**，服务器 / 测试环境，直接拉取预构建镜像 |
| 本地源码容器构建 | `docker-compose.local.yml` | 本地改代码后做完整容器验证 |
| SQLite 轻量部署 | `docker-compose.sqlite.yml` | 单机轻量部署，不依赖 PostgreSQL / Redis |
| SQLite 本地源码构建 | `docker-compose.sqlite.local.yml` | 本地改代码后验证 SQLite 轻量模式 |
| 本地开发 | `go run .` + `npm run dev` | 前后端联调与调试 |

### 部署命令速查

标准镜像版：

```bash
git clone https://github.com/james-6-23/codex2api.git
cd codex2api
cp .env.example .env
docker compose pull
docker compose up -d
docker compose logs -f codex2api
```

标准本地构建版：

```bash
cp .env.example .env
docker compose -f docker-compose.local.yml up -d --build
docker compose -f docker-compose.local.yml logs -f codex2api
```

SQLite 镜像版：

```bash
cp .env.sqlite.example .env
docker compose -f docker-compose.sqlite.yml pull
docker compose -f docker-compose.sqlite.yml up -d
docker compose -f docker-compose.sqlite.yml logs -f codex2api
```

SQLite 本地构建版：

```bash
cp .env.sqlite.example .env
docker compose -f docker-compose.sqlite.local.yml up -d --build
docker compose -f docker-compose.sqlite.local.yml logs -f codex2api
```

补充说明：

- 标准版和 SQLite 版都读取 `.env`
- 切换部署模式前，需要先用对应的示例文件覆盖当前 `.env`
- 标准镜像版项目名固定为 `codex2api`，数据卷固定为 `codex2api_pgdata`、`codex2api_redisdata`
- 标准本地构建版项目名固定为 `codex2api-local`，数据卷固定为 `codex2api-local_pgdata`、`codex2api-local_redisdata`
- SQLite 镜像版项目名固定为 `codex2api-sqlite`，数据卷固定为 `codex2api-sqlite_sqlite-data`
- SQLite 本地构建版项目名固定为 `codex2api-sqlite-local`，数据卷固定为 `codex2api-sqlite-local_sqlite-data-local`
- 标准版容器名：`codex2api`
- SQLite 镜像版容器名：`codex2api-sqlite`
- SQLite 本地构建版容器名：`codex2api-sqlite-local`
- SQLite 轻量版只启动 `codex2api` 单容器，数据保存在 `/data/codex2api.db`
- 生图工作台图库默认保存在 `/data/images`，标准版和 SQLite 版 Docker 配置都会持久化 `/data`
- `docker compose down` 默认不会删除命名卷；只有 `docker compose down -v`、`docker volume rm` 或 `docker volume prune` 才会删除持久化数据
- 不同部署模式的数据卷彼此隔离；切换 compose 文件后看到空数据，通常是切到了另一组卷，而不是原卷被自动删除

启动后访问：

- 管理台：`http://localhost:8080/admin/`
- 健康检查：`http://localhost:8080/health`

> 更多部署详情请参考：[DEPLOYMENT.md](docs/DEPLOYMENT.md)

---

## 完整文档

| 文档 | 说明 | 路径 |
|------|------|------|
| [API 文档](docs/API.md) | 所有 API 端点、请求/响应示例、错误码说明 | `docs/API.md` |
| [部署文档](docs/DEPLOYMENT.md) | 各种部署模式、升级指南、备份恢复 | `docs/DEPLOYMENT.md` |
| [配置文档](docs/CONFIGURATION.md) | 环境变量、系统设置、配置优先级 | `docs/CONFIGURATION.md` |
| [架构文档](docs/ARCHITECTURE.md) | 系统架构、调度算法、存储设计 | `docs/ARCHITECTURE.md` |
| [故障排查](docs/TROUBLESHOOTING.md) | 常见问题排查、诊断脚本、解决方案 | `docs/TROUBLESHOOTING.md` |
| [贡献指南](docs/CONTRIBUTING.md) | 开发规范、PR 流程、代码标准 | `docs/CONTRIBUTING.md` |

---

## 环境配置

```bash
git pull && docker compose pull && docker compose up -d && docker compose logs -f codex2api
```

> **⚠️ 重要：升级前请先备份数据库！**
>
> ```bash
> docker exec codex2api-postgres pg_dump -U codex2api codex2api > backup_$(date +%Y%m%d_%H%M%S).sql
> ```
>
> 如果升级后数据异常，可通过以下命令恢复：
>
> ```bash
> docker exec -i codex2api-postgres psql -U codex2api codex2api < backup_xxx.sql
> ```

如非必要，不建议在升级时执行 `docker compose down`；标准升级直接 `pull + up -d` 即可复用现有容器和命名卷。

### 本地开发模式

**后端：**

```bash
cp .env.example .env
cd frontend && npm ci && npm run build && cd ..
go run .
```

> 首次启动需要先构建前端，因为 Go 使用 `go:embed` 嵌入 `frontend/dist` 。

**前端开发服务器（联调）：**

```bash
cd frontend && npm ci && npm run dev
```

Vite 会自动代理 `/api` 和 `/health` 到后端，开发时访问 `http://localhost:5173/admin/`。

---

## 环境配置

### `.env` 环境变量

> 完整配置说明请参考：[CONFIGURATION.md](docs/CONFIGURATION.md)

| 变量 | 说明 |
| --- | --- |
| `CODEX_PORT` | HTTP 端口，默认 `8080` |
| `ADMIN_SECRET` | 管理后台登录密钥；设置后首次访问 `/admin` 会弹出密码输入框 |
| `DATABASE_DRIVER` | 数据库驱动，支持 `postgres` / `sqlite` |
| `DATABASE_PATH` | SQLite 数据文件路径，`DATABASE_DRIVER=sqlite` 时生效 |
| `DATABASE_HOST` | PostgreSQL 主机，`DATABASE_DRIVER=postgres` 时生效 |
| `DATABASE_PORT` | PostgreSQL 端口，默认 `5432` |
| `DATABASE_USER` | PostgreSQL 用户 |
| `DATABASE_PASSWORD` | PostgreSQL 密码 |
| `DATABASE_NAME` | PostgreSQL 数据库名 |
| `DATABASE_SSLMODE` | PostgreSQL SSL 模式，默认 `disable` |
| `CACHE_DRIVER` | 缓存驱动，支持 `redis` / `memory` |
| `REDIS_ADDR` | Redis 地址，例如 `redis:6379`、`redis://default:pass@host:6379/0`、`rediss://default:pass@host:6379/0`，`CACHE_DRIVER=redis` 时生效 |
| `REDIS_USERNAME` | Redis ACL 用户名，可选；URL 中带用户名时可不填 |
| `REDIS_PASSWORD` | Redis 密码 |
| `REDIS_DB` | Redis DB 库号 |
| `REDIS_TLS` | 是否为 `host:port` 形式的 Redis 启用 TLS；使用 `rediss://` 时会自动启用 |
| `REDIS_INSECURE_SKIP_VERIFY` | 跳过 Redis TLS 证书校验，默认 `false`，仅用于自签证书或排障 |
| `TZ` | 时区，例如 `Asia/Shanghai` |

> Aiven、Upstash 等云 Redis 通常要求 TLS。推荐直接将 `REDIS_ADDR` 配置为平台提供的 `rediss://...` URL；如果只填写 `host:port`，请同时设置 `REDIS_TLS=true`。

标准版 `.env.example` 已显式声明 `DATABASE_DRIVER=postgres` 与 `CACHE_DRIVER=redis`；SQLite 轻量版请改用 `.env.sqlite.example`。

### 业务运行配置

以下参数**保存在数据库 `SystemSettings` 中**，通过管理台设置页面修改：

`MaxConcurrency`、`GlobalRPM`、`TestModel`、`TestConcurrency`、`ProxyURL`、`PgMaxConns`、`RedisPoolSize`、`AdminSecret`、自动清理开关等。

首次启动时程序会自动写入默认设置。

### API Key 与管理密钥

- **对外 API Key**：以数据库中的 API Keys 为准。如果没有配置任何 Key，则 `/v1/*` 跳过鉴权。
- **管理后台 Admin Secret**：
  - 如果 `.env` 中设置了 `ADMIN_SECRET`，则优先使用环境变量。
  - 如果未设置 `ADMIN_SECRET`，则回退到数据库中的 `AdminSecret`。
  - 鉴权生效时，首次访问 `/admin` 会弹出密码输入框；前端登录成功后通过 `X-Admin-Key` 请求头访问 `/api/admin/*`。

---

## 对外接口

| 接口 | 说明 |
| --- | --- |
| `POST /v1/chat/completions` | Chat Completions 风格入口 |
| `POST /v1/responses` | Responses 风格入口 |
| `POST /v1/images/generations` | OpenAI Images 生成入口 |
| `POST /v1/images/edits` | OpenAI Images 编辑入口 |
| `GET /v1/models` | 返回可用模型列表 |
| `GET /health` | 健康检查 |

> 完整请求/响应格式、错误码参见 [API 文档](docs/API.md)。

### Token 上传与账号管理

以下接口需要 `X-Admin-Key` 认证头。

#### 添加 Refresh Token 账号

```bash
# 单个添加
curl -X POST http://localhost:8080/api/admin/accounts \
  -H "X-Admin-Key: your-admin-secret" \
  -H "Content-Type: application/json" \
  -d '{"name": "my-account", "refresh_token": "rt_xxxxxxxxxxxx"}'

# 批量添加（换行分隔，单次最多 100 个）
curl -X POST http://localhost:8080/api/admin/accounts \
  -H "X-Admin-Key: your-admin-secret" \
  -H "Content-Type: application/json" \
  -d '{"name": "batch", "refresh_token": "rt_xxx1\nrt_xxx2\nrt_xxx3"}'
```

#### 添加 Access Token 账号（AT-only）

```bash
# 单个添加
curl -X POST http://localhost:8080/api/admin/accounts/at \
  -H "X-Admin-Key: your-admin-secret" \
  -H "Content-Type: application/json" \
  -d '{"name": "my-at", "access_token": "eyJhbGciOiJSUzI1NiIs..."}'

# 批量添加（换行分隔）
curl -X POST http://localhost:8080/api/admin/accounts/at \
  -H "X-Admin-Key: your-admin-secret" \
  -H "Content-Type: application/json" \
  -d '{"access_token": "eyJtoken1...\neyJtoken2...\neyJtoken3..."}'
```

#### 文件批量导入

```bash
# 导入 Refresh Token（TXT，每行一个）
curl -X POST http://localhost:8080/api/admin/accounts/import \
  -H "X-Admin-Key: your-admin-secret" \
  -F "file=@tokens.txt" \
  -F "format=txt"

# 导入 Refresh Token（JSON 格式）
curl -X POST http://localhost:8080/api/admin/accounts/import \
  -H "X-Admin-Key: your-admin-secret" \
  -F "file=@credentials.json" \
  -F "format=json"

# 导入 Access Token（AT-TXT，每行一个）
curl -X POST http://localhost:8080/api/admin/accounts/import \
  -H "X-Admin-Key: your-admin-secret" \
  -F "file=@access_tokens.txt" \
  -F "format=at_txt"
```

> 所有导入接口自动去重，已存在的 Token 不会重复写入。更多管理接口（导出、迁移、OAuth 授权等）参见 [API 文档](docs/API.md)。

---

## 管理后台

浏览器访问 `/admin/`，提供以下页面：

| 页面 | 路径 | 功能 |
| --- | --- | --- |
| Dashboard | `/admin/` | 总览指标、请求趋势、延迟趋势、Token 分布、模型排行 |
| 账号管理 | `/admin/accounts` | 导入、测试、批量处理、调度信息查看 |
| 使用统计 | `/admin/usage` | 请求日志、统计卡片、图表、日志清空 |
| 运维概览 | `/admin/ops` | 运行态监控与系统概览 |
| 调度看板 | `/admin/ops/scheduler` | 调度健康度、惩罚项和评分拆解 |
| 系统设置 | `/admin/settings` | 业务运行参数与后台密钥配置 |

---

## 核心能力

### 项目定位

这个项目不是单纯的接口转发，而是一套面向长期运行的 Codex 网关与管理后台：

- 对外提供统一的 OpenAI 风格入口，屏蔽上游多账号差异
- 对内维护基于 `Refresh Token` 的账号池、`Access Token` 生命周期和运行时调度
- 通过 PostgreSQL + Redis 或 SQLite + 内存缓存实现配置持久化与运行态协调
- 通过 `/admin` 管理台提供全面的运维观测能力

### 架构概览

**对外请求链路：** 客户端请求 → Gin RPM 限流 → `proxy.Handler` API Key 校验 → `auth.Store` 调度选号 → 上游请求 → 响应回传 + 用量写入

**管理后台链路：** 浏览器 → `/admin/` 嵌入式前端 → `/api/admin/*` 管理接口 → 数据库 / 账号池 / 缓存层

### 调度系统

调度核心位于 `auth.Store`，将账号可用性、健康度、动态并发、历史错误和近期用量综合纳入选择。

**运行时状态模型：**

- `Status`：`ready` / `cooldown` / `error`
- `HealthTier`：`healthy` / `warm` / `risky` / `banned`
- `SchedulerScore`：以 100 为基线的实时调度分
- `DynamicConcurrencyLimit`：按健康层级动态收缩的并发上限

**账号选择策略：**

1. 过滤不可用账号（error / banned / 冷却中 / 无 AccessToken）
2. 重算健康层级、调度分和动态并发
3. 排除已达并发上限的账号
4. 按 `healthy > warm > risky > banned` 排序，同层级按调度分和并发数择优
5. 15% 概率随机打散，降低热点与饥饿

**动态并发规则：**

| 层级 | 并发上限 |
| --- | --- |
| `healthy` | 系统 `MaxConcurrency` |
| `warm` | 基础并发 ÷ 2（最少 1） |
| `risky` | 固定 1 |
| `banned` | 固定 0，不参与调度 |

**调度分惩罚/奖励：**

| 信号 | 影响 |
| --- | --- |
| `unauthorized` | `-50`，24h 线性衰减 |
| `rate_limited` | `-22`，1h 线性衰减 |
| `timeout` | `-18`，15min 线性衰减 |
| `server error` | `-12`，15min 线性衰减 |
| 连续失败 | 每次 `-6`，最多 `-24` |
| 连续成功 | 每次 `+2`，最多 `+12` |
| 近期成功率过低 | `<75%` 扣 8，`<50%` 扣 15 |
| Free 7d 用量 | `≥70%` 扣 8 → `≥100%` 扣 40 |
| 延迟 EWMA | `≥5s` 扣 4 → `≥20s` 扣 15 |

**冷却与恢复机制：**

- **429**：优先解析上游 `resets_at`，否则按套餐类型推断冷却时间
- **401**：直接进入 `banned`，6h 冷却，24h 内再触发升至 24h
- 冷却状态持久化到 PostgreSQL，重启后自动恢复
- 后台会对 `banned` 账号做周期性低频恢复探测

**调度可观测性：**

- `GET /api/admin/accounts` — 健康层级、调度分、惩罚拆解
- `GET /api/admin/ops/overview` — 系统运行态与连接池概览
- `/admin/ops/scheduler` — 前端调度看板

---

## 目录结构

```text
codex2api/
├─ main.go                      # 程序入口
├─ Dockerfile                   # 多阶段镜像构建
├─ docker-compose.yml           # 镜像部署模板
├─ docker-compose.local.yml     # 本地源码构建模板
├─ .env.example                 # 环境变量示例
├─ admin/                       # 管理后台 API
├─ auth/                        # 账号池、调度与 token 管理
├─ cache/                       # Redis 缓存封装
├─ config/                      # 环境变量加载
├─ database/                    # PostgreSQL 访问层
├─ proxy/                       # 对外代理、转发与限流
└─ frontend/                    # React + Vite 管理后台
   ├─ src/pages/                # Dashboard / Accounts / Usage / Ops / Settings
   ├─ src/components/           # UI 组件
   ├─ src/locales/              # 国际化语言文件 (zh/en)
   └─ vite.config.js            # Vite 配置
```

---


## 常见注意事项

- `docker-compose.yml` 拉取 GHCR 镜像用于部署；`docker-compose.local.yml` 用 `build: .` 做本地构建
- 前端基路径固定为 `/admin/`，本地开发和生产部署一致
- 本地手动构建 Go 二进制前需先执行 `frontend/` 的 `npm run build`
- `.env` 只负责端口、数据库、Redis 等物理层配置；业务参数在管理台数据库里维护
- API Key 以数据库为准，在管理台中配置

---

## 免责声明与开源协议

- 本项目仅供学习、研究与技术交流使用。
- 本项目采用 `MIT License` 开源协议。
- 项目不对任何直接或间接使用后果提供担保；生产环境使用风险由使用者自行承担。

---

## Star History

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=james-6-23/codex2api&type=Date&theme=dark" />
  <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/svg?repos=james-6-23/codex2api&type=Date" />
  <img alt="Star History Chart" src="https://api.star-history.com/svg?repos=james-6-23/codex2api&type=Date" />
</picture>

---

## 友情链接

- [LINUX DO](https://linux.do/)
