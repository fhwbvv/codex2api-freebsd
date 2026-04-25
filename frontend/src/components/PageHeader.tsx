import type { ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'

interface PageHeaderProps {
  title: string
  description?: string
  onRefresh?: () => void
  refreshLabel?: string
  actions?: ReactNode
}

export default function PageHeader({
  title,
  description,
  onRefresh,
  refreshLabel,
  actions,
}: PageHeaderProps) {
  const { t } = useTranslation()
  const hasActions = Boolean(onRefresh) || Boolean(actions)
  const resolvedRefreshLabel = refreshLabel ?? t('common.refresh')

  return (
    <div className="flex items-end justify-between gap-5 mb-6 max-sm:flex-col max-sm:items-stretch">
      <div className="max-w-[760px]">
        <h2 className="text-2xl font-semibold leading-tight text-foreground sm:text-[28px]">
          {title}
        </h2>
        {description ? (
          <p className="mt-2 max-w-[640px] text-muted-foreground text-sm leading-relaxed">
            {description}
          </p>
        ) : null}
      </div>
      {hasActions ? (
        <div className="flex flex-wrap justify-end gap-2 items-center max-sm:w-full max-sm:justify-start">
          {onRefresh ? (
            <Button variant="outline" onClick={onRefresh} className="max-sm:w-full">
              <RefreshCw className="size-3.5" />
              {resolvedRefreshLabel}
            </Button>
          ) : null}
          {actions}
        </div>
      ) : null}
    </div>
  )
}
