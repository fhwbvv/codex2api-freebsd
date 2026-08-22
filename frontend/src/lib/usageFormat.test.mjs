import assert from "node:assert/strict";
import test from "node:test";

import {
  getAccountStatusBadgeStatus,
  isOfficialCostHiddenAccount,
  isOfficialCostTooNew,
  isUnsampledQuotaAccount,
  needsOfficialCostReload,
  needsUsageReload,
} from "./usageFormat.ts";

test("usage reload accepts either optional usage window as sampled", () => {
  assert.equal(needsUsageReload({ status: "active" }), true);
  assert.equal(
    needsUsageReload({ status: "active", usage_percent_5h: 12 }),
    false,
  );
  assert.equal(
    needsUsageReload({ status: "ready", usage_percent_7d: 34 }),
    false,
  );
});

test("usage reload skips accounts that cannot be sampled", () => {
  assert.equal(needsUsageReload({ status: "unauthorized" }), false);
});

test("unsampled quota accounts are not treated as available", () => {
  assert.equal(isUnsampledQuotaAccount({ status: "active" }), true);
  assert.equal(
    isUnsampledQuotaAccount({ status: "active", usage_percent_5h: 8 }),
    false,
  );
  assert.equal(
    isUnsampledQuotaAccount({ status: "unauthorized" }),
    false,
  );
  assert.equal(
    isUnsampledQuotaAccount({ status: "active", grok_api: true }),
    false,
  );
  assert.equal(
    isUnsampledQuotaAccount({ status: "active", openai_responses_api: true }),
    false,
  );
  assert.equal(getAccountStatusBadgeStatus({ status: "active" }), "unsampled");
  assert.equal(
    getAccountStatusBadgeStatus({ status: "active", usage_percent_7d: 12 }),
    "active",
  );
  assert.equal(
    getAccountStatusBadgeStatus({ status: "rate_limited" }),
    "rate_limited",
  );
  assert.equal(
    getAccountStatusBadgeStatus({ status: "overload_paused" }),
    "active",
  );
});

test("official cost reload only retries Codex accounts missing the snapshot", () => {
  assert.equal(needsOfficialCostReload({}), true);
  assert.equal(needsOfficialCostReload({ official_usd_7d: 0 }), false);
  assert.equal(needsOfficialCostReload({ official_usd_7d: 12.5 }), false);
  assert.equal(needsOfficialCostReload({ openai_responses_api: true }), false);
  assert.equal(needsOfficialCostReload({ grok_api: true }), false);
  assert.equal(needsOfficialCostReload({ status: "error" }), false);
  assert.equal(needsOfficialCostReload({ status: "unauthorized" }), false);
  assert.equal(
    needsOfficialCostReload({
      created_at: new Date(Date.now() - 60 * 60 * 1000).toISOString(),
    }),
    false,
  );
  assert.equal(
    needsOfficialCostReload({
      created_at: new Date(Date.now() - 25 * 60 * 60 * 1000).toISOString(),
    }),
    true,
  );
  assert.equal(isOfficialCostHiddenAccount({ status: "error" }), true);
  assert.equal(isOfficialCostHiddenAccount({ status: "active" }), false);
  assert.equal(
    isOfficialCostTooNew({
      created_at: new Date(Date.now() - 2 * 60 * 60 * 1000).toISOString(),
    }),
    true,
  );
});

test("official cost reload stops once the backend reports a completed sync", () => {
  // 同步成功但上游没有数据(官方统计滞后):继续重拉不会有结果,必须停。
  assert.equal(needsOfficialCostReload({ official_usage_synced: true }), false);
  assert.equal(
    needsOfficialCostReload({ official_usage_synced: false }),
    true,
  );
});
