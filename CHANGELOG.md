# Changelog

## v2.3.2 - 2026-06-12

### Features

- **Group filter: ungrouped and exclude modes (#229).** The account group filter now supports an "ungrouped" quick option and per-group tri-state filtering (only / hide / off), so accounts outside chosen groups can be isolated, selected in bulk, and assigned to a group quickly. The groups column is also shown by default in the table view.

### Fixes

- **`gpt-5.3-codex-spark` no longer receives the default image tool (#230).** The Responses translator auto-injected the hosted `image_generation` tool (plus bridge instructions) into ordinary text requests; spark rejects hosted image tools, so text-only spark requests failed on the HTTP upstream path. The default injection is now skipped for spark while explicitly supplied image tools are still honored.
- **Tools without a `type` field no longer fail requests (#219).** Tool definitions missing `type` (or with `type: null`) were forwarded verbatim and the upstream rejected the whole request with 400 `Unsupported tool type: None`. Function-shaped tools (a nested `function` object or a top-level `name`) are now treated as `type: "function"` per OpenAI SDK convention on both the Chat Completions and Responses paths; unrecognizable typeless tools are dropped instead of failing the request.
- **Usage logs are requeued when a flush fails (#233).** Failed usage-log batches are put back at the front of the buffer and retried on the next flush instead of being dropped, and usage-log inserts plus API key quota updates now run in a single transaction on both SQLite and PostgreSQL.
- **Reduced timer allocations while waiting for Redis token refresh (#231).** `WaitForRefreshComplete` now reuses a single ticker and timeout timer instead of allocating a new timer every 200ms poll (previously up to ~150 timers per 30s wait).

### Performance

- **Request bodies are read once and reused (#232).** `RequestSizeLimiter` caches the body it already reads, and the body-cache middleware plus all JSON proxy handlers reuse it, cutting up to ~2/3 of duplicated request-body buffer allocations on hot paths.

## v2.3.1 - 2026-06-11

### Features

- **Group-level and global auto-pause thresholds (#227).** Usage auto-pause thresholds can now be set globally (Settings) and per account group (group dialog), resolved with account > group > global priority. Account-level settings win when present, otherwise the smallest non-zero group threshold applies, falling back to the global default. Changes take effect immediately without a restart, and out-of-range values are rejected consistently across all three levels.
- **API key concurrency limits (#226).** Each API key can cap its concurrent in-flight requests via `limits.max_concurrency`. Requests over the cap receive 429 with a descriptive message across all proxy entrances (responses, compact, chat completions, Anthropic messages, images, WebSocket turns). Unset or 0 means unlimited.
- **Account group multi-select binding (#217, #222).** Account group assignment now uses a searchable multi-select dropdown instead of assigning new groups to all accounts.

### Fixes

- **WebSocket prompt cache restored (#202).** Since v2.2.7, requests without an explicit session identifier got a random per-request `stateless-` ID on the WS upstream path, which leaked into `prompt_cache_key` and the WS handshake session headers — so the upstream prompt cache never hit in WS mode (HTTP was unaffected). WS now injects a deterministic cache key derived from the downstream API key (matching HTTP behavior), restoring cache hits (~86% of input tokens on repeated prefixes in live testing).
- **WebSocket connection reuse under high RPM.** Sessionless requests previously opened a new WS connection per request; sustained load (~200 RPM) triggered upstream handshake throttling (`bad handshake` → 503). Such requests now reuse warm connections from a per-(account, cache key) slot pool (8 slots, falling back to one-shot connections when all slots are busy). Live testing at 200 RPM went from 128/200 success to 200/200 with zero handshake failures and roughly halved latency.
- **Codex CLI compact v2 input items (#224).** `/responses` no longer returns 400 `Invalid input type 'compaction_trigger'` for Codex CLI compact v2 payloads.
- **Usage probed immediately after OAuth account add.** Newly added OAuth accounts get an immediate usage probe instead of waiting for the next cycle.
- **Account table scrolling.** Adjusted account table scrolling behavior in the admin UI.

## v2.3.0 - 2026-06-10

### Features

- **Account recycle bin.** Soft-deleted accounts now land in a recycle bin instead of vanishing. Accounts can be restored to the runtime pool without a restart, test-connected before restoring (429/usage-limit results are recorded as rate-limited rather than failed), batch-tested with an optional auto-restore-on-pass toggle, filtered by plan type and searched with pagination, and permanently purged behind a typed confirmation enforced on the API as well.
- **Client IP in usage logs.** Usage records now capture the requesting client IP and display it in the usage views.

### Fixes

- **Relay accounts on `/v1/messages` and `/v1/chat/completions` (#181).** Both endpoints now schedule OpenAI Responses (relay API-key) accounts and forward over HTTP like `/v1/responses`, instead of returning 503 "No available accounts" once Codex OAuth accounts were rate-limited on relay-only deployments.
- **Image generation no longer routed over WebSocket (#220).** Requests carrying an `image_generation` tool (including the auto-injected one) are forced onto HTTP, and requests that do go over WS get the auto-injected image tool, its `tool_choice`, and bridge instructions stripped, so image generation can no longer stall WS streams on large base64 payloads.
- **Image SSE keep-alive.** Image generation SSE streams now stay alive through long upstream pauses, preventing idle disconnects mid-generation.
- **Account rate-limit state sync.** Rate-limit state observed by usage probes and WHAM checks is synced back to runtime account state so scheduling and status displays stay consistent.
- **Account status summary counts.** Admin account status summary counters now align with the account list filters.

### Security

- **Code-scanning hardening.** Local image storage rejects keys containing path separators on write and `..` traversal segments on read/delete (legacy relative refs keep working); remote-migration and sub2api import URLs must be complete http/https URLs with a host; SVG logo minification strips comments to a fixed point so `<!--` cannot survive sanitization.

## v2.2.9 - 2026-06-09

### Features

- **Account proxy validation (#212).** Added proxy test controls across account add/edit, OAuth generation, and OpenAI Responses account forms, backed by an admin proxy test endpoint that reports reachability, latency, IP, and location.
- **Usage range and reset-time display.** Dashboard usage cards now label and query the selected time range instead of always showing "today", and account quota reset labels now display precise seconds with full timestamps available in tooltips.

### Fixes

- **WebSocket message-too-big fallback (#214).** Upstream WebSocket `close 1009` / `message too big` failures before the first downstream token now fall back to HTTP for the same request instead of rotating accounts through another WS attempt, with logs classified as `message_too_big`.
- **CPA/sub2api JSON import (#215).** JSON imports now avoid treating repeated or conflicting ChatGPT account IDs as the same account when credentials differ, fixing failed or collapsed imports from CPA/sub2api export files.
- **Batch account test state updates.** Batch connection tests now update account status consistently with single-account tests, including `response.failed`, unauthorized, generic upstream errors, and usage-limit results; successful tests still recover stale banned/cooldown state.
- **Quota auto-pause scheduling (#216).** Accounts that reached configured usage thresholds remain out of scheduling while their quota window is still active, and stale reset timestamps are ignored so reset/cooldown state does not linger incorrectly.

## v2.2.8 - 2026-06-08

### Features

- **Account usage analytics.** Expanded the account usage details modal with Overview, Details, and Quality views, range-aware totals, richer model distribution controls, and quality signals including error rate, retry count, average first-token latency, P95 response time, streaming ratio, and compact ratio.
- **Batch account import workflow.** Refresh Token batch import now has progress feedback and kicks off usage sampling so newly imported accounts do not require a separate manual batch test before usage data becomes visible.
- **First-token mode setting (#207).** Added a system setting for strict vs loose first-token detection, making TTFT behavior configurable while keeping thinking/pre-output events from being counted incorrectly in strict mode.
- **Usage visibility controls (#203, #209, #210).** Usage stats now support additional time ranges, full token-number display, compact request markers, and dedicated WS/Fast request badges.

### Fixes

- **Batch account test consistency (#194).** Batch tests now match single-account tests more closely by using the WHAM usage preflight and parsing real SSE terminal events from a short request; `response.failed`, missing output, and `usage_limit_reached` are no longer treated as successful account recovery.
- **Forced WebSocket usage logs (#205, #210).** Requests forced through upstream WebSocket now persist and display the `via_websocket` marker correctly, with Fast tier shown as its own request badge.
- **Responses compact and quota handling (#174, #201).** OpenAI Responses API-key accounts can forward `/v1/responses/compact`, and nested `usage_limit_reached` errors are handled consistently for cooldown and sampling.
- **Billing tier policy persistence (#206).** `billing_tier_policy` now stays aligned across frontend save behavior, backend normalization, and database persistence.
- **OAuth authorization UX.** OAuth authorization links can be regenerated and copied reliably, including HTTP admin deployments where the Clipboard API may be unavailable.
- **Image and WS routing stability (#198).** Explicit image generation requests bypass forced WS routing, and `gpt-5.3-codex-spark` scheduling/model filtering is hardened.

## v2.2.7 - 2026-06-05

### Features

- **WebSocket silent retry controls (#195).** Added Codex upstream WS settings for hiding upstream errors, silently retrying pre-first-token upstream failures, and capping silent retries (`codex_ws_hide_upstream_errors`, `codex_ws_silent_retry_enabled`, `codex_ws_silent_max_retries`). These settings are available in the admin UI and persist through both Postgres and SQLite.
- **WHAM-only usage probe controls.** Added runtime/admin controls so usage probes can rely on the zero-cost WHAM endpoint without falling back to `/responses` probes when that is preferred.

### Fixes

- **WS upstream failure handling (#195).** Retryable upstream WS failures before the first token, including usage-limit, 429, 5xx, read errors, timeouts, and EOFs, now stay server-side while codex2api switches accounts and rebuilds the upstream WS connection. If retries are exhausted, clients receive a unified friendly message while the original upstream error remains in backend logs and usage records.
- **Responses routing and model hardening (#198).** Hardened `/v1/responses` routing for recent Codex models including `gpt-5.3-codex-spark`, widened local plan gating so stale local `plan_type` records do not incorrectly block real upstream calls, and improved WS/TTFT handling around response payload content.
- **OpenAI Responses compact routing.** OpenAI Responses API accounts added with `base_url` + `api_key` can now use `/v1/responses/compact`; compact bodies are normalized for that upstream path instead of being sent through the ChatGPT-only compact route.
- **Usage-limit detection.** `usage_limit_reached` is now recognized even when wrapped inside `response.error`, `response.status_details`, or upstream 5xx-shaped payloads, so exhausted accounts are treated as quota-limited consistently.
- **Accounts toolbar wrapping.** Improved the admin accounts toolbar layout so search/filter/action controls wrap cleanly on narrower viewports.
- **Security scan recovery.** Updated React Router to a patched release and raised the Go toolchain directive to `1.26.4`, clearing the failing frontend npm audit and backend govulncheck jobs.

## v2.2.6 - 2026-06-03

### Features

- **Codex upstream WebSocket mode.** Added an opt-in `codex_force_websocket` system setting that routes Codex upstream traffic over a persistent WebSocket long-connection (reusing the `wsrelay` connection pool), reducing per-request TLS/handshake overhead to better match the official CLI. Disabled by default; when off, requests keep using the existing HTTP path. A downstream `https://.../v1/responses` (or `/v1/chat/completions`) HTTP POST is transparently forwarded as a `wss://` upstream connection.
- **Idle WS keepalive.** Added `codex_ws_keepalive_enabled` and `codex_ws_keepalive_interval_sec` (default 60s) to keep idle upstream WebSocket connections alive with background Ping frames. Keepalive never opens new connections and never sends business frames, so it consumes zero account quota. Disabled by default.
- **WS request badge.** The Usage log now shows a `ws` badge between the status code and model for requests that went over WebSocket.
- **Dedicated WebSocket settings card.** WS-related settings are split out of the crowded scheduling card into their own full-width "WebSocket (Codex Upstream)" card in the admin UI.
- **Bundled Codex CLI bump.** Updated the bundled Codex CLI version from `0.128.0` to `0.136.0` for a more faithful fingerprint and access to newer models.

### Fixes

- **WS upstream error passthrough.** Upstream WebSocket error frames are now relayed to the client as a `response.failed` SSE event preserving the original error, instead of being turned into a low-level read error that surfaced as a mysterious empty response / 32-90s hang. Any upstream error (rate limit, unsupported model, invalid parameter) is now visible to the client.
- **WS transport robustness.** Fixed the `wsrelay` dialer copy dropping `NetDialContext` (TCP KeepAlive never took effect); added 64KB read/write buffers with a shared write-buffer pool for large 48-91KB upstream frames; replaced the fixed 10ms busy-connection spin with exponential backoff plus a max-wait timeout; raised the read timeout from 60s to 120s so long-reasoning turns are not falsely disconnected; and log a warning when a WS path silently falls back to HTTP.
- **TLS session resumption.** Standard and uTLS transports now share a `ClientSessionCache`, so reconnects use TLS resumption (1-RTT) to cut cold-connection cost. Bumped `MaxIdleConnsPerHost` and `IdleConnTimeout`, and switched the HTTP server to explicit `ReadHeaderTimeout`/`IdleTimeout` with graceful shutdown (keeping `WriteTimeout` at 0 so streaming responses are never cut off).
- **Account lookup O(N) → O(1).** The store now keeps a `DBID → account` index, so session-affinity hot paths and all `FindByID` lookups no longer scan the account slice linearly.

## v2.2.5 - 2026-06-02

### Features

- **Codex model registry and reasoning models (#165).** Added system-setting controls for upstream-synced base models and reasoning-effort variants so model list entries can be configured from the admin UI.
- **Codex model redirect mapping (#189, #190).** Added a dedicated Codex model redirect map that can route downstream model names to another Codex model while preserving request reasoning effort.
- **Account quota auto-pause controls.** Added 5h/7d usage-threshold auto-pause controls with global defaults, per-account disables, edit-account controls, and batch editing.
- **Compact request visibility.** Usage logs now detect compact requests and show a compact badge alongside request model/reasoning information.
- **Email domain account tags (#191).** Account management now derives email domains, supports domain filtering with banned/total stats, searches by domain, and lets users show or hide domain badges.
- **Configuration examples.** Updated Codex config examples to cover the current model mapping and reasoning-effort setup.

### Fixes

- **Service-tier billing semantics (#183).** Split requested, actual, and billing service-tier handling so billing policy stays explicit when upstream reports a different actual tier.
- **SQLite and admin billing stability (#185, #186).** Stabilized account billing window reads and SQLite access paths so large account pools do not turn simple admin/API-key queries into transient 503s.
- **Account status and probe handling.** Unauthorized account errors are recorded more consistently, and batch account rendering has less unnecessary work at scale.
- **Refresh token reuse handling.** Reused refresh tokens are now treated as non-retryable credential errors instead of entering avoidable retry paths.

## v2.2.4 - 2026-05-28

### Features

- **Scheduler warm-tier bypass (#176).** Added a scheduler option to skip warm-tier demotion so selected accounts can stay in the healthy scheduling lane.
- **API Reference image previews.** The Try it panel now renders image responses directly when `/v1/images/generations` or `/v1/images/edits` returns `b64_json`, image URLs, or Responses-style image output, while still keeping the raw JSON visible.
- **Long-context billing details (#178).** Usage cost tooltips now expose when long-context pricing is active (`input_tokens > 272,000`) and show the actual input, output, and cache-read unit prices used for the request.
- **Root changelog.** Moved the project changelog to the repository root so release notes and documentation links point to a stable location.

### Fixes

- **WebSocket response continuity (#182).** Fixed WS mode context loss so `previous_response_id` continuation remains connected across `/v1/responses` WebSocket turns.
- **First-token timeout retries (#172).** First-token timeouts now retry by account-pool round and clear transient unavailable markers after the pool has been tried, reducing false "no available account" failures for small pools.
- **Banned-account test recovery.** Successful account tests now recover stale banned/error state instead of leaving the account marked unhealthy after the probe succeeds.
- **Bounded batch account tests.** Batch account tests now keep their execution bounded so duplicate or repeated test requests do not destabilize account state handling.
- **Version popover clipping.** The version popover is rendered through a portal so sidebar overflow no longer clips the latest-version panel.

## v2.2.3 - 2026-05-27

### Features

- **Usage reset radar.** New `/subscriptions` page consolidates the Codex Reset Radar summary, recent RSS events, and a reset-time hook. When a window-close signal is detected the backend clears stale cooldown/usage caches and re-tests every account so the pool reflects the new reset boundary immediately.
- **Streaming batch operations in admin.** Account batch refresh/test/enable/disable/lock/reset now stream per-account progress events (`success/banned/rate_limited/failed`) to the admin UI instead of waiting for the full operation to complete.
- **Compact usage number setting.** Added a system setting to render token counts with K/M units in the usage table for easier reading at scale.
- **Card view for account management.** Desktop accounts page gains a table/card view toggle (up to 5 cards per row on `xl` screens). Choice is persisted in `localStorage`.
- **Status badge error tooltip.** Hovering an `unauthorized` or `error` status badge now surfaces the full upstream error message in a popover, matching the usage log status code tooltip style.
- **Anthropic `speed: fast` forwarding (#170).** Anthropic-style `speed: fast` requests now map to the Codex priority tier upstream so fast clients get fast tokens end-to-end.

### Fixes

- **Version popover always clickable.** The sidebar version badge now opens the popover even when the GitHub latest-version lookup is still pending or blocked; a "checking…" hint is shown until the remote tag arrives.
- **First-token timeout and scheduler races.** Hardened the proxy path so first-token timeouts and concurrent scheduler races no longer collapse into a spurious "no available account" 503.
- **`/responses` WebSocket ingress.** WebSocket clients hitting `ws://host/v1/responses` are now accepted; the prior 404/101 misclassification has been fixed. Setting `CODEX_UPSTREAM_TRANSPORT=ws` no longer reports the connect handshake as an unknown error.
- **Anthropic content preservation + deactivated probe flagging.** Anthropic-shaped responses keep their original content blocks; deactivated accounts are clearly flagged in probe state instead of silently appearing healthy.
- **Wham window classification.** Usage window classification now uses `limit_window_seconds` rather than field position, so free-tier accounts no longer have a 7d window misclassified as 5h.

## v2.2.2 - 2026-05-26

### Features

- **First-run setup and admin auth polish.** Added setup guidance for unconfigured deployments, improved the admin authentication flow, and added a frontend logout path.
- **Runtime status API and page.** Added machine-readable runtime checks for service, database, cache, usage log writer, probes, account pool, image storage, and admin auth.
- **Background media customization.** Added configurable background image/video support, realtime glass opacity/blur controls, and raised MP4 dynamic wallpaper uploads to 40MB while keeping image uploads capped at 20MB.
- **Quick-start configuration options.** Added fast-mode and reasoning-effort snippets for supported client templates in the usage docs.
- **Issue templates.** Added structured Chinese and English GitHub issue templates for bugs, ideas, UI feedback, deployment help, and questions.

### Fixes

- **Request body sizing for wallpapers.** Raised the default request body limit to 48MB so 40MB MP4 background uploads can pass multipart overhead safely.

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
