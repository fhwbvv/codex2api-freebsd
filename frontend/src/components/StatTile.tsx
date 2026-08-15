import type { ReactNode } from 'react'
import { cn } from '../lib/utils'

export type StatTileTone = 'neutral' | 'info' | 'success' | 'warning' | 'danger'

const SURFACE_TONE: Record<StatTileTone, string> = {
  neutral: 'border-border bg-card/85',
  info: 'border-primary/25 bg-primary/[0.06]',
  success: 'border-emerald-500/25 bg-emerald-500/[0.07]',
  warning: 'border-amber-500/25 bg-amber-500/10',
  danger: 'border-destructive/20 bg-destructive/10',
}

const ICON_TONE: Record<StatTileTone, string> = {
  neutral: 'bg-muted/60 text-muted-foreground',
  info: 'bg-blue-500/10 text-blue-600 dark:bg-blue-500/20 dark:text-blue-300',
  success: 'bg-emerald-500/10 text-emerald-600 dark:bg-emerald-500/20 dark:text-emerald-300',
  warning: 'bg-amber-500/10 text-amber-600 dark:bg-amber-500/20 dark:text-amber-300',
  danger: 'bg-red-500/10 text-red-600 dark:bg-red-500/20 dark:text-red-300',
}

/**
 * 页面级 KPI 磁贴，统一各页各自手搓的统计磁贴。
 * 标签/数值排版跟随 Settings 页 StatusTile 的 house 规格
 * （uppercase tracking-wider 标签 + tabular-nums 数值）。
 * tone 默认只染图标芯片；toneSurface 时整块表面着色，用于健康态直陈。
 * 传 onClick 时渲染为可点击筛选磁贴（active 表示当前选中）。
 */
export function StatTile({
  label,
  value,
  sub,
  icon,
  tone = 'neutral',
  toneSurface = false,
  active = false,
  onClick,
  className,
}: {
  label: string
  value: ReactNode
  sub?: ReactNode
  icon?: ReactNode
  tone?: StatTileTone
  toneSurface?: boolean
  active?: boolean
  onClick?: () => void
  className?: string
}) {
  const surface = toneSurface ? SURFACE_TONE[tone] : 'border-border bg-card/85'
  const body = (
    <>
      <div className="flex items-center justify-between gap-3">
        <div className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
          {label}
        </div>
        {icon ? (
          <div className={cn('flex size-8 shrink-0 items-center justify-center rounded-lg', ICON_TONE[tone])}>
            {icon}
          </div>
        ) : null}
      </div>
      <div className={cn('mt-2 text-[20px] font-semibold tabular-nums text-foreground', icon && 'leading-none')}>
        {value}
      </div>
      {sub ? <div className="mt-1 text-[11px] leading-snug text-muted-foreground">{sub}</div> : null}
    </>
  )

  if (onClick) {
    return (
      <button
        type="button"
        onClick={onClick}
        aria-pressed={active}
        className={cn(
          'rounded-lg border px-3 py-2.5 text-left shadow-sm transition-colors',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50',
          active
            ? 'border-primary/40 bg-primary/5 ring-1 ring-primary/20'
            : cn(surface, 'hover:border-primary/25 hover:bg-muted/30'),
          className,
        )}
      >
        {body}
      </button>
    )
  }

  return <div className={cn('rounded-lg border px-3 py-2.5 shadow-sm', surface, className)}>{body}</div>
}
