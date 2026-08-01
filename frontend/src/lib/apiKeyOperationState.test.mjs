import assert from "node:assert/strict";
import test from "node:test";

import { canStartAPIKeyBulkReset } from "./apiKeyOperationState.ts";

const idleState = {
  keyCount: 2,
  resettingAll: false,
  resettingIds: new Set(),
  deletingIds: new Set(),
};

test("bulk API key reset is available only while key operations are idle", () => {
  assert.equal(canStartAPIKeyBulkReset(idleState), true);
  assert.equal(
    canStartAPIKeyBulkReset({ ...idleState, resettingAll: true }),
    false,
  );
  assert.equal(
    canStartAPIKeyBulkReset({ ...idleState, resettingIds: new Set([1]) }),
    false,
  );
  assert.equal(
    canStartAPIKeyBulkReset({ ...idleState, deletingIds: new Set([2]) }),
    false,
  );
  assert.equal(
    canStartAPIKeyBulkReset({ ...idleState, keyCount: 0 }),
    false,
  );
});
