import assert from "node:assert/strict";
import test from "node:test";

import {
  OPERATION_RESULTS_VISIBILITY_STORAGE_KEY,
  readOperationResultsVisibility,
  writeOperationResultsVisibility,
} from "./operationResultsPreference.ts";

function createStorage() {
  const values = new Map();
  return {
    getItem(key) {
      return values.get(key) ?? null;
    },
    setItem(key, value) {
      values.set(key, value);
    },
  };
}

test("operation result modal preference is disabled by default", () => {
  assert.equal(readOperationResultsVisibility(createStorage()), false);
});

test("operation result modal preference persists explicit opt-in", () => {
  const storage = createStorage();

  writeOperationResultsVisibility(true, storage);
  assert.equal(
    storage.getItem(OPERATION_RESULTS_VISIBILITY_STORAGE_KEY),
    "true",
  );
  assert.equal(readOperationResultsVisibility(storage), true);

  writeOperationResultsVisibility(false, storage);
  assert.equal(readOperationResultsVisibility(storage), false);
});

test("operation result modal preference tolerates unavailable storage", () => {
  const storage = {
    getItem() {
      throw new Error("storage blocked");
    },
    setItem() {
      throw new Error("storage blocked");
    },
  };

  assert.equal(readOperationResultsVisibility(storage), false);
  assert.doesNotThrow(() => writeOperationResultsVisibility(true, storage));
});
