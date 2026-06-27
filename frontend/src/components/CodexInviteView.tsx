import { useEffect, useMemo, useRef, useState } from 'react'
import type { KeyboardEvent as ReactKeyboardEvent } from 'react'
import { useTranslation } from 'react-i18next'
import {
  AlertTriangle,
  ArrowLeft,
  Check,
  ChevronDown,
  Copy,
  Mail,
  Send,
  Sparkles,
  UserCircle2,
} from 'lucide-react'
import PageHeader from './PageHeader'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { api } from '../api'
import type { AccountRow, InviteResult } from '../types'
import { getErrorMessage } from '../utils/error'
import { useToast } from '../hooks/useToast'

interface Props {
  accounts: AccountRow[]
  onClose: () => void
}

const MAX_EMAILS = 10
const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/
const SPLIT_RE = /[,;\r\n\t ]+/

interface ParsedEmails {
  valid: string[]
  invalid: string[]
  duplicates: number
}

// 与后端 collectInviteEmails 保持一致：按分隔符切分、去重（忽略大小写）、正则校验。
function parseEmails(text: string): ParsedEmails {
  const tokens = text.split(SPLIT_RE).map((s) => s.trim()).filter(Boolean)
  const seen = new Set<string>()
  const valid: string[] = []
  const invalid: string[] = []
  let duplicates = 0
  for (const tk of tokens) {
    if (!EMAIL_RE.test(tk)) {
      invalid.push(tk)
      continue
    }
    const key = tk.toLowerCase()
    if (seen.has(key)) {
      duplicates++
      continue
    }
    seen.add(key)
    valid.push(tk)
  }
  return { valid, invalid, duplicates }
}

function accountDisplayName(account: AccountRow): string {
  return account.email || account.name || `#${account.id}`
}

function accountSearchText(account: AccountRow): string {
  return [
    String(account.id),
    `#${account.id}`,
    account.email,
    account.name,
    account.status,
    account.plan_type,
  ]
    .filter(Boolean)
    .join(' ')
    .toLowerCase()
}

function isCodexInviteCandidate(account: AccountRow): boolean {
  // 仅排除发不出邀请的硬条件：中转 / AT-only 账号没有可用于 referral 的 Codex OAuth 凭证。
  // enabled / locked / status 只是调度开关与健康状态，不影响 access token 是否可用——
  // 后端 SendCodexInvite 只要求账号有 access token，不校验这些字段，故前端不在此过滤，
  // 否则会把仅被禁用调度或临时异常、但凭证仍可用的账号从下拉中隐藏（见 issue #281）。
  return !account.openai_responses_api && !account.at_only
}

// 状态圆点配色，与全局 StatusBadge 保持一致。
const STATUS_DOT_COLOR: Record<string, string> = {
  active: 'bg-emerald-500',
  ready: 'bg-emerald-500',
  cooldown: 'bg-amber-500',
  rate_limited: 'bg-yellow-500',
  usage_exhausted: 'bg-yellow-500',
  quota_paused: 'bg-yellow-500',
  unauthorized: 'bg-red-500',
  error: 'bg-red-400',
  refreshing: 'bg-blue-500 animate-pulse',
  paused: 'bg-blue-500',
}

function statusDotColor(status?: string | null): string {
  return STATUS_DOT_COLOR[(status || '').toLowerCase()] ?? 'bg-gray-400'
}

// 账号是否处于「非正常」状态。仅用于 UI 提示与视觉区分，不影响能否发送邀请——
// 凭证（access token）可用即可发邀请，这里只是提醒用户当前选的是什么状态的号。
function accountAbnormalKey(account: AccountRow): 'disabled' | 'locked' | 'unauthorized' | 'error' | null {
  if (account.enabled === false) return 'disabled'
  if (account.locked) return 'locked'
  const status = (account.status || '').toLowerCase()
  if (status === 'unauthorized') return 'unauthorized'
  if (status === 'error') return 'error'
  return null
}

function resolveAccountInput(accounts: AccountRow[], input: string): AccountRow | null {
  const normalized = input.trim().toLowerCase()
  if (!normalized) return null
  return accounts.find((account) => {
    const id = String(account.id)
    const name = account.name?.trim().toLowerCase()
    const normalizedNameWithID = normalized.replace(/\s+#(?=\d+$)/, '#')
    return (
      normalized === id ||
      normalized === `#${id}` ||
      normalized === account.email?.trim().toLowerCase() ||
      normalized === name ||
      (Boolean(name) && normalizedNameWithID === `${name}#${id}`)
    )
  }) ?? null
}

// CodexInviteView 是账号管理页内的「Codex 邀请」视图，入口与回收站一致。
export default function CodexInviteView({ accounts, onClose }: Props) {
  const { t } = useTranslation()
  const { showToast } = useToast()

  // 仅保留可用于 referral 的 Codex OAuth 账号；中转 / AT-only / 失效账号不能发送邀请。
  const codexAccounts = useMemo(
    () => accounts.filter(isCodexInviteCandidate),
    [accounts],
  )
  const firstAccount = codexAccounts[0] ?? null

  const [accountId, setAccountId] = useState<number | null>(firstAccount?.id ?? null)
  const [accountQuery, setAccountQuery] = useState(() => firstAccount ? accountDisplayName(firstAccount) : '')
  const [accountOpen, setAccountOpen] = useState(false)
  // accountTyping 区分「用户正在输入搜索」与「输入框只是回显已选账号」。仅在输入时
  // 才按文本过滤，否则展开下拉应显示全部账号（否则会被已选账号的邮箱过滤成只剩一条）。
  const [accountTyping, setAccountTyping] = useState(false)
  // 下拉键盘导航的高亮项索引（指向 filteredAccounts）。-1 表示未高亮任何项。
  const [activeIndex, setActiveIndex] = useState(-1)
  const [emailsText, setEmailsText] = useState('')
  const [showAdvanced, setShowAdvanced] = useState(false)
  const [proxyUrl, setProxyUrl] = useState('')
  const [sending, setSending] = useState(false)
  const [result, setResult] = useState<InviteResult | null>(null)
  const [error, setError] = useState<string | null>(null)
  const accountPickerRef = useRef<HTMLDivElement>(null)

  const parsed = useMemo(() => parseEmails(emailsText), [emailsText])
  const selectedAccount = useMemo(
    () => codexAccounts.find((a) => a.id === accountId) ?? null,
    [codexAccounts, accountId],
  )
  const filteredAccounts = useMemo(() => {
    // 未在输入搜索时显示全部；正在输入才按文本过滤。
    if (!accountTyping) return codexAccounts
    const query = accountQuery.trim().toLowerCase()
    if (!query) return codexAccounts
    return codexAccounts.filter((account) => accountSearchText(account).includes(query))
  }, [accountTyping, accountQuery, codexAccounts])
  const overLimit = parsed.valid.length > MAX_EMAILS
  const canSend =
    !sending && accountQuery.trim() !== '' && parsed.valid.length > 0 && parsed.invalid.length === 0 && !overLimit
  // 选中账号的非正常状态（禁用/锁定/封禁/错误）；用于提示用户当前选的不是正常号。
  const selectedAbnormal = useMemo(
    () => (selectedAccount ? accountAbnormalKey(selectedAccount) : null),
    [selectedAccount],
  )

  // 统一的选中逻辑：下拉点击、键盘回车共用。
  const selectAccount = (account: AccountRow) => {
    setAccountId(account.id)
    setAccountQuery(accountDisplayName(account))
    setAccountOpen(false)
    setAccountTyping(false)
    setActiveIndex(-1)
    setError(null)
  }

  // 打开下拉或过滤结果变化时，重置高亮到当前选中项（没有则不高亮）。
  useEffect(() => {
    if (!accountOpen) {
      setActiveIndex(-1)
      return
    }
    setActiveIndex((prev) => {
      if (prev >= 0 && prev < filteredAccounts.length) return prev
      const selectedIdx = filteredAccounts.findIndex((a) => a.id === accountId)
      return selectedIdx >= 0 ? selectedIdx : filteredAccounts.length > 0 ? 0 : -1
    })
  }, [accountOpen, filteredAccounts, accountId])

  // 下拉键盘导航：↑↓ 移动高亮、回车确认、Esc 关闭。
  const handlePickerKeyDown = (event: ReactKeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Escape') {
      if (accountOpen) {
        event.preventDefault()
        setAccountOpen(false)
      }
      return
    }
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      if (!accountOpen) {
        setAccountOpen(true)
        setAccountTyping(false)
        return
      }
      if (filteredAccounts.length === 0) return
      setActiveIndex((prev) => {
        const delta = event.key === 'ArrowDown' ? 1 : -1
        const base = prev < 0 ? (delta === 1 ? -1 : 0) : prev
        return (base + delta + filteredAccounts.length) % filteredAccounts.length
      })
      return
    }
    if (event.key === 'Enter') {
      if (accountOpen && activeIndex >= 0 && activeIndex < filteredAccounts.length) {
        event.preventDefault()
        selectAccount(filteredAccounts[activeIndex])
      }
    }
  }

  useEffect(() => {
    if (accountId == null) return
    if (codexAccounts.some((a) => a.id === accountId)) return
    setAccountId(null)
    setAccountQuery('')
  }, [accountId, codexAccounts])

  useEffect(() => {
    const handlePointerDown = (event: PointerEvent) => {
      const target = event.target
      if (target instanceof Node && accountPickerRef.current?.contains(target)) return
      setAccountOpen(false)
    }
    document.addEventListener('pointerdown', handlePointerDown)
    return () => document.removeEventListener('pointerdown', handlePointerDown)
  }, [])

  const handleSend = async () => {
    const accountInput = accountQuery.trim()
    if (!accountInput) {
      setError(t('invite.noAccountSelected'))
      return
    }
    const account = selectedAccount ?? resolveAccountInput(codexAccounts, accountInput)
    if (!account) {
      setError(t('invite.accountNotFound'))
      showToast(t('invite.accountNotFound'), 'error')
      return
    }
    if (parsed.valid.length === 0) {
      setError(t('invite.noValidEmails'))
      return
    }
    setAccountId(account.id)
    setAccountQuery(accountDisplayName(account))
    setSending(true)
    setError(null)
    setResult(null)
    try {
      const res = await api.sendInvite(account.id, {
        emails: parsed.valid,
        proxy_url: proxyUrl.trim() || undefined,
      })
      setResult(res.result)
      if (res.ok) {
        showToast(t('invite.sendSuccess'), 'success')
      } else {
        showToast(t('invite.sendUpstreamFailed', { code: res.result.status_code }), 'error')
      }
    } catch (err) {
      setError(getErrorMessage(err))
      showToast(t('invite.sendFailed', { error: getErrorMessage(err) }), 'error')
    } finally {
      setSending(false)
    }
  }

  return (
    <div>
      <PageHeader
        title={t('invite.title')}
        description={t('invite.description')}
        actions={
          <div className="flex flex-wrap items-center justify-end gap-1.5">
            <Button variant="outline" onClick={onClose} className="max-sm:w-full">
              <ArrowLeft className="size-3.5" />
              {t('invite.back')}
            </Button>
          </div>
        }
      />

      <div className="mx-auto mt-4 max-w-2xl space-y-5">
        {codexAccounts.length === 0 ? (
          <EmptyState message={t('invite.noCodexAccounts')} />
        ) : (
          <div className="rounded-2xl border bg-card shadow-sm">
            {/* 账号选择 */}
            <div className="border-b p-5">
              <div className="mb-2 flex items-center gap-2">
                <UserCircle2 className="size-4 text-muted-foreground" />
                <label className="text-sm font-semibold">{t('invite.accountLabel')}</label>
              </div>
              <div ref={accountPickerRef} className="relative">
                <div className="relative">
                  <Input
                    value={accountQuery}
                    onFocus={() => { setAccountOpen(true); setAccountTyping(false) }}
                    onClick={() => { setAccountOpen(true); setAccountTyping(false) }}
                    onKeyDown={handlePickerKeyDown}
                    onChange={(e) => {
                      const next = e.target.value
                      setAccountQuery(next)
                      setAccountOpen(true)
                      setAccountTyping(true)
                      setAccountId(resolveAccountInput(codexAccounts, next)?.id ?? null)
                      if (error === t('invite.accountNotFound')) setError(null)
                    }}
                    placeholder={t('invite.accountPlaceholder')}
                    role="combobox"
                    autoComplete="off"
                    aria-expanded={accountOpen}
                    aria-controls="codex-invite-account-list"
                    aria-activedescendant={
                      accountOpen && activeIndex >= 0 && activeIndex < filteredAccounts.length
                        ? `codex-invite-option-${filteredAccounts[activeIndex].id}`
                        : undefined
                    }
                    className="h-10 pr-9"
                  />
                  <button
                    type="button"
                    onClick={() => { setAccountOpen((open) => !open); setAccountTyping(false) }}
                    className="absolute inset-y-0 right-0 inline-flex w-9 items-center justify-center text-muted-foreground transition-colors hover:text-foreground"
                    aria-label={t('invite.accountToggle')}
                  >
                    <ChevronDown className={`size-4 transition-transform ${accountOpen ? 'rotate-180' : ''}`} />
                  </button>
                </div>
                {accountOpen && (
                  <div
                    id="codex-invite-account-list"
                    role="listbox"
                    className="absolute z-30 mt-1.5 max-h-72 w-full overflow-auto rounded-lg border bg-popover p-1 text-popover-foreground shadow-lg"
                  >
                    {filteredAccounts.length > 0 ? (
                      filteredAccounts.map((account, index) => {
                        const active = account.id === accountId
                        const highlighted = index === activeIndex
                        const abnormal = accountAbnormalKey(account)
                        return (
                          <button
                            key={account.id}
                            id={`codex-invite-option-${account.id}`}
                            type="button"
                            role="option"
                            aria-selected={active}
                            ref={highlighted ? (el) => el?.scrollIntoView({ block: 'nearest' }) : undefined}
                            onMouseDown={(event) => event.preventDefault()}
                            onMouseEnter={() => setActiveIndex(index)}
                            onClick={() => selectAccount(account)}
                            className={`flex w-full items-center gap-2 rounded-md px-2.5 py-2 text-left text-sm transition-colors ${
                              highlighted ? 'bg-accent text-accent-foreground' : 'hover:bg-accent/70 hover:text-accent-foreground'
                            }`}
                          >
                            <span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-muted text-[11px] font-semibold text-muted-foreground">
                              #{account.id}
                            </span>
                            <span className="min-w-0 flex-1">
                              <span className="flex items-center gap-1.5">
                                <span className={`size-1.5 shrink-0 rounded-full ${statusDotColor(account.status)}`} />
                                <span className="truncate font-medium">{accountDisplayName(account)}</span>
                                {abnormal && (
                                  <span className="shrink-0 rounded-full bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
                                    {t(`invite.state.${abnormal}`)}
                                  </span>
                                )}
                              </span>
                              <span className="block truncate text-xs text-muted-foreground">
                                {[account.name && account.name !== account.email ? account.name : '', account.plan_type, account.status]
                                  .filter(Boolean)
                                  .join(' · ') || '-'}
                              </span>
                            </span>
                            {active && <Check className="size-4 shrink-0 text-primary" />}
                          </button>
                        )
                      })
                    ) : (
                      <div className="px-3 py-6 text-center text-sm text-muted-foreground">
                        {t('invite.noAccountMatches')}
                      </div>
                    )}
                  </div>
                )}
              </div>
              {selectedAccount && (
                <div className="mt-2 flex flex-wrap items-center gap-1.5">
                  {selectedAccount.plan_type && (
                    <InfoPill label={t('invite.planLabel')} value={selectedAccount.plan_type} />
                  )}
                  <InfoPill label={t('invite.statusLabel')} value={selectedAccount.status || '-'} />
                </div>
              )}
              {selectedAbnormal && (
                <p className="mt-2 flex items-start gap-1.5 text-xs text-amber-600">
                  <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
                  <span>{t('invite.abnormalHint', { state: t(`invite.state.${selectedAbnormal}`) })}</span>
                </p>
              )}
              <p className="mt-2 text-xs text-muted-foreground">{t('invite.accountHint')}</p>
            </div>

            {/* 邮箱输入 */}
            <div className="p-5">
              <div className="mb-2 flex items-center gap-2">
                <Mail className="size-4 text-muted-foreground" />
                <label className="text-sm font-semibold">{t('invite.emailsLabel')}</label>
              </div>
              <textarea
                value={emailsText}
                onChange={(e) => setEmailsText(e.target.value)}
                rows={6}
                placeholder={t('invite.emailsPlaceholder')}
                className="w-full resize-y rounded-lg border bg-background px-3 py-2 font-mono text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/40"
              />

              {/* 实时解析统计 */}
              {(parsed.valid.length > 0 || parsed.invalid.length > 0 || parsed.duplicates > 0) && (
                <div className="mt-2 flex flex-wrap items-center gap-1.5">
                  <CountPill tone="success" text={t('invite.parsedValid', { count: parsed.valid.length })} />
                  {parsed.duplicates > 0 && (
                    <CountPill tone="muted" text={t('invite.parsedDuplicate', { count: parsed.duplicates })} />
                  )}
                  {parsed.invalid.length > 0 && (
                    <CountPill tone="danger" text={t('invite.parsedInvalid', { count: parsed.invalid.length })} />
                  )}
                </div>
              )}
              {parsed.invalid.length > 0 && (
                <p className="mt-1.5 break-all text-xs text-red-500">
                  {t('invite.invalidList')} {parsed.invalid.join(', ')}
                </p>
              )}
              {overLimit && (
                <p className="mt-1.5 flex items-center gap-1 text-xs text-amber-600">
                  <AlertTriangle className="size-3.5" />
                  {t('invite.overLimit', { max: MAX_EMAILS })}
                </p>
              )}
              {!overLimit && parsed.invalid.length === 0 && (
                <p className="mt-1.5 text-xs text-muted-foreground">{t('invite.emailsHint')}</p>
              )}

              {/* 高级选项 */}
              <button
                type="button"
                onClick={() => setShowAdvanced((v) => !v)}
                className="mt-4 inline-flex items-center gap-1 text-xs font-medium text-muted-foreground transition-colors hover:text-foreground"
              >
                <ChevronDown className={`size-3.5 transition-transform ${showAdvanced ? 'rotate-180' : ''}`} />
                {t('invite.advanced')}
              </button>
              {showAdvanced && (
                <div className="mt-3 rounded-xl border bg-muted/30 p-3">
                  <label className="mb-1 block text-xs font-medium text-muted-foreground">
                    {t('invite.proxyLabel')}
                  </label>
                  <input
                    value={proxyUrl}
                    onChange={(e) => setProxyUrl(e.target.value)}
                    placeholder={t('invite.proxyPlaceholder')}
                    className="h-9 w-full rounded-lg border bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/40"
                  />
                </div>
              )}

              {error && <div className="mt-3 text-sm text-red-500">{error}</div>}

              <div className="mt-4 flex justify-end">
                <Button disabled={!canSend} onClick={() => void handleSend()}>
                  <Send className="size-3.5" />
                  {sending
                    ? t('invite.sending')
                    : parsed.valid.length > 0
                      ? t('invite.sendCount', { count: parsed.valid.length })
                      : t('invite.send')}
                </Button>
              </div>
            </div>
          </div>
        )}

        {result && <InviteResultCard result={result} />}
      </div>
    </div>
  )
}

function InviteResultCard({ result }: { result: InviteResult }) {
  const { t } = useTranslation()
  const [showRaw, setShowRaw] = useState(false)
  const rawText =
    result.upstream != null
      ? JSON.stringify(result.upstream, null, 2)
      : result.upstream_raw || ''

  return (
    <div className="rounded-2xl border bg-card shadow-sm">
      <div className="flex items-center gap-2 border-b p-5">
        <div className={`flex size-9 items-center justify-center rounded-xl ${result.ok ? 'bg-emerald-500/10 text-emerald-600' : 'bg-red-500/10 text-red-600'}`}>
          {result.ok ? <Check className="size-4" /> : <AlertTriangle className="size-4" />}
        </div>
        <div className="min-w-0">
          <h4 className="text-base font-semibold">{t('invite.resultTitle')}</h4>
          <p className="text-xs text-muted-foreground">
            {result.ok
              ? t('invite.resultOkDesc', { count: result.emails.length })
              : t('invite.resultFailed', { code: result.status_code })}
          </p>
        </div>
        {result.request_id && (
          <span className="ml-auto hidden rounded-full bg-muted px-2.5 py-1 font-mono text-[11px] text-muted-foreground sm:inline">
            {result.request_id}
          </span>
        )}
      </div>

      <div className="space-y-3 p-5">
        {/* 无资格的友好提示 */}
        {!result.ok && result.status_code === 403 && (
          <div className="flex items-start gap-2 rounded-xl border border-amber-500/30 bg-amber-500/5 p-3 text-sm text-amber-700 dark:text-amber-300">
            <Sparkles className="mt-0.5 size-4 shrink-0" />
            <span>{t('invite.eligibilityHint')}</span>
          </div>
        )}

        {/* 邀请明细 */}
        {result.invites && result.invites.length > 0 && (
          <div className="space-y-2">
            {result.invites.map((inv, i) => (
              <div
                key={inv.referral_id || inv.email || i}
                className="flex items-center justify-between gap-3 rounded-xl border bg-background px-3 py-2.5"
              >
                <div className="min-w-0">
                  <div className="truncate text-sm font-medium text-foreground">{inv.email || '-'}</div>
                  {inv.invite_url && (
                    <a
                      href={inv.invite_url}
                      target="_blank"
                      rel="noreferrer"
                      className="block truncate text-xs text-primary hover:underline"
                    >
                      {inv.invite_url}
                    </a>
                  )}
                </div>
                {inv.invite_url && <CopyButton text={inv.invite_url} />}
              </div>
            ))}
          </div>
        )}

        {/* 原始响应（折叠） */}
        {rawText && (
          <div>
            <button
              type="button"
              onClick={() => setShowRaw((v) => !v)}
              className="inline-flex items-center gap-1 text-xs font-medium text-muted-foreground transition-colors hover:text-foreground"
            >
              <ChevronDown className={`size-3.5 transition-transform ${showRaw ? 'rotate-180' : ''}`} />
              {t('invite.rawResponse')}
            </button>
            {showRaw && (
              <pre className="mt-2 max-h-64 overflow-auto rounded-lg border bg-muted/40 p-3 text-xs">
                {rawText}
              </pre>
            )}
          </div>
        )}
      </div>
    </div>
  )
}

function CopyButton({ text }: { text: string }) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)
  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    } catch {
      /* 忽略剪贴板权限错误 */
    }
  }
  return (
    <button
      type="button"
      onClick={() => void handleCopy()}
      title={copied ? t('invite.copied') : t('invite.copy')}
      className="inline-flex size-8 shrink-0 items-center justify-center rounded-lg border bg-background text-muted-foreground transition-colors hover:text-foreground"
    >
      {copied ? <Check className="size-3.5 text-emerald-600" /> : <Copy className="size-3.5" />}
    </button>
  )
}

function EmptyState({ message }: { message: string }) {
  return (
    <div className="flex flex-col items-center justify-center rounded-2xl border border-dashed bg-card py-16 text-center">
      <div className="mb-3 flex size-12 items-center justify-center rounded-2xl bg-muted text-muted-foreground">
        <Mail className="size-6" />
      </div>
      <p className="text-sm text-muted-foreground">{message}</p>
    </div>
  )
}

function InfoPill({ label, value }: { label: string; value: string }) {
  return (
    <span className="inline-flex items-center gap-1 rounded-full bg-muted/60 px-2.5 py-1 text-xs text-muted-foreground">
      <span className="text-muted-foreground/70">{label}</span>
      <span className="font-medium text-foreground">{value}</span>
    </span>
  )
}

function CountPill({ tone, text }: { tone: 'success' | 'danger' | 'muted'; text: string }) {
  const cls =
    tone === 'success'
      ? 'bg-emerald-500/10 text-emerald-600'
      : tone === 'danger'
        ? 'bg-red-500/10 text-red-600'
        : 'bg-muted text-muted-foreground'
  return <span className={`inline-flex items-center rounded-full px-2.5 py-1 text-xs font-semibold ${cls}`}>{text}</span>
}
