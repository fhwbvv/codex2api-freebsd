# Changelog — iteration/may-2026-v2

Dates: 2026-05-13 to 2026-05-20. 17 commits.

## Features

- **Credit quota support (#141).** Added `credit_enabled` and `credit_skip_usage_window` flags to the accounts table. Credit-marked accounts skip usage-window penalties in the scheduler. Managed via `PATCH /api/admin/accounts/:id/credit`.

- **Scheduler mode (#133).** Added `scheduler_mode` system setting with two modes: `round_robin` (default, weighted by dispatch score) and `remaining_quota` (prioritize accounts with lowest usage percent). Configurable from Admin Settings page.

- **5h/7d windowed USD cost display.** Replaced the single total-cost column with a windowed billing view. Each account now shows `billed_5h` and `billed_7d` fields aligned with the account's usage-reset boundaries. This reflects actual spending, not estimated token costs.

- **Image-to-image in Image Studio (#135, #136).** The admin Image Studio now supports image-to-image generation via `POST /api/admin/images/edit-jobs`, accepting reference image URLs or data URIs. Added text-to-image and image-to-image tabs in the frontend.

- **Billing model expansion.** Added pricing for gpt-5.5-pro and gpt-5.4-pro families. Implemented long context (>272K tokens) premium pricing for gpt-5.5, gpt-5.5-pro, gpt-5.4, gpt-5.4-pro with automatic detection. Fixed gpt-4o and gpt-4o-mini cache-read pricing.

## Fixes

- **GPT-5.5 pricing corrected.** Updated standard-tier billing from old values to $5.00/M input / $30.00/M output (priority: $12.50/M / $75.00/M), matching current official pricing.

- **SSE stream isolation.** Prevented SSE response mixing when retrying across accounts, using `c.Writer.Written()` as the retry guard instead of a package-level flag.

- **Usage logging for image errors.** Added usage-log emits for read-error paths in image generation, ensuring billing records are not silently dropped on stream failures.

- **Model mapping initialization.** Restored `modelMapping` init that was accidentally removed during the scheduler_mode refactor.

- **Credit field Scan order.** Fixed PostgreSQL `Scan` argument ordering for credit fields that was causing silent zero-values.

- **Round 2 review fixes.** Addressed Haiku review findings including api.ts syntax cleanup, billing test corrections, and several CRITICAL/HIGH issues from automated review.

## Security

- **SQLite default binds to localhost.** SQLite compose files (`docker-compose.sqlite.yml`, `docker-compose.sqlite.local.yml`) now bind ports to `127.0.0.1` by default. Previously they bound to `0.0.0.0`, exposing the service on all interfaces. Standard (PostgreSQL) compose files retain the `0.0.0.0` default.

- **BIND_HOST env var.** Added `BIND_HOST` environment variable support to control the HTTP listen address across all deployment modes. Documented in `.env.example`, `.env.sqlite.example`, and `CONFIGURATION.md`.

## Breaking Changes

- **SQLite compose port binding.** SQLite deployments upgrading from a previous version that relied on external access via the default compose configuration must now explicitly set `BIND_HOST=0.0.0.0` in `.env` or override the port binding in the compose file. All other behavior remains backwards-compatible.
