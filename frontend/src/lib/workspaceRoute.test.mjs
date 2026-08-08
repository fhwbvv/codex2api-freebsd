import assert from "node:assert/strict";
import test from "node:test";

import {
  applyOptionalWorkspaceRouteHeader,
  applyWorkspaceRouteHeader,
  workspaceRouteFromHeaders,
} from "./workspaceRoute.ts";

test("workspace route replaces a case-insensitive existing header", () => {
  assert.deepEqual(
    applyWorkspaceRouteHeader(
      {
        "chatgpt-account-id": "old-team",
        "X-Test": "ok",
      },
      " new-team ",
    ),
    {
      "Chatgpt-Account-Id": "new-team",
      "X-Test": "ok",
    },
  );
});

test("empty route removes only the workspace override", () => {
  assert.deepEqual(
    applyWorkspaceRouteHeader(
      {
        "CHATGPT-ACCOUNT-ID": "team",
        "X-Test": "ok",
      },
      "",
    ),
    {
      "X-Test": "ok",
    },
  );
});

test("workspace route lookup is case-insensitive", () => {
  assert.equal(
    workspaceRouteFromHeaders({ "CHATGPT-ACCOUNT-ID": " team " }),
    "team",
  );
});

test("an omitted import route preserves a manually entered workspace header", () => {
  const headers = {
    "chatgpt-account-id": "manual-team",
    "X-Test": "ok",
  };
  const routed = applyOptionalWorkspaceRouteHeader(headers, undefined);
  assert.deepEqual(
    routed,
    headers,
  );
  assert.notEqual(routed, headers);
});
