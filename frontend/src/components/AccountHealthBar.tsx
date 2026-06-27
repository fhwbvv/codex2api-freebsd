import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { AccountHealthBucket } from '../types'

// 「健康状态」条：把账号最近的请求成败分桶渲染成一排色块 + 成功率。
// 移植自 CLIProxyAPI 的 ProviderStatusBar，改为 Tailwind 实现。
// 数据来自后端 /api/accounts/health-bars（20 格 × 10 分钟 ≈ 最近 3.3 小时）。

interface StatusBlockDetail {
  success: number
  failure: number
  rate: number // -1 表示该时段无请求（idle）
  startTime: number
  endTime: number
}

interface StatusBarData {
  blockDetails: StatusBlockDetail[]
  successRate: number
  totalSuccess: number
  totalFailure: number
}

// 0 → 红(#ef4444) → 0.5 → 金黄(#facc15) → 1 → 绿(#22c55e) 的 RGB 线性插值。
const COLOR_STOPS = [
  { r: 239, g: 68, b: 68 },
  { r: 250, g: 204, b: 21 },
  { r: 34, g: 197, b: 94 },
] as const

function rateToColor(rate: number): string {
  const t = Math.max(0, Math.min(1, rate))
  const segment = t < 0.5 ? 0 : 1
  const localT = segment === 0 ? t * 2 : (t - 0.5) * 2
  const from = COLOR_STOPS[segment]
  const to = COLOR_STOPS[segment + 1]
  const r = Math.round(from.r + (to.r - from.r) * localT)
  const g = Math.round(from.g + (to.g - from.g) * localT)
  const b = Math.round(from.b + (to.b - from.b) * localT)
  return `rgb(${r}, ${g}, ${b})`
}

function formatClock(timestamp: number): string {
  const date = new Date(timestamp)
  const h = date.getHours().toString().padStart(2, '0')
  const m = date.getMinutes().toString().padStart(2, '0')
  return `${h}:${m}`
}

function formatSuccessRate(rate: number): string {
  const rounded = rate.toFixed(1)
  return `${rounded.endsWith('.0') ? rounded.slice(0, -2) : rounded}%`
}

// statusBarDataFromBuckets 把后端的成败分桶（由旧到新）转换为渲染所需结构。
// 桶数不足时在头部补空桶（与 endTime=now 对齐）。
function statusBarDataFromBuckets(
  buckets: AccountHealthBucket[],
  blockCount: number,
  blockMinutes: number,
): StatusBarData {
  const blockDurationMs = blockMinutes * 60 * 1000
  const padCount = Math.max(0, blockCount - buckets.length)
  const stats = [
    ...Array.from({ length: padCount }, () => ({ success: 0, failed: 0 })),
    ...buckets.slice(-blockCount),
  ]

  const now = Date.now()
  const windowStart = now - blockCount * blockDurationMs

  const blockDetails: StatusBlockDetail[] = []
  let totalSuccess = 0
  let totalFailure = 0

  stats.forEach((bucket, index) => {
    const success = bucket.success
    const failure = bucket.failed
    const total = success + failure
    totalSuccess += success
    totalFailure += failure

    const start = windowStart + index * blockDurationMs
    blockDetails.push({
      success,
      failure,
      rate: total > 0 ? success / total : -1,
      startTime: start,
      endTime: start + blockDurationMs,
    })
  })

  const total = totalSuccess + totalFailure
  return {
    blockDetails,
    successRate: total > 0 ? (totalSuccess / total) * 100 : 100,
    totalSuccess,
    totalFailure,
  }
}

interface Props {
  buckets: AccountHealthBucket[] | undefined
  blockCount?: number
  blockMinutes?: number
}

export default function AccountHealthBar({
  buckets,
  blockCount = 20,
  blockMinutes = 10,
}: Props) {
  const { t } = useTranslation()
  const [activeTooltip, setActiveTooltip] = useState<number | null>(null)
  const blocksRef = useRef<HTMLDivElement>(null)

  const data = statusBarDataFromBuckets(buckets ?? [], blockCount, blockMinutes)
  const hasData = data.totalSuccess + data.totalFailure > 0
  const rateClass = !hasData
    ? 'bg-muted text-muted-foreground'
    : data.successRate >= 90
      ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-400'
      : data.successRate >= 50
        ? 'bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-400'
        : 'bg-red-100 text-red-600 dark:bg-red-950 dark:text-red-400'

  // 点击外部关闭 tooltip（移动端 / 触摸）。
  useEffect(() => {
    if (activeTooltip === null) return
    const handler = (e: PointerEvent) => {
      if (blocksRef.current && !blocksRef.current.contains(e.target as Node)) {
        setActiveTooltip(null)
      }
    }
    document.addEventListener('pointerdown', handler)
    return () => document.removeEventListener('pointerdown', handler)
  }, [activeTooltip])

  const handlePointerEnter = useCallback((e: React.PointerEvent, idx: number) => {
    if (e.pointerType === 'mouse') setActiveTooltip(idx)
  }, [])
  const handlePointerLeave = useCallback((e: React.PointerEvent) => {
    if (e.pointerType === 'mouse') setActiveTooltip(null)
  }, [])
  const handlePointerDown = useCallback((e: React.PointerEvent, idx: number) => {
    if (e.pointerType === 'touch') {
      e.preventDefault()
      setActiveTooltip((prev) => (prev === idx ? null : idx))
    }
  }, [])

  const total = data.blockDetails.length

  const renderTooltip = (detail: StatusBlockDetail, idx: number) => {
    const count = detail.success + detail.failure
    // 边缘块靠左/右对齐，避免 tooltip 溢出。
    const posClass =
      idx <= 2 ? 'left-0 translate-x-0' : idx >= total - 3 ? 'right-0 left-auto translate-x-0' : 'left-1/2 -translate-x-1/2'
    const timeRange = `${formatClock(detail.startTime)} – ${formatClock(detail.endTime)}`

    return (
      <div
        className={`pointer-events-none absolute bottom-[calc(100%+8px)] z-30 whitespace-nowrap rounded-md border bg-popover px-2.5 py-1.5 text-[11px] leading-snug text-popover-foreground shadow-md ${posClass}`}
      >
        <span className="mb-0.5 block text-muted-foreground">{timeRange}</span>
        {count > 0 ? (
          <span className="flex items-center gap-2">
            <span className="text-emerald-600">
              {t('accounts.healthBarSuccessShort')} {detail.success}
            </span>
            <span className="text-red-500">
              {t('accounts.healthBarFailureShort')} {detail.failure}
            </span>
            <span className="text-muted-foreground">({(detail.rate * 100).toFixed(1)}%)</span>
          </span>
        ) : (
          <span className="text-muted-foreground">{t('accounts.healthBarNoRequests')}</span>
        )}
      </div>
    )
  }

  return (
    <div className="flex max-w-full items-center gap-1.5">
      <div className="relative flex min-w-[120px] flex-1 gap-[2px]" ref={blocksRef}>
        {data.blockDetails.map((detail, idx) => {
          const isIdle = detail.rate === -1
          const isActive = activeTooltip === idx
          return (
            <div
              key={idx}
              className="group relative min-w-[4px] flex-1 cursor-pointer"
              onPointerEnter={(e) => handlePointerEnter(e, idx)}
              onPointerLeave={handlePointerLeave}
              onPointerDown={(e) => handlePointerDown(e, idx)}
            >
              <div
                className={`h-1.5 w-full rounded-[2px] transition-transform duration-150 group-hover:scale-y-[1.8] ${
                  isIdle ? 'bg-border' : ''
                } ${isActive ? 'scale-y-[1.8] opacity-90' : ''}`}
                style={isIdle ? undefined : { backgroundColor: rateToColor(detail.rate) }}
              />
              {isActive && renderTooltip(detail, idx)}
            </div>
          )
        })}
      </div>
      <span
        className={`inline-flex items-center rounded px-1.5 py-0.5 text-[11px] font-semibold tabular-nums ${rateClass}`}
      >
        {hasData ? formatSuccessRate(data.successRate) : '--'}
      </span>
    </div>
  )
}
