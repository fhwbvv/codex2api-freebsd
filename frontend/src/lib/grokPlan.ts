import type { AccountRow, GrokPlanInfo } from "../types";

export const GROK_KNOWN_PLAN_KEYS = [
  "free",
  "supergrok",
  "x_basic",
  "x_premium",
  "x_premium_plus",
  "supergrok_heavy",
  "supergrok_lite",
] as const;

export type GrokKnownPlanKey = (typeof GROK_KNOWN_PLAN_KEYS)[number];
export type GrokPlanFilter = "all" | GrokKnownPlanKey | "other";

const KNOWN_PLAN_KEY_SET = new Set<string>(GROK_KNOWN_PLAN_KEYS);

const PLANS_BY_TIER: Record<string, GrokPlanInfo> = {
  "0": { key: "free", display: "Free", paid: false, billing: false },
  "1": {
    key: "supergrok",
    display: "SuperGrok",
    paid: true,
    billing: true,
  },
  "2": {
    key: "x_basic",
    display: "X Basic",
    paid: true,
    billing: false,
  },
  "3": {
    key: "x_premium",
    display: "X Premium",
    paid: true,
    billing: true,
  },
  "4": {
    key: "x_premium_plus",
    display: "X Premium+",
    paid: true,
    billing: true,
  },
  "5": {
    key: "supergrok_heavy",
    display: "SuperGrok Heavy",
    paid: true,
    billing: true,
  },
  "6": {
    key: "supergrok_lite",
    display: "SuperGrok Lite",
    paid: true,
    billing: true,
  },
};

const PLAN_TIER_BY_ALIAS: Record<string, string> = {
  free: "0",
  supergrok: "1",
  x_basic: "2",
  xbasic: "2",
  x_premium: "3",
  xpremium: "3",
  x_premium_plus: "4",
  xpremium_plus: "4",
  xpremiumplus: "4",
  supergrok_heavy: "5",
  supergrokheavy: "5",
  supergrok_lite: "6",
  supergroklite: "6",
};

export function resolveGrokPlan(value: unknown): GrokPlanInfo | null {
  if (typeof value === "string") {
    const raw = value.trim();
    if (!raw) return null;
    const tier = PLAN_TIER_BY_ALIAS[normalizePlanAlias(raw)];
    if (tier) return PLANS_BY_TIER[tier];
    const numeric = Number(raw);
    if (!Number.isFinite(numeric)) return null;
    return resolveGrokPlan(numeric);
  }
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0) {
    return null;
  }
  const tier = String(value);
  if (PLANS_BY_TIER[tier]) return PLANS_BY_TIER[tier];
  if (value > 0) {
    return { key: tier, display: tier, paid: true, billing: true };
  }
  return null;
}

export function resolveAccountGrokPlan(
  account: Pick<AccountRow, "plan_type" | "grok_plan">,
): GrokPlanInfo | null {
  const serverPlan = account.grok_plan;
  if (
    serverPlan &&
    serverPlan.key.trim() &&
    serverPlan.display.trim() &&
    typeof serverPlan.paid === "boolean" &&
    typeof serverPlan.billing === "boolean"
  ) {
    return serverPlan;
  }
  return resolveGrokPlan(account.plan_type);
}

export function grokPlanFilterCategory(
  account: Pick<AccountRow, "plan_type" | "grok_plan">,
): Exclude<GrokPlanFilter, "all"> {
  const plan = resolveAccountGrokPlan(account);
  if (plan && KNOWN_PLAN_KEY_SET.has(plan.key)) {
    return plan.key as GrokKnownPlanKey;
  }
  return "other";
}

function normalizePlanAlias(raw: string): string {
  return raw
    .toLowerCase()
    .replace(/\+/g, "_plus")
    .replace(/[\s-]+/g, "_")
    .replace(/_+/g, "_")
    .replace(/^_|_$/g, "");
}
