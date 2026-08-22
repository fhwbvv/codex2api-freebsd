export const ACCOUNT_LIST_FULL_POOL_SCAN_MAX = 500

export const LARGE_POOL_DISABLED_SORTS = ["requests", "today"] as const

export function toAccountListApiSort(
  sortKey: string | null | undefined,
): string | undefined {
  if (!sortKey) return undefined
  if (sortKey === "importTime") return "created_at"
  if (sortKey === "schedulerPriority") return "scheduler_priority"
  if (sortKey === "updated") return "updated_at"
  return sortKey
}

export function resolveDisabledAccountSorts(
  disabledSorts?: string[] | null,
  poolTotal?: number,
): string[] {
  // 空数组是后端明确说「已聚完,可以排序」。不能再按池子规模回退,
  // 否则分批查询完成后前端仍会永远禁用请求(7d)/今日排序。
  if (Array.isArray(disabledSorts)) {
    return disabledSorts
  }
  if ((poolTotal ?? 0) > ACCOUNT_LIST_FULL_POOL_SCAN_MAX) {
    return [...LARGE_POOL_DISABLED_SORTS]
  }
  return []
}

export function isLargePoolSortDisabled(
  sortKey: string | null | undefined,
  disabledSorts?: string[] | null,
): boolean {
  const apiSort = toAccountListApiSort(sortKey)
  if (!apiSort) return false
  return (disabledSorts ?? []).includes(apiSort)
}
