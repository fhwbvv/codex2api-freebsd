import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  ArrowUpRight,
  Check,
  ChevronDown,
  CloudDownload,
  Link2,
  Loader2,
  RotateCcw,
  Save,
  Search,
  Sparkles,
  Wand2,
  X,
} from 'lucide-react'

import { api } from '@/api'
import ModelLogo from '../components/ModelLogo'
import PageHeader from '../components/PageHeader'
import StateShell from '../components/StateShell'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import { useToast } from '../hooks/useToast'
import { getErrorMessage } from '../utils/error'
import type { ModelPricingOverride } from '@/types'

type Row = { model: string; source: string; pricing: ModelPricingOverride }
type SourceFilter = 'all' | 'custom' | 'synced' | 'default'

type FieldDef = {
  key: keyof ModelPricingOverride
  labelKey: string
  shortKey: string
  tone: 'sky' | 'cyan' | 'violet' | 'amber' | 'orange' | 'emerald' | 'teal'
}

const PRIMARY_FIELDS: FieldDef[] = [
  { key: 'input', labelKey: 'settings.pricing.input', shortKey: 'settings.pricing.shortInput', tone: 'sky' },
  { key: 'cached_input', labelKey: 'settings.pricing.cached', shortKey: 'settings.pricing.shortCached', tone: 'cyan' },
  { key: 'output', labelKey: 'settings.pricing.output', shortKey: 'settings.pricing.shortOutput', tone: 'violet' },
]

const ADVANCED_FIELDS: FieldDef[] = [
  { key: 'input_priority', labelKey: 'settings.pricing.inputPriority', shortKey: 'settings.pricing.shortInputPriority', tone: 'amber' },
  { key: 'output_priority', labelKey: 'settings.pricing.outputPriority', shortKey: 'settings.pricing.shortOutputPriority', tone: 'orange' },
  { key: 'input_long', labelKey: 'settings.pricing.inputLong', shortKey: 'settings.pricing.shortInputLong', tone: 'emerald' },
  { key: 'output_long', labelKey: 'settings.pricing.outputLong', shortKey: 'settings.pricing.shortOutputLong', tone: 'teal' },
]

const ALL_FIELDS = [...PRIMARY_FIELDS, ...ADVANCED_FIELDS]

const TONE_STYLES: Record<FieldDef['tone'], { dot: string; ring: string; soft: string }> = {
  sky: {
    dot: 'bg-sky-500',
    ring: 'focus-within:border-sky-500/40 focus-within:ring-sky-500/15',
    soft: 'bg-sky-500/10 text-sky-700 dark:text-sky-300',
  },
  cyan: {
    dot: 'bg-cyan-500',
    ring: 'focus-within:border-cyan-500/40 focus-within:ring-cyan-500/15',
    soft: 'bg-cyan-500/10 text-cyan-700 dark:text-cyan-300',
  },
  violet: {
    dot: 'bg-violet-500',
    ring: 'focus-within:border-violet-500/40 focus-within:ring-violet-500/15',
    soft: 'bg-violet-500/10 text-violet-700 dark:text-violet-300',
  },
  amber: {
    dot: 'bg-amber-500',
    ring: 'focus-within:border-amber-500/40 focus-within:ring-amber-500/15',
    soft: 'bg-amber-500/10 text-amber-700 dark:text-amber-300',
  },
  orange: {
    dot: 'bg-orange-500',
    ring: 'focus-within:border-orange-500/40 focus-within:ring-orange-500/15',
    soft: 'bg-orange-500/10 text-orange-700 dark:text-orange-300',
  },
  emerald: {
    dot: 'bg-emerald-500',
    ring: 'focus-within:border-emerald-500/40 focus-within:ring-emerald-500/15',
    soft: 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300',
  },
  teal: {
    dot: 'bg-teal-500',
    ring: 'focus-within:border-teal-500/40 focus-within:ring-teal-500/15',
    soft: 'bg-teal-500/10 text-teal-700 dark:text-teal-300',
  },
}

function normalizePrice(value: unknown): number {
  const n = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(n) ? n : 0
}

function isDirty(draft: ModelPricingOverride | undefined, saved: ModelPricingOverride | undefined): boolean {
  for (const field of ALL_FIELDS) {
    if (normalizePrice(draft?.[field.key]) !== normalizePrice(saved?.[field.key])) return true
  }
  return false
}

function formatPriceDisplay(value: number): string {
  if (!Number.isFinite(value) || value === 0) return '0'
  if (Number.isInteger(value)) return String(value)
  const fixed = value.toFixed(4).replace(/\.?0+$/, '')
  return fixed
}

/** 定价列表置顶：gpt-5.6-sol → terra → luna。 */
const PREFERRED_MODEL_ORDER = ['gpt-5.6-sol', 'gpt-5.6-terra', 'gpt-5.6-luna'] as const

function modelPreferredRank(model: string): number {
  const lower = model.trim().toLowerCase()
  for (let i = 0; i < PREFERRED_MODEL_ORDER.length; i += 1) {
    const preferred = PREFERRED_MODEL_ORDER[i]
    if (lower === preferred || lower.startsWith(`${preferred}-`) || lower.startsWith(`${preferred}(`)) {
      return i
    }
  }
  return -1
}

/** 从模型名提取数字段（gpt-5.6-luna → [5,6]），用于新→旧排序。 */
function modelVersionParts(model: string): number[] {
  const matches = model.match(/\d+/g)
  if (!matches) return []
  return matches.map((m) => Number(m)).filter((n) => Number.isFinite(n))
}

/** 比较模型名：置顶 sol/terra/luna，其余版本号高的在前；同版本再按字典序。 */
function compareModelsNewestFirst(a: string, b: string): number {
  if (a === b) return 0
  const ra = modelPreferredRank(a)
  const rb = modelPreferredRank(b)
  if (ra >= 0 || rb >= 0) {
    if (ra < 0) return 1
    if (rb < 0) return -1
    if (ra !== rb) return ra - rb
    return a.localeCompare(b)
  }
  const va = modelVersionParts(a)
  const vb = modelVersionParts(b)
  if (va.length === 0 && vb.length === 0) return a.localeCompare(b)
  if (va.length === 0) return 1
  if (vb.length === 0) return -1
  const n = Math.min(va.length, vb.length)
  for (let i = 0; i < n; i += 1) {
    if (va[i] !== vb[i]) return vb[i] - va[i]
  }
  if (va.length !== vb.length) return vb.length - va.length
  return a.localeCompare(b)
}

function sourceMeta(source: string): { labelKey: string; className: string; dot: string } {
  if (source === 'custom') {
    return {
      labelKey: 'settings.pricing.source.custom',
      className: 'bg-primary/10 text-primary ring-primary/15',
      dot: 'bg-primary',
    }
  }
  if (source === 'synced') {
    return {
      labelKey: 'settings.pricing.source.synced',
      className: 'bg-sky-500/10 text-sky-700 ring-sky-500/15 dark:text-sky-300',
      dot: 'bg-sky-500',
    }
  }
  return {
    labelKey: 'settings.pricing.source.default',
    className: 'bg-muted text-muted-foreground ring-border/60',
    dot: 'bg-muted-foreground/50',
  }
}

function PriceField({
  field,
  value,
  changed,
  dense,
  onChange,
}: {
  field: FieldDef
  value: number
  changed: boolean
  dense?: boolean
  onChange: (next: string) => void
}) {
  const { t } = useTranslation()
  const tone = TONE_STYLES[field.tone]

  return (
    <label
      className={cn(
        'group relative flex min-w-0 flex-col rounded-2xl border bg-background/80 transition-all duration-200',
        dense ? 'gap-1.5 p-2.5 sm:p-3' : 'gap-2 p-3 sm:p-3.5',
        changed
          ? 'border-amber-500/35 bg-amber-500/[0.04] shadow-[0_0_0_3px_hsl(38_92%_50%/0.08)]'
          : 'border-border/80 hover:border-border hover:bg-card hover:shadow-sm',
        'focus-within:ring-[3px]',
        tone.ring,
      )}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="flex min-w-0 items-center gap-1.5">
          <span className={cn('size-1.5 shrink-0 rounded-full', tone.dot)} aria-hidden />
          <span className="truncate text-[11px] font-semibold tracking-wide text-muted-foreground">
            {t(field.labelKey)}
          </span>
        </span>
        {changed ? (
          <span className="size-1.5 shrink-0 rounded-full bg-amber-500 shadow-[0_0_0_3px_hsl(38_92%_50%/0.18)]" />
        ) : null}
      </div>
      <div className="relative">
        <span className="pointer-events-none absolute left-0 top-1/2 -translate-y-1/2 text-sm font-medium text-muted-foreground/70">
          $
        </span>
        <input
          type="number"
          step="0.01"
          min={0}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className={cn(
            'w-full border-0 bg-transparent pl-4 font-mono font-semibold tabular-nums tracking-tight text-foreground outline-none',
            dense ? 'h-7 text-[15px]' : 'h-8 text-lg sm:text-[1.35rem]',
            '[appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none',
          )}
        />
      </div>
      <span className="text-[10px] font-medium text-muted-foreground/70">/ 1M tok</span>
    </label>
  )
}

function MetricChip({
  label,
  value,
  active,
  onClick,
  accent,
}: {
  label: string
  value: number
  active?: boolean
  onClick?: () => void
  accent?: string
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        'group relative min-w-0 flex-1 overflow-hidden rounded-2xl border px-3.5 py-3 text-left transition-all duration-200',
        active
          ? 'border-primary/35 bg-primary/[0.07] shadow-[0_8px_24px_-12px_color-mix(in_oklab,var(--color-primary)_45%,transparent)]'
          : 'border-white/10 bg-white/5 hover:border-white/20 hover:bg-white/10 dark:border-white/8',
      )}
    >
      <div className="text-[11px] font-semibold uppercase tracking-[0.08em] text-white/55">
        {label}
      </div>
      <div className="mt-1.5 flex items-baseline gap-1.5">
        <span className="text-2xl font-semibold tabular-nums tracking-tight text-white sm:text-[1.75rem]">
          {value}
        </span>
      </div>
      <span
        aria-hidden
        className="pointer-events-none absolute -right-3 -top-3 size-14 rounded-full opacity-40 blur-2xl transition-opacity group-hover:opacity-70"
        style={{ background: accent || 'hsl(var(--color-primary))' }}
      />
    </button>
  )
}

export default function ModelPricing() {
  const { t } = useTranslation()
  const { showToast } = useToast()
  const [rows, setRows] = useState<Row[]>([])
  const [drafts, setDrafts] = useState<Record<string, ModelPricingOverride>>({})
  const [syncUrl, setSyncUrl] = useState('')
  const [defaultUrl, setDefaultUrl] = useState('')
  const [modelsDevUrl, setModelsDevUrl] = useState('')
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [syncing, setSyncing] = useState(false)
  const [savingModel, setSavingModel] = useState('')
  const [query, setQuery] = useState('')
  const [sourceFilter, setSourceFilter] = useState<SourceFilter>('all')
  const [syncOpen, setSyncOpen] = useState(false)
  const [expandedAdvanced, setExpandedAdvanced] = useState<Record<string, boolean>>({})

  const load = useCallback(async () => {
    setLoading(true)
    setLoadError(null)
    try {
      const res = await api.listModelPricing()
      setRows(res.models)
      setDefaultUrl(res.default_sync_url)
      setModelsDevUrl(res.models_dev_url)
      setSyncUrl(res.sync_url || '')
      const d: Record<string, ModelPricingOverride> = {}
      for (const r of res.models) d[r.model] = { ...r.pricing }
      setDrafts(d)
    } catch (error) {
      const msg = getErrorMessage(error)
      setLoadError(msg)
      showToast(msg, 'error')
    } finally {
      setLoading(false)
    }
  }, [showToast])

  useEffect(() => {
    void load()
  }, [load])

  const setField = (model: string, key: keyof ModelPricingOverride, value: string) => {
    const num = value.trim() === '' ? 0 : Number(value)
    setDrafts((prev) => ({
      ...prev,
      [model]: { ...prev[model], [key]: Number.isFinite(num) ? num : 0 },
    }))
  }

  const save = async (model: string) => {
    setSavingModel(model)
    try {
      await api.updateModelPricing({ model, pricing: drafts[model] })
      showToast(t('settings.pricing.saved', { model }))
      await load()
    } catch (error) {
      showToast(getErrorMessage(error), 'error')
    } finally {
      setSavingModel('')
    }
  }

  const reset = async (model: string) => {
    setSavingModel(model)
    try {
      await api.updateModelPricing({ model, reset: true })
      showToast(t('settings.pricing.reset', { model }))
      await load()
    } catch (error) {
      showToast(getErrorMessage(error), 'error')
    } finally {
      setSavingModel('')
    }
  }

  const sync = async () => {
    setSyncing(true)
    try {
      const res = await api.syncModelPricing(syncUrl)
      showToast(t('settings.pricing.syncDone', { applied: res.applied, skipped: res.skipped }))
      await load()
    } catch (error) {
      showToast(`${t('settings.pricing.syncFailed')}: ${getErrorMessage(error)}`, 'error')
    } finally {
      setSyncing(false)
    }
  }

  const activePreset = useMemo(() => {
    const url = syncUrl.trim()
    if (url === '' || url === defaultUrl) return 'default'
    if (modelsDevUrl && url === modelsDevUrl) return 'modelsdev'
    return 'custom'
  }, [defaultUrl, modelsDevUrl, syncUrl])

  const counts = useMemo(() => {
    let custom = 0
    let synced = 0
    let defaults = 0
    for (const r of rows) {
      if (r.source === 'custom') custom += 1
      else if (r.source === 'synced') synced += 1
      else defaults += 1
    }
    return { total: rows.length, custom, synced, defaults }
  }, [rows])

  const dirtyCount = useMemo(() => {
    let n = 0
    for (const r of rows) {
      if (isDirty(drafts[r.model], r.pricing)) n += 1
    }
    return n
  }, [drafts, rows])

  const filteredRows = useMemo(() => {
    const q = query.trim().toLowerCase()
    return rows
      .filter((r) => {
        if (sourceFilter !== 'all' && r.source !== sourceFilter) return false
        if (q && !r.model.toLowerCase().includes(q)) return false
        return true
      })
      .slice()
      .sort((a, b) => compareModelsNewestFirst(a.model, b.model))
  }, [query, rows, sourceFilter])

  const sourceFilters: Array<{ id: SourceFilter; label: string; count: number }> = [
    { id: 'all', label: t('settings.pricing.filterAll'), count: counts.total },
    { id: 'custom', label: t('settings.pricing.source.custom'), count: counts.custom },
    { id: 'synced', label: t('settings.pricing.source.synced'), count: counts.synced },
    { id: 'default', label: t('settings.pricing.source.default'), count: counts.defaults },
  ]

  return (
    <div className="w-full min-w-0">
      <PageHeader
        title={t('settings.pricing.title')}
        description={t('settings.pricing.desc')}
        onRefresh={() => void load()}
        actions={
          <Button
            variant="outline"
            size="sm"
            className="gap-1.5"
            onClick={() => setSyncOpen((v) => !v)}
          >
            <CloudDownload className="size-3.5" />
            {t('settings.pricing.syncTitle')}
            <ChevronDown className={cn('size-3.5 transition-transform', syncOpen && 'rotate-180')} />
          </Button>
        }
      />

      <StateShell
        variant="page"
        loading={loading && rows.length === 0}
        error={loadError && rows.length === 0 ? loadError : null}
        onRetry={() => void load()}
      >
        <div className="space-y-5 sm:space-y-6">
          {/* Hero metrics */}
          <section className="relative overflow-hidden rounded-[28px] border border-border/60 bg-[hsl(222_28%_12%)] text-white shadow-[0_24px_60px_-28px_rgba(15,23,42,0.55)] dark:bg-[hsl(222_20%_10%)]">
            <div
              aria-hidden
              className="pointer-events-none absolute inset-0 opacity-90"
              style={{
                background:
                  'radial-gradient(ellipse 80% 70% at 0% 0%, hsl(214 84% 54% / 0.45), transparent 55%), radial-gradient(ellipse 60% 50% at 100% 20%, hsl(262 70% 58% / 0.28), transparent 50%), radial-gradient(ellipse 50% 40% at 70% 100%, hsl(190 70% 45% / 0.2), transparent 45%)',
              }}
            />
            <div
              aria-hidden
              className="pointer-events-none absolute inset-0 opacity-[0.12]"
              style={{
                backgroundImage:
                  'linear-gradient(to right, rgba(255,255,255,0.08) 1px, transparent 1px), linear-gradient(to bottom, rgba(255,255,255,0.08) 1px, transparent 1px)',
                backgroundSize: '28px 28px',
                maskImage: 'linear-gradient(to bottom, black, transparent 90%)',
              }}
            />

            <div className="relative z-10 p-5 sm:p-7">
              <div className="flex flex-col gap-5 lg:flex-row lg:items-end lg:justify-between">
                <div className="max-w-xl">
                  <div className="inline-flex items-center gap-1.5 rounded-full border border-white/12 bg-white/8 px-2.5 py-1 text-[11px] font-semibold tracking-wide text-white/80 backdrop-blur-sm">
                    <Sparkles className="size-3" />
                    {t('settings.pricing.heroBadge')}
                  </div>
                  <h3 className="mt-3 text-[1.65rem] font-semibold leading-tight tracking-tight text-white sm:text-[2rem]">
                    {t('settings.pricing.heroTitle')}
                  </h3>
                  <p className="mt-2 max-w-lg text-sm leading-relaxed text-white/65">
                    {t('settings.pricing.heroDesc')}
                  </p>
                </div>
                <div className="flex flex-wrap items-center gap-2 text-[12px] text-white/60">
                  <span className="inline-flex items-center gap-1.5 rounded-full border border-white/10 bg-white/5 px-2.5 py-1">
                    <span className="size-1.5 rounded-full bg-emerald-400" />
                    {t('settings.pricing.unitHint')}
                  </span>
                  {dirtyCount > 0 ? (
                    <span className="inline-flex items-center gap-1.5 rounded-full border border-amber-400/25 bg-amber-400/10 px-2.5 py-1 text-amber-100">
                      {t('settings.pricing.unsavedCount', { count: dirtyCount })}
                    </span>
                  ) : null}
                </div>
              </div>

              <div className="mt-6 grid grid-cols-2 gap-2.5 lg:grid-cols-4">
                <MetricChip
                  label={t('settings.pricing.statTotal')}
                  value={counts.total}
                  active={sourceFilter === 'all'}
                  onClick={() => setSourceFilter('all')}
                  accent="hsl(214 84% 56%)"
                />
                <MetricChip
                  label={t('settings.pricing.statCustom')}
                  value={counts.custom}
                  active={sourceFilter === 'custom'}
                  onClick={() => setSourceFilter('custom')}
                  accent="hsl(214 90% 60%)"
                />
                <MetricChip
                  label={t('settings.pricing.statSynced')}
                  value={counts.synced}
                  active={sourceFilter === 'synced'}
                  onClick={() => setSourceFilter('synced')}
                  accent="hsl(190 80% 50%)"
                />
                <MetricChip
                  label={t('settings.pricing.statDefault')}
                  value={counts.defaults}
                  active={sourceFilter === 'default'}
                  onClick={() => setSourceFilter('default')}
                  accent="hsl(40 90% 55%)"
                />
              </div>
            </div>
          </section>

          {/* Sync panel (collapsible) */}
          <div
            className={cn(
              'grid transition-[grid-template-rows,opacity] duration-300 ease-out',
              syncOpen ? 'grid-rows-[1fr] opacity-100' : 'grid-rows-[0fr] opacity-0',
            )}
          >
            <div className="min-h-0 overflow-hidden">
              <section className="rounded-3xl border border-border/80 bg-card p-4 shadow-sm sm:p-5">
                <div className="flex flex-col gap-4 lg:flex-row lg:items-start">
                  <div className="flex min-w-0 flex-1 gap-3">
                    <div className="flex size-11 shrink-0 items-center justify-center rounded-2xl bg-gradient-to-br from-primary/15 to-primary/5 text-primary ring-1 ring-primary/15">
                      <CloudDownload className="size-5" />
                    </div>
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <h3 className="text-base font-semibold tracking-tight text-foreground">
                          {t('settings.pricing.syncTitle')}
                        </h3>
                        <span className="rounded-full bg-muted px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                          {activePreset === 'default'
                            ? t('settings.pricing.presetDefault')
                            : activePreset === 'modelsdev'
                              ? 'models.dev'
                              : t('settings.pricing.presetCustom')}
                        </span>
                      </div>
                      <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
                        {t('settings.pricing.syncSubtitle')}
                      </p>
                    </div>
                  </div>
                  <button
                    type="button"
                    className="self-start rounded-lg p-1.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground lg:ml-auto"
                    onClick={() => setSyncOpen(false)}
                    aria-label={t('common.close')}
                  >
                    <X className="size-4" />
                  </button>
                </div>

                <div className="mt-5 space-y-3">
                  <div className="flex flex-col gap-2.5 sm:flex-row sm:items-center">
                    <div className="relative min-w-0 flex-1">
                      <Link2 className="pointer-events-none absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
                      <Input
                        className="h-11 rounded-xl border-border/80 bg-muted/20 pl-9 font-mono text-xs shadow-none"
                        value={syncUrl}
                        placeholder={defaultUrl}
                        onChange={(e) => setSyncUrl(e.target.value)}
                      />
                    </div>
                    <Button
                      className="h-11 shrink-0 rounded-xl px-5"
                      onClick={() => void sync()}
                      disabled={syncing}
                    >
                      {syncing ? (
                        <Loader2 className="size-3.5 animate-spin" />
                      ) : (
                        <ArrowUpRight className="size-3.5" />
                      )}
                      {syncing ? t('settings.pricing.syncing') : t('settings.pricing.syncNow')}
                    </Button>
                  </div>

                  <div className="flex flex-wrap items-center gap-2">
                    <span className="text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                      {t('settings.pricing.presets')}
                    </span>
                    <button
                      type="button"
                      onClick={() => setSyncUrl('')}
                      className={cn(
                        'inline-flex h-8 items-center gap-1.5 rounded-full border px-3 text-xs font-semibold transition-all',
                        activePreset === 'default'
                          ? 'border-primary/30 bg-primary text-primary-foreground shadow-sm'
                          : 'border-border bg-background text-muted-foreground hover:border-border hover:bg-muted/50 hover:text-foreground',
                      )}
                    >
                      <Sparkles className="size-3" />
                      {t('settings.pricing.presetDefault')}
                    </button>
                    <button
                      type="button"
                      onClick={() => setSyncUrl(modelsDevUrl)}
                      disabled={!modelsDevUrl}
                      className={cn(
                        'inline-flex h-8 items-center gap-1.5 rounded-full border px-3 text-xs font-semibold transition-all disabled:opacity-40',
                        activePreset === 'modelsdev'
                          ? 'border-primary/30 bg-primary text-primary-foreground shadow-sm'
                          : 'border-border bg-background text-muted-foreground hover:border-border hover:bg-muted/50 hover:text-foreground',
                      )}
                    >
                      <Wand2 className="size-3" />
                      models.dev
                    </button>
                  </div>

                  <p className="rounded-2xl border border-dashed border-border/80 bg-muted/25 px-3.5 py-3 text-[12px] leading-relaxed text-muted-foreground">
                    {t('settings.pricing.hint')}
                  </p>
                </div>
              </section>
            </div>
          </div>

          {/* Sticky toolbar */}
          <div className="sticky top-2 z-20 -mx-1 px-1">
            <div className="flex flex-col gap-3 rounded-2xl border border-border/80 bg-card/90 p-2.5 shadow-[0_8px_30px_-18px_rgba(15,23,42,0.35)] backdrop-blur-xl sm:flex-row sm:items-center sm:justify-between sm:p-2 sm:pl-3">
              <div className="relative min-w-0 flex-1 sm:max-w-xs">
                <Search className="pointer-events-none absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
                <Input
                  className="h-9 border-transparent bg-muted/40 pl-9 text-sm shadow-none focus-visible:bg-background"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder={t('settings.pricing.searchPlaceholder')}
                />
              </div>

              <div
                className="flex max-w-full gap-0.5 overflow-x-auto rounded-xl bg-muted/50 p-0.5 [-ms-overflow-style:none] [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
                role="tablist"
              >
                {sourceFilters.map((item) => {
                  const active = sourceFilter === item.id
                  return (
                    <button
                      key={item.id}
                      type="button"
                      role="tab"
                      aria-selected={active}
                      onClick={() => setSourceFilter(item.id)}
                      className={cn(
                        'inline-flex h-8 shrink-0 items-center gap-1.5 rounded-[10px] px-2.5 text-xs font-semibold transition-all',
                        active
                          ? 'bg-background text-foreground shadow-sm'
                          : 'text-muted-foreground hover:text-foreground',
                      )}
                    >
                      {item.label}
                      <span
                        className={cn(
                          'tabular-nums rounded-md px-1 py-px text-[10px] font-bold',
                          active ? 'bg-primary/10 text-primary' : 'bg-background/60 text-muted-foreground',
                        )}
                      >
                        {item.count}
                      </span>
                    </button>
                  )
                })}
              </div>
            </div>
          </div>

          {/* Model list */}
          {filteredRows.length === 0 ? (
            <StateShell
              isEmpty
              emptyTitle={t('settings.pricing.emptyTitle')}
              emptyDescription={
                query || sourceFilter !== 'all'
                  ? t('settings.pricing.emptyFiltered')
                  : t('settings.pricing.emptyDesc')
              }
            >
              {null}
            </StateShell>
          ) : (
            <div className="space-y-3.5">
              <div className="flex items-center justify-between px-1">
                <p className="text-xs font-medium text-muted-foreground">
                  {t('settings.pricing.listCount', {
                    shown: filteredRows.length,
                    total: counts.total,
                  })}
                </p>
              </div>

              {filteredRows.map((r) => {
                const draft = drafts[r.model] ?? {}
                const dirty = isDirty(draft, r.pricing)
                const busy = savingModel === r.model
                const source = sourceMeta(r.source)
                const advancedOpen = expandedAdvanced[r.model] ?? false
                const advancedDirty = ADVANCED_FIELDS.some(
                  (f) => normalizePrice(draft[f.key]) !== normalizePrice(r.pricing[f.key]),
                )
                const inputVal = normalizePrice(draft.input)
                const outputVal = normalizePrice(draft.output)

                return (
                  <article
                    key={r.model}
                    className={cn(
                      'group/card relative overflow-hidden rounded-[24px] border bg-card transition-all duration-300',
                      dirty
                        ? 'border-primary/30 shadow-[0_18px_50px_-28px_color-mix(in_oklab,var(--color-primary)_55%,transparent)] ring-1 ring-primary/10'
                        : 'border-border/80 shadow-[0_1px_0_rgba(15,23,42,0.03)] hover:border-border hover:shadow-[0_16px_40px_-28px_rgba(15,23,42,0.28)]',
                    )}
                  >
                    <div className="p-4 sm:p-5">
                      {/* Header */}
                      <div className="flex flex-col gap-3.5 sm:flex-row sm:items-start sm:justify-between">
                        <div className="flex min-w-0 items-start gap-3.5">
                          <ModelLogo model={r.model} size={44} variant="ring" className="rounded-2xl" />
                          <div className="min-w-0">
                            <div className="flex flex-wrap items-center gap-2">
                              <h4 className="truncate font-mono text-[15px] font-semibold tracking-tight text-foreground sm:text-base">
                                {r.model}
                              </h4>
                              <span
                                className={cn(
                                  'inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-bold ring-1 ring-inset',
                                  source.className,
                                )}
                              >
                                <span className={cn('size-1.5 rounded-full', source.dot)} />
                                {t(source.labelKey)}
                              </span>
                              {dirty ? (
                                <span className="inline-flex items-center gap-1 rounded-full bg-amber-500/12 px-2 py-0.5 text-[10px] font-bold text-amber-700 ring-1 ring-inset ring-amber-500/20 dark:text-amber-300">
                                  <span className="size-1.5 animate-pulse rounded-full bg-amber-500" />
                                  {t('settings.pricing.unsaved')}
                                </span>
                              ) : (
                                <span className="inline-flex items-center gap-1 rounded-full bg-emerald-500/10 px-2 py-0.5 text-[10px] font-bold text-emerald-700 ring-1 ring-inset ring-emerald-500/15 dark:text-emerald-300">
                                  <Check className="size-2.5" />
                                  {t('settings.pricing.syncedState')}
                                </span>
                              )}
                            </div>
                            <p className="mt-1 text-[12px] text-muted-foreground">
                              <span className="font-medium text-foreground/80">
                                ${formatPriceDisplay(inputVal)}
                              </span>
                              <span className="mx-1.5 text-border">→</span>
                              <span className="font-medium text-foreground/80">
                                ${formatPriceDisplay(outputVal)}
                              </span>
                              <span className="ml-1.5 text-muted-foreground/80">
                                {t('settings.pricing.perMillion')}
                              </span>
                            </p>
                          </div>
                        </div>

                        <div className="flex shrink-0 items-center gap-1.5 sm:pt-0.5">
                          {r.source !== 'default' ? (
                            <Button
                              size="sm"
                              variant="ghost"
                              className="h-9 rounded-xl text-muted-foreground"
                              disabled={busy}
                              onClick={() => void reset(r.model)}
                            >
                              <RotateCcw className={cn('size-3.5', busy && 'animate-spin')} />
                              <span className="max-sm:hidden">{t('settings.pricing.resetBtn')}</span>
                            </Button>
                          ) : null}
                          <Button
                            size="sm"
                            className={cn(
                              'h-9 min-w-[96px] rounded-xl transition-all',
                              dirty
                                ? 'shadow-[0_8px_20px_-10px_color-mix(in_oklab,var(--color-primary)_70%,transparent)]'
                                : '',
                            )}
                            disabled={busy || !dirty}
                            onClick={() => void save(r.model)}
                          >
                            {busy ? (
                              <Loader2 className="size-3.5 animate-spin" />
                            ) : (
                              <Save className="size-3.5" />
                            )}
                            {busy ? t('common.saving') : t('common.save')}
                          </Button>
                        </div>
                      </div>

                      {/* Primary rates */}
                      <div className="mt-4 grid grid-cols-1 gap-2.5 min-[480px]:grid-cols-3">
                        {PRIMARY_FIELDS.map((field) => (
                          <PriceField
                            key={field.key}
                            field={field}
                            value={normalizePrice(draft[field.key])}
                            changed={
                              normalizePrice(draft[field.key]) !== normalizePrice(r.pricing[field.key])
                            }
                            onChange={(next) => setField(r.model, field.key, next)}
                          />
                        ))}
                      </div>

                      {/* Advanced rates */}
                      <div className="mt-3">
                        <button
                          type="button"
                          onClick={() =>
                            setExpandedAdvanced((prev) => ({
                              ...prev,
                              [r.model]: !advancedOpen,
                            }))
                          }
                          className="flex w-full items-center justify-between gap-2 rounded-xl px-1 py-1.5 text-left transition-colors hover:bg-muted/40"
                        >
                          <span className="flex items-center gap-2 text-[12px] font-semibold text-muted-foreground">
                            <ChevronDown
                              className={cn(
                                'size-3.5 transition-transform duration-200',
                                advancedOpen && 'rotate-180',
                              )}
                            />
                            {t('settings.pricing.advancedRates')}
                            {advancedDirty ? (
                              <span className="rounded-full bg-amber-500/12 px-1.5 py-0.5 text-[10px] font-bold text-amber-700 dark:text-amber-300">
                                {t('settings.pricing.unsaved')}
                              </span>
                            ) : null}
                          </span>
                          <span className="text-[11px] text-muted-foreground/70">
                            {t('settings.pricing.advancedRatesHint')}
                          </span>
                        </button>

                        <div
                          className={cn(
                            'grid transition-[grid-template-rows,opacity] duration-300 ease-out',
                            advancedOpen ? 'grid-rows-[1fr] opacity-100' : 'grid-rows-[0fr] opacity-0',
                          )}
                        >
                          <div className="min-h-0 overflow-hidden">
                            <div className="grid grid-cols-1 gap-2.5 pt-2 min-[480px]:grid-cols-2 xl:grid-cols-4">
                              {ADVANCED_FIELDS.map((field) => (
                                <PriceField
                                  key={field.key}
                                  field={field}
                                  dense
                                  value={normalizePrice(draft[field.key])}
                                  changed={
                                    normalizePrice(draft[field.key]) !==
                                    normalizePrice(r.pricing[field.key])
                                  }
                                  onChange={(next) => setField(r.model, field.key, next)}
                                />
                              ))}
                            </div>
                          </div>
                        </div>
                      </div>
                    </div>
                  </article>
                )
              })}
            </div>
          )}
        </div>
      </StateShell>
    </div>
  )
}
