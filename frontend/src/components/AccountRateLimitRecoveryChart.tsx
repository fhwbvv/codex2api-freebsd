import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Bar,
  CartesianGrid,
  Cell,
  ComposedChart,
  Line,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import { CircleHelp, TimerReset } from 'lucide-react'
import { Card, CardContent } from '@/components/ui/card'
import { Tooltip as UITooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import type { AccountRow } from '../types'
import { formatBeijingTime } from '../utils/time'
import {
  buildPoolRunway,
  estimatePressureForecast,
  hasBurnPrediction,
  isPremiumUsagePlan,
  isWindowRateLimitLike,
  type PressureForecast,
  type RecoveryWindow,
} from '../lib/poolRunway'

interface AccountRateLimitRecoveryChartProps {
  accounts: AccountRow[]
  currentRpm?: number
  rpmLimit?: number
  avgDurationMs?: number
  className?: string
  compact?: boolean
}

interface RecoveryCandidate {
  id: number
  label: string
  recoveryAt: number
  secondsUntil: number
  reason: RecoveryReason
}

type RecoveryReason = '5h' | '7d' | 'cooldown'
type RecoveryViewMode = 'recovery' | 'reset'

interface RecoveryGroup {
  key: string
  startAt: number
  endAt: number
  label: string
  fullLabel: string
  count: number
  fill: string
}

interface ResetCandidate {
  id: number
  label: string
  resetAt: number
}

interface ResetStats {
  candidates: ResetCandidate[]
  points: RecoveryGroup[]
  total: number
  unknown: number
}

const recoveryWindows: RecoveryWindow[] = ['5h', '7d']
const recoveryViewModes: RecoveryViewMode[] = ['recovery', 'reset']
const recoveryReasonFill: Record<RecoveryReason, string> = {
  '5h': 'var(--color-primary)',
  '7d': 'hsl(30 82% 44%)',
  cooldown: 'hsl(var(--info))',
}

const chartMargin = { top: 8, right: 8, left: -18, bottom: 0 }
const resetChartMargin = { top: 8, right: 8, left: -4, bottom: 0 }
const gridColor = 'var(--color-border)'
const axisColor = 'var(--color-muted-foreground)'
const tooltipContentStyle = {
  backgroundColor: 'var(--color-card)',
  border: '1px solid var(--color-border)',
  borderRadius: '12px',
  boxShadow: '0 18px 40px rgba(0, 0, 0, 0.12)',
}
const tooltipLabelStyle = { color: 'var(--color-foreground)', fontWeight: 600 }
const tooltipItemStyle = { color: 'var(--color-foreground)' }

export default function AccountRateLimitRecoveryChart({ accounts, currentRpm = 0, rpmLimit = 0, avgDurationMs = 0, className = '', compact = false }: AccountRateLimitRecoveryChartProps) {
  const { t } = useTranslation()
  const [nowMs, setNowMs] = useState(() => Date.now())
  const [windowKey, setWindowKey] = useState<RecoveryWindow>('5h')
  const [viewMode, setViewMode] = useState<RecoveryViewMode>('recovery')

  useEffect(() => {
    const timer = window.setInterval(() => setNowMs(Date.now()), 60_000)
    return () => window.clearInterval(timer)
  }, [])

  const recovery = useMemo(() => {
    const candidates: RecoveryCandidate[] = []
    let unknown = 0

    for (const account of accounts) {
      const candidate = getAccountRecoveryCandidate(account, nowMs, windowKey)
      if (candidate) {
        candidates.push(candidate)
      } else if (isWindowRateLimitLike(account, windowKey)) {
        unknown += 1
      }
    }

    candidates.sort((a, b) => a.recoveryAt - b.recoveryAt)

    return {
      candidates,
      points: createRecoveryPoints(candidates, windowKey, nowMs),
      unknown,
      forecast: estimatePressureForecast(accounts, windowKey, nowMs, currentRpm, rpmLimit, avgDurationMs),
    }
  }, [accounts, avgDurationMs, currentRpm, nowMs, rpmLimit, windowKey])

  const resetStats = useMemo(() => createResetStats(accounts, nowMs), [accounts, nowMs])
  const limitedTotal = recovery.candidates.length + recovery.unknown
  const nextRecovery = recovery.candidates[0]
  const nextReset = resetStats.candidates[0]
  const chartPoints = viewMode === 'recovery' ? recovery.points : resetStats.points
  const yAxisConfig = getCountAxisConfig(chartPoints)
  const currentTitle = viewMode === 'recovery' ? t('accounts.recoveryDistributionTitle') : t('accounts.quotaResetDistributionTitle')
  const currentDescription = viewMode === 'recovery'
    ? t('accounts.recoveryDistributionDesc', {
      recoverable: recovery.candidates.length,
      limited: limitedTotal,
    })
    : t('accounts.quotaResetDistributionDesc', {
      known: resetStats.candidates.length,
      total: resetStats.total,
    })

  return (
    <Card className={`${compact ? 'lg:h-[430px]' : 'mb-4'} py-0 ${className}`}>
      <CardContent className={compact ? 'flex h-full flex-col p-4' : 'p-4 sm:p-5'}>
        <div className="mb-2 flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <TimerReset className="size-4 text-primary" />
              <h3 className="text-base font-semibold text-foreground">{currentTitle}</h3>
            </div>
            <p className="mt-1 text-sm text-muted-foreground">
              {currentDescription}
            </p>
          </div>
          <div className="flex flex-wrap justify-end gap-2">
            <div className="inline-flex rounded-lg border border-border bg-muted/50 p-0.5">
              {recoveryViewModes.map((mode) => (
                <button
                  key={mode}
                  type="button"
                  onClick={() => setViewMode(mode)}
                  className={`rounded-md px-2.5 py-1.5 text-xs font-medium transition-all ${
                    viewMode === mode
                      ? 'border border-border bg-background text-foreground shadow-sm'
                      : 'text-muted-foreground hover:text-foreground'
                  }`}
                >
                  {t(mode === 'recovery' ? 'accounts.recoveryModeRecovery' : 'accounts.recoveryModeReset')}
                </button>
              ))}
            </div>
            {viewMode === 'recovery' ? (
              <div className="inline-flex rounded-lg border border-border bg-muted/50 p-0.5">
                {recoveryWindows.map((key) => (
                  <button
                    key={key}
                    type="button"
                    onClick={() => setWindowKey(key)}
                    className={`rounded-md px-3 py-1.5 text-xs font-medium transition-all ${
                      windowKey === key
                        ? 'border border-border bg-background text-foreground shadow-sm'
                        : 'text-muted-foreground hover:text-foreground'
                    }`}
                  >
                    {key}
                  </button>
                ))}
              </div>
            ) : (
              <div className="inline-flex rounded-lg border border-border bg-muted/50 p-0.5">
                <span className="rounded-md border border-border bg-background px-3 py-1.5 text-xs font-medium text-foreground shadow-sm">
                  7d
                </span>
              </div>
            )}
          </div>
        </div>

        <div className={compact ? 'mb-3 grid grid-cols-2 gap-2 sm:grid-cols-4' : 'mb-4 grid grid-cols-2 gap-2 sm:grid-cols-4'}>
          {viewMode === 'recovery' ? (
            <>
              <RecoveryMetric label={t('accounts.recoveryLimitedTotal')} value={limitedTotal} tone={limitedTotal > 0 ? 'warning' : 'success'} compact={compact} />
              <RecoveryMetric label={t('accounts.recoveryRecoverable')} value={recovery.candidates.length} compact={compact} />
              <RecoveryMetric label={t('accounts.recoveryNext')} value={nextRecovery ? formatChartTime(nextRecovery.recoveryAt) : '-'} tone={nextRecovery ? 'success' : 'neutral'} compact={compact} />
              <RecoveryMetric label={t('accounts.recoveryUnknown')} value={recovery.unknown} tone={recovery.unknown > 0 ? 'warning' : 'neutral'} compact={compact} />
            </>
          ) : (
            <>
              <RecoveryMetric label={t('accounts.quotaResetTotal')} value={resetStats.total} compact={compact} />
              <RecoveryMetric label={t('accounts.quotaResetKnown')} value={resetStats.candidates.length} compact={compact} />
              <RecoveryMetric label={t('accounts.quotaResetNext')} value={nextReset ? formatChartTime(nextReset.resetAt) : '-'} tone={nextReset ? 'success' : 'neutral'} compact={compact} />
              <RecoveryMetric label={t('accounts.quotaResetUnknown')} value={resetStats.unknown} tone={resetStats.unknown > 0 ? 'warning' : 'neutral'} compact={compact} />
            </>
          )}
        </div>

        <div className={compact ? 'grid min-h-0 flex-1 grid-rows-[200px_auto] gap-3 lg:grid-rows-[minmax(116px,1fr)_94px]' : 'grid gap-3'}>
          <div className={compact ? 'min-h-0' : 'h-[260px]'}>
            <ResponsiveContainer width="100%" height="100%">
              <ComposedChart data={chartPoints} margin={viewMode === 'reset' ? resetChartMargin : chartMargin}>
                <CartesianGrid vertical={false} stroke={gridColor} strokeDasharray="4 4" />
                <XAxis
                  dataKey="label"
                  tick={{ fill: axisColor, fontSize: compact ? 11 : 12 }}
                  axisLine={{ stroke: gridColor }}
                  tickLine={{ stroke: gridColor }}
                  tickMargin={6}
                  minTickGap={compact ? 4 : 8}
                  interval={0}
                />
                <YAxis
                  tick={{ fill: axisColor, fontSize: compact ? 11 : 12 }}
                  axisLine={{ stroke: gridColor }}
                  tickLine={{ stroke: gridColor }}
                  allowDecimals={false}
                  domain={yAxisConfig.domain}
                  ticks={yAxisConfig.ticks}
                  tickFormatter={(value) => String(Math.round(Number(value)))}
                  width={viewMode === 'reset' ? (compact ? 44 : 50) : (compact ? 34 : 44)}
                />
                <Tooltip
                  formatter={(value) => [t('accounts.recoveryTooltipCount', { count: Number(value) }), t('accounts.recoveryAccountCount')]}
                  labelFormatter={(_, payload) => {
                    const point = payload?.[0]?.payload as RecoveryGroup | undefined
                    return t(viewMode === 'recovery' ? 'accounts.recoveryTooltipTime' : 'accounts.quotaResetTooltipTime', { time: point?.fullLabel ?? '' })
                  }}
                  contentStyle={tooltipContentStyle}
                  labelStyle={tooltipLabelStyle}
                  itemStyle={tooltipItemStyle}
                />
                <Bar
                  dataKey="count"
                  name={t('accounts.recoveryAccountCount')}
                  radius={[6, 6, 0, 0]}
                  maxBarSize={compact ? 34 : 46}
                >
                  {chartPoints.map((entry) => (
                    <Cell key={entry.key} fill={entry.fill} />
                  ))}
                </Bar>
                {viewMode === 'reset' ? (
                  <Line
                    type="monotone"
                    dataKey="count"
                    name={t('accounts.quotaResetTrend')}
                    stroke="var(--color-foreground)"
                    strokeWidth={2.5}
                    dot={{ r: 3, fill: 'var(--color-card)', stroke: 'var(--color-foreground)', strokeWidth: 2 }}
                    activeDot={{ r: 5 }}
                  />
                ) : null}
              </ComposedChart>
            </ResponsiveContainer>
          </div>
          {viewMode === 'recovery'
            ? <PressureForecastCard forecast={recovery.forecast} windowKey={windowKey} nowMs={nowMs} t={t} />
            : <QuotaResetSummaryCard stats={resetStats} t={t} />}
        </div>
      </CardContent>
    </Card>
  )
}

function RecoveryMetric({ label, value, tone = 'neutral', compact = false }: { label: string; value: number | string; tone?: 'neutral' | 'warning' | 'danger' | 'success'; compact?: boolean }) {
  const toneClass = {
    neutral: 'text-foreground',
    warning: 'text-amber-600 dark:text-amber-400',
    danger: 'text-red-600 dark:text-red-400',
    success: 'text-emerald-600 dark:text-emerald-400',
  }[tone]

  return (
    <div className={`min-w-0 rounded-lg border border-border bg-muted/20 ${compact ? 'px-2.5 py-1.5' : 'px-3 py-2.5'}`}>
      <div className="truncate text-[11px] font-medium text-muted-foreground">{label}</div>
      <div className={`${compact ? 'mt-0.5 text-base' : 'mt-1 text-lg'} font-semibold ${toneClass}`}>{value}</div>
    </div>
  )
}

function PressureForecastCard({
  forecast,
  windowKey,
  nowMs,
  t,
}: {
  forecast: PressureForecast
  windowKey: RecoveryWindow
  nowMs: number
  t: (key: string, options?: Record<string, unknown>) => string
}) {
  const runway = buildPoolRunway(forecast, nowMs, windowKey)
  const pressureAt = runway.pressureAt
  const predictedText = pressureAt
    ? formatChartTime(pressureAt)
    : t('accounts.pressureForecastNone')
  const stateText = runway.state === 'shortage'
    ? t('accounts.pressureForecastShortage')
    : runway.state === 'high'
      ? t('accounts.pressureForecastHigh')
      : runway.state === 'limit_risk'
        ? t('accounts.pressureForecastLimitRisk')
        : t('accounts.pressureForecastStable')
  const runwayText = formatRunwayLabel(runway, t)
  const adviceText = formatRunwayAdvice(runway, t)
  const pillClass = forecast.riskLevel === 'high'
    ? 'bg-destructive/12 text-destructive'
    : forecast.riskLevel === 'medium'
      ? 'bg-amber-500/12 text-amber-700 dark:text-amber-300'
      : 'bg-emerald-500/12 text-emerald-700 dark:text-emerald-300'
  const dotClass = forecast.riskLevel === 'high'
    ? 'bg-destructive'
    : forecast.riskLevel === 'medium'
      ? 'bg-amber-500'
      : 'bg-emerald-500'
  const lowConfidence = runway.lowConfidence
  const logicText = t('accounts.pressureForecastLogic')
  const metaParts = [
    `RPM ${formatWholeNumber(forecast.rpm)}${forecast.effectiveRpmLimit > 0 ? `/${formatWholeNumber(forecast.effectiveRpmLimit)}` : ''}`,
    t('dashboard.poolRunwayDispatchable') + ' ' + forecast.dispatchableAccounts,
    `${forecast.sampled}${forecast.unknown > 0 ? `+${forecast.unknown}?` : ''} samples`,
  ]

  return (
    <TooltipProvider>
      <div className="min-h-0 overflow-hidden rounded-xl border border-border/70 bg-background/40 px-3 py-2.5">
        <div className="flex items-center justify-between gap-3">
          <div className="min-w-0 flex items-center gap-2">
            <span className="text-xs font-semibold text-foreground">
              {t('accounts.pressureForecastTitle')}
            </span>
            <UITooltip>
              <TooltipTrigger asChild>
                <button
                  type="button"
                  className="inline-flex size-4 shrink-0 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                  aria-label={t('accounts.pressureForecastHelp')}
                >
                  <CircleHelp className="size-3.5" />
                </button>
              </TooltipTrigger>
              <TooltipContent side="top" sideOffset={6} className="max-w-[340px] whitespace-normal text-left leading-relaxed">
                {logicText}
              </TooltipContent>
            </UITooltip>
            {lowConfidence ? (
              <span className="rounded-full bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-medium text-amber-700 dark:text-amber-300">
                {t('accounts.pressureForecastLowConfidence', {
                  percent: Math.round(forecast.confidence * 100),
                })}
              </span>
            ) : null}
          </div>
          <div className="shrink-0 text-right text-[11px] text-muted-foreground">
            <span className="font-medium">{t('accounts.pressureForecastEta')}</span>
            {' '}
            <span className="font-semibold tabular-nums text-foreground">{predictedText}</span>
            <span className="ml-1.5 text-muted-foreground/70">{windowKey}</span>
          </div>
        </div>

        <div className="mt-2 flex flex-wrap items-center gap-2">
          <span className="text-lg font-bold tabular-nums tracking-tight text-foreground">
            {runwayText}
          </span>
          <span className={`inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[11px] font-semibold ${pillClass}`}>
            <span className={`size-1.5 rounded-full ${dotClass}`} />
            {stateText}
          </span>
        </div>

        <div className="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[11px] text-muted-foreground">
          {metaParts.map((part, i) => (
            <span key={part} className="inline-flex items-center gap-2">
              {i > 0 ? <span className="text-border">·</span> : null}
              {part}
            </span>
          ))}
        </div>
        {adviceText ? (
          <div className="mt-1 truncate text-[11px] text-muted-foreground" title={adviceText}>
            {adviceText}
          </div>
        ) : null}
      </div>
    </TooltipProvider>
  )
}

function formatRunwayLabel(
  runway: ReturnType<typeof buildPoolRunway>,
  t: (key: string, options?: Record<string, unknown>) => string,
): string {
  switch (runway.kind) {
    case 'critical':
      return t('accounts.poolRunwayCritical')
    case 'hours':
      return t('accounts.poolRunwayHours', { hours: runway.remainingHours ?? 1 })
    case 'day_plus':
      return t('accounts.poolRunwayDayPlus')
    case 'stable':
      return t('accounts.poolRunwayStable')
    default:
      return t('accounts.poolRunwayUnknown')
  }
}

function formatRunwayAdvice(
  runway: ReturnType<typeof buildPoolRunway>,
  t: (key: string, options?: Record<string, unknown>) => string,
): string {
  switch (runway.adviceCode) {
    case 'critical':
      return runway.suggestedAddAccounts > 0
        ? t('accounts.poolRunwayAdviceCriticalAdd', { count: runway.suggestedAddAccounts })
        : t('accounts.poolRunwayAdviceCriticalLoad')
    case 'add_accounts':
      return t('accounts.poolRunwayAdviceAdd', { count: runway.suggestedAddAccounts })
    case 'reduce_load':
      return t('accounts.poolRunwayAdviceReduceLoad')
    case 'low_confidence':
      return t('accounts.poolRunwayAdviceLowConfidence')
    default:
      return ''
  }
}

function QuotaResetSummaryCard({ stats, t }: { stats: ResetStats; t: (key: string, options?: Record<string, unknown>) => string }) {
  const nextReset = stats.candidates[0]
  const nextText = nextReset
    ? formatChartTime(nextReset.resetAt)
    : t('accounts.quotaResetSummaryNone')
  const futureCount = stats.points.reduce((sum, point) => sum + point.count, 0)
  const tone = nextReset ? 'text-emerald-600 dark:text-emerald-400' : 'text-muted-foreground'
  const descText = t('accounts.quotaResetSummaryDesc', {
    count: futureCount,
    known: stats.candidates.length,
    total: stats.total,
    unknown: stats.unknown,
  })

  return (
    <div className="min-h-0 overflow-hidden rounded-lg border border-border bg-muted/20 px-3 py-2">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="text-xs font-semibold text-foreground">{t('accounts.quotaResetSummaryTitle')}</div>
          <div className="mt-1 truncate text-[11px] text-muted-foreground" title={descText}>
            {descText}
          </div>
        </div>
        <div className="shrink-0 text-right">
          <div className="text-[11px] font-medium text-muted-foreground">{t('accounts.quotaResetNext')}</div>
          <div className={`text-sm font-semibold ${tone}`}>{nextText}</div>
        </div>
      </div>
      <div className="mt-1 truncate text-[11px] text-muted-foreground">
        {t('accounts.quotaResetSummaryKnown', {
          known: stats.candidates.length,
          total: stats.total,
          unknown: stats.unknown,
        })}
      </div>
    </div>
  )
}

function getAccountRecoveryCandidate(account: AccountRow, nowMs: number, windowKey: RecoveryWindow): RecoveryCandidate | null {
  const reset5h = futureTimestamp(account.reset_5h_at, nowMs)
  const reset7d = futureTimestamp(account.reset_7d_at, nowMs)
  const cooldownUntil = futureTimestamp(account.cooldown_until, nowMs)

  if (windowKey === '5h') {
    if (isPremiumUsagePlan(account.plan_type) && isUsageExhausted(account.usage_percent_5h) && reset5h) {
      return buildRecoveryCandidate(account, reset5h, nowMs, '5h')
    }
    if (cooldownUntil && isShortRateLimitLike(account)) {
      return buildRecoveryCandidate(account, cooldownUntil, nowMs, 'cooldown')
    }
    return null
  }

  if (isUsageExhausted(account.usage_percent_7d) && reset7d) {
    return buildRecoveryCandidate(account, reset7d, nowMs, '7d')
  }
  return null
}

function buildRecoveryCandidate(account: AccountRow, recoveryAt: number, nowMs: number, reason: RecoveryReason): RecoveryCandidate {
  return {
    id: account.id,
    label: account.email || account.name || `ID ${account.id}`,
    recoveryAt,
    secondsUntil: Math.max(0, Math.ceil((recoveryAt - nowMs) / 1000)),
    reason,
  }
}

function isShortRateLimitLike(account: AccountRow): boolean {
  const status = (account.status || '').toLowerCase()
  const reason = (account.cooldown_reason || '').toLowerCase()
  if (status === 'rate_limited' || status === 'rate_limited_5h' || status === 'cooldown') {
    return true
  }
  if (reason === 'rate_limited' || reason === 'rate_limited_5h') {
    return true
  }
  return false
}

function createResetStats(accounts: AccountRow[], nowMs: number): ResetStats {
  const candidates: ResetCandidate[] = []
  let total = 0
  let unknown = 0

  for (const account of accounts) {
    if (!hasBurnPrediction(account, '7d')) {
      continue
    }
    total += 1
    const resetAt = futureTimestamp(account.reset_7d_at, nowMs)
    if (!resetAt) {
      unknown += 1
      continue
    }
    candidates.push({
      id: account.id,
      label: account.email || account.name || `ID ${account.id}`,
      resetAt,
    })
  }

  candidates.sort((a, b) => a.resetAt - b.resetAt)

  return {
    candidates,
    points: createResetPoints(candidates, nowMs),
    total,
    unknown,
  }
}

function createResetPoints(candidates: ResetCandidate[], nowMs: number): RecoveryGroup[] {
  const bucketCount = 7
  const startOfToday = startOfBeijingDay(nowMs)
  const points: RecoveryGroup[] = Array.from({ length: bucketCount }, (_, index) => {
    const startAt = startOfToday + index * 24 * 60 * 60_000
    const endAt = startAt + 24 * 60 * 60_000
    return {
      key: `7d-reset-${index}`,
      startAt,
      endAt,
      label: formatRecoveryPointLabel(startAt, '7d'),
      fullLabel: formatRecoveryPointRange(startAt, endAt, '7d'),
      count: 0,
      fill: recoveryReasonFill['7d'],
    }
  })

  for (const candidate of candidates) {
    const point = points.find((item) => candidate.resetAt >= item.startAt && candidate.resetAt < item.endAt)
    if (!point) {
      continue
    }
    point.count += 1
  }

  return points
}

function startOfBeijingDay(timestamp: number): number {
  const day = formatBeijingTime(new Date(timestamp).toISOString()).slice(0, 10)
  return new Date(`${day}T00:00:00+08:00`).getTime()
}

function createRecoveryPoints(candidates: RecoveryCandidate[], windowKey: RecoveryWindow, nowMs: number): RecoveryGroup[] {
  const bucketCount = windowKey === '5h' ? 5 : 7
  const bucketMs = windowKey === '5h' ? 60 * 60_000 : 24 * 60 * 60_000
  const points: RecoveryGroup[] = Array.from({ length: bucketCount }, (_, index) => {
    const startAt = nowMs + index * bucketMs
    const endAt = startAt + bucketMs
    return {
      key: `${windowKey}-${index}`,
      startAt,
      endAt,
      label: formatRecoveryPointLabel(endAt, windowKey),
      fullLabel: formatRecoveryPointRange(startAt, endAt, windowKey),
      count: 0,
      fill: recoveryReasonFill[windowKey],
    }
  })

  for (const candidate of candidates) {
    const point = points.find((item) => candidate.recoveryAt >= item.startAt && candidate.recoveryAt < item.endAt)
    if (!point) {
      continue
    }
    point.count += 1
    if (candidate.reason === 'cooldown') {
      point.fill = recoveryReasonFill.cooldown
    }
  }

  return points
}

function getCountAxisConfig(points: RecoveryGroup[]): { domain: [number, number]; ticks: number[] } {
  const maxCount = Math.max(0, ...points.map((point) => point.count))
  if (maxCount <= 4) {
    return {
      domain: [0, 4],
      ticks: [0, 1, 2, 3, 4],
    }
  }

  const step = getNiceTickStep(maxCount / 4)
  const top = Math.max(step, Math.ceil(maxCount / step) * step)
  const tickCount = Math.floor(top / step) + 1
  return {
    domain: [0, top],
    ticks: Array.from({ length: tickCount }, (_, index) => index * step),
  }
}

function getNiceTickStep(rawStep: number): number {
  if (!Number.isFinite(rawStep) || rawStep <= 1) {
    return 1
  }
  const magnitude = 10 ** Math.floor(Math.log10(rawStep))
  const normalized = rawStep / magnitude
  if (normalized <= 1.5) return magnitude
  if (normalized <= 3) return 2 * magnitude
  if (normalized <= 7) return 5 * magnitude
  return 10 * magnitude
}

function futureTimestamp(value: string | undefined, nowMs: number): number | null {
  if (!value) return null
  const timestamp = new Date(value).getTime()
  if (!Number.isFinite(timestamp) || timestamp <= nowMs) {
    return null
  }
  return timestamp
}

function isUsageExhausted(value?: number | null): boolean {
  return typeof value === 'number' && Number.isFinite(value) && value >= 100
}

function formatWholeNumber(value: number): string {
  return Number.isFinite(value) ? String(Math.round(value)) : '-'
}

function formatChartTime(timestamp: number): string {
  return formatBeijingTime(new Date(timestamp).toISOString()).slice(5, 16)
}

function formatRecoveryPointLabel(timestamp: number, windowKey: RecoveryWindow): string {
  const value = formatBeijingTime(new Date(timestamp).toISOString())
  return windowKey === '5h' ? value.slice(11, 16) : value.slice(5, 10)
}

function formatRecoveryPointRange(startAt: number, endAt: number, windowKey: RecoveryWindow): string {
  const start = formatBeijingTime(new Date(startAt).toISOString())
  const end = formatBeijingTime(new Date(endAt).toISOString())
  if (windowKey === '5h') {
    return `${start.slice(5, 16)} - ${end.slice(11, 16)}`
  }
  return `${start.slice(5, 10)} - ${end.slice(5, 10)}`
}
