# FreeBSD / serv00 Deployment

This project can be packaged as a single FreeBSD binary for `serv00`.

The recommended runtime mode on `serv00` is:

- `DATABASE_DRIVER=sqlite`
- `CACHE_DRIVER=memory`
- `DATABASE_PATH=./data/codex2api.db`

## Files You Need

After building or downloading the FreeBSD artifact, prepare a directory like this:

```text
codex2api/
|-- codex2api-freebsd-amd64
|-- .env
`-- data/
```

## Permissions

The binary itself needs execute permission:

```bash
chmod +x ./codex2api-freebsd-amd64
```

The SQLite directory needs to exist and be writable:

```bash
mkdir -p ./data
chmod 755 ./data
```

If you use a custom database path or auth path, make sure those directories are also writable by your current account.

## Minimal `.env` Example

Create `.env` in the same directory where you will run the binary:

```dotenv
CODEX_PORT=8080
ADMIN_SECRET=change-this-admin-secret

DATABASE_DRIVER=sqlite
DATABASE_PATH=./data/codex2api.db

CACHE_DRIVER=memory
TZ=Asia/Shanghai

# Optional: protect your API with one or more keys
# CODEX_API_KEYS=sk-example-1,sk-example-2

# Optional: upstream proxy
# CODEX_PROXY_URL=http://127.0.0.1:3067
```

## Start Command

Start it from the directory that contains both the binary and `.env`:

```bash
./codex2api-freebsd-amd64
```

Because `main.go` loads `.env` from the current working directory, starting the binary from another directory without copying `.env` there will fail or load the wrong settings.

## Verify It Started

After startup, open:

- Admin panel: `http://127.0.0.1:8080/admin/`
- Health check: `http://127.0.0.1:8080/health`

## Build Locally

If you want to build the FreeBSD binary locally from source:

```bash
chmod +x ./scripts/package-freebsd.sh
./scripts/package-freebsd.sh
```

The output file will be:

```text
dist/codex2api-freebsd-amd64
```
