import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { CircleHelp } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'
import type { AccountRow } from '../types'
import { selectPoolRunway, type PoolRunway } from '../lib/poolRunway'
import { formatBeijingTime } from '../utils/time'

interface PoolRunwayCardProps {
  accounts: AccountRow[]
  currentRpm?: number
  rpmLimit?: number
  avgDurationMs?: number
  className?: string
}

export default function PoolRunwayCard({
  accounts,
  currentRpm = 0,
  rpmLimit = 0,
  avgDurationMs = 0,
  className = '',
}: PoolRunwayCardProps) {
  const { t } = useTranslation()
  const [nowMs, setNowMs] = useState(() => Date.now())
  useEffect(() => {
    setNowMs(Date.now())
    const timer = window.setInterval(() => setNowMs(Date.now()), 60_000)
    return () => window.clearInterval(timer)
  }, [accounts, currentRpm, rpmLimit, avgDurationMs])

  const runway = useMemo(
    () => selectPoolRunway(accounts, nowMs, currentRpm, rpmLimit, avgDurationMs),
    [accounts, avgDurationMs, currentRpm, nowMs, rpmLimit],
  )

  const display = formatRunwayDisplay(runway, t)
  const stateText = formatRunwayState(runway, t)
  const adviceText = formatRunwayAdvice(runway, t)
  const palette = riskPalette(runway.riskLevel)
  const forecast = runway.forecast
  const etaText = runway.pressureAt
    ? formatBeijingTime(new Date(runway.pressureAt).toISOString()).slice(5, 16)
    : t('dashboard.poolRunwayEtaNone')
  const rpmText = forecast.effectiveRpmLimit > 0
    ? `${Math.round(forecast.rpm)}/${Math.round(forecast.effectiveRpmLimit)}`
    : `${Math.round(forecast.rpm)}`

  return (
    <div
      className={cn(
        'relative overflow-hidden rounded-2xl border border-border/80 bg-card p-4 shadow-sm sm:p-5',
        className,
      )}
    >
      {/* soft risk wash — same language as dashboard hero */}
      <div
        aria-hidden
        className={cn(
          'pointer-events-none absolute inset-0 opacity-90',
          palette.wash,
        )}
      />

      <div className="relative z-10 flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        {/* Primary */}
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <div className="text-[11px] font-bold uppercase tracking-wide text-muted-foreground">
              {t('dashboard.poolRunwayTitle')}
            </div>
            <TooltipProvider delayDuration={200}>
              <Tooltip>
                <TooltipTrigger asChild>
                  <button
                    type="button"
                    className="inline-flex size-4 items-center justify-center rounded-full text-muted-foreground/80 transition-colors hover:bg-muted hover:text-foreground"
                    aria-label={t('dashboard.poolRunwayHelp')}
                  >
                    <CircleHelp className="size-3.5" />
                  </button>
                </TooltipTrigger>
                <TooltipContent side="top" sideOffset={6} className="max-w-[300px] whitespace-normal text-left leading-relaxed">
                  {t('dashboard.poolRunwayHelpBody')}
                </TooltipContent>
              </Tooltip>
            </TooltipProvider>
            <span className="rounded-full bg-muted/70 px-2 py-0.5 text-[10px] font-medium tabular-nums text-muted-foreground">
              {runway.windowKey}
            </span>
            {runway.lowConfidence ? (
              <span className="rounded-full bg-amber-500/10 px-2 py-0.5 text-[10px] font-medium text-amber-700 dark:text-amber-300">
                {t('accounts.pressureForecastLowConfidence', {
                  percent: Math.round(forecast.confidence * 100),
                })}
              </span>
            ) : null}
          </div>

          <div className="mt-2 flex flex-wrap items-end gap-x-3 gap-y-1">
            {display.kind === 'split' ? (
              <>
                <div className={cn('text-3xl font-bold tabular-nums tracking-tight sm:text-4xl', palette.fg)}>
                  {display.primary}
                </div>
                <div className="pb-1 text-sm font-medium text-muted-foreground">
                  {display.secondary}
                </div>
              </>
            ) : (
              <div className={cn('text-3xl font-bold tracking-tight sm:text-4xl', palette.fg)}>
                {display.primary}
              </div>
            )}
          </div>

          <div className="mt-2.5 flex flex-wrap items-center gap-2">
            <span
              className={cn(
                'inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-semibold',
                palette.pill,
              )}
            >
              <span className={cn('size-1.5 rounded-full', palette.dot)} />
              {stateText}
            </span>
            {adviceText && runway.adviceCode !== 'ok' ? (
              <span className="text-xs text-muted-foreground">{adviceText}</span>
            ) : null}
          </div>
        </div>

        {/* Secondary metrics — chips, not mini-cards */}
        <div className="flex flex-wrap gap-2 lg:max-w-md lg:justify-end">
          <Chip label={t('dashboard.poolRunwayEta')} value={etaText} emphasize={Boolean(runway.pressureAt)} />
          <Chip label={t('dashboard.poolRunwayDispatchable')} value={String(forecast.dispatchableAccounts)} />
          <Chip label={t('dashboard.poolRunwayRpm')} value={rpmText} />
          <Chip
            label={t('dashboard.poolRunwaySampled')}
            value={
              forecast.unknown > 0
                ? `${forecast.sampled} · ${forecast.unknown}?`
                : String(forecast.sampled)
            }
          />
        </div>
      </div>
    </div>
  )
}

function Chip({
  label,
  value,
  emphasize = false,
}: {
  label: string
  value: string
  emphasize?: boolean
}) {
  return (
    <div className="inline-flex min-w-[5.5rem] flex-col rounded-xl border border-border/60 bg-background/65 px-3 py-2 backdrop-blur-[2px]">
      <span className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
        {label}
      </span>
      <span
        className={cn(
          'mt-0.5 text-sm font-semibold tabular-nums',
          emphasize ? 'text-foreground' : 'text-foreground/90',
        )}
      >
        {value}
      </span>
    </div>
  )
}

function riskPalette(level: PoolRunway['riskLevel']) {
  if (level === 'high') {
    return {
      wash: 'bg-[radial-gradient(ellipse_at_top_left,color-mix(in_oklab,var(--color-destructive)_14%,transparent),transparent_55%)]',
      fg: 'text-destructive',
      pill: 'bg-destructive/12 text-destructive',
      dot: 'bg-destructive',
    }
  }
  if (level === 'medium') {
    return {
      wash: 'bg-[radial-gradient(ellipse_at_top_left,color-mix(in_oklab,#f59e0b_14%,transparent),transparent_55%)]',
      fg: 'text-amber-600 dark:text-amber-400',
      pill: 'bg-amber-500/12 text-amber-700 dark:text-amber-300',
      dot: 'bg-amber-500',
    }
  }
  return {
    wash: 'bg-[radial-gradient(ellipse_at_top_left,color-mix(in_oklab,#22c55e_12%,transparent),transparent_55%)]',
    fg: 'text-emerald-600 dark:text-emerald-400',
    pill: 'bg-emerald-500/12 text-emerald-700 dark:text-emerald-300',
    dot: 'bg-emerald-500',
  }
}

type RunwayDisplay =
  | { kind: 'split'; primary: string; secondary: string }
  | { kind: 'text'; primary: string }

function formatRunwayDisplay(
  runway: PoolRunway,
  t: (key: string, options?: Record<string, unknown>) => string,
): RunwayDisplay {
  switch (runway.kind) {
    case 'hours':
      return {
        kind: 'split',
        primary: String(runway.remainingHours ?? 1),
        secondary: t('dashboard.poolRunwayHoursUnit'),
      }
    case 'day_plus':
      return { kind: 'text', primary: t('dashboard.poolRunwayDayPlus') }
    case 'critical':
      return { kind: 'text', primary: t('dashboard.poolRunwayCritical') }
    case 'stable':
      return { kind: 'text', primary: t('dashboard.poolRunwayStable') }
    default:
      return { kind: 'text', primary: t('dashboard.poolRunwayUnknown') }
  }
}

function formatRunwayState(
  runway: PoolRunway,
  t: (key: string, options?: Record<string, unknown>) => string,
): string {
  switch (runway.state) {
    case 'shortage':
      return t('accounts.pressureForecastShortage')
    case 'high':
      return t('accounts.pressureForecastHigh')
    case 'limit_risk':
      return t('accounts.pressureForecastLimitRisk')
    default:
      return t('accounts.pressureForecastStable')
  }
}

function formatRunwayAdvice(
  runway: PoolRunway,
  t: (key: string, options?: Record<string, unknown>) => string,
): string {
  switch (runway.adviceCode) {
    case 'critical':
      return runway.suggestedAddAccounts > 0
        ? t('dashboard.poolRunwayAdviceCriticalAdd', { count: runway.suggestedAddAccounts })
        : t('dashboard.poolRunwayAdviceCriticalLoad')
    case 'add_accounts':
      return t('dashboard.poolRunwayAdviceAdd', { count: runway.suggestedAddAccounts })
    case 'reduce_load':
      return t('dashboard.poolRunwayAdviceReduceLoad')
    case 'low_confidence':
      return t('dashboard.poolRunwayAdviceLowConfidence')
    default:
      return ''
  }
}
