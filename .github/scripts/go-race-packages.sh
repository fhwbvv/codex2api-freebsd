#!/usr/bin/env bash
# Print the package list for one test-race shard so heavy packages do not
# share a 2-core runner. The `rest` shard is everything except the dedicated
# shards (admin, database, proxy/..., security/promptfilter).
set -euo pipefail

shard="${1:-}"
case "$shard" in
  admin)
    echo ./admin
    ;;
  database)
    echo ./database
    ;;
  proxy)
    echo ./proxy/...
    ;;
  promptfilter)
    echo ./security/promptfilter
    ;;
  rest)
    go list ./... | grep -Ev '/(admin|database)($|/)' | grep -Ev '/proxy($|/)' | grep -Ev '/security/promptfilter$'
    ;;
  *)
    echo "unknown test-race shard: ${shard}" >&2
    exit 1
    ;;
esac
