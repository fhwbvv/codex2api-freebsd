import { useCallback, useEffect, useRef, useState } from "react";
import { getAdminKey, resetAdminAuthState } from "../api";
import {
  collectAccountOperationResult,
  snapshotAccountOperationResults,
  type AccountOperationResult,
  type AccountOperationResultsState,
} from "../lib/accountOperationResults";

// 批量操作（测试/删除/刷新）的流式进度：SSE 读取 + 右上角进度浮层状态机。
// 从 Codex 账号页抽出，供 Codex / Grok 账号页共用，确保两处进度条一致。

const OPERATION_PROGRESS_FLUSH_INTERVAL_MS = 200;

export type BatchOperationAction =
  | "batch_test"
  | "batch_delete"
  | "batch_refresh"
  | "grok_import";

export interface BatchOperationEvent {
  type: "start" | "progress" | "complete";
  action: BatchOperationAction;
  status?: string;
  http_status?: number;
  current?: number;
  total?: number;
  success?: number;
  failed?: number;
  banned?: number;
  rate_limited?: number;
  deleted?: number;
  account_id?: number;
  message?: string;
  error?: string;
}

export interface OperationProgressState {
  show: boolean;
  action: BatchOperationAction;
  title: string;
  current: number;
  total: number;
  success: number;
  failed: number;
  banned: number;
  rateLimited: number;
  deleted: number;
  done: boolean;
  message?: string;
}

export async function readOperationSSE(
  res: Response,
  onEvent: (event: BatchOperationEvent) => void,
) {
  const reader = res.body?.getReader();
  if (!reader) return;

  const decoder = new TextDecoder();
  let buffer = "";
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split("\n");
    buffer = lines.pop() ?? "";
    for (const line of lines) {
      if (!line.startsWith("data: ")) continue;
      try {
        onEvent(JSON.parse(line.slice(6)) as BatchOperationEvent);
      } catch {
        /* 忽略格式异常的进度帧 */
      }
    }
  }
}

async function readAdminStreamError(res: Response): Promise<string> {
  const body = await res.text();
  if (!body.trim()) return `HTTP ${res.status}`;
  try {
    const parsed = JSON.parse(body) as { error?: string };
    if (parsed.error?.trim()) return parsed.error;
  } catch {
    /* ignore */
  }
  return body;
}

export async function postAdminSSE(
  path: string,
  body?: unknown,
): Promise<Response> {
  const headers: Record<string, string> = {};
  const adminKey = getAdminKey();
  if (adminKey) headers["X-Admin-Key"] = adminKey;
  if (body !== undefined) headers["Content-Type"] = "application/json";

  const res = await fetch(`/api/admin${path}`, {
    method: "POST",
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    cache: "no-store",
  });
  if (!res.ok) {
    if (res.status === 401) resetAdminAuthState();
    throw new Error(await readAdminStreamError(res));
  }
  return res;
}

export interface UseOperationProgressResult {
  operationProgress: OperationProgressState | null;
  operationResults: AccountOperationResultsState | null;
  // 发起一次流式批量操作，把进度事件驱动到右上角浮层；返回 complete 事件。
  runStreamingOperation: (
    path: string,
    body: unknown,
    title: string,
  ) => Promise<BatchOperationEvent | null>;
  // 直接向浮层喂一个合成进度事件。供非 SSE 的分片式操作(如文件导入按片
  // 汇报)复用同一个右上角进度条,与流式批量操作视觉一致。
  reportOperationEvent: (title: string, event: BatchOperationEvent) => void;
  closeOperationProgress: () => void;
  closeOperationResults: () => void;
}

// useOperationProgress 封装批量操作的进度浮层状态机（含节流刷新与自动隐藏）。
export function useOperationProgress(
  showOperationResults = false,
): UseOperationProgressResult {
  const [operationProgress, setOperationProgress] =
    useState<OperationProgressState | null>(null);
  const [operationResults, setOperationResults] =
    useState<AccountOperationResultsState | null>(null);
  const showOperationResultsRef = useRef(showOperationResults);
  const operationResultMap = useRef<Map<number, AccountOperationResult>>(
    new Map(),
  );
  const operationProgressHideTimer = useRef<number | null>(null);
  const operationProgressFrame = useRef<number | null>(null);
  const operationProgressFlushTimer = useRef<number | null>(null);
  const lastOperationProgressFlushAt = useRef(0);
  const pendingOperationProgress = useRef<{
    title: string;
    event: BatchOperationEvent;
  } | null>(null);

  const closeOperationProgress = useCallback(() => {
    if (operationProgressHideTimer.current !== null) {
      window.clearTimeout(operationProgressHideTimer.current);
      operationProgressHideTimer.current = null;
    }
    setOperationProgress(null);
  }, []);

  const closeOperationResults = useCallback(() => {
    setOperationResults(null);
  }, []);

  useEffect(() => {
    showOperationResultsRef.current = showOperationResults;
    if (!showOperationResults) {
      setOperationResults(null);
    }
  }, [showOperationResults]);

  const scheduleOperationProgressClose = useCallback(() => {
    if (operationProgressHideTimer.current !== null) {
      window.clearTimeout(operationProgressHideTimer.current);
    }
    operationProgressHideTimer.current = window.setTimeout(() => {
      setOperationProgress(null);
      operationProgressHideTimer.current = null;
    }, 5000);
  }, []);

  const commitOperationProgressEvent = useCallback(
    (title: string, event: BatchOperationEvent) => {
      setOperationProgress((prev) => ({
        show: true,
        action: event.action,
        title,
        current: event.current ?? prev?.current ?? 0,
        total: event.total ?? prev?.total ?? 0,
        success: event.success ?? prev?.success ?? 0,
        failed: event.failed ?? prev?.failed ?? 0,
        banned: event.banned ?? prev?.banned ?? 0,
        rateLimited: event.rate_limited ?? prev?.rateLimited ?? 0,
        deleted: event.deleted ?? prev?.deleted ?? 0,
        done: event.type === "complete",
        message: event.error || event.message || prev?.message,
      }));
      if (event.type === "complete") {
        if (
          showOperationResultsRef.current &&
          (event.action === "batch_test" ||
            event.action === "batch_refresh")
        ) {
          setOperationResults({
            action: event.action,
            results: snapshotAccountOperationResults(
              operationResultMap.current,
            ),
          });
        }
        scheduleOperationProgressClose();
      }
    },
    [scheduleOperationProgressClose],
  );

  const flushOperationProgressEvent = useCallback(() => {
    operationProgressFrame.current = null;
    const pending = pendingOperationProgress.current;
    if (!pending) return;
    pendingOperationProgress.current = null;
    lastOperationProgressFlushAt.current = performance.now();
    commitOperationProgressEvent(pending.title, pending.event);
  }, [commitOperationProgressEvent]);

  const applyOperationProgressEvent = useCallback(
    (title: string, event: BatchOperationEvent) => {
      collectAccountOperationResult(operationResultMap.current, event);
      if (event.type === "start") {
        setOperationResults(null);
      }
      if (operationProgressHideTimer.current !== null) {
        window.clearTimeout(operationProgressHideTimer.current);
        operationProgressHideTimer.current = null;
      }

      pendingOperationProgress.current = { title, event };

      if (event.type === "complete") {
        if (operationProgressFrame.current !== null) {
          window.cancelAnimationFrame(operationProgressFrame.current);
          operationProgressFrame.current = null;
        }
        if (operationProgressFlushTimer.current !== null) {
          window.clearTimeout(operationProgressFlushTimer.current);
          operationProgressFlushTimer.current = null;
        }
        flushOperationProgressEvent();
        return;
      }

      if (
        operationProgressFrame.current === null &&
        operationProgressFlushTimer.current === null
      ) {
        const now = performance.now();
        const delay = Math.max(
          0,
          OPERATION_PROGRESS_FLUSH_INTERVAL_MS -
            (now - lastOperationProgressFlushAt.current),
        );
        if (delay > 0) {
          operationProgressFlushTimer.current = window.setTimeout(() => {
            operationProgressFlushTimer.current = null;
            operationProgressFrame.current = window.requestAnimationFrame(
              flushOperationProgressEvent,
            );
          }, delay);
          return;
        }
        operationProgressFrame.current = window.requestAnimationFrame(
          flushOperationProgressEvent,
        );
      }
    },
    [flushOperationProgressEvent],
  );

  useEffect(() => {
    return () => {
      if (operationProgressHideTimer.current !== null) {
        window.clearTimeout(operationProgressHideTimer.current);
      }
      if (operationProgressFrame.current !== null) {
        window.cancelAnimationFrame(operationProgressFrame.current);
      }
      if (operationProgressFlushTimer.current !== null) {
        window.clearTimeout(operationProgressFlushTimer.current);
      }
      pendingOperationProgress.current = null;
      operationResultMap.current.clear();
    };
  }, []);

  const runStreamingOperation = useCallback(
    async (
      path: string,
      body: unknown,
      title: string,
    ): Promise<BatchOperationEvent | null> => {
      let finalEvent: BatchOperationEvent | null = null;
      const res = await postAdminSSE(path, body);
      await readOperationSSE(res, (event) => {
        applyOperationProgressEvent(title, event);
        if (event.type === "complete") {
          finalEvent = event;
        }
      });
      return finalEvent;
    },
    [applyOperationProgressEvent],
  );

  return {
    operationProgress,
    operationResults,
    runStreamingOperation,
    reportOperationEvent: applyOperationProgressEvent,
    closeOperationProgress,
    closeOperationResults,
  };
}
