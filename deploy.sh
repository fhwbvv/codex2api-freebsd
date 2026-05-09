#!/usr/bin/env bash
# ============================================================
#  codex2api 交互式部署脚本
#  用法: bash deploy.sh
# ============================================================

set -euo pipefail

# ---------- 颜色 ----------
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

# ---------- 工具函数 ----------
info()    { printf "${CYAN}▸ %s${NC}\n" "$*"; }
success() { printf "${GREEN}✔ %s${NC}\n" "$*"; }
warn()    { printf "${YELLOW}⚠ %s${NC}\n" "$*"; }
error()   { printf "${RED}✘ %s${NC}\n" "$*"; exit 1; }

banner() {
  printf "\n${BOLD}${CYAN}"
  cat << 'EOF'
   ___          _           ____    _    ____ ___
  / __\ ___  __| | _____  _|___ \  / \  |  _ \_ _|
 / /   / _ \/ _` |/ _ \ \/ / __) |/ _ \ | |_) | |
/ /___| (_) | (_| |  __/>  < / __// ___ \|  __/| |
\____/ \___/ \__,_|\___/_/\_\_____/_/   \_\_|  |___|

EOF
  printf "${NC}"
  echo "  交互式部署脚本 v1.0"
  echo "  ────────────────────────────────────────"
  echo ""
}

# 输入源：兼容 `bash <(curl ...)` / 管道执行场景，强制从终端读取
_INPUT_FD="/dev/tty"
if [[ ! -r "$_INPUT_FD" ]]; then
  _INPUT_FD="/dev/stdin"
fi

# 读取用户输入，支持默认值
ask() {
  local prompt="$1" default="$2" varname="$3"
  if [[ -n "$default" ]]; then
    printf "${BOLD}%s${NC} [${GREEN}%s${NC}]: " "$prompt" "$default"
  else
    printf "${BOLD}%s${NC}: " "$prompt"
  fi
  read -r input < "$_INPUT_FD"
  eval "$varname=\"${input:-$default}\""
}

# 读取密码（不回显）
ask_secret() {
  local prompt="$1" default="$2" varname="$3"
  if [[ -n "$default" ]]; then
    printf "${BOLD}%s${NC} [${GREEN}%s${NC}]: " "$prompt" "(已设置)"
  else
    printf "${BOLD}%s${NC} (留空则自动生成): " "$prompt"
  fi
  read -rs input < "$_INPUT_FD"
  echo ""
  eval "$varname=\"${input:-$default}\""
}

# 生成随机密钥
gen_secret() {
  if command -v openssl &>/dev/null; then
    openssl rand -hex 16
  elif [[ -r /dev/urandom ]]; then
    head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' | head -c 32
  else
    date +%s%N | sha256sum | head -c 32
  fi
}

# ---------- 自举：确保位于 codex2api 仓库目录 ----------
# 触发条件:
#   1) 通过 `bash <(curl ...)` 远程拉起 (BASH_SOURCE 不是真实文件)
#   2) 或当前目录缺少必要的 compose / deploy.sh 文件
# 行为:
#   - 若已在仓库目录: 直接返回
#   - 否则: clone 仓库到 ./codex2api，切入并 exec ./deploy.sh
REPO_URL="${CODEX2API_REPO_URL:-https://github.com/james-6-23/codex2api.git}"
REPO_BRANCH="${CODEX2API_REPO_BRANCH:-main}"
REPO_DIR_NAME="${CODEX2API_DIR_NAME:-codex2api}"

is_codex2api_repo() {
  [[ -f "docker-compose.yml" ]] && [[ -f "deploy.sh" ]] \
    && grep -q '^name: codex2api' docker-compose.yml 2>/dev/null
}

bootstrap_repo() {
  # 已经在仓库目录里：什么都不做
  if is_codex2api_repo; then
    success "检测到当前目录为 codex2api 仓库"
    return 0
  fi

  warn "当前目录不是 codex2api 仓库，进入自动拉取流程"

  if ! command -v git >/dev/null 2>&1; then
    error "未找到 git，请先安装 git 后重试"
  fi

  # 如果同名目录已存在且是仓库，直接复用
  if [[ -d "$REPO_DIR_NAME/.git" ]]; then
    info "发现已有目录 $REPO_DIR_NAME，尝试更新到最新代码..."
    (cd "$REPO_DIR_NAME" && git fetch --depth=1 origin "$REPO_BRANCH" && git reset --hard "origin/$REPO_BRANCH") \
      || warn "拉取更新失败，将沿用已有代码继续部署"
  elif [[ -e "$REPO_DIR_NAME" ]]; then
    error "目录 $REPO_DIR_NAME 已存在但不是 git 仓库，请手动处理后重试"
  else
    info "克隆仓库: $REPO_URL ($REPO_BRANCH) → ./$REPO_DIR_NAME"
    git clone --depth=1 --branch "$REPO_BRANCH" "$REPO_URL" "$REPO_DIR_NAME" \
      || error "git clone 失败，请检查网络或仓库地址"
  fi

  cd "$REPO_DIR_NAME" || error "无法进入 $REPO_DIR_NAME 目录"

  if ! is_codex2api_repo; then
    error "克隆后仍未识别为 codex2api 仓库，请手动检查"
  fi

  success "已切换到 $(pwd)"
  info "重新运行 ./deploy.sh 完成部署..."
  echo ""
  # exec 掉本进程，避免远程脚本/旧上下文继续运行
  exec bash ./deploy.sh "$@"
}

# ---------- 前置检查 ----------
preflight() {
  info "检查运行环境..."

  if ! command -v docker &>/dev/null; then
    error "未找到 docker，请先安装 Docker"
  fi

  if docker compose version &>/dev/null; then
    COMPOSE_CMD="docker compose"
  elif command -v docker-compose &>/dev/null; then
    COMPOSE_CMD="docker-compose"
  else
    error "未找到 docker compose，请安装 Docker Compose v2+"
  fi

  success "Docker 环境就绪 ($COMPOSE_CMD)"
}

# ---------- 第一步：端口 ----------
step_port() {
  echo ""
  printf "${BOLD}${CYAN}━━━ 1/6 服务端口 ━━━${NC}\n"
  ask "服务监听端口" "8080" PORT

  if ! [[ "$PORT" =~ ^[0-9]+$ ]] || (( PORT < 1 || PORT > 65535 )); then
    error "无效端口号: $PORT"
  fi
  success "端口: $PORT"
}

# ---------- 第二步：监听范围 ----------
step_bind() {
  echo ""
  printf "${BOLD}${CYAN}━━━ 2/6 监听范围 ━━━${NC}\n"
  echo ""
  echo "  1) 仅本机访问  — 绑定 127.0.0.1，外部无法访问 (内网/反向代理后端推荐)"
  echo "  2) 全部网络    — 绑定 0.0.0.0，可通过内网/公网 IP 访问 (默认)"
  echo ""
  ask "请选择 (1 或 2)" "2" BIND_CHOICE

  case "$BIND_CHOICE" in
    1|local|loopback|127*)
      BIND_HOST="127.0.0.1"
      BIND_MODE="loopback"
      success "监听范围: 仅本机 (127.0.0.1)"
      ;;
    2|all|public|0*)
      BIND_HOST="0.0.0.0"
      BIND_MODE="all"
      success "监听范围: 全部网络 (0.0.0.0)"
      ;;
    *)
      error "无效选择: $BIND_CHOICE"
      ;;
  esac
}

# ---------- 第三步：数据库模式 ----------
step_database() {
  echo ""
  printf "${BOLD}${CYAN}━━━ 3/6 数据库模式 ━━━${NC}\n"
  echo ""
  echo "  1) SQLite   — 轻量单文件，适合个人 / 测试"
  echo "  2) PG+Redis — PostgreSQL + Redis，适合生产 / 多并发"
  echo ""
  ask "请选择 (1 或 2)" "1" DB_CHOICE

  case "$DB_CHOICE" in
    1|sqlite|SQLite)
      DB_MODE="sqlite"
      success "数据库模式: SQLite (轻量)"
      step_sqlite_config
      ;;
    2|pg|postgres|PG)
      DB_MODE="postgres"
      success "数据库模式: PostgreSQL + Redis"
      step_pg_config
      ;;
    *)
      error "无效选择: $DB_CHOICE"
      ;;
  esac
}

step_sqlite_config() {
  echo ""
  ask "SQLite 数据文件路径 (容器内)" "/data/codex2api.db" SQLITE_PATH
}

step_pg_config() {
  echo ""
  info "PostgreSQL 配置 (Docker 内置，通常保持默认即可)"
  ask "数据库用户名" "codex2api" PG_USER
  ask "数据库名称"   "codex2api" PG_DB
  echo ""
  ask_secret "数据库密码" "" PG_PASS
  if [[ -z "$PG_PASS" ]]; then
    PG_PASS=$(gen_secret)
    success "已自动生成数据库密码"
  fi
  echo ""
  info "Redis 配置"
  ask_secret "Redis 密码 (留空则无密码)" "" REDIS_PASS
}

# ---------- 第四步：密钥 ----------
step_secrets() {
  echo ""
  printf "${BOLD}${CYAN}━━━ 4/6 安全密钥 ━━━${NC}\n"
  echo ""

  ask_secret "管理后台密钥 (ADMIN_SECRET)" "" ADMIN_SECRET
  if [[ -z "$ADMIN_SECRET" ]]; then
    ADMIN_SECRET=$(gen_secret)
    success "已自动生成管理密钥"
  fi

  echo ""
  ask "下游 API 密钥 (CODEX_API_KEYS, 多个用逗号分隔, 留空不启用)" "" API_KEYS
}

# ---------- 第五步：构建方式 ----------
step_build_mode() {
  echo ""
  printf "${BOLD}${CYAN}━━━ 5/6 构建方式 ━━━${NC}\n"
  echo ""
  echo "  1) 拉取镜像 — 使用预构建镜像 (ghcr.io)，部署快"
  echo "  2) 本地构建 — 从源码编译，适合自定义修改"
  echo ""
  ask "请选择 (1 或 2)" "1" BUILD_CHOICE

  case "$BUILD_CHOICE" in
    1|pull|image)
      BUILD_MODE="image"
      success "构建方式: 拉取预构建镜像"
      ;;
    2|local|build)
      BUILD_MODE="local"
      success "构建方式: 本地源码构建"
      ;;
    *)
      error "无效选择: $BUILD_CHOICE"
      ;;
  esac
}

# ---------- 第六步：确认 ----------
step_confirm() {
  echo ""
  printf "${BOLD}${CYAN}━━━ 6/6 配置确认 ━━━${NC}\n"
  echo ""
  echo "  端口:       $PORT"
  if [[ "$BIND_MODE" == "loopback" ]]; then
    echo "  监听范围:   127.0.0.1 (仅本机访问)"
  else
    echo "  监听范围:   0.0.0.0 (全部网络)"
  fi
  echo "  数据库:     $DB_MODE"
  if [[ "$DB_MODE" == "sqlite" ]]; then
    echo "  数据路径:   $SQLITE_PATH"
    echo "  缓存:       memory"
  else
    echo "  PG 用户:    $PG_USER"
    echo "  PG 数据库:  $PG_DB"
    echo "  Redis:      内置容器"
  fi
  echo "  构建方式:   $( [[ "$BUILD_MODE" == "image" ]] && echo "拉取镜像" || echo "本地构建" )"
  echo "  管理密钥:   ${ADMIN_SECRET}"
  if [[ -n "${API_KEYS:-}" ]]; then
    echo "  API 密钥:   已设置"
  else
    echo "  API 密钥:   未启用"
  fi
  echo ""
  ask "确认部署? (y/n)" "y" CONFIRM
  if [[ "$CONFIRM" != "y" && "$CONFIRM" != "Y" ]]; then
    warn "已取消部署"
    exit 0
  fi
}

# ---------- 生成 .env ----------
generate_env() {
  info "生成 .env 文件..."

  # 备份已有 .env
  if [[ -f .env ]]; then
    cp .env ".env.bak.$(date +%Y%m%d%H%M%S)"
    warn "已备份原 .env 文件"
  fi

  if [[ "$DB_MODE" == "sqlite" ]]; then
    cat > .env << EOF
# ============================
#  codex2api 配置 (SQLite 模式)
#  由 deploy.sh 自动生成于 $(date '+%Y-%m-%d %H:%M:%S')
# ============================

# 服务端口
CODEX_PORT=${PORT}

# 端口绑定地址 (127.0.0.1=仅本机, 0.0.0.0=全部网络)
BIND_HOST=${BIND_HOST}

# 管理后台密钥
ADMIN_SECRET=${ADMIN_SECRET}

# 数据库 — SQLite
DATABASE_DRIVER=sqlite
DATABASE_PATH=${SQLITE_PATH}

# 缓存 — 内存
CACHE_DRIVER=memory

# 时区
TZ=Asia/Shanghai
EOF
  else
    cat > .env << EOF
# ============================
#  codex2api 配置 (PG + Redis 模式)
#  由 deploy.sh 自动生成于 $(date '+%Y-%m-%d %H:%M:%S')
# ============================

# 服务端口
CODEX_PORT=${PORT}

# 端口绑定地址 (127.0.0.1=仅本机, 0.0.0.0=全部网络)
BIND_HOST=${BIND_HOST}

# 管理后台密钥
ADMIN_SECRET=${ADMIN_SECRET}

# 数据库 — PostgreSQL
DATABASE_DRIVER=postgres
DATABASE_HOST=postgres
DATABASE_PORT=5432
DATABASE_USER=${PG_USER}
DATABASE_PASSWORD=${PG_PASS}
DATABASE_NAME=${PG_DB}
DATABASE_SSLMODE=disable
POSTGRES_USER=${PG_USER}
POSTGRES_PASSWORD=${PG_PASS}
POSTGRES_DB=${PG_DB}

# 缓存 — Redis
CACHE_DRIVER=redis
REDIS_ADDR=redis:6379
REDIS_USERNAME=
REDIS_PASSWORD=${REDIS_PASS:-}
REDIS_DB=0
REDIS_TLS=false
REDIS_INSECURE_SKIP_VERIFY=false

# 时区
TZ=Asia/Shanghai
EOF
  fi

  # 追加 API Keys
  if [[ -n "${API_KEYS:-}" ]]; then
    echo "" >> .env
    echo "# 下游 API 密钥鉴权" >> .env
    echo "CODEX_API_KEYS=${API_KEYS}" >> .env
  fi

  success ".env 已生成"
}

# ---------- 选择 compose 文件 ----------
resolve_compose_file() {
  if [[ "$DB_MODE" == "sqlite" && "$BUILD_MODE" == "local" ]]; then
    COMPOSE_FILE="docker-compose.sqlite.local.yml"
  elif [[ "$DB_MODE" == "sqlite" ]]; then
    COMPOSE_FILE="docker-compose.sqlite.yml"
  elif [[ "$BUILD_MODE" == "local" ]]; then
    COMPOSE_FILE="docker-compose.local.yml"
  else
    COMPOSE_FILE="docker-compose.yml"
  fi

  if [[ ! -f "$COMPOSE_FILE" ]]; then
    error "找不到 $COMPOSE_FILE，请确认在项目根目录下运行"
  fi

  success "Compose 文件: $COMPOSE_FILE"
}

# ---------- 部署 ----------
deploy() {
  echo ""
  info "开始部署..."

  if [[ "$BUILD_MODE" == "local" ]]; then
    info "本地构建并启动..."
    $COMPOSE_CMD -f "$COMPOSE_FILE" up -d --build
  else
    info "拉取最新镜像..."
    $COMPOSE_CMD -f "$COMPOSE_FILE" pull
    info "启动服务..."
    $COMPOSE_CMD -f "$COMPOSE_FILE" up -d
  fi

  echo ""
  success "部署完成!"
  echo ""

  local PUBLIC_IP="" LAN_IP=""

  if [[ "$BIND_MODE" == "all" ]]; then
    # 仅在对外开放时才探测/展示对外 IP
    PUBLIC_IP=$(curl -fsS4 --max-time 3 https://ifconfig.me 2>/dev/null \
      || curl -fsS4 --max-time 3 https://api.ipify.org 2>/dev/null \
      || curl -fsS4 --max-time 3 https://ipinfo.io/ip 2>/dev/null \
      || true)
    PUBLIC_IP=$(echo "$PUBLIC_IP" | tr -d '[:space:]')

    if command -v hostname >/dev/null 2>&1; then
      LAN_IP=$(hostname -I 2>/dev/null | awk '{print $1}' || true)
    fi
    if [[ -z "$LAN_IP" ]] && command -v ip >/dev/null 2>&1; then
      LAN_IP=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}')
    fi
    if [[ -z "$LAN_IP" ]] && command -v ifconfig >/dev/null 2>&1; then
      LAN_IP=$(ifconfig 2>/dev/null | awk '/inet /{print $2}' | grep -v '^127\.' | head -n1)
    fi
  fi

  echo "  ┌──────────────────────────────────────────┐"
  echo "  │  部署信息                                  │"
  echo "  └──────────────────────────────────────────┘"
  echo ""
  if [[ "$BIND_MODE" == "loopback" ]]; then
    echo "  监听范围 : 127.0.0.1 (仅本机访问)"
    echo ""
    echo "  本地访问 : http://127.0.0.1:${PORT}"
    echo "             http://127.0.0.1:${PORT}/admin"
  else
    echo "  监听范围 : 0.0.0.0 (全部网络)"
    echo ""
    echo "  本地访问 : http://localhost:${PORT}"
    echo "             http://localhost:${PORT}/admin"
    if [[ -n "$LAN_IP" ]]; then
      echo "  内网访问 : http://${LAN_IP}:${PORT}"
      echo "             http://${LAN_IP}:${PORT}/admin"
    fi
    if [[ -n "$PUBLIC_IP" ]]; then
      echo "  公网访问 : http://${PUBLIC_IP}:${PORT}"
      echo "             http://${PUBLIC_IP}:${PORT}/admin"
    fi
  fi
  echo ""
  echo "  管理密钥 : ${ADMIN_SECRET}"
  echo ""
  echo "  查看日志 : $COMPOSE_CMD -f $COMPOSE_FILE logs -f"
  echo "  停止服务 : $COMPOSE_CMD -f $COMPOSE_FILE down"
  echo ""
  if [[ "$BIND_MODE" == "all" && -n "$PUBLIC_IP" ]]; then
    warn "服务对外开放，请确认防火墙/安全组已放行 ${PORT} 端口"
  fi
  if [[ "$BIND_MODE" == "loopback" ]]; then
    info "如需对外暴露，可重新运行 deploy.sh 选择「全部网络」，或在 .env 中将 BIND_HOST 改为 0.0.0.0"
  fi
  echo ""
}

# ---------- 主流程 ----------
main() {
  banner
  bootstrap_repo "$@"
  preflight
  step_port
  step_bind
  step_database
  step_secrets
  step_build_mode
  step_confirm
  generate_env
  resolve_compose_file
  deploy
}

main "$@"
