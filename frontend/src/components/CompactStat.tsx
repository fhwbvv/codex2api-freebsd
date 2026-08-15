import { cn } from '../lib/utils'

export type CompactStatTone = 'neutral' | 'success' | 'warning' | 'danger'

const TONE_STYLE: Record<CompactStatTone, { chip: string; dot: string }> = {
  neutral: { chip: 'bg-muted text-muted-foreground', dot: 'bg-muted-foreground' },
  success: { chip: 'bg-[hsl(var(--success-bg))] text-[hsl(var(--success))]', dot: 'bg-[hsl(var(--success))]' },
  warning: { chip: 'bg-[hsl(var(--warning-bg))] text-[hsl(var(--warning))]', dot: 'bg-[hsl(var(--warning))]' },
  danger: { chip: 'bg-destructive/10 text-destructive', dot: 'bg-destructive' },
}

/**
 * 可点击的账号筛选统计磁贴（数值 + 状态芯片 + 可选明细列），
 * 从 Accounts/GrokAccounts 的两份本地实现合并而来。
 * 标签排版跟随 house 磁贴规格（uppercase tracking-wider 小标签）。
 */
export function CompactStat({
  label,
  chipLabel,
  value,
  tone,
  details,
  active = false,
  onClick,
}: {
  label: string
  chipLabel?: string
  value: number
  tone: CompactStatTone
  details?: Array<{ label: string; value: number }>
  active?: boolean
  onClick?: () => void
}) {
  const toneStyle = TONE_STYLE[tone]

  const className = cn(
    'flex min-h-[72px] w-full items-center justify-between gap-2 rounded-xl border px-2.5 py-2 text-left shadow-sm transition-[border-color,box-shadow,background-color,transform] duration-200 sm:min-h-[84px] sm:gap-3 sm:px-3 sm:py-2.5',
    active
      ? 'border-primary/40 bg-primary/5 ring-1 ring-primary/25 shadow-sm'
      : 'border-border bg-card/85 hover:border-border hover:bg-card',
    onClick &&
      'cursor-pointer hover:shadow-sm active:scale-[0.99] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50',
  )

  const content = (
    <>
      <div className="min-w-0">
        <div className="truncate text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
          {label}
        </div>
        <div className="mt-1.5 text-[22px] font-semibold leading-none tabular-nums tracking-tight text-foreground sm:text-[26px]">
          {value}
        </div>
      </div>
      <div className="flex min-h-[48px] shrink-0 flex-col items-end gap-1 sm:min-h-[54px] sm:gap-1.5">
        <div
          className={cn(
            'inline-flex items-center gap-1.5 rounded-full px-1.5 py-0.5 text-[11px] font-medium sm:px-2 sm:py-1 sm:text-[12px]',
            toneStyle.chip,
          )}
        >
          <span className={cn('size-1.5 rounded-full sm:size-1.5', toneStyle.dot)} />
          <span className="max-w-[4.5rem] truncate sm:max-w-none">{chipLabel ?? label}</span>
        </div>
        {details && details.length > 0 && (
          <div className="flex flex-col items-end gap-0.5 text-[11px] font-medium leading-4 text-muted-foreground">
            {details.map((item) => (
              <div
                key={item.label}
                className="grid grid-cols-[max-content_auto_max-content] items-center gap-x-0.5 whitespace-nowrap tabular-nums"
              >
                <span className="justify-self-start">{item.label}</span>
                <span className="justify-self-center">：</span>
                <span className="justify-self-end text-foreground">{item.value}</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </>
  )

  if (onClick) {
    return (
      <button type="button" onClick={onClick} aria-pressed={active} className={className}>
        {content}
      </button>
    )
  }

  return <div className={className}>{content}</div>
}
