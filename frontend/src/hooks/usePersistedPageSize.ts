import { useCallback, useEffect, useState } from 'react'

const STORAGE_PREFIX = 'codex2api_page_size_'

/**
 * usePersistedPageSize 提供按 key 持久化的分页大小状态。
 *
 * - 初值优先使用 localStorage 中保存的值;若不在 allowed 中或非法则回落 fallback。
 * - 调用 setPageSize 时同步写回 localStorage。
 *
 * @param key       页面/表格的标识(写入 localStorage 时会加 codex2api_page_size_ 前缀)
 * @param fallback  默认值
 * @param allowed   合法 pageSize 集合;不在其中的持久化值会被忽略
 */
export function usePersistedPageSize(
  key: string,
  fallback: number,
  allowed: number[],
): [number, (next: number) => void] {
  const storageKey = `${STORAGE_PREFIX}${key}`

  const readInitial = (): number => {
    if (typeof window === 'undefined') return fallback
    try {
      const raw = window.localStorage.getItem(storageKey)
      if (!raw) return fallback
      const parsed = Number.parseInt(raw, 10)
      if (Number.isFinite(parsed) && allowed.includes(parsed)) {
        return parsed
      }
    } catch {
      /* localStorage 不可用时静默回落 */
    }
    return fallback
  }

  const [pageSize, setPageSizeState] = useState<number>(readInitial)

  // allowed 变化时,如果当前值不再合法则回落到 fallback
  useEffect(() => {
    if (!allowed.includes(pageSize)) {
      setPageSizeState(fallback)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [allowed.join(',')])

  const setPageSize = useCallback(
    (next: number) => {
      if (!allowed.includes(next)) return
      setPageSizeState(next)
      try {
        window.localStorage.setItem(storageKey, String(next))
      } catch {
        /* 静默 */
      }
    },
    [storageKey, allowed],
  )

  return [pageSize, setPageSize]
}

/** 全站统一的可选分页大小,可在调用方按需裁剪。 */
export const DEFAULT_PAGE_SIZE_OPTIONS = [10, 20, 50, 100, 200, 500]
