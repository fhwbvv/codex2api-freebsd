import assert from "node:assert/strict";
import test from "node:test";

import {
  MIB,
  buildResponseCacheBudgetPatch,
  bytesToMiB,
  cacheUtilizationPercent,
  formatIECBytes,
  mergeResponseCacheGeneration,
  mibToBytes,
  validateResponseCacheBudget,
} from "./responseCacheMetrics.ts";

test("response cache MiB conversions use exact binary units", () => {
  assert.equal(MIB, 1_048_576);
  assert.equal(mibToBytes(1), 1_048_576);

  for (const mib of [8, 64, 256, 512, 4096]) {
    assert.equal(mibToBytes(mib), mib * 1_048_576);
    assert.equal(bytesToMiB(mibToBytes(mib)), mib);
  }
});

test("response cache budget validation covers every bound and cross-field rule", () => {
  assert.equal(
    validateResponseCacheBudget({
      totalMiB: 8,
      entryMiB: 1,
      reconstructMiB: 8,
    }),
    null,
  );
  assert.equal(
    validateResponseCacheBudget({
      totalMiB: 4096,
      entryMiB: 256,
      reconstructMiB: 512,
    }),
    null,
  );

  const cases = [
    [{ totalMiB: 7, entryMiB: 1, reconstructMiB: 8 }, "total_range"],
    [{ totalMiB: 4097, entryMiB: 1, reconstructMiB: 8 }, "total_range"],
    [{ totalMiB: 8.5, entryMiB: 1, reconstructMiB: 8 }, "total_integer"],
    [{ totalMiB: 64, entryMiB: 0, reconstructMiB: 8 }, "entry_range"],
    [{ totalMiB: 4096, entryMiB: 257, reconstructMiB: 8 }, "entry_range"],
    [{ totalMiB: 64, entryMiB: 1.5, reconstructMiB: 8 }, "entry_integer"],
    [{ totalMiB: 8, entryMiB: 9, reconstructMiB: 8 }, "entry_exceeds_total"],
    [{ totalMiB: 64, entryMiB: 8, reconstructMiB: 7 }, "reconstruct_range"],
    [{ totalMiB: 64, entryMiB: 8, reconstructMiB: 513 }, "reconstruct_range"],
    [{ totalMiB: 64, entryMiB: 8, reconstructMiB: 8.5 }, "reconstruct_integer"],
  ];

  for (const [budget, expected] of cases) {
    assert.equal(validateResponseCacheBudget(budget), expected);
  }
});

test("response cache utilization handles zero budgets and clamps to 0..100", () => {
  assert.equal(cacheUtilizationPercent(64, 0), 0);
  assert.equal(cacheUtilizationPercent(-1, 64), 0);
  assert.equal(cacheUtilizationPercent(16, 64), 25);
  assert.equal(cacheUtilizationPercent(96, 64), 100);
});

test("IEC byte formatting is stable at cache budget boundaries", () => {
  assert.equal(formatIECBytes(0), "0 B");
  assert.equal(formatIECBytes(1024), "1 KiB");
  assert.equal(formatIECBytes(8 * MIB), "8 MiB");
  assert.equal(formatIECBytes(4 * 1024 * MIB), "4 GiB");
});

test("atomic cache patch sends all writable byte fields and never generation", () => {
  const patch = buildResponseCacheBudgetPatch({
    totalMiB: 64,
    entryMiB: 8,
    reconstructMiB: 64,
  });

  assert.deepEqual(patch, {
    response_cache_local_max_bytes: 64 * MIB,
    response_cache_local_max_entry_bytes: 8 * MIB,
    response_cache_reconstruct_max_bytes: 64 * MIB,
  });
  assert.equal(
    Object.hasOwn(patch, "response_cache_config_generation"),
    false,
  );
});

test("response cache generation merge is monotonic across out-of-order saves", () => {
  assert.equal(mergeResponseCacheGeneration(7, 9), 9);
  assert.equal(mergeResponseCacheGeneration(9, 8), 9);

  let generation = 1;
  generation = mergeResponseCacheGeneration(generation, 2);
  assert.equal(
    generation,
    2,
    "an older successful request remains visible if a newer request later fails",
  );
});

test("response cache generation merge preserves current for old or invalid backends", () => {
  for (const returned of [undefined, null, "9", -1, 1.5, Number.NaN, Number.POSITIVE_INFINITY]) {
    assert.equal(mergeResponseCacheGeneration(7, returned), 7);
  }
});
