import assert from "node:assert/strict";
import test from "node:test";

import {
  grokPlanFilterCategory,
  resolveAccountGrokPlan,
  resolveGrokPlan,
} from "./grokPlan.ts";

test("Grok tier mapping preserves key, display, paid, and billing metadata", () => {
  const cases = [
    [0, { key: "free", display: "Free", paid: false, billing: false }],
    [1, { key: "supergrok", display: "SuperGrok", paid: true, billing: true }],
    [2, { key: "x_basic", display: "X Basic", paid: true, billing: false }],
    [3, { key: "x_premium", display: "X Premium", paid: true, billing: true }],
    [4, { key: "x_premium_plus", display: "X Premium+", paid: true, billing: true }],
    [5, { key: "supergrok_heavy", display: "SuperGrok Heavy", paid: true, billing: true }],
    [6, { key: "supergrok_lite", display: "SuperGrok Lite", paid: true, billing: true }],
  ];

  for (const [tier, expected] of cases) {
    assert.deepEqual(resolveGrokPlan(tier), expected);
  }
});

test("Grok plans normalize persisted keys, legacy displays, and numeric tiers", () => {
  assert.equal(resolveGrokPlan("SuperGrok Heavy")?.key, "supergrok_heavy");
  assert.equal(resolveGrokPlan("X Premium+")?.key, "x_premium_plus");
  assert.equal(resolveGrokPlan("x_premium_plus")?.display, "X Premium+");
  assert.equal(resolveGrokPlan("6")?.display, "SuperGrok Lite");

  assert.deepEqual(resolveGrokPlan(9), {
    key: "9",
    display: "9",
    paid: true,
    billing: true,
  });
  for (const invalid of [-1, "", "api", "unknown", "not-a-tier", null]) {
    assert.equal(resolveGrokPlan(invalid), null);
  }
});

test("Grok account plan prefers backend metadata and keeps unknowns filterable", () => {
  const account = {
    plan_type: "free",
    grok_plan: {
      key: "x_basic",
      display: "X Basic",
      paid: true,
      billing: false,
    },
  };
  assert.equal(resolveAccountGrokPlan(account)?.key, "x_basic");
  assert.equal(grokPlanFilterCategory(account), "x_basic");
  assert.equal(grokPlanFilterCategory({ plan_type: "9" }), "other");
  assert.equal(grokPlanFilterCategory({ plan_type: "api" }), "other");
});
