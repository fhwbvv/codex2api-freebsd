import { Box, History } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import {
  getCompactionBadgeKinds,
  type CompactionBadgeKind,
} from '../lib/compactionBadges'

interface CompactionBadgesProps {
  compact?: boolean
  hasCompactionHistory?: boolean
}

const badgeStyles: Record<CompactionBadgeKind, string> = {
  trigger:
    'gap-0.5 whitespace-nowrap border-transparent bg-teal-500/12 text-[11px] font-semibold text-teal-700 dark:bg-teal-500/20 dark:text-teal-300',
  history:
    'gap-0.5 whitespace-nowrap border-transparent bg-violet-500/12 text-[11px] font-semibold text-violet-700 dark:bg-violet-500/20 dark:text-violet-300',
}

export default function CompactionBadges({
  compact,
  hasCompactionHistory,
}: CompactionBadgesProps) {
  const { t } = useTranslation()
  const kinds = getCompactionBadgeKinds({
    compact,
    has_compaction_history: hasCompactionHistory,
  })

  return kinds.map((kind) => (
    <Badge
      key={kind}
      variant="outline"
      className={badgeStyles[kind]}
      title={t(
        kind === 'trigger'
          ? 'usage.compactionTriggerTooltip'
          : 'usage.compactionHistoryTooltip',
      )}
    >
      {kind === 'trigger' ? (
        <Box className="size-3" />
      ) : (
        <History className="size-3" />
      )}
      {t(
        kind === 'trigger'
          ? 'usage.compactionTrigger'
          : 'usage.compactionHistory',
      )}
    </Badge>
  ))
}
