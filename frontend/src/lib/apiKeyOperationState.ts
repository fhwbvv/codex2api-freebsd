export interface APIKeyBulkResetState {
  keyCount: number
  resettingAll: boolean
  resettingIds: ReadonlySet<number>
  deletingIds: ReadonlySet<number>
}

export function canStartAPIKeyBulkReset({
  keyCount,
  resettingAll,
  resettingIds,
  deletingIds,
}: APIKeyBulkResetState): boolean {
  return (
    keyCount > 0 &&
    !resettingAll &&
    resettingIds.size === 0 &&
    deletingIds.size === 0
  )
}
