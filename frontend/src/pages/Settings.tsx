import type { ChangeEvent, KeyboardEvent } from 'react'
import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api, resetAdminAuthState, setAdminKey } from '../api'
import { getTimezone, setTimezone } from '../utils/time'
import PageHeader from '../components/PageHeader'
import StateShell from '../components/StateShell'
import ToastNotice from '../components/ToastNotice'
import { useDataLoader } from '../hooks/useDataLoader'
import { useConfirmDialog } from '../hooks/useConfirmDialog'
import { useToast } from '../hooks/useToast'
import type { APIKeyRow, HealthResponse, SystemSettings } from '../types'
import { getErrorMessage } from '../utils/error'
import { formatRelativeTime } from '../utils/time'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import { Trash2, Eye, EyeOff } from 'lucide-react'

// 默认模型映射
const DEFAULT_MODEL_MAPPING: Record<string, string> = {
  'claude-opus-4-6': 'gpt-5.4',
  'claude-opus-4-6-20250610': 'gpt-5.4',
  'claude-haiku-4-5-20251001': 'gpt-5.4-mini',
  'claude-haiku-4-5': 'gpt-5.4-mini',
  'claude-sonnet-4-6': 'gpt-5.3-codex',
  'claude-sonnet-4-5-20250929': 'gpt-5.2-codex',
  'claude-opus-4-5-20251101': 'gpt-5.3-codex',
}

// 模型映射编辑器组件
function ModelMappingEditor({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const { t } = useTranslation()

  let mappings: [string, string][] = []
  try {
    const parsed = JSON.parse(value || '{}')
    const entries = Object.entries(parsed) as [string, string][]
    // 如果数据库中为空，用默认值填充
    mappings = entries.length > 0 ? entries : Object.entries(DEFAULT_MODEL_MAPPING) as [string, string][]
  } catch {
    mappings = Object.entries(DEFAULT_MODEL_MAPPING) as [string, string][]
  }

  const updateMappings = (entries: [string, string][]) => {
    const obj: Record<string, string> = {}
    for (const [k, v] of entries) {
      if (k.trim()) obj[k.trim()] = v.trim()
    }
    onChange(JSON.stringify(obj))
  }

  const handleChange = (index: number, field: 0 | 1, val: string) => {
    const next = [...mappings]
    next[index] = [...next[index]] as [string, string]
    next[index][field] = val
    updateMappings(next)
  }

  const handleRemove = (index: number) => {
    const next = mappings.filter((_, i) => i !== index)
    updateMappings(next)
  }

  const handleAdd = () => {
    updateMappings([...mappings, ['', '']])
  }

  return (
    <div className="space-y-2">
      <div className="grid grid-cols-[1fr_1fr_40px] gap-2 text-xs font-semibold text-muted-foreground">
        <span>{t('settings2.anthropicModel')}</span>
        <span>{t('settings2.codexModel')}</span>
        <span />
      </div>
      {mappings.map(([k, v], i) => (
        <div key={i} className="grid grid-cols-[1fr_1fr_40px] gap-2 items-center">
          <Input
            className="font-mono text-[13px]"
            placeholder="claude-opus-4-6"
            value={k}
            onChange={(e: ChangeEvent<HTMLInputElement>) => handleChange(i, 0, e.target.value)}
          />
          <Input
            className="font-mono text-[13px]"
            placeholder="gpt-5.4"
            value={v}
            onChange={(e: ChangeEvent<HTMLInputElement>) => handleChange(i, 1, e.target.value)}
          />
          <button
            onClick={() => handleRemove(i)}
            className="flex items-center justify-center size-9 rounded-lg text-muted-foreground hover:text-red-500 hover:bg-red-50 dark:hover:bg-red-500/10 transition-colors"
          >
            <Trash2 className="size-4" />
          </button>
        </div>
      ))}
      <Button variant="outline" size="sm" onClick={handleAdd}>
        + {t('settings2.addMapping')}
      </Button>
    </div>
  )
}

function maskKey(key: string): string {
  if (!key || key.length < 12) return key
  return key.slice(0, 7) + '???????' + key.slice(-4)
}

export default function Settings() {
  const { t } = useTranslation()
  const booleanOptions = [
    { label: t('common.disabled'), value: 'false' },
    { label: t('common.enabled'), value: 'true' },
  ]
  const [newKeyName, setNewKeyName] = useState('')
  const [newKeyValue, setNewKeyValue] = useState('')
  const [createdKey, setCreatedKey] = useState<string | null>(null)
  const [settingsForm, setSettingsForm] = useState<SystemSettings>({
    max_concurrency: 2,
    global_rpm: 0,
    test_model: '',
    test_concurrency: 50,
    background_refresh_interval_minutes: 2,
    usage_probe_max_age_minutes: 10,
    recovery_probe_interval_minutes: 30,
    pg_max_conns: 50,
    redis_pool_size: 30,
    auto_clean_unauthorized: false,
    auto_clean_rate_limited: false,
    auto_clean_error: false,
    auto_clean_expired: false,
    admin_secret: '',
    admin_auth_source: 'disabled',
    auto_clean_full_usage: false,
    proxy_pool_enabled: false,
    fast_scheduler_enabled: false,
    max_retries: 2,
    allow_remote_migration: false,
    database_driver: 'postgres',
    database_label: 'PostgreSQL',
    cache_driver: 'redis',
    cache_label: 'Redis',
    model_mapping: '{}',
    resin_url: '',
    resin_platform_name: '',
  })
  const [savingSettings, setSavingSettings] = useState(false)
  const [loadedAdminSecret, setLoadedAdminSecret] = useState('')
  const [modelList, setModelList] = useState<string[]>([])
  const [visibleKeys, setVisibleKeys] = useState<Set<number>>(new Set())
  const { toast, showToast } = useToast()
  const { confirm, confirmDialog } = useConfirmDialog()

  const loadSettingsData = useCallback(async () => {
    const [health, keysResponse, settings, modelsResp] = await Promise.all([api.getHealth(), api.getAPIKeys(), api.getSettings(), api.getModels()])
    setSettingsForm(settings)
    setLoadedAdminSecret(settings.admin_secret ?? '')
    setModelList(modelsResp.models ?? [])
    return {
      health,
      keys: keysResponse.keys ?? [],
    }
  }, [])

  const { data, loading, error, reload } = useDataLoader<{
    health: HealthResponse | null
    keys: APIKeyRow[]
  }>({
    initialData: {
      health: null,
      keys: [],
    },
    load: loadSettingsData,
  })

  const handleCreateKey = async () => {
    try {
      const result = await api.createAPIKey(newKeyName.trim() || 'default', newKeyValue.trim() || undefined)
      setCreatedKey(result.key)
      setNewKeyName('')
      setNewKeyValue('')
      showToast(t('settings.keyCreateSuccess'))
      void reload()
    } catch (error) {
      showToast(`${t('settings.createFailed')}: ${getErrorMessage(error)}`, 'error')
    }
  }

  const handleDeleteKey = async (id: number) => {
    const confirmed = await confirm({
      title: t('settings.deleteKeyTitle'),
      description: t('settings.deleteKeyDesc'),
      confirmText: t('settings.confirmDelete'),
      tone: 'destructive',
      confirmVariant: 'destructive',
    })
    if (!confirmed) {
      return
    }

    try {
      await api.deleteAPIKey(id)
      showToast(t('settings.keyDeleted'))
      void reload()
    } catch (error) {
      showToast(`${t('settings.deleteFailed')}: ${getErrorMessage(error)}`, 'error')
    }
  }

  const handleCopy = async (text: string) => {
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(text)
        showToast(t('common.copied'))
        return
      }

      const textarea = document.createElement('textarea')
      textarea.value = text
      textarea.setAttribute('readonly', 'true')
      textarea.style.position = 'fixed'
      textarea.style.opacity = '0'
      textarea.style.pointerEvents = 'none'
      document.body.appendChild(textarea)
      textarea.select()
      textarea.setSelectionRange(0, text.length)
      const copied = document.execCommand('copy')
      document.body.removeChild(textarea)

      if (!copied) {
        throw new Error('copy failed')
      }

      showToast(t('common.copied'))
    } catch {
      showToast(t('common.copyFailed'), 'error')
    }
  }

  const handleSaveSettings = async () => {
    setSavingSettings(true)
    try {
      const adminSecretChanged = settingsForm.admin_auth_source !== 'env' && settingsForm.admin_secret !== loadedAdminSecret
      const updated = await api.updateSettings(settingsForm)
      setSettingsForm(updated)
      setLoadedAdminSecret(updated.admin_secret ?? '')
      if (updated.admin_auth_source !== 'env') {
        setAdminKey(updated.admin_secret ?? '')
      }
      if (adminSecretChanged) {
        resetAdminAuthState()
        return
      }
      if (updated.expired_cleaned && updated.expired_cleaned > 0) {
        showToast(t('settings.expiredCleanedResult', { count: updated.expired_cleaned }))
      } else {
        showToast(t('settings.saveSuccess'))
      }
    } catch (error) {
      showToast(`${t('settings.saveFailed')}: ${getErrorMessage(error)}`, 'error')
    } finally {
      setSavingSettings(false)
    }
  }

  const { health, keys } = data
  const isExternalDatabase = settingsForm.database_driver === 'postgres'
  const isExternalCache = settingsForm.cache_driver === 'redis'
  const showConnectionPool = isExternalDatabase || isExternalCache
  const canConfigureRemoteMigration = settingsForm.admin_auth_source === 'env' || settingsForm.admin_secret.trim() !== ''
  return (
    <StateShell
      variant="page"
      loading={loading}
      error={error}
      onRetry={() => void reload()}
      loadingTitle={t('settings.loadingTitle')}
      loadingDescription={t('settings.loadingDesc')}
      errorTitle={t('settings.errorTitle')}
    >
      <>
        <PageHeader
          title={t('settings.title')}
          description={t('settings.description')}
        />

        {/* API Keys */}
        <Card className="mb-4">
          <CardContent className="p-6">
            <div className="flex items-center justify-between gap-4 mb-4">
              <h3 className="text-base font-semibold text-foreground">{t('settings.apiKeys')}</h3>
            </div>

            <div className="flex gap-2 mb-4 flex-wrap">
              <Input
                className="flex-[1_1_120px]"
                placeholder={t('settings.keyNamePlaceholder')}
                value={newKeyName}
                onChange={(event: ChangeEvent<HTMLInputElement>) => setNewKeyName(event.target.value)}
              />
              <Input
                className="flex-[2_1_240px]"
                placeholder={t('settings.keyValuePlaceholder')}
                value={newKeyValue}
                onChange={(event: ChangeEvent<HTMLInputElement>) => setNewKeyValue(event.target.value)}
                onKeyDown={(event: KeyboardEvent<HTMLInputElement>) => {
                  if (event.key === 'Enter') {
                    void handleCreateKey()
                  }
                }}
              />
              <Button onClick={() => void handleCreateKey()} className="whitespace-nowrap">
                {t('settings.createKey')}
              </Button>
            </div>

            {createdKey ? (
              <div className="p-3 mb-4 rounded-xl bg-[hsl(var(--success-bg))] border border-[hsl(var(--success))]/20 text-sm">
                <div className="font-semibold mb-1 text-[hsl(var(--success))]">{t('settings.keyCreated')}</div>
                <div className="flex items-center gap-2">
                  <code className="flex-1 font-mono text-[13px] break-all">{createdKey}</code>
                  <Button variant="outline" size="sm" onClick={() => void handleCopy(createdKey)}>{t('common.copy')}</Button>
                </div>
              </div>
            ) : null}

            <StateShell
              variant="section"
              isEmpty={keys.length === 0}
              emptyTitle={t('settings.noKeys')}
              emptyDescription={t('settings.noKeysDesc')}
            >
              <div className="overflow-auto border border-border rounded-xl">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead className="text-[13px] font-semibold">{t('common.name')}</TableHead>
                      <TableHead className="text-[13px] font-semibold">{t('common.key')}</TableHead>
                      <TableHead className="text-[13px] font-semibold">{t('common.createdAt')}</TableHead>
                      <TableHead className="text-[13px] font-semibold">{t('common.actions')}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {keys.map((keyRow) => (
                      <TableRow key={keyRow.id}>
                        <TableCell className="text-[14px] font-medium">{keyRow.name}</TableCell>
                        <TableCell>
                          <div className="flex items-center gap-2">
                            <span className="font-mono text-[14px]">
                              {visibleKeys.has(keyRow.id) ? keyRow.raw_key : keyRow.key}
                            </span>
                            <button
                              onClick={() => setVisibleKeys(prev => {
                                const next = new Set(prev)
                                if (next.has(keyRow.id)) next.delete(keyRow.id)
                                else next.add(keyRow.id)
                                return next
                              })}
                              className="flex items-center justify-center size-7 rounded-lg text-muted-foreground hover:text-foreground hover:bg-muted/60 transition-colors shrink-0"
                            >
                              {visibleKeys.has(keyRow.id) ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
                            </button>
                          </div>
                        </TableCell>
                        <TableCell className="text-[14px] text-muted-foreground">
                          {formatRelativeTime(keyRow.created_at, { variant: 'compact' })}
                        </TableCell>
                        <TableCell>
                          <Button variant="destructive" size="sm" onClick={() => void handleDeleteKey(keyRow.id)}>
                            {t('common.delete')}
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            </StateShell>

            <div className="text-xs text-muted-foreground mt-3">
              {t('settings.keyAuthNote')}
            </div>
          </CardContent>
        </Card>

        {/* System Status */}
        <Card className="mb-4">
          <CardContent className="p-6">
            <h3 className="text-base font-semibold text-foreground mb-4">{t('settings.systemStatus')}</h3>
            <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-3.5">
              <div className="flex flex-col gap-1.5 p-3.5 rounded-2xl bg-white/40 border border-border">
                <label className="text-xs font-bold text-muted-foreground">{t('settings.service')}</label>
                <div className="text-[15px] font-semibold">
                  <Badge variant={health?.status === 'ok' ? 'default' : 'destructive'} className="gap-1.5">
                    <span className={`size-1.5 rounded-full ${health?.status === 'ok' ? 'bg-emerald-500' : 'bg-red-400'}`} />
                    {health?.status === 'ok' ? t('common.running') : t('common.error')}
                  </Badge>
                </div>
              </div>
              <div className="flex flex-col gap-1.5 p-3.5 rounded-2xl bg-white/40 border border-border">
                <label className="text-xs font-bold text-muted-foreground">{t('settings.accountsLabel')}</label>
                <div className="text-[15px] font-semibold">{health?.available ?? 0} / {health?.total ?? 0}</div>
              </div>
              <div className="flex flex-col gap-1.5 p-3.5 rounded-2xl bg-white/40 border border-border">
                <label className="text-xs font-bold text-muted-foreground">{settingsForm.database_label}</label>
                <div className="text-[15px] font-semibold">
                  <Badge variant="default" className="gap-1.5">
                    <span className="size-1.5 rounded-full bg-emerald-500" />
                    {isExternalDatabase ? t('common.connected') : t('common.running')}
                  </Badge>
                </div>
              </div>
              <div className="flex flex-col gap-1.5 p-3.5 rounded-2xl bg-white/40 border border-border">
                <label className="text-xs font-bold text-muted-foreground">{settingsForm.cache_label}</label>
                <div className="text-[15px] font-semibold">
                  <Badge variant="default" className="gap-1.5">
                    <span className="size-1.5 rounded-full bg-emerald-500" />
                    {isExternalCache ? t('common.connected') : t('common.running')}
                  </Badge>
                </div>
              </div>
            </div>
          </CardContent>
        </Card>

        {/* Protection Settings */}
        <Card className="mb-4">
          <CardContent className="p-6">
            <h3 className="text-base font-semibold text-foreground mb-4">{t('settings.trafficProtection')}</h3>
            <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4 mb-4">
              <div>
                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.maxConcurrency')}</label>
                <Input
                  type="number"
                  min={1}
                  max={50}
                  value={settingsForm.max_concurrency}
                  onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm(f => ({ ...f, max_concurrency: parseInt(e.target.value) || 1 }))}
                />
                <p className="text-xs text-muted-foreground mt-1">{t('settings.maxConcurrencyRange')}</p>
              </div>
              <div>
                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.globalRpm')}</label>
                <Input
                  type="number"
                  min={0}
                  value={settingsForm.global_rpm}
                  onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm(f => ({ ...f, global_rpm: parseInt(e.target.value) || 0 }))}
                />
                <p className="text-xs text-muted-foreground mt-1">{t('settings.globalRpmRange')}</p>
              </div>
              <div>
                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.maxRetries')}</label>
                <Input
                  type="number"
                  min={0}
                  max={10}
                  value={settingsForm.max_retries}
                  onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm(f => ({ ...f, max_retries: parseInt(e.target.value) || 0 }))}
                />
                <p className="text-xs text-muted-foreground mt-1">{t('settings.maxRetriesRange')}</p>
              </div>
              <div>
                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.testModelLabel')}</label>
                <Select
                  value={settingsForm.test_model}
                  onValueChange={(value) => setSettingsForm((f) => ({ ...f, test_model: value }))}
                  options={modelList.map((model) => ({ label: model, value: model }))}
                />
                <p className="text-xs text-muted-foreground mt-1">{t('settings.testModelHint')}</p>
              </div>
              <div>
                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.testConcurrency')}</label>
                <Input
                  type="number"
                  min={1}
                  max={200}
                  value={settingsForm.test_concurrency}
                  onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm(f => ({ ...f, test_concurrency: parseInt(e.target.value) || 1 }))}
                />
                <p className="text-xs text-muted-foreground mt-1">{t('settings.testConcurrencyRange')}</p>
              </div>
              <div>
                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.backgroundRefreshInterval')}</label>
                <Input
                  type="number"
                  min={1}
                  max={1440}
                  value={settingsForm.background_refresh_interval_minutes}
                  onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm(f => ({ ...f, background_refresh_interval_minutes: parseInt(e.target.value) || 1 }))}
                />
                <p className="text-xs text-muted-foreground mt-1">{t('settings.backgroundRefreshIntervalDesc')}</p>
              </div>
              <div>
                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.usageProbeMaxAge')}</label>
                <Input
                  type="number"
                  min={1}
                  max={10080}
                  value={settingsForm.usage_probe_max_age_minutes}
                  onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm(f => ({ ...f, usage_probe_max_age_minutes: parseInt(e.target.value) || 1 }))}
                />
                <p className="text-xs text-muted-foreground mt-1">{t('settings.usageProbeMaxAgeDesc')}</p>
              </div>
              <div>
                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.recoveryProbeInterval')}</label>
                <Input
                  type="number"
                  min={1}
                  max={10080}
                  value={settingsForm.recovery_probe_interval_minutes}
                  onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm(f => ({ ...f, recovery_probe_interval_minutes: parseInt(e.target.value) || 1 }))}
                />
                <p className="text-xs text-muted-foreground mt-1">{t('settings.recoveryProbeIntervalDesc')}</p>
              </div>
            </div>
            {showConnectionPool ? (
              <>
                <h3 className="text-base font-semibold text-foreground mb-4 mt-6">{t('settings.connectionPool')}</h3>
                <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-4 mb-4">
                  {isExternalDatabase ? (
                    <div>
                      <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.pgMaxConns')}</label>
                      <Input
                        type="number"
                        min={5}
                        max={500}
                        value={settingsForm.pg_max_conns}
                        onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm(f => ({ ...f, pg_max_conns: parseInt(e.target.value) || 50 }))}
                      />
                      <p className="text-xs text-muted-foreground mt-1">{t('settings.pgMaxConnsRange')}</p>
                    </div>
                  ) : null}
                  {isExternalCache ? (
                    <div>
                      <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.redisPoolSize')}</label>
                      <Input
                        type="number"
                        min={5}
                        max={500}
                        value={settingsForm.redis_pool_size}
                        onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm(f => ({ ...f, redis_pool_size: parseInt(e.target.value) || 30 }))}
                      />
                      <p className="text-xs text-muted-foreground mt-1">{t('settings.redisPoolSizeRange')}</p>
                    </div>
                  ) : null}
                </div>
              </>
            ) : null}
            <h3 className="text-base font-semibold text-foreground mb-4 mt-6">{t('settings.autoCleanup')}</h3>
            <div className="grid grid-cols-[repeat(auto-fit,minmax(280px,1fr))] gap-4 mb-4">
              <div>
                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.autoCleanUnauthorized')}</label>
                <Select
                  value={settingsForm.auto_clean_unauthorized ? 'true' : 'false'}
                  onValueChange={(value) => setSettingsForm((f) => ({ ...f, auto_clean_unauthorized: value === 'true' }))}
                  options={booleanOptions}
                />
                <p className="text-xs text-muted-foreground mt-1">{t('settings.autoCleanUnauthorizedDesc')}</p>
              </div>
              <div>
                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.autoCleanRateLimited')}</label>
                <Select
                  value={settingsForm.auto_clean_rate_limited ? 'true' : 'false'}
                  onValueChange={(value) => setSettingsForm((f) => ({ ...f, auto_clean_rate_limited: value === 'true' }))}
                  options={booleanOptions}
                />
                <p className="text-xs text-muted-foreground mt-1">{t('settings.autoCleanRateLimitedDesc')}</p>
              </div>
              <div>
                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.autoCleanFullUsage')}</label>
                <Select
                  value={settingsForm.auto_clean_full_usage ? 'true' : 'false'}
                  onValueChange={(value) => setSettingsForm((f) => ({ ...f, auto_clean_full_usage: value === 'true' }))}
                  options={booleanOptions}
                />
                <p className="text-xs text-muted-foreground mt-1">{t('settings.autoCleanFullUsageDesc')}</p>
              </div>
              <div>
                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.autoCleanError')}</label>
                <Select
                  value={settingsForm.auto_clean_error ? 'true' : 'false'}
                  onValueChange={(value) => setSettingsForm((f) => ({ ...f, auto_clean_error: value === 'true' }))}
                  options={booleanOptions}
                />
                <p className="text-xs text-muted-foreground mt-1">{t('settings.autoCleanErrorDesc')}</p>
              </div>
              <div>
                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.autoCleanExpired')}</label>
                <Select
                  value={settingsForm.auto_clean_expired ? 'true' : 'false'}
                  onValueChange={(value) => setSettingsForm((f) => ({ ...f, auto_clean_expired: value === 'true' }))}
                  options={booleanOptions}
                />
                <p className="text-xs text-muted-foreground mt-1">{t('settings.autoCleanExpiredDesc')}</p>
              </div>
            </div>
            <h3 className="text-base font-semibold text-foreground mb-4 mt-6">{t('settings.scheduler')}</h3>
            <div className="grid grid-cols-[repeat(auto-fit,minmax(280px,1fr))] gap-4 mb-4">
              <div>
                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.fastSchedulerEnabled')}</label>
                <Select
                  value={settingsForm.fast_scheduler_enabled ? 'true' : 'false'}
                  onValueChange={(value) => setSettingsForm((f) => ({ ...f, fast_scheduler_enabled: value === 'true' }))}
                  options={booleanOptions}
                />
                <p className="text-xs text-muted-foreground mt-1">{t('settings.fastSchedulerEnabledDesc')}</p>
              </div>
            </div>
            <h3 className="text-base font-semibold text-foreground mb-4 mt-6">{t('settings.display')}</h3>
            <div className="grid grid-cols-[repeat(auto-fit,minmax(280px,1fr))] gap-4 mb-4">
              <div>
                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.timezone')}</label>
                <Select
                  value={getTimezone()}
                  onValueChange={(value) => {
                    setTimezone(value)
                    window.location.reload()
                  }}
                  options={[
                    { label: t('settings.timezoneAuto'), value: Intl.DateTimeFormat().resolvedOptions().timeZone },
                    { label: '(UTC) UTC', value: 'UTC' },
                    { label: '(GMT+08:00) Asia/Shanghai', value: 'Asia/Shanghai' },
                    { label: '(GMT+09:00) Asia/Tokyo', value: 'Asia/Tokyo' },
                    { label: '(GMT+09:00) Asia/Seoul', value: 'Asia/Seoul' },
                    { label: '(GMT+08:00) Asia/Singapore', value: 'Asia/Singapore' },
                    { label: '(GMT+08:00) Asia/Hong_Kong', value: 'Asia/Hong_Kong' },
                    { label: '(GMT+08:00) Asia/Taipei', value: 'Asia/Taipei' },
                    { label: '(GMT+07:00) Asia/Bangkok', value: 'Asia/Bangkok' },
                    { label: '(GMT+04:00) Asia/Dubai', value: 'Asia/Dubai' },
                    { label: '(GMT+05:30) Asia/Kolkata', value: 'Asia/Kolkata' },
                    { label: '(GMT+01:00) Europe/London', value: 'Europe/London' },
                    { label: '(GMT+02:00) Europe/Paris', value: 'Europe/Paris' },
                    { label: '(GMT+02:00) Europe/Berlin', value: 'Europe/Berlin' },
                    { label: '(GMT+03:00) Europe/Moscow', value: 'Europe/Moscow' },
                    { label: '(GMT+02:00) Europe/Amsterdam', value: 'Europe/Amsterdam' },
                    { label: '(GMT+02:00) Europe/Rome', value: 'Europe/Rome' },
                    { label: '(GMT-04:00) America/New_York', value: 'America/New_York' },
                    { label: '(GMT-07:00) America/Los_Angeles', value: 'America/Los_Angeles' },
                    { label: '(GMT-05:00) America/Chicago', value: 'America/Chicago' },
                    { label: '(GMT-03:00) America/Sao_Paulo', value: 'America/Sao_Paulo' },
                    { label: '(GMT+10:00) Australia/Sydney', value: 'Australia/Sydney' },
                    { label: '(GMT+12:00) Pacific/Auckland', value: 'Pacific/Auckland' },
                  ]}
                />
                <p className="text-xs text-muted-foreground mt-1">{t('settings.timezoneDesc')}</p>
              </div>
            </div>
            <h3 className="text-base font-semibold text-foreground mb-4 mt-6">{t('settings.security')}</h3>
            <div className="grid grid-cols-[repeat(auto-fit,minmax(280px,1fr))] gap-4 mb-4">
              <div>
                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.adminSecret')}</label>
                <Input
                  type="text"
                  placeholder={t('settings.adminSecretPlaceholder')}
                  value={settingsForm.admin_secret}
                  disabled={settingsForm.admin_auth_source === 'env'}
                  onChange={(e: ChangeEvent<HTMLInputElement>) => setSettingsForm(f => {
                    const nextSecret = e.target.value
                    return {
                      ...f,
                      admin_secret: nextSecret,
                      allow_remote_migration: nextSecret.trim() === '' ? false : f.allow_remote_migration,
                    }
                  })}
                />
                <p className="text-xs text-muted-foreground mt-1">{t('settings.adminSecretDesc')}</p>
                {settingsForm.admin_auth_source === 'env' ? (
                  <p className="text-xs text-amber-600 mt-1">{t('settings.adminSecretEnvOverride')}</p>
                ) : null}
              </div>
              <div>
                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.allowRemoteMigration')}</label>
                <Select
                  value={settingsForm.allow_remote_migration ? 'true' : 'false'}
                  disabled={!canConfigureRemoteMigration}
                  onValueChange={(value) => setSettingsForm((f) => ({ ...f, allow_remote_migration: value === 'true' }))}
                  options={booleanOptions}
                />
                <p className="text-xs text-muted-foreground mt-1">{t('settings.allowRemoteMigrationDesc')}</p>
                {!canConfigureRemoteMigration ? (
                  <p className="text-xs text-amber-600 mt-1">{t('settings.allowRemoteMigrationRequiresSecret')}</p>
                ) : null}
              </div>
            </div>
            <Button onClick={() => void handleSaveSettings()} disabled={savingSettings}>
              {savingSettings ? t('common.saving') : t('settings.saveSettings')}
            </Button>
          </CardContent>
        </Card>

        {/* Model Mapping */}
        <Card className="mb-4">
          <CardContent className="p-6">
            <h3 className="text-base font-semibold text-foreground mb-2">{t('settings2.modelMapping')}</h3>
            <p className="text-xs text-muted-foreground mb-4">{t('settings2.modelMappingDesc')}</p>
            <ModelMappingEditor
              value={settingsForm.model_mapping}
              onChange={(v) => setSettingsForm(f => ({ ...f, model_mapping: v }))}
            />
          </CardContent>
        </Card>

        {/* Resin Proxy Pool */}
        <Card className="mb-4">
          <CardContent className="p-6">
            <h3 className="text-base font-semibold text-foreground mb-2">{t('settings.resinTitle')}</h3>
            <p className="text-xs text-muted-foreground mb-4">{t('settings.resinDesc')}</p>
            <div className="grid grid-cols-[repeat(auto-fit,minmax(280px,1fr))] gap-4">
              <div>
                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.resinUrl')}</label>
                <Input
                  placeholder="http://127.0.0.1:2260/your-token"
                  value={settingsForm.resin_url}
                  onChange={(e: React.ChangeEvent<HTMLInputElement>) => setSettingsForm(f => ({ ...f, resin_url: e.target.value }))}
                />
                <p className="text-xs text-muted-foreground mt-1">{t('settings.resinUrlDesc')}</p>
              </div>
              <div>
                <label className="block mb-2 text-sm font-semibold text-muted-foreground">{t('settings.resinPlatformName')}</label>
                <Input
                  placeholder="codex2api"
                  value={settingsForm.resin_platform_name}
                  onChange={(e: React.ChangeEvent<HTMLInputElement>) => setSettingsForm(f => ({ ...f, resin_platform_name: e.target.value }))}
                />
                <p className="text-xs text-muted-foreground mt-1">{t('settings.resinPlatformNameDesc')}</p>
              </div>
            </div>
            <Button className="mt-4" onClick={() => void handleSaveSettings()} disabled={savingSettings}>
              {savingSettings ? t('common.saving') : t('settings.saveSettings')}
            </Button>
          </CardContent>
        </Card>

        {/* API Endpoints */}
        <Card>
          <CardContent className="p-6">
            <h3 className="text-base font-semibold text-foreground mb-4">{t('settings.apiEndpoints')}</h3>
            <div className="overflow-auto border border-border rounded-xl">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="text-[13px] font-semibold">{t('settings.method')}</TableHead>
                    <TableHead className="text-[13px] font-semibold">{t('settings.path')}</TableHead>
                    <TableHead className="text-[13px] font-semibold">{t('settings.endpointDesc')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  <TableRow>
                    <TableCell><Badge variant="default" className="text-[13px]">POST</Badge></TableCell>
                    <TableCell className="font-mono text-[20px]">/v1/chat/completions</TableCell>
                    <TableCell className="text-[14px] text-muted-foreground">{t('settings.openaiCompat')}</TableCell>
                  </TableRow>
                  <TableRow>
                    <TableCell><Badge variant="outline" className="text-[13px]">POST</Badge></TableCell>
                    <TableCell className="font-mono text-[20px]">/v1/responses</TableCell>
                    <TableCell className="text-[14px] text-muted-foreground">{t('settings.responsesApi')}</TableCell>
                  </TableRow>
                  <TableRow>
                    <TableCell><Badge variant="outline" className="text-[13px]">POST</Badge></TableCell>
                    <TableCell className="font-mono text-[20px]">/v1/messages</TableCell>
                    <TableCell className="text-[14px] text-muted-foreground">{t('settings2.messagesEndpoint')}</TableCell>
                  </TableRow>
                  <TableRow>
                    <TableCell><Badge variant="secondary" className="text-[13px]">GET</Badge></TableCell>
                    <TableCell className="font-mono text-[20px]">/v1/models</TableCell>
                    <TableCell className="text-[14px] text-muted-foreground">{t('settings.modelList')}</TableCell>
                  </TableRow>
                </TableBody>
              </Table>
            </div>
          </CardContent>
        </Card>

        <ToastNotice toast={toast} />
        {confirmDialog}
      </>
    </StateShell>
  )
}
