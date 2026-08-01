import assert from "node:assert/strict";
import test from "node:test";

import { selectAutoScrollKey } from "./selectScroll.ts";

test("selected option auto-scroll key is stable across dropdown repositioning", () => {
  assert.equal(selectAutoScrollKey(false, false, "gpt-5.4"), null);
  assert.equal(selectAutoScrollKey(true, false, "gpt-5.4"), null);
  assert.equal(selectAutoScrollKey(true, true, "gpt-5.4"), "gpt-5.4");
  assert.equal(selectAutoScrollKey(true, true, "gpt-5.4"), "gpt-5.4");
});

test("selected option auto-scroll runs again after the selected value changes", () => {
  assert.equal(selectAutoScrollKey(true, true, "gpt-5.4"), "gpt-5.4");
  assert.equal(selectAutoScrollKey(true, true, "gpt-5.5"), "gpt-5.5");
});
