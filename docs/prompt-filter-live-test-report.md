# Prompt Filter validation guide

Use this guide to validate Prompt Filter without relying on production data or
provider-specific accounts.

## Isolated environment

- Bind the test server to a loopback address.
- Use a temporary SQLite database or a dedicated PostgreSQL test database.
- Use mock upstream servers for JSON, SSE, WebSocket, and multipart flows.
- Never copy production credentials, account exports, or raw user prompts into
  the repository or test output.
- Remove temporary databases and stop mock services after the run.

## Functional cases

1. A benign request is allowed and reaches the mock upstream.
2. A local audit-only match is logged but does not block.
3. Warn and block rules produce the configured local action.
4. Encoded harmful input is detected within configured decode limits.
5. External review can clear or block a local verdict according to policy.
6. External-review timeout and malformed JSON follow fail-open/fail-closed.
7. Scope selection behaves as documented:
   - `all_requests` reviews every extractable request;
   - `local_candidates` reviews local warn and block candidates;
   - `local_blocks` reviews local block decisions only.
8. Audit records distinguish missing scores from genuine zero scores.
9. Pagination returns stable, non-overlapping pages.
10. Clearing Prompt Filter logs follows the documented retention boundaries.

## Streaming and WebSocket cases

- Validate fragmented SSE events and ensure buffered enforcement content is not
  leaked before a blocking decision.
- Validate request extraction per WebSocket logical turn rather than once per
  connection.
- Confirm identity verification and prompt evaluation are not repeated more
  often than required by the protocol.
- Confirm audit queue work does not delay safe streaming output beyond the
  configured synchronous checks.

## Storage cases

- Run schema creation and incremental migrations on SQLite and PostgreSQL.
- Verify incident, usage-log, candidate-evidence, and risk-profile relationships.
- Verify transaction rollback when any part of a compound audit write fails.
- Verify redaction, prompt fingerprints, preview limits, and error-field
  isolation.

## Suggested commands

```sh
go test ./security/promptfilter ./proxy ./admin ./auth ./database
go test ./...
cd frontend && npm test && npm run typecheck && npm run build
```

Before publishing a change, also run the repository's code-impact and changed-
flow checks required by `AGENTS.md`.
