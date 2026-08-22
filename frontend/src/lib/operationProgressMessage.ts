export interface OperationProgressMessageEvent {
  type: "start" | "progress" | "complete";
  action?: string;
  error?: string;
  message?: string;
  account_email?: string;
  account_name?: string;
}

const CLEAN_DONE_MESSAGE = /账号已清理|已清理 \d+ 个账号|accounts? cleared/i;

function currentAccountLabel(event: OperationProgressMessageEvent): string | undefined {
  return event.account_email?.trim() || event.account_name?.trim() || undefined;
}

// 清理进行中只展示当前账号或失败原因，整批完成后再出现总结。
export function operationProgressMessage(
  event: OperationProgressMessageEvent,
  previous?: string,
): string | undefined {
  if (event.type === "complete") {
    return event.error || event.message || previous;
  }
  if (event.action === "clean") {
    return event.error || currentAccountLabel(event);
  }
  const next = event.error || event.message;
  if (next && CLEAN_DONE_MESSAGE.test(next)) {
    return currentAccountLabel(event) || previous;
  }
  return next || previous;
}
