import { useCallback, useEffect, useState } from 'react'
import type { AccountGroup } from '../types'

const STORAGE_KEY = 'codex2api.importGroupIds'

function readStored(): number[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter((id): id is number => typeof id === 'number' && id > 0)
  } catch {
    return []
  }
}

/**
 * useImportGroupIds 记住「导入/添加账号时选择的分组」。
 *
 * 批量导入往往是同一批分组，每次都重选很烦；选择存在 localStorage 里，跨页面（Codex 账号页与
 * Grok 账号页）与刷新后都保持。分组被删掉后本地记录会失效，因此提供 prune 让调用方在拿到
 * 分组列表后剔除已不存在的 ID——否则提交时会被后端以「分组不存在」400 掉。
 */
export function useImportGroupIds(): {
  groupIds: number[]
  setGroupIds: (ids: number[]) => void
  prune: (groups: AccountGroup[]) => void
} {
  const [groupIds, setGroupIdsState] = useState<number[]>(readStored)

  useEffect(() => {
    try {
      if (groupIds.length === 0) localStorage.removeItem(STORAGE_KEY)
      else localStorage.setItem(STORAGE_KEY, JSON.stringify(groupIds))
    } catch {
      // localStorage 不可用（隐私模式等）时只是不记住，不影响功能
    }
  }, [groupIds])

  const setGroupIds = useCallback((ids: number[]) => {
    setGroupIdsState(ids.filter((id) => typeof id === 'number' && id > 0))
  }, [])

  const prune = useCallback((groups: AccountGroup[]) => {
    if (groups.length === 0) return
    const alive = new Set(groups.map((group) => group.id))
    setGroupIdsState((current) => {
      const next = current.filter((id) => alive.has(id))
      return next.length === current.length ? current : next
    })
  }, [])

  return { groupIds, setGroupIds, prune }
}
