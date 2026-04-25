import type { ChangeEvent, KeyboardEvent, ReactNode } from 'react'
import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../api'
import PageHeader from '../components/PageHeader'
import StateShell from '../components/StateShell'
import ToastNotice from '../components/ToastNotice'
import { useConfirmDialog } from '../hooks/useConfirmDialog'
import { useDataLoader } from '../hooks/useDataLoader'
import { useToast } from '../hooks/useToast'
import type { APIKeyRow } from '../types'
import { getErrorMessage } from '../utils/error'
import { formatRelativeTime } from '../utils/time'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  Copy,
  Eye,
  EyeOff,
  Fingerprint,
  KeyRound,
  LockKeyhole,
  Plus,
  ShieldCheck,
  Trash2,
} from 'lucide-react'

export default function APIKeys() {
  const { t } = useTranslation()
  const [newKeyName, setNewKeyName] = useState('')
  const [newKeyValue, setNewKeyValue] = useState('')
  const [createdKeyId, setCreatedKeyId] = useState<number | null>(null)
  const [visibleKeys, setVisibleKeys] = useState<Set<number>>(new Set())
  const [creating, setCreating] = useState(false)
  const [deletingIds, setDeletingIds] = useState<Set<number>>(new Set())
  const { toast, showToast } = useToast()
  const { confirm, confirmDialog } = useConfirmDialog()

  const loadKeys = useCallback(async () => {
    const response = await api.getAPIKeys()
    return response.keys ?? []
  }, [])

  const { data: keys, loading, error, reload } = useDataLoader<APIKeyRow[]>({
    initialData: [],
    load: loadKeys,
  })

  const latestKey = useMemo(() => {
    return keys
      .slice()
      .sort((a, b) => new Date(b.created_at || 0).getTime() - new Date(a.created_at || 0).getTime())[0]
  }, [keys])

  const handleCreateKey = async () => {
    setCreating(true)
    try {
      const result = await api.createAPIKey(newKeyName.trim() || t('apiKeys.defaultName'), newKeyValue.trim() || undefined)
      setCreatedKeyId(result.id)
      setVisibleKeys((prev) => new Set(prev).add(result.id))
      setNewKeyName('')
      setNewKeyValue('')
      showToast(t('apiKeys.keyCreateSuccess'))
      void reload()
    } catch (error) {
      showToast(`${t('apiKeys.createFailed')}: ${getErrorMessage(error)}`, 'error')
    } finally {
      setCreating(false)
    }
  }

  const handleDeleteKey = async (id: number) => {
    const confirmed = await confirm({
      title: t('apiKeys.deleteKeyTitle'),
      description: t('apiKeys.deleteKeyDesc'),
      confirmText: t('apiKeys.confirmDelete'),
      tone: 'destructive',
      confirmVariant: 'destructive',
    })
    if (!confirmed) return

    setDeletingIds((prev) => new Set(prev).add(id))
    try {
      await api.deleteAPIKey(id)
      showToast(t('apiKeys.keyDeleted'))
      if (createdKeyId === id) setCreatedKeyId(null)
      setVisibleKeys((prev) => {
        const next = new Set(prev)
        next.delete(id)
        return next
      })
      void reload()
    } catch (error) {
      showToast(`${t('apiKeys.deleteFailed')}: ${getErrorMessage(error)}`, 'error')
    } finally {
      setDeletingIds((prev) => {
        const next = new Set(prev)
        next.delete(id)
        return next
      })
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

      if (!copied) throw new Error('copy failed')
      showToast(t('common.copied'))
    } catch {
      showToast(t('common.copyFailed'), 'error')
    }
  }

  const toggleVisible = (id: number) => {
    setVisibleKeys((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  return (
    <StateShell
      variant="page"
      loading={loading}
      error={error}
      onRetry={() => void reload()}
      loadingTitle={t('apiKeys.loadingTitle')}
      loadingDescription={t('apiKeys.loadingDesc')}
      errorTitle={t('apiKeys.errorTitle')}
    >
      <>
        <PageHeader
          title={t('apiKeys.title')}
          description={t('apiKeys.description')}
          onRefresh={() => void reload()}
        />

        <div className="mb-4 grid gap-3 md:grid-cols-3">
          <KeySummaryCard
            icon={<KeyRound className="size-5" />}
            label={t('apiKeys.totalKeys')}
            value={String(keys.length)}
            sub={keys.length > 0 ? t('apiKeys.totalKeysDesc') : t('apiKeys.noKeysShort')}
            tone="info"
          />
          <KeySummaryCard
            icon={<ShieldCheck className="size-5" />}
            label={t('apiKeys.authMode')}
            value={keys.length > 0 ? t('apiKeys.authEnabled') : t('apiKeys.authDisabled')}
            sub={keys.length > 0 ? t('apiKeys.authEnabledDesc') : t('apiKeys.authDisabledDesc')}
            tone={keys.length > 0 ? 'success' : 'warning'}
          />
          <KeySummaryCard
            icon={<Fingerprint className="size-5" />}
            label={t('apiKeys.newestKey')}
            value={latestKey?.name || '-'}
            sub={latestKey ? formatRelativeTime(latestKey.created_at, { variant: 'compact' }) : t('apiKeys.noLatest')}
            tone="neutral"
          />
        </div>

        <div className="grid gap-4 xl:grid-cols-[minmax(320px,420px)_minmax(0,1fr)]">
          <div className="space-y-4">
            <Card className="py-0">
              <CardContent className="p-4">
                <div className="mb-4 flex items-start gap-3">
                  <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
                    <Plus className="size-5" />
                  </div>
                  <div>
                    <h3 className="text-base font-semibold text-foreground">{t('apiKeys.createTitle')}</h3>
                    <p className="mt-1 text-sm leading-relaxed text-muted-foreground">{t('apiKeys.createDesc')}</p>
                  </div>
                </div>

                <div className="space-y-3">
                  <div>
                    <label className="mb-1.5 block text-xs font-semibold text-muted-foreground">{t('apiKeys.nameLabel')}</label>
                    <Input
                      placeholder={t('apiKeys.keyNamePlaceholder')}
                      value={newKeyName}
                      onChange={(event: ChangeEvent<HTMLInputElement>) => setNewKeyName(event.target.value)}
                    />
                  </div>
                  <div>
                    <label className="mb-1.5 block text-xs font-semibold text-muted-foreground">{t('apiKeys.keyLabel')}</label>
                    <Input
                      placeholder={t('apiKeys.keyValuePlaceholder')}
                      value={newKeyValue}
                      onChange={(event: ChangeEvent<HTMLInputElement>) => setNewKeyValue(event.target.value)}
                      onKeyDown={(event: KeyboardEvent<HTMLInputElement>) => {
                        if (event.key === 'Enter' && !creating) {
                          void handleCreateKey()
                        }
                      }}
                    />
                  </div>
                  <Button onClick={() => void handleCreateKey()} disabled={creating} className="w-full">
                    <Plus className="size-3.5" />
                    {creating ? t('apiKeys.creating') : t('apiKeys.createKey')}
                  </Button>
                </div>
              </CardContent>
            </Card>

            <Card className="py-0">
              <CardContent className="p-4">
                <div className="mb-3 flex items-center gap-2 text-sm font-semibold text-foreground">
                  <LockKeyhole className="size-4 text-primary" />
                  {t('apiKeys.securityTitle')}
                </div>
                <p className="text-sm leading-relaxed text-muted-foreground">{t('apiKeys.keyAuthNote')}</p>
              </CardContent>
            </Card>
          </div>

          <Card>
            <CardContent className="p-4">
              <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
                <div>
                  <h3 className="text-base font-semibold text-foreground">{t('apiKeys.tableTitle')}</h3>
                  <p className="mt-1 text-sm text-muted-foreground">{t('apiKeys.tableDesc')}</p>
                </div>
                <Badge variant={keys.length > 0 ? 'default' : 'secondary'}>
                  {t('apiKeys.keyCount', { count: keys.length })}
                </Badge>
              </div>

              <StateShell
                variant="section"
                isEmpty={keys.length === 0}
                emptyTitle={t('apiKeys.noKeys')}
                emptyDescription={t('apiKeys.noKeysDesc')}
              >
                <div className="data-table-shell">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>{t('common.name')}</TableHead>
                        <TableHead>{t('apiKeys.keyColumn')}</TableHead>
                        <TableHead>{t('common.createdAt')}</TableHead>
                        <TableHead className="text-right">{t('common.actions')}</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {keys.map((keyRow) => {
                        const isVisible = visibleKeys.has(keyRow.id)
                        const isNew = createdKeyId === keyRow.id
                        const displayKey = isVisible ? keyRow.raw_key : keyRow.key
                        const copyValue = keyRow.raw_key || keyRow.key
                        return (
                          <TableRow key={keyRow.id} className={isNew ? 'bg-[hsl(var(--success-bg))]' : ''}>
                            <TableCell className="font-medium text-foreground">
                              <div className="flex items-center gap-2">
                                <span>{keyRow.name}</span>
                                {isNew ? (
                                  <Badge variant="outline" className="border-transparent bg-[hsl(var(--success-bg))] text-[hsl(var(--success))]">
                                    {t('apiKeys.newBadge')}
                                  </Badge>
                                ) : null}
                              </div>
                            </TableCell>
                            <TableCell>
                              <div className="flex min-w-[260px] items-center gap-2">
                                <code className="min-w-0 max-w-[420px] truncate rounded-md bg-muted px-2 py-1 font-mono text-[13px] text-foreground" title={displayKey}>
                                  {displayKey}
                                </code>
                                <Button
                                  variant="ghost"
                                  size="icon-xs"
                                  onClick={() => toggleVisible(keyRow.id)}
                                  title={isVisible ? t('apiKeys.hideKey') : t('apiKeys.showKey')}
                                >
                                  {isVisible ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
                                </Button>
                                <Button
                                  variant="ghost"
                                  size="icon-xs"
                                  onClick={() => void handleCopy(copyValue)}
                                  title={t('common.copy')}
                                >
                                  <Copy className="size-3.5" />
                                </Button>
                              </div>
                            </TableCell>
                            <TableCell className="text-muted-foreground">
                              {formatRelativeTime(keyRow.created_at, { variant: 'compact' })}
                            </TableCell>
                            <TableCell>
                              <div className="flex justify-end">
                                <Button
                                  variant="destructive"
                                  size="sm"
                                  disabled={deletingIds.has(keyRow.id)}
                                  onClick={() => void handleDeleteKey(keyRow.id)}
                                >
                                  <Trash2 className="size-3.5" />
                                  {t('common.delete')}
                                </Button>
                              </div>
                            </TableCell>
                          </TableRow>
                        )
                      })}
                    </TableBody>
                  </Table>
                </div>
              </StateShell>
            </CardContent>
          </Card>
        </div>

        <ToastNotice toast={toast} />
        {confirmDialog}
      </>
    </StateShell>
  )
}

function KeySummaryCard({
  icon,
  label,
  value,
  sub,
  tone,
}: {
  icon: ReactNode
  label: string
  value: string
  sub: string
  tone: 'neutral' | 'info' | 'success' | 'warning'
}) {
  const toneClassName = {
    neutral: 'bg-muted text-muted-foreground',
    info: 'bg-primary/10 text-primary',
    success: 'bg-[hsl(var(--success-bg))] text-[hsl(var(--success))]',
    warning: 'bg-[hsl(var(--warning-bg))] text-[hsl(var(--warning))]',
  }[tone]

  return (
    <Card className="py-0">
      <CardContent className="flex items-center justify-between gap-3 p-4">
        <div className="min-w-0">
          <div className="text-[11px] font-bold uppercase text-muted-foreground">{label}</div>
          <div className="mt-2 truncate text-[24px] font-bold leading-none text-foreground">{value}</div>
          <div className="mt-2 truncate text-xs text-muted-foreground">{sub}</div>
        </div>
        <div className={`flex size-10 shrink-0 items-center justify-center rounded-lg ${toneClassName}`}>
          {icon}
        </div>
      </CardContent>
    </Card>
  )
}
