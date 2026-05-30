import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { Activity, Bell, Clock3, Copy, ExternalLink, FileJson, ListChecks, RefreshCw, Rss } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { api } from '../api'
import resetRadarSaintImage from '../assets/reset.jpeg'
import PageHeader from '../components/PageHeader'
import StateShell from '../components/StateShell'
import { useDataLoader } from '../hooks/useDataLoader'
import type { ResetRadarResponse } from '../types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'

const REFRESH_INTERVAL_MS = 60_000

export default function Subscriptions() {
  const { t, i18n } = useTranslation()
  const [copiedKey, setCopiedKey] = useState<string | null>(null)
  const loadResetRadar = useCallback(() => api.getResetRadar(), [])
  const { data, loading, error, reload, reloadSilently } = useDataLoader<ResetRadarResponse | null>({
    initialData: null,
    load: loadResetRadar,
  })

  useEffect(() => {
    const timer = window.setInterval(() => {
      if (document.visibilityState !== 'visible') return
      void reloadSilently()
    }, REFRESH_INTERVAL_MS)

    return () => window.clearInterval(timer)
  }, [reloadSilently])

  const locale = i18n.language === 'zh' ? 'zh-CN' : 'en-US'
  const lastUpdated = data?.monitored_at || data?.checked_at || data?.fetched_at

  const copyURL = async (key: string, value: string) => {
    try {
      await navigator.clipboard.writeText(value)
      setCopiedKey(key)
      window.setTimeout(() => setCopiedKey(null), 1600)
    } catch {
      setCopiedKey(null)
    }
  }

  return (
    <StateShell
      variant="page"
      loading={loading}
      error={error}
      onRetry={() => void reload()}
      loadingTitle={t('subscriptions.loadingTitle')}
      loadingDescription={t('subscriptions.loadingDesc')}
      errorTitle={t('subscriptions.errorTitle')}
    >
      <>
        <PageHeader
          title={t('subscriptions.title')}
          description={t('subscriptions.description')}
          onRefresh={() => void reload()}
          actionMeta={lastUpdated ? t('subscriptions.lastUpdated', { time: formatDateTime(lastUpdated, locale) }) : undefined}
        />

        {data ? (
          <div className="space-y-6">
            <Card className="py-0">
              <CardContent className="p-4 md:p-5">
                <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(260px,360px)] xl:grid-cols-[minmax(0,1fr)_minmax(280px,380px)_120px] xl:items-stretch">
                  <div className="min-w-0">
                    <div className="mb-3 flex flex-wrap items-center gap-2">
                      <StatusBadge active={data.window_open} label={data.window_open ? t('subscriptions.windowOpen') : t('subscriptions.windowClosed')} />
                      {data.cached ? <Badge variant="outline">{t('subscriptions.cached')}</Badge> : null}
                      {data.source_name ? <Badge variant="secondary">{data.source_name}</Badge> : null}
                    </div>
                    <h3 className="text-xl font-semibold leading-tight text-foreground">
                      {data.message || t('subscriptions.noWindow')}
                    </h3>
                    <p className="mt-2 max-w-[760px] text-sm leading-relaxed text-muted-foreground">
                      {data.current_window?.message || t('subscriptions.currentWindowFallback')}
                    </p>
                  </div>

                  <div className="grid gap-2 rounded-lg border border-border bg-muted/35 px-4 py-3 text-sm">
                    <InfoLine label={t('subscriptions.source')} value={data.source_url} />
                    <InfoLine label={t('subscriptions.checkedAt')} value={formatDateTime(data.checked_at || data.fetched_at, locale)} />
                    <InfoLine label={t('subscriptions.refreshCadence')} value={data.feed?.ttl ? t('subscriptions.minutesValue', { count: data.feed.ttl }) : t('subscriptions.serverCache')} />
                  </div>

                  <figure className="hidden overflow-hidden rounded-lg border border-border bg-muted/20 shadow-sm xl:block">
                    <img
                      src={resetRadarSaintImage}
                      alt={t('subscriptions.saintImageAlt')}
                      className="block h-full max-h-[150px] min-h-[120px] w-full object-cover object-top"
                    />
                  </figure>

                  <div className="flex items-center gap-3 rounded-lg border border-border bg-muted/25 p-3 lg:col-span-2 xl:hidden">
                    <img
                      src={resetRadarSaintImage}
                      alt={t('subscriptions.saintImageAlt')}
                      className="h-[72px] w-14 shrink-0 rounded-md object-cover object-top"
                    />
                    <div className="min-w-0">
                      <div className="text-sm font-semibold text-foreground">{t('subscriptions.imageCaptionTitle')}</div>
                      <div className="mt-1 text-sm leading-relaxed text-muted-foreground">{t('subscriptions.imageCaptionDesc')}</div>
                    </div>
                  </div>
                </div>
              </CardContent>
            </Card>

            <div className="grid gap-4 lg:grid-cols-3">
              <MetricCard
                icon={<Bell className="size-5" />}
                label={t('subscriptions.prediction')}
                value={levelLabel(data.prediction?.level, t)}
                detail={data.prediction?.reasoning_summary || t('subscriptions.noPrediction')}
              />
              <MetricCard
                icon={<Activity className="size-5" />}
                label={t('subscriptions.probability')}
                value={`${formatProbability(data.prediction?.probability_24h)} / ${formatProbability(data.prediction?.probability_48h)}`}
                detail={t('subscriptions.probabilityHint')}
              />
              <MetricCard
                icon={<Clock3 className="size-5" />}
                label={t('subscriptions.lastWindow')}
                value={data.last_window?.window_human || '-'}
                detail={lastWindowDetail(data, locale, t)}
              />
            </div>

            <div className="grid gap-4 xl:grid-cols-[1.1fr_0.9fr]">
              <Card className="py-0">
                <CardContent className="p-6">
                  <div className="mb-5 flex items-start gap-3">
                    <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
                      <ListChecks className="size-5" />
                    </div>
                    <div>
                      <h3 className="text-base font-semibold text-foreground">{t('subscriptions.operatorBrief')}</h3>
                      <p className="mt-1 text-sm leading-relaxed text-muted-foreground">{t('subscriptions.operatorBriefDesc')}</p>
                    </div>
                  </div>
                  <div className="grid gap-3 md:grid-cols-3">
                    <BriefTile label={t('subscriptions.currentAction')} value={actionLabel(data.recommended_action, t)} />
                    <BriefTile label={t('subscriptions.notifyDecision')} value={data.prediction?.should_notify ? t('subscriptions.notifyYes') : t('subscriptions.notifyNo')} />
                    <BriefTile label={t('subscriptions.radarScope')} value={data.last_window?.scope || t('subscriptions.unknownScope')} />
                  </div>
                  <div className="mt-4 rounded-lg border border-amber-500/20 bg-amber-500/10 px-4 py-3 text-sm leading-relaxed text-amber-800 dark:text-amber-200">
                    {t('subscriptions.thirdPartyNotice')}
                  </div>
                </CardContent>
              </Card>

              <Card className="py-0">
                <CardContent className="p-6">
                  <div className="mb-5">
                    <h3 className="text-base font-semibold text-foreground">{t('subscriptions.signalBreakdown')}</h3>
                    <p className="mt-1 text-sm leading-relaxed text-muted-foreground">{t('subscriptions.signalBreakdownDesc', { total: data.prediction?.signal_summary_24h?.total ?? 0 })}</p>
                  </div>
                  <SignalBreakdown data={data} t={t} />
                </CardContent>
              </Card>
            </div>

            <Card className="py-0">
              <CardContent className="p-6">
                <div className="mb-5 flex flex-wrap items-start justify-between gap-4">
                  <div className="flex items-start gap-3">
                    <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
                      <RefreshCw className={`size-5 ${data.hook?.running ? 'animate-spin' : ''}`} />
                    </div>
                    <div>
                      <h3 className="text-base font-semibold text-foreground">{t('subscriptions.resetHookTitle')}</h3>
                      <p className="mt-1 text-sm leading-relaxed text-muted-foreground">{t('subscriptions.resetHookDesc')}</p>
                    </div>
                  </div>
                  <Badge variant={data.hook?.running ? 'default' : data.hook?.signal_detected ? 'secondary' : 'outline'}>
                    {hookStateLabel(data.hook, t)}
                  </Badge>
                </div>

                <div className="grid gap-3 md:grid-cols-4">
                  <BriefTile
                    label={t('subscriptions.resetHookSignal')}
                    value={data.hook?.signal_detected ? hookSignalTypeLabel(data.hook.signal_type, t) : t('subscriptions.notDetected')}
                  />
                  <BriefTile
                    label={t('subscriptions.resetHookLastRun')}
                    value={formatDateTime(data.hook?.last_triggered_at, locale)}
                  />
                  <BriefTile
                    label={t('subscriptions.resetHookResult')}
                    value={hookResultLine(data.hook, t)}
                  />
                  <BriefTile
                    label={t('subscriptions.resetHookMessage')}
                    value={hookMessageLabel(data.hook?.message, t)}
                  />
                </div>

                {data.hook?.last_result?.error ? (
                  <div className="mt-4 rounded-lg border border-destructive/25 bg-destructive/10 px-4 py-3 text-sm leading-relaxed text-destructive">
                    {data.hook.last_result.error}
                  </div>
                ) : null}
              </CardContent>
            </Card>

            <div className="grid gap-4 xl:grid-cols-[minmax(0,1.15fr)_minmax(340px,0.85fr)]">
              <Card className="py-0">
                <CardContent className="p-6">
                  <div className="mb-5 flex flex-wrap items-start justify-between gap-4">
                    <div>
                      <h3 className="text-base font-semibold text-foreground">{t('subscriptions.recentEventsTitle')}</h3>
                      <p className="mt-1 text-sm leading-relaxed text-muted-foreground">{t('subscriptions.recentEventsDesc')}</p>
                    </div>
                    {data.feed?.error ? <Badge variant="outline">{t('subscriptions.feedPartial')}</Badge> : <Badge variant="secondary">{data.feed?.title || t('subscriptions.rssFeed')}</Badge>}
                  </div>
                  {data.feed?.items?.length ? (
                    <div className="grid gap-3">
                      {data.feed.items.slice(0, 6).map((item) => (
                        <FeedEventRow key={item.guid || item.link || item.title} item={item} locale={locale} t={t} />
                      ))}
                    </div>
                  ) : (
                    <div className="rounded-lg border border-dashed border-border bg-muted/25 px-4 py-8 text-center text-sm text-muted-foreground">
                      {data.feed?.error || t('subscriptions.noRecentEvents')}
                    </div>
                  )}
                </CardContent>
              </Card>

              <Card className="py-0">
                <CardContent className="p-6">
                  <div className="mb-5 flex flex-wrap items-start justify-between gap-4">
                    <div>
                      <h3 className="text-base font-semibold text-foreground">{t('subscriptions.feedsTitle')}</h3>
                      <p className="mt-1 text-sm leading-relaxed text-muted-foreground">{t('subscriptions.feedsDesc')}</p>
                    </div>
                    <Badge variant="outline">{t('subscriptions.readOnly')}</Badge>
                  </div>

                  <div className="grid gap-3">
                    <SubscriptionLink
                      icon={<ExternalLink className="size-4" />}
                      title={t('subscriptions.originalSite')}
                      description={t('subscriptions.originalSiteDesc')}
                      url={data.source_url}
                      copied={copiedKey === 'site'}
                      onCopy={() => void copyURL('site', data.source_url)}
                      t={t}
                    />
                    <SubscriptionLink
                      icon={<Rss className="size-4" />}
                      title={t('subscriptions.rssFeed')}
                      description={t('subscriptions.rssFeedDesc')}
                      url={data.rss_url}
                      copied={copiedKey === 'rss'}
                      onCopy={() => void copyURL('rss', data.rss_url)}
                      t={t}
                    />
                    <SubscriptionLink
                      icon={<Activity className="size-4" />}
                      title={t('subscriptions.jsonStatus')}
                      description={t('subscriptions.jsonStatusDesc')}
                      url={data.current_status_url}
                      copied={copiedKey === 'json'}
                      onCopy={() => void copyURL('json', data.current_status_url)}
                      t={t}
                    />
                    <SubscriptionLink
                      icon={<FileJson className="size-4" />}
                      title={t('subscriptions.adminProxy')}
                      description={t('subscriptions.adminProxyDesc')}
                      url="/api/admin/reset-radar"
                      copied={copiedKey === 'proxy'}
                      onCopy={() => void copyURL('proxy', `${window.location.origin}/api/admin/reset-radar`)}
                      t={t}
                    />
                  </div>
                </CardContent>
              </Card>
            </div>
          </div>
        ) : null}
      </>
    </StateShell>
  )
}

function StatusBadge({ active, label }: { active: boolean; label: string }) {
  return (
    <span className={`inline-flex min-h-7 items-center gap-2 rounded-full px-3 text-xs font-bold ${
      active ? 'bg-amber-500/12 text-amber-700 dark:text-amber-300' : 'bg-emerald-500/12 text-emerald-700 dark:text-emerald-300'
    }`}>
      <span className={`size-2 rounded-full ${active ? 'bg-amber-500' : 'bg-emerald-500'}`} />
      {label}
    </span>
  )
}

function InfoLine({ label, value }: { label: string; value: string }) {
  return (
    <div className="grid grid-cols-[72px_minmax(0,1fr)] gap-3">
      <span className="font-semibold text-muted-foreground">{label}</span>
      <span className="truncate text-foreground" title={value}>{value || '-'}</span>
    </div>
  )
}

function MetricCard({
  icon,
  label,
  value,
  detail,
}: {
  icon: ReactNode
  label: string
  value: string
  detail: string
}) {
  return (
    <Card className="py-0">
      <CardContent className="p-5">
        <div className="mb-5 flex items-center justify-between gap-3">
          <span className="text-[13px] font-semibold text-muted-foreground">{label}</span>
          <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
            {icon}
          </div>
        </div>
        <div className="text-2xl font-bold leading-none text-foreground">{value}</div>
        <p className="mt-3 text-sm leading-relaxed text-muted-foreground">{detail}</p>
      </CardContent>
    </Card>
  )
}

function BriefTile({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-border bg-muted/30 px-4 py-3">
      <div className="text-[12px] font-bold uppercase text-muted-foreground">{label}</div>
      <div className="mt-2 text-base font-semibold text-foreground">{value || '-'}</div>
    </div>
  )
}

function SignalBreakdown({ data, t }: { data: ResetRadarResponse; t: (key: string, options?: Record<string, unknown>) => string }) {
  const counts = data.prediction?.signal_summary_24h?.counts
  const rows = [
    ['openai_status', t('subscriptions.signal.openaiStatus'), counts?.openai_status ?? 0],
    ['official_x', t('subscriptions.signal.officialX'), counts?.official_x ?? 0],
    ['community_x', t('subscriptions.signal.communityX'), counts?.community_x ?? 0],
    ['x_reply', t('subscriptions.signal.xReply'), counts?.x_reply ?? 0],
    ['market_x', t('subscriptions.signal.marketX'), counts?.market_x ?? 0],
  ] as const
  const max = Math.max(1, ...rows.map(([, , value]) => value))

  return (
    <div className="grid gap-3">
      {rows.map(([key, label, value]) => (
        <div key={key}>
          <div className="mb-1.5 flex items-center justify-between gap-3 text-sm">
            <span className="font-semibold text-muted-foreground">{label}</span>
            <span className="font-bold text-foreground">{value}</span>
          </div>
          <div className="h-2 overflow-hidden rounded-full bg-muted">
            <div className="h-full rounded-full bg-primary" style={{ width: value > 0 ? `${Math.max(4, (value / max) * 100)}%` : 0 }} />
          </div>
        </div>
      ))}
    </div>
  )
}

function FeedEventRow({
  item,
  locale,
  t,
}: {
  item: ResetRadarResponse['feed']['items'][number]
  locale: string
  t: (key: string, options?: Record<string, unknown>) => string
}) {
  const eventTone = item.event === 'open'
    ? 'bg-amber-500/12 text-amber-700 dark:text-amber-300'
    : item.event === 'close'
      ? 'bg-emerald-500/12 text-emerald-700 dark:text-emerald-300'
      : 'bg-primary/10 text-primary'

  return (
    <div className="grid gap-3 overflow-hidden rounded-lg border border-border bg-muted/25 p-4 md:grid-cols-[140px_minmax(0,1fr)_auto] md:items-start">
      <div className="grid gap-2">
        <span className={`inline-flex min-h-6 w-fit items-center rounded-full px-2.5 text-xs font-bold ${eventTone}`}>
          {t(`subscriptions.event.${item.event}`)}
        </span>
        <span className="text-xs font-semibold text-muted-foreground">{formatDateTime(item.published_at || item.pub_date, locale)}</span>
      </div>
      <div className="min-w-0">
        <div className="truncate text-sm font-semibold text-foreground" title={item.title}>{item.title}</div>
        {item.summary ? <p className="mt-1 break-words text-sm leading-relaxed text-muted-foreground">{item.summary}</p> : null}
      </div>
      {item.link ? (
        <Button asChild variant="outline" size="sm">
          <a href={item.link} target="_blank" rel="noopener noreferrer">
            <ExternalLink className="size-3.5" />
            {t('subscriptions.open')}
          </a>
        </Button>
      ) : null}
    </div>
  )
}

function SubscriptionLink({
  icon,
  title,
  description,
  url,
  copied,
  onCopy,
  t,
}: {
  icon: ReactNode
  title: string
  description: string
  url: string
  copied: boolean
  onCopy: () => void
  t: (key: string, options?: Record<string, unknown>) => string
}) {
  return (
    <div className="grid gap-4 rounded-lg border border-border bg-muted/30 p-4 md:grid-cols-[minmax(0,1fr)_auto] md:items-center">
      <div className="min-w-0">
        <div className="mb-1 flex items-center gap-2 text-sm font-semibold text-foreground">
          <span className="text-primary">{icon}</span>
          {title}
        </div>
        <p className="text-sm leading-relaxed text-muted-foreground">{description}</p>
        <code className="mt-2 block truncate rounded-md bg-background px-2.5 py-1.5 text-xs text-muted-foreground" title={url}>{url}</code>
      </div>
      <div className="flex flex-wrap gap-2 md:justify-end">
        <Button variant="outline" size="sm" onClick={onCopy}>
          <Copy className="size-3.5" />
          {copied ? t('common.copied') : t('common.copy')}
        </Button>
        <Button asChild size="sm">
          <a href={url} target="_blank" rel="noopener noreferrer">
            <ExternalLink className="size-3.5" />
            {t('subscriptions.open')}
          </a>
        </Button>
      </div>
    </div>
  )
}

function actionLabel(action: string | undefined, t: (key: string) => string) {
  if (!action) return t('subscriptions.action.unknown')
  const key = `subscriptions.action.${action}`
  const translated = t(key)
  return translated === key ? action : translated
}

function hookStateLabel(hook: ResetRadarResponse['hook'] | undefined, t: (key: string) => string) {
  if (!hook) return t('subscriptions.hook.waiting')
  if (hook.running) return t('subscriptions.hook.running')
  if (hook.triggered) return t('subscriptions.hook.triggered')
  if (hook.last_result) return t('subscriptions.hook.completed')
  if (hook.signal_detected) return t('subscriptions.hook.armed')
  return t('subscriptions.hook.waiting')
}

function hookMessageLabel(message: string | undefined, t: (key: string) => string) {
  if (!message) return '-'
  const key = `subscriptions.hookMessage.${message}`
  const translated = t(key)
  return translated === key ? message : translated
}

function hookSignalTypeLabel(signalType: string | undefined, t: (key: string) => string) {
  if (!signalType) return t('subscriptions.detected')
  const key = `subscriptions.hookSignal.${signalType}`
  const translated = t(key)
  return translated === key ? signalType : translated
}

function hookResultLine(hook: ResetRadarResponse['hook'] | undefined, t: (key: string, options?: Record<string, unknown>) => string) {
  const result = hook?.last_result
  if (!result) {
    return hook?.running ? t('subscriptions.running') : '-'
  }
  return t('subscriptions.resetHookResultLine', {
    total: result.total,
    success: result.success,
    failed: result.failed,
    limited: result.rate_limited,
  })
}

function levelLabel(level: string | undefined, t: (key: string) => string) {
  if (!level) return t('subscriptions.levelUnknown')
  const key = `subscriptions.level.${level}`
  const translated = t(key)
  return translated === key ? level : translated
}

function formatProbability(value: number | undefined) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '-'
  return `${Math.round(value * 100)}%`
}

function formatDateTime(value: string | undefined, locale: string) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat(locale, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(date)
}

function lastWindowDetail(data: ResetRadarResponse, locale: string, t: (key: string, options?: Record<string, unknown>) => string) {
  const last = data.last_window
  if (!last?.id) return t('subscriptions.noLastWindow')
  return t('subscriptions.lastWindowDetail', {
    title: last.title || '-',
    closed: formatDateTime(last.closed_at, locale),
    scope: last.scope || '-',
  })
}
