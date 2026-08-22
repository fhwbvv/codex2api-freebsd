import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { operationProgressMessage } from "./operationProgressMessage.ts";

describe("operationProgressMessage", () => {
  it("shows the current account while a clean is still running", () => {
    assert.equal(
      operationProgressMessage({
        type: "progress",
        action: "clean",
        message: "账号已清理",
        account_email: "a@example.com",
      }),
      "a@example.com",
    );
  });

  it("keeps the summary only after the clean completes", () => {
    assert.equal(
      operationProgressMessage(
        {
          type: "complete",
          action: "clean",
          message: "已清理 532 个账号",
        },
        "a@example.com",
      ),
      "已清理 532 个账号",
    );
  });

  it("prefers a clean failure over the current account", () => {
    assert.equal(
      operationProgressMessage({
        type: "progress",
        action: "clean",
        error: "账号不存在",
        account_email: "a@example.com",
      }),
      "账号不存在",
    );
  });
});
