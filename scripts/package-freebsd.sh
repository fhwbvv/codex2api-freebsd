#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FRONTEND_DIR="${ROOT_DIR}/frontend"
DIST_DIR="${ROOT_DIR}/dist"
VERSION="${VERSION:-dev}"
OUTPUT_NAME="${OUTPUT_NAME:-codex2api-freebsd-amd64}"

echo "[1/3] Building frontend assets"
cd "${FRONTEND_DIR}"
npm ci
VITE_APP_VERSION="${VERSION}" npm run build

echo "[2/3] Building FreeBSD binary"
cd "${ROOT_DIR}"
mkdir -p "${DIST_DIR}"
CGO_ENABLED=0 GOOS=freebsd GOARCH=amd64 \
  go build -trimpath -ldflags="-s -w" -o "${DIST_DIR}/${OUTPUT_NAME}" .

echo "[3/3] Build complete"
echo "Output: ${DIST_DIR}/${OUTPUT_NAME}"
