import assert from "node:assert/strict";
import test from "node:test";

import { getCompactionBadgeKinds } from "./compactionBadges.ts";

test("compaction badges are omitted when neither state is present", () => {
  assert.deepEqual(
    getCompactionBadgeKinds({
      compact: false,
      has_compaction_history: false,
    }),
    [],
  );
});

test("compaction trigger and history can be observed independently", () => {
  assert.deepEqual(
    getCompactionBadgeKinds({
      compact: true,
      has_compaction_history: false,
    }),
    ["trigger"],
  );
  assert.deepEqual(
    getCompactionBadgeKinds({
      compact: false,
      has_compaction_history: true,
    }),
    ["history"],
  );
});

test("dual compaction badges keep trigger before history", () => {
  assert.deepEqual(
    getCompactionBadgeKinds({
      compact: true,
      has_compaction_history: true,
    }),
    ["trigger", "history"],
  );
});
