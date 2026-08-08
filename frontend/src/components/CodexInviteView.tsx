import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { KeyboardEvent as ReactKeyboardEvent } from 'react'
import { useTranslation } from 'react-i18next'
import {
  AlertTriangle,
  ArrowLeft,
  Check,
  ChevronDown,
  Copy,
  Gift,
  History,
  Loader2,
  Mail,
  RefreshCw,
  Send,
  Sparkles,
  UserCircle2,
} from 'lucide-react'
import PageHeader from './PageHeader'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { api } from '../api'
import type { AccountRow, InviteEligibility, InviteResult, InviteTrackingItem } from '../types'
import { getErrorMessage } from '../utils/error'
import { useToast } from '../hooks/useToast'

interface Props {
  accounts: AccountRow[]
  onClose: () => void
  // loading 区分「账号还没拉回来」与「确实没有可用账号」。直接深链进本页（/accounts/invite）
  // 时账号列表为空是正常的加载中状态，若按空列表渲染会误报「没有可用于邀请的账号」。
  loading?: boolean
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

// 邀请记录状态配色。上游 status 实测有 redeemed / pending，其余走兜底样式。
const INVITE_STATUS_TONE: Record<string, string> = {
  redeemed: 'bg-emerald-500/10 text-emerald-600',
  accepted: 'bg-emerald-500/10 text-emerald-600',
  pending: 'bg-amber-500/10 text-amber-600',
  sent: 'bg-amber-500/10 text-amber-600',
  expired: 'bg-muted text-muted-foreground',
  revoked: 'bg-red-500/10 text-red-600',
}

function inviteStatusTone(status?: string): string {
  return INVITE_STATUS_TONE[(status || '').toLowerCase()] ?? 'bg-muted text-muted-foreground'
}

// 上游返回 ISO 时间串；解析失败时原样显示，不吞掉信息。
function formatInviteTime(value?: string): string {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

// 从 time_frame_rules 里取指定维度的规则（send = 发送次数、reward = 奖励次数）。
function findCapacityRule(eligibility: InviteEligibility | null, capacityType: string) {
  return eligibility?.time_frame_rules?.find((rule) => rule.capacity_type === capacityType) ?? null
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
export default function CodexInviteView({ accounts, onClose, loading = false }: Props) {
  const { t } = useTranslation()
  const { showToast } = useToast()

  const [pickerAccounts, setPickerAccounts] = useState<AccountRow[]>(accounts)
  const [pickerLoading, setPickerLoading] = useState(loading)

  // 仅保留可用于 referral 的 Codex OAuth 账号；选择器由服务端分页搜索驱动。
  const codexAccounts = useMemo(
    () => pickerAccounts.filter(isCodexInviteCandidate),
    [pickerAccounts],
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
  const [eligibility, setEligibility] = useState<InviteEligibility | null>(null)
  const [tracking, setTracking] = useState<InviteTrackingItem[] | null>(null)
  const [infoLoading, setInfoLoading] = useState(false)
  const [eligibilityError, setEligibilityError] = useState<string | null>(null)
  const [trackingError, setTrackingError] = useState<string | null>(null)
  const accountPickerRef = useRef<HTMLDivElement>(null)
  // 代理走 ref：它只是查询时的可选参数，不该让每次输入都重新拉取上游。
  const proxyUrlRef = useRef(proxyUrl)
  // 递增序号用于丢弃过期响应（快速切换账号时后到的旧响应不能覆盖新账号的数据）。
  const infoSeqRef = useRef(0)

  useEffect(() => {
    const controller = new AbortController()
    const timer = window.setTimeout(() => {
      setPickerLoading(true)
      void api.getAccountsPage({
        channel: 'codex',
        page: 1,
        pageSize: 100,
        search: accountTyping ? accountQuery.trim() : undefined,
      }, controller.signal)
        .then((response) => {
          if (!controller.signal.aborted) setPickerAccounts(response.accounts ?? [])
        })
        .catch(() => undefined)
        .finally(() => {
          if (!controller.signal.aborted) setPickerLoading(false)
        })
    }, accountTyping ? 250 : 0)
    return () => {
      window.clearTimeout(timer)
      controller.abort()
    }
  }, [accountQuery, accountTyping])

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
  // 上游明确回 0 才算配额用尽；字段缺失（undefined）时不做任何拦截。
  const sendCapacity = eligibility?.remaining_send_capacity
  const rewardCapacity = eligibility?.remaining_reward_capacity
  const sendCapacityExhausted = sendCapacity === 0
  const rewardCapacityExhausted = rewardCapacity === 0
  const overCapacity = typeof sendCapacity === 'number' && sendCapacity > 0 && parsed.valid.length > sendCapacity
  // should_show=false 是上游给的硬性无资格结论，直接拦住，省一次注定 4xx 的请求。
  const ineligible = eligibility != null && eligibility.ok && !eligibility.should_show
  const canSend =
    !sending &&
    accountQuery.trim() !== '' &&
    parsed.valid.length > 0 &&
    parsed.invalid.length === 0 &&
    !overLimit &&
    !sendCapacityExhausted &&
    !ineligible
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
    proxyUrlRef.current = proxyUrl
  }, [proxyUrl])

  // 拉取资格与已发邀请。两个请求串行发出而非并发：上游端点在 Cloudflare bot 管理后面，
  // 后端按账号复用 cookie，第一个请求拿到的 __cf_bm 能让第二个请求少被挑战。
  // 一个失败不影响另一个展示。
  const loadInviteInfo = useCallback(async (id: number) => {
    const seq = ++infoSeqRef.current
    setInfoLoading(true)
    setEligibilityError(null)
    setTrackingError(null)
    const proxy = proxyUrlRef.current.trim() || undefined

    try {
      const res = await api.getInviteEligibility(id, { proxy_url: proxy })
      if (seq !== infoSeqRef.current) return
      setEligibility(res.result)
    } catch (err) {
      if (seq !== infoSeqRef.current) return
      setEligibility(null)
      setEligibilityError(getErrorMessage(err))
    }

    try {
      const res = await api.getInviteTracking(id, { proxy_url: proxy })
      if (seq !== infoSeqRef.current) return
      setTracking(res.result.challenged ? null : res.result.items ?? [])
      setTrackingError(res.result.challenged ? t('invite.challengedRetry') : null)
    } catch (err) {
      if (seq !== infoSeqRef.current) return
      setTracking(null)
      setTrackingError(getErrorMessage(err))
    }

    if (seq !== infoSeqRef.current) return
    setInfoLoading(false)
  }, [t])

  // 切换账号时重新拉取；未选中账号则清空，避免显示上一个账号的配额。
  useEffect(() => {
    if (accountId == null) {
      infoSeqRef.current++
      setEligibility(null)
      setTracking(null)
      setEligibilityError(null)
      setTrackingError(null)
      setInfoLoading(false)
      return
    }
    void loadInviteInfo(accountId)
  }, [accountId, loadInviteInfo])

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
      // 无论成败都刷新配额与记录：成功要扣减剩余次数，失败也可能是配额已被别处用掉。
      void loadInviteInfo(account.id)
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

      {/* 宽屏水平三列：账号/资格 | 发送表单 | 邀请记录；窄屏纵向堆叠 */}
      <div className="mt-4 space-y-5">
        {codexAccounts.length === 0 ? (
          <div className="mx-auto max-w-2xl">
            <EmptyState message={pickerLoading ? t('invite.accountsLoading') : t('invite.noCodexAccounts')} spinning={pickerLoading} />
          </div>
        ) : (
          <div className="grid grid-cols-1 items-stretch gap-5 lg:grid-cols-2 xl:grid-cols-12">
            {/* 左列：账号选择 + 资格配额；与中/右列拉齐等高 */}
            <section className="flex min-h-0 min-w-0 flex-col gap-5 xl:col-span-4">
              <div className="shrink-0 rounded-2xl border bg-card shadow-sm">
                <div className="border-b px-5 py-4">
                  <div className="flex items-center gap-2">
                    <div className="flex size-8 items-center justify-center rounded-lg bg-muted text-muted-foreground">
                      <UserCircle2 className="size-4" />
                    </div>
                    <div className="min-w-0">
                      <h3 className="text-sm font-semibold leading-tight">{t('invite.accountLabel')}</h3>
                      <p className="text-xs text-muted-foreground">{t('invite.accountHint')}</p>
                    </div>
                  </div>
                </div>
                <div className="p-5">
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
                    <div className="mt-3 flex flex-wrap items-center gap-1.5">
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
                </div>
              </div>

              {accountId != null ? (
                <EligibilityPanel
                  eligibility={eligibility}
                  loading={infoLoading}
                  error={eligibilityError}
                  onRefresh={() => void loadInviteInfo(accountId)}
                  className="min-h-0 flex-1"
                />
              ) : (
                <div className="min-h-0 flex-1 rounded-2xl border border-dashed bg-card/60 shadow-sm" aria-hidden />
              )}
            </section>

            {/* 中列：邮箱输入与发送 */}
            <section className="flex min-h-0 min-w-0 flex-col xl:col-span-4">
              <div className="flex h-full min-h-[22rem] flex-col rounded-2xl border bg-card shadow-sm">
                <div className="shrink-0 border-b px-5 py-4">
                  <div className="flex items-center gap-2">
                    <div className="flex size-8 items-center justify-center rounded-lg bg-muted text-muted-foreground">
                      <Mail className="size-4" />
                    </div>
                    <div className="min-w-0">
                      <h3 className="text-sm font-semibold leading-tight">{t('invite.emailsLabel')}</h3>
                      <p className="text-xs text-muted-foreground">{t('invite.emailsHint')}</p>
                    </div>
                  </div>
                </div>
                <div className="flex min-h-0 flex-1 flex-col p-5">
                  <textarea
                    value={emailsText}
                    onChange={(e) => setEmailsText(e.target.value)}
                    rows={6}
                    placeholder={t('invite.emailsPlaceholder')}
                    className="min-h-[8rem] w-full flex-1 resize-none rounded-lg border bg-background px-3 py-2 font-mono text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/40"
                  />

                  {(parsed.valid.length > 0 || parsed.invalid.length > 0 || parsed.duplicates > 0) && (
                    <div className="mt-3 flex flex-wrap items-center gap-1.5">
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

                  {/* 配额提示：0 是硬拦截，超出剩余次数只警告（配额是月度累计，本地数据可能已过时）。 */}
                  {ineligible && (
                    <p className="mt-1.5 flex items-start gap-1.5 text-xs text-red-500">
                      <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
                      <span>{t('invite.blockedIneligible')}</span>
                    </p>
                  )}
                  {sendCapacityExhausted && (
                    <p className="mt-1.5 flex items-start gap-1.5 text-xs text-red-500">
                      <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
                      <span>{t('invite.blockedSendCapacity')}</span>
                    </p>
                  )}
                  {!sendCapacityExhausted && overCapacity && (
                    <p className="mt-1.5 flex items-start gap-1.5 text-xs text-amber-600">
                      <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
                      <span>{t('invite.warnOverCapacity', { remaining: sendCapacity, count: parsed.valid.length })}</span>
                    </p>
                  )}
                  {!sendCapacityExhausted && rewardCapacityExhausted && (
                    <p className="mt-1.5 flex items-start gap-1.5 text-xs text-amber-600">
                      <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
                      <span>{t('invite.warnRewardExhausted')}</span>
                    </p>
                  )}

                  <button
                    type="button"
                    onClick={() => setShowAdvanced((v) => !v)}
                    className="mt-4 inline-flex items-center gap-1 self-start text-xs font-medium text-muted-foreground transition-colors hover:text-foreground"
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

                  <div className="mt-auto flex justify-end pt-4">
                    <Button disabled={!canSend} onClick={() => void handleSend()} className="min-w-[8.5rem]">
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
            </section>

            {/* 右列：邀请记录；中屏整行铺开，宽屏与左右列并排等高 */}
            <section className="flex min-h-0 min-w-0 flex-col lg:col-span-2 xl:col-span-4">
              {accountId != null ? (
                <TrackingCard
                  items={tracking}
                  loading={infoLoading}
                  error={trackingError}
                  onRefresh={() => void loadInviteInfo(accountId)}
                  className="h-full min-h-[22rem]"
                />
              ) : (
                <div className="flex h-full min-h-[22rem] flex-col items-center justify-center rounded-2xl border border-dashed bg-card/60 px-6 py-12 text-center shadow-sm">
                  <div className="mb-3 flex size-10 items-center justify-center rounded-xl bg-muted text-muted-foreground">
                    <History className="size-5" />
                  </div>
                  <p className="text-sm font-medium text-foreground">{t('invite.trackingTitle')}</p>
                  <p className="mt-1 text-xs text-muted-foreground">{t('invite.accountHint')}</p>
                </div>
              )}
            </section>
          </div>
        )}

        {result && <InviteResultCard result={result} />}
      </div>
    </div>
  )
}

// EligibilityPanel 展示上游给的资格结论、剩余配额与官方文案。
// 文案由上游按 Oai-Language 下发（目前上游 i18n 不完整，会中英混排），原样透传不做二次翻译。
function EligibilityPanel({
  eligibility,
  loading,
  error,
  onRefresh,
  className,
}: {
  eligibility: InviteEligibility | null
  loading: boolean
  error: string | null
  onRefresh: () => void
  className?: string
}) {
  const { t } = useTranslation()
  const sendRule = findCapacityRule(eligibility, 'send')
  const rewardRule = findCapacityRule(eligibility, 'reward')
  const shell = `flex flex-col rounded-2xl border bg-card shadow-sm ${className ?? ''}`

  if (loading && !eligibility) {
    return (
      <div className={`${shell} items-center justify-center p-5 text-sm text-muted-foreground`}>
        <Loader2 className="size-4 animate-spin" />
        <span className="mt-2">{t('invite.eligibilityLoading')}</span>
      </div>
    )
  }

  if (error) {
    return (
      <div className={`${shell} p-5 text-sm text-muted-foreground`}>
        <div className="flex items-start gap-2">
          <AlertTriangle className="mt-0.5 size-4 shrink-0 text-amber-600" />
          <span className="min-w-0 flex-1 break-all">{t('invite.eligibilityLoadFailed', { error })}</span>
          <RefreshButton onClick={onRefresh} />
        </div>
      </div>
    )
  }

  if (!eligibility) return null

  // 上游非 2xx：把状态码摊开，不假装有资格。被 Cloudflare 挑战时状态码同为 403，
  // 但含义是「没问到」而非「没资格」，必须分开说，否则会把正常账号误报成无权限。
  if (!eligibility.ok) {
    return (
      <div className={`flex flex-col rounded-2xl border border-amber-500/30 bg-amber-500/5 p-5 text-sm text-amber-700 shadow-sm dark:text-amber-300 ${className ?? ''}`}>
        <div className="flex items-start gap-2">
          <AlertTriangle className="mt-0.5 size-4 shrink-0" />
          <span className="min-w-0 flex-1">
            {eligibility.challenged
              ? t('invite.challengedRetry')
              : eligibility.upstream_message ||
                t('invite.eligibilityUpstreamFailed', { code: eligibility.status_code })}
          </span>
          <RefreshButton onClick={onRefresh} />
        </div>
      </div>
    )
  }

  return (
    <div className={shell}>
      <div className="flex shrink-0 items-start gap-3 border-b px-5 py-4">
        <div className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
          <Gift className="size-4" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="text-sm font-semibold">{eligibility.title || t('invite.eligibilityTitle')}</span>
            {eligibility.offer_id && (
              <span className="rounded-full bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
                {eligibility.offer_id}
              </span>
            )}
          </div>
          {eligibility.description && (
            <p className="mt-1 text-xs leading-relaxed text-muted-foreground">{eligibility.description}</p>
          )}
        </div>
        {loading ? (
          <Loader2 className="mt-0.5 size-3.5 shrink-0 animate-spin text-muted-foreground" />
        ) : (
          <RefreshButton onClick={onRefresh} />
        )}
      </div>

      <div className="flex min-h-0 flex-1 flex-col gap-3 p-5">
        {!eligibility.should_show && (
          <p className="flex items-start gap-1.5 text-xs text-amber-600">
            <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
            <span>
              {eligibility.ineligible_reason ||
                t('invite.ineligibleNoReason', { code: eligibility.ineligible_reason_code || '-' })}
            </span>
          </p>
        )}

        {/* 两种配额分开展示：能发几封 ≠ 能拿几次奖励，后者才决定还有没有收益。 */}
        <div className="flex flex-wrap items-center gap-1.5">
          <CapacityPill
            label={t('invite.remainingSend')}
            remaining={eligibility.remaining_send_capacity}
            used={sendRule?.invites_sent}
            total={sendRule?.invites_total}
          />
          <CapacityPill
            label={t('invite.remainingReward')}
            remaining={eligibility.remaining_reward_capacity}
            used={rewardRule?.invites_sent}
            total={rewardRule?.invites_total}
            highlight
          />
        </div>

        {eligibility.rules && eligibility.rules.length > 0 && (
          <ul className="mt-auto space-y-1.5 rounded-xl border bg-muted/20 px-3 py-2.5 text-xs text-muted-foreground">
            {eligibility.rules.map((rule, i) => (
              <li key={i} className="flex items-start gap-1.5">
                <span className="mt-1.5 size-1 shrink-0 rounded-full bg-muted-foreground/50" />
                <span className="leading-relaxed">{rule}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}

// CapacityPill 展示单个维度的剩余配额。remaining 为 undefined 表示上游没给这个字段，
// 显示为「未知」而不是 0 —— 两者的处理方式完全不同。
function CapacityPill({
  label,
  remaining,
  used,
  total,
  highlight,
}: {
  label: string
  remaining?: number
  used?: number
  total?: number
  highlight?: boolean
}) {
  const { t } = useTranslation()
  const unknown = typeof remaining !== 'number'
  const exhausted = remaining === 0
  const cls = unknown
    ? 'bg-muted text-muted-foreground'
    : exhausted
      ? 'bg-red-500/10 text-red-600'
      : highlight
        ? 'bg-primary/10 text-primary'
        : 'bg-emerald-500/10 text-emerald-600'
  const detail =
    typeof used === 'number' && typeof total === 'number' ? ` (${used}/${total})` : ''
  return (
    <span className={`inline-flex items-center gap-1 rounded-full px-2.5 py-1 text-xs font-semibold ${cls}`}>
      <span className="font-normal opacity-70">{label}</span>
      <span>{unknown ? t('invite.capacityUnknown') : `${remaining}${detail}`}</span>
    </span>
  )
}

// TrackingCard 展示该账号已发出的邀请及兑换状态（上游 90 天窗口）。
function TrackingCard({
  items,
  loading,
  error,
  onRefresh,
  className,
}: {
  items: InviteTrackingItem[] | null
  loading: boolean
  error: string | null
  onRefresh: () => void
  className?: string
}) {
  const { t } = useTranslation()

  return (
    <div className={`flex flex-col rounded-2xl border bg-card shadow-sm ${className ?? ''}`}>
      <div className="flex items-center gap-2 border-b px-5 py-4">
        <div className="flex size-8 items-center justify-center rounded-lg bg-muted text-muted-foreground">
          <History className="size-4" />
        </div>
        <div className="min-w-0 flex-1">
          <h4 className="text-sm font-semibold leading-tight">{t('invite.trackingTitle')}</h4>
          <p className="text-xs text-muted-foreground">{t('invite.trackingDescription')}</p>
        </div>
        <div className="flex items-center gap-2">
          {loading && <Loader2 className="size-3.5 animate-spin text-muted-foreground" />}
          <RefreshButton onClick={onRefresh} />
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-auto p-5">
        {error ? (
          <p className="flex items-start gap-1.5 break-all text-sm text-amber-600">
            <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
            <span>{t('invite.trackingLoadFailed', { error })}</span>
          </p>
        ) : items == null ? (
          <p className="text-sm text-muted-foreground">{t('invite.trackingLoading')}</p>
        ) : items.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t('invite.trackingEmpty')}</p>
        ) : (
          <div className="space-y-2">
            {items.map((item, i) => (
              <div
                key={item.referral_id || item.email || i}
                className="rounded-xl border bg-background px-3 py-2.5"
              >
                <div className="flex items-center gap-2">
                  <span className="min-w-0 flex-1 truncate text-sm font-medium text-foreground">
                    {item.email || '-'}
                  </span>
                  <span className={`shrink-0 rounded-full px-2 py-0.5 text-[11px] font-semibold ${inviteStatusTone(item.status)}`}>
                    {item.status || '-'}
                  </span>
                  {item.invite_url && <CopyButton text={item.invite_url} />}
                </div>
                <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-muted-foreground">
                  <span>{t('invite.trackingCreatedAt', { time: formatInviteTime(item.created_at) })}</span>
                  {item.expires_at && (
                    <span>{t('invite.trackingExpiresAt', { time: formatInviteTime(item.expires_at) })}</span>
                  )}
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

function RefreshButton({ onClick }: { onClick: () => void }) {
  const { t } = useTranslation()
  return (
    <button
      type="button"
      onClick={onClick}
      title={t('invite.refresh')}
      aria-label={t('invite.refresh')}
      className="inline-flex size-7 shrink-0 items-center justify-center rounded-lg border bg-background text-muted-foreground transition-colors hover:text-foreground"
    >
      <RefreshCw className="size-3.5" />
    </button>
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
      <div className="flex items-center gap-2 border-b px-5 py-4">
        <div className={`flex size-8 items-center justify-center rounded-lg ${result.ok ? 'bg-emerald-500/10 text-emerald-600' : 'bg-red-500/10 text-red-600'}`}>
          {result.ok ? <Check className="size-4" /> : <AlertTriangle className="size-4" />}
        </div>
        <div className="min-w-0 flex-1">
          <h4 className="text-sm font-semibold leading-tight">{t('invite.resultTitle')}</h4>
          <p className="text-xs text-muted-foreground">
            {result.ok
              ? t('invite.resultOkDesc', { count: result.emails.length })
              : t('invite.resultFailed', { code: result.status_code })}
          </p>
        </div>
        {result.request_id && (
          <span className="hidden rounded-full bg-muted px-2.5 py-1 font-mono text-[11px] text-muted-foreground sm:inline">
            {result.request_id}
          </span>
        )}
      </div>

      <div className="space-y-3 p-5">
        {/* 被 Cloudflare 挑战：状态码也是 403，但邀请并未发出，且与资格无关，提示重试。 */}
        {!result.ok && result.challenged && (
          <div className="flex items-start gap-2 rounded-xl border border-amber-500/30 bg-amber-500/5 p-3 text-sm text-amber-700 dark:text-amber-300">
            <AlertTriangle className="mt-0.5 size-4 shrink-0" />
            <span>{t('invite.challengedRetry')}</span>
          </div>
        )}

        {/* 上游给了具体原因就直接显示它（如「此人已收到推荐邀请」），并列出被拒邮箱。
            这类失败是收件人级的，账号资格完好，不能套用下面那条无资格提示。 */}
        {!result.ok && !result.challenged && result.upstream_message && (
          <div className="rounded-xl border border-amber-500/30 bg-amber-500/5 p-3 text-sm text-amber-700 dark:text-amber-300">
            <div className="flex items-start gap-2">
              <AlertTriangle className="mt-0.5 size-4 shrink-0" />
              <span className="min-w-0 flex-1">{result.upstream_message}</span>
            </div>
            {result.failed_emails && result.failed_emails.length > 0 && (
              <p className="mt-1.5 break-all pl-6 text-xs">
                {t('invite.failedEmails')} {result.failed_emails.join(', ')}
              </p>
            )}
          </div>
        )}

        {/* 无资格的兜底提示：仅在既不是挑战、上游又没给具体原因时才用这句推测。 */}
        {!result.ok && !result.challenged && !result.upstream_message && result.status_code === 403 && (
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

function EmptyState({ message, spinning }: { message: string; spinning?: boolean }) {
  return (
    <div className="flex flex-col items-center justify-center rounded-2xl border border-dashed bg-card py-16 text-center">
      <div className="mb-3 flex size-12 items-center justify-center rounded-2xl bg-muted text-muted-foreground">
        {spinning ? <Loader2 className="size-6 animate-spin" /> : <Mail className="size-6" />}
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
