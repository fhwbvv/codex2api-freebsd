export type CompactionBadgeKind = 'trigger' | 'history'

export interface CompactionBadgeState {
  compact?: boolean
  has_compaction_history?: boolean
}

export function getCompactionBadgeKinds(
  state: CompactionBadgeState,
): CompactionBadgeKind[] {
  const kinds: CompactionBadgeKind[] = []
  if (state.compact) kinds.push('trigger')
  if (state.has_compaction_history) kinds.push('history')
  return kinds
}
