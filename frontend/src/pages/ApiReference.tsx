import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Copy, Check, Play, Loader2 } from 'lucide-react'
import { api } from '../api'
import PageHeader from '../components/PageHeader'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Select } from '@/components/ui/select'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

// 带复制按钮的代码块
function CodeBlock({ label, content, lang }: { label?: string; content: string; lang?: string }) {
  const [copied, setCopied] = useState(false)
  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(content)
    } catch {
      const ta = document.createElement('textarea')
      ta.value = content
      ta.style.cssText = 'position:fixed;left:-9999px'
      document.body.appendChild(ta)
      ta.select()
      document.execCommand('copy')
      document.body.removeChild(ta)
    }
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <div className="bg-zinc-900 dark:bg-zinc-950 rounded-xl overflow-hidden">
      {label && (
        <div className="flex items-center justify-between px-4 py-2 bg-zinc-800 dark:bg-zinc-900 border-b border-zinc-700">
          <span className="text-xs text-zinc-400 font-mono">{label}</span>
          <button
            onClick={() => void handleCopy()}
            className={`flex items-center gap-1.5 px-2.5 py-1 text-xs font-medium rounded-lg transition-colors ${
              copied ? 'bg-emerald-500/20 text-emerald-400' : 'bg-zinc-700 hover:bg-zinc-600 text-zinc-300 hover:text-white'
            }`}
          >
            {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
            {copied ? 'Copied' : 'Copy'}
          </button>
        </div>
      )}
      {!label && (
        <div className="absolute top-2 right-2 z-10">
          <button
            onClick={() => void handleCopy()}
            className={`flex items-center gap-1 px-2 py-1 text-xs rounded-md transition-colors ${
              copied ? 'bg-emerald-500/20 text-emerald-400' : 'bg-zinc-700/80 hover:bg-zinc-600 text-zinc-400 hover:text-white'
            }`}
          >
            {copied ? <Check className="size-3" /> : <Copy className="size-3" />}
          </button>
        </div>
      )}
      <pre className={`p-4 font-mono text-zinc-100 overflow-x-auto leading-relaxed ${lang === 'json' ? 'text-[13px]' : 'text-sm'}`}>
        <code>{content}</code>
      </pre>
    </div>
  )
}

// 状态码 Tab 切换
function StatusTabs({ tabs, active, onChange }: { tabs: { code: number; label?: string }[]; active: number; onChange: (c: number) => void }) {
  return (
    <div className="flex items-center gap-0.5 border-b border-border mb-0">
      {tabs.map(tab => {
        const isActive = active === tab.code
        const codeColor = tab.code < 300 ? 'text-emerald-600 dark:text-emerald-400'
          : tab.code < 400 ? 'text-amber-600 dark:text-amber-400'
          : 'text-red-500 dark:text-red-400'
        return (
          <button
            key={tab.code}
            onClick={() => onChange(tab.code)}
            className={`px-3 py-2 text-sm font-semibold border-b-2 transition-colors ${
              isActive
                ? `border-foreground ${codeColor}`
                : 'border-transparent text-muted-foreground/60 hover:text-muted-foreground'
            }`}
          >
            {tab.code}
          </button>
        )
      })}
    </div>
  )
}

// 方法颜色
function MethodBadge({ method, sm }: { method: string; sm?: boolean }) {
  const colors: Record<string, string> = {
    GET: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400 border-emerald-200 dark:border-emerald-800',
    POST: 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400 border-blue-200 dark:border-blue-800',
    PUT: 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400 border-amber-200 dark:border-amber-800',
    DELETE: 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400 border-red-200 dark:border-red-800',
  }
  const size = sm ? 'px-1.5 py-0.5 rounded text-[10px]' : 'px-2.5 py-1 rounded-lg text-xs'
  return (
    <span className={`inline-flex items-center font-bold border ${size} ${colors[method] || 'bg-muted text-foreground border-border'}`}>
      {method}
    </span>
  )
}

// Try It 测试弹窗
function TryItDialog({ open, onClose, method, path, defaultBody, apiKey, baseUrl, allKeys }: {
  open: boolean
  onClose: () => void
  method: string
  path: string
  defaultBody: string
  apiKey: string
  baseUrl: string
  allKeys: { name: string; key: string }[]
}) {
  const { t } = useTranslation()
  const [body, setBody] = useState(defaultBody)
  const [token, setToken] = useState(apiKey)
  const [response, setResponse] = useState('')
  const [status, setStatus] = useState<number | null>(null)
  const [loading, setLoading] = useState(false)
  const [duration, setDuration] = useState<number | null>(null)

  useEffect(() => {
    if (open) {
      setBody(defaultBody)
      setToken(apiKey)
      setResponse('')
      setStatus(null)
      setDuration(null)
    }
  }, [open, defaultBody, apiKey])

  const handleSend = async () => {
    setLoading(true)
    setResponse('')
    setStatus(null)
    setDuration(null)
    const start = performance.now()
    try {
      const isAdmin = path.startsWith('/api/admin')
      const headers: Record<string, string> = { 'Content-Type': 'application/json' }
      if (isAdmin) {
        headers['X-Admin-Key'] = token
      } else if (path === '/v1/messages') {
        headers['x-api-key'] = token
        headers['anthropic-version'] = '2023-06-01'
      } else {
        headers['Authorization'] = `Bearer ${token}`
      }

      const isGet = method === 'GET'
      const url = baseUrl + path
      const res = await fetch(url, {
        method,
        headers: isGet ? { 'Authorization': `Bearer ${token}`, 'X-Admin-Key': token } : headers,
        body: isGet ? undefined : body.trim() || undefined,
      })
      setStatus(res.status)
      setDuration(Math.round(performance.now() - start))
      const text = await res.text()
      try {
        setResponse(JSON.stringify(JSON.parse(text), null, 2))
      } catch {
        setResponse(text)
      }
    } catch (e) {
      setDuration(Math.round(performance.now() - start))
      setResponse(`Error: ${e instanceof Error ? e.message : String(e)}`)
    } finally {
      setLoading(false)
    }
  }

  const statusColor = status === null ? '' : status < 300 ? 'text-emerald-600' : status < 400 ? 'text-amber-600' : 'text-red-500'
  const statusBg = status === null ? '' : status < 300 ? 'bg-emerald-50 dark:bg-emerald-900/20' : status < 400 ? 'bg-amber-50 dark:bg-amber-900/20' : 'bg-red-50 dark:bg-red-900/20'

  return (
    <Dialog open={open} onOpenChange={(v) => { if (!v) onClose() }}>
      <DialogContent className="sm:max-w-3xl max-h-[90vh] overflow-visible flex flex-col gap-0 p-0" showCloseButton={false}>
        {/* 顶部端点栏 + Send */}
        <div className="flex items-center gap-3 px-5 py-3.5 border-b border-border bg-muted/30">
          <div className="flex items-center gap-2.5 flex-1 px-3 py-2 rounded-xl border border-border bg-background">
            <MethodBadge method={method} />
            <code className="font-mono text-sm font-medium text-foreground">{path}</code>
          </div>
          <Button
            onClick={() => void handleSend()}
            disabled={loading}
            className="gap-2 bg-emerald-600 hover:bg-emerald-700 text-white px-5 shrink-0"
          >
            {loading ? <Loader2 className="size-4 animate-spin" /> : <Play className="size-4" />}
            {loading ? t('apiRef.tryIt.sending') : 'Send'}
          </Button>
        </div>

        {/* 内容区：左右分栏 */}
        <div className="flex flex-1 min-h-0 overflow-hidden">
          {/* 左侧：参数 */}
          <div className="flex-1 overflow-visible p-5 space-y-4 border-r border-border">
            {/* Authorization */}
            <div className="rounded-xl border border-border overflow-visible">
              <div className="px-4 py-2.5 bg-muted/30 border-b border-border">
                <span className="text-sm font-semibold text-foreground">Authorization</span>
              </div>
              <div className="p-4 space-y-3">
                <div className="flex items-center justify-between gap-3">
                  <div>
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-semibold text-foreground">
                        {path === '/v1/messages' ? 'x-api-key' : path.startsWith('/api/admin') ? 'X-Admin-Key' : 'Authorization'}
                      </span>
                      <span className="text-[11px] text-muted-foreground font-mono">string</span>
                    </div>
                    <Badge variant="destructive" className="mt-1 text-[10px] px-1.5 py-0">required</Badge>
                  </div>
                  <input
                    className="w-52 px-3 py-1.5 rounded-lg border border-border bg-background text-sm font-mono focus:outline-none focus:ring-2 focus:ring-primary/30"
                    placeholder="enter token"
                    value={token}
                    onChange={e => setToken(e.target.value)}
                  />
                </div>
                {allKeys.length > 0 && (
                  <div className="flex items-center gap-2">
                    <span className="text-xs text-muted-foreground shrink-0">{t('apiRef.tryIt.selectKey')}</span>
                    <Select
                      value={token}
                      onValueChange={v => setToken(v)}
                      options={allKeys.map(k => ({
                        label: `${k.name} — ${k.key.length > 20 ? k.key.slice(0, 8) + '...' + k.key.slice(-4) : k.key}`,
                        value: k.key,
                      }))}
                    />
                  </div>
                )}
              </div>
            </div>

            {/* Request Body */}
            {method !== 'GET' && method !== 'DELETE' && (
              <div className="rounded-xl border border-border overflow-hidden">
                <div className="px-4 py-2.5 bg-muted/30 border-b border-border">
                  <span className="text-sm font-semibold text-foreground">Request Body</span>
                </div>
                <textarea
                  className="w-full h-56 p-4 bg-background font-mono text-[20px] leading-relaxed resize-none focus:outline-none border-0"
                  value={body}
                  onChange={e => setBody(e.target.value)}
                  spellCheck={false}
                />
              </div>
            )}
          </div>

          {/* 右侧：响应 */}
          <div className="flex-1 overflow-auto p-5">
            <div className="rounded-xl border border-border overflow-hidden h-full flex flex-col">
              <div className="px-4 py-2.5 bg-muted/30 border-b border-border flex items-center justify-between">
                <span className="text-sm font-semibold text-foreground">Response</span>
                {status !== null && (
                  <div className="flex items-center gap-2.5">
                    <span className={`px-2 py-0.5 rounded-md text-xs font-bold ${statusColor} ${statusBg}`}>{status}</span>
                    {duration !== null && <span className="text-xs text-muted-foreground">{duration}ms</span>}
                  </div>
                )}
              </div>
              <div className="flex-1 overflow-auto">
                {response ? (
                  <pre className="p-4 font-mono text-[20px] text-foreground leading-relaxed whitespace-pre-wrap">
                    <code>{response}</code>
                  </pre>
                ) : (
                  <div className="flex items-center justify-center h-full min-h-[200px] text-sm text-muted-foreground">
                    {loading ? (
                      <div className="flex items-center gap-2">
                        <Loader2 className="size-4 animate-spin" />
                        <span>{t('apiRef.tryIt.sending')}</span>
                      </div>
                    ) : (
                      <span>{t('apiRef.tryIt.placeholder')}</span>
                    )}
                  </div>
                )}
              </div>
            </div>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}

// 单个端点文档
function EndpointDoc({ id, method, path, title, description, curlExample, responseExamples, defaultBody, apiKey, baseUrl, allKeys }: {
  id?: string
  method: string
  path: string
  title: string
  description: string
  curlExample: string
  responseExamples: { code: number; body: string }[]
  defaultBody?: string
  apiKey?: string
  baseUrl?: string
  allKeys?: { name: string; key: string }[]
}) {
  const { t } = useTranslation()
  const [activeStatus, setActiveStatus] = useState(responseExamples[0]?.code ?? 200)
  const activeBody = responseExamples.find(r => r.code === activeStatus)?.body ?? ''
  const [tryOpen, setTryOpen] = useState(false)

  return (
    <Card id={id} className="mb-6 scroll-mt-20">
      <CardContent className="p-6">
        {/* 标题 */}
        <h3 className="text-xl font-bold text-foreground mb-1">{title}</h3>
        <p className="text-sm text-muted-foreground mb-4">{description}</p>

        {/* 端点路径栏 + Try it */}
        <div className="flex items-center gap-3 p-3 rounded-xl border border-border bg-muted/30 mb-5">
          <MethodBadge method={method} />
          <code className="font-mono text-sm font-semibold text-foreground flex-1">{path}</code>
          <Button
            size="sm"
            onClick={() => setTryOpen(true)}
            className="gap-1.5 bg-emerald-600 hover:bg-emerald-700 text-white shrink-0"
          >
            <Play className="size-3.5" />
            {t('apiRef.tryIt.button')}
          </Button>
        </div>

        <TryItDialog
          open={tryOpen}
          onClose={() => setTryOpen(false)}
          method={method}
          path={path}
          defaultBody={defaultBody || ''}
          apiKey={apiKey || ''}
          baseUrl={baseUrl || ''}
          allKeys={allKeys || []}
        />

        {/* cURL 示例 */}
        <div className="mb-5">
          <CodeBlock label="cURL" content={curlExample} />
        </div>

        {/* 响应示例 */}
        <div className="border border-border rounded-xl overflow-hidden">
          <div className="px-4 pt-1.5 bg-muted/30">
            <StatusTabs
              tabs={responseExamples.map(r => ({ code: r.code }))}
              active={activeStatus}
              onChange={setActiveStatus}
            />
          </div>
          <pre className="p-4 font-mono text-[15px] text-muted-foreground overflow-x-auto leading-[1.8] bg-muted/5 max-h-[400px]">
            <code>{activeBody}</code>
          </pre>
        </div>
      </CardContent>
    </Card>
  )
}

export default function ApiReference() {
  const { t } = useTranslation()
  const baseUrl = useMemo(() => window.location.origin, [])
  const [firstKey, setFirstKey] = useState('')
  const [allKeys, setAllKeys] = useState<{ name: string; key: string }[]>([])

  // 加载 API Key 列表
  useEffect(() => {
    api.getAPIKeys().then(res => {
      const keys = (res.keys ?? []).map(k => ({ name: k.name, key: k.raw_key || k.key }))
      setAllKeys(keys)
      if (keys.length > 0) setFirstKey(keys[0].key)
    }).catch(() => {})
  }, [])

  const navItems = [
    { id: 'auth', label: t('apiRef.authSection'), method: '' },
    { id: 'responses', label: '/v1/responses', method: 'POST' },
    { id: 'chat', label: '/v1/chat/completions', method: 'POST' },
    { id: 'messages', label: '/v1/messages', method: 'POST' },
    { id: 'models', label: '/v1/models', method: 'GET' },
    { id: 'health', label: '/health', method: 'GET' },
    { id: 'add-account', label: t('apiRef.addAccount.title'), method: 'POST' },
    { id: 'add-account-at', label: t('apiRef.addATAccount.title'), method: 'POST' },
    { id: 'import-accounts', label: t('apiRef.importAccounts.title'), method: 'POST' },
    { id: 'delete-account', label: '/accounts/:id', method: 'DELETE' },
    { id: 'list-accounts', label: '/accounts', method: 'GET' },
  ]

  const [activeNav, setActiveNav] = useState(navItems[0].id)
  const navRefs = useRef<Record<string, HTMLButtonElement | null>>({})
  const [indicator, setIndicator] = useState({ left: 0, width: 0 })

  const updateIndicator = useCallback((id: string) => {
    const el = navRefs.current[id]
    if (!el) return
    setIndicator({
      left: el.offsetLeft,
      width: el.offsetWidth,
    })
    // 将激活的导航项滚动到可见区域
    el.scrollIntoView({ behavior: 'smooth', block: 'nearest', inline: 'nearest' })
  }, [])

  useEffect(() => {
    updateIndicator(activeNav)
  }, [activeNav, updateIndicator])

  // 滚动时自动高亮当前可见的端点
  useEffect(() => {
    const ids = navItems.map(n => n.id)
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            setActiveNav(entry.target.id)
            break
          }
        }
      },
      { rootMargin: '-80px 0px -60% 0px', threshold: 0.1 }
    )
    for (const id of ids) {
      const el = document.getElementById(id)
      if (el) observer.observe(el)
    }
    return () => observer.disconnect()
  }, [])

  const scrollTo = (id: string) => {
    setActiveNav(id)
    document.getElementById(id)?.scrollIntoView({ behavior: 'smooth' })
  }

  return (
    <>
      <PageHeader
        title={t('apiRef.title')}
        description={t('apiRef.description')}
      />

      {/* 悬浮导航栏 */}
      <div className="sticky top-2 z-30 mb-4">
        <div className="relative flex items-center gap-x-0.5 px-3 py-2 rounded-2xl border border-border bg-background/80 backdrop-blur-lg shadow-sm overflow-x-auto [&::-webkit-scrollbar]:hidden [-ms-overflow-style:none] [scrollbar-width:none]">
          {/* 滑动指示器 */}
          <div
            className="absolute top-2 h-[calc(100%-16px)] rounded-xl bg-primary/8 border border-primary/15 transition-all duration-300 ease-out pointer-events-none"
            style={{ left: indicator.left, width: indicator.width }}
          />
          {navItems.map(item => (
            <button
              key={item.id}
              ref={el => { navRefs.current[item.id] = el }}
              onClick={() => scrollTo(item.id)}
              className={`relative flex items-center gap-1 px-2 py-1.5 rounded-xl text-[11px] font-medium whitespace-nowrap transition-colors ${
                activeNav === item.id
                  ? 'text-primary'
                  : 'text-muted-foreground hover:text-foreground'
              }`}
            >
              {item.method && <MethodBadge method={item.method} sm />}
              <span>{item.label}</span>
            </button>
          ))}
        </div>
      </div>

      {/* 认证说明 */}
      <Card id="auth" className="mb-6 scroll-mt-20">
        <CardContent className="p-6">
          <h3 className="text-base font-semibold text-foreground mb-2">{t('apiRef.authSection')}</h3>
          <p className="text-sm text-muted-foreground mb-4">{t('apiRef.authDesc')}</p>
          <div className="space-y-2.5">
            <div className="flex items-center gap-2.5 px-3.5 py-2.5 rounded-xl bg-muted/40 border border-border">
              <Badge variant="outline" className="text-[10px] font-bold shrink-0">Header</Badge>
              <code className="font-mono text-sm font-medium text-foreground/80">Authorization: Bearer <span className="text-muted-foreground italic">&lt;key&gt;</span></code>
            </div>
            <div className="flex items-center gap-2.5 px-3.5 py-2.5 rounded-xl bg-muted/40 border border-border">
              <Badge variant="outline" className="text-[10px] font-bold shrink-0">Header</Badge>
              <code className="font-mono text-sm font-medium text-foreground/80">x-api-key: <span className="text-muted-foreground italic">&lt;key&gt;</span></code>
            </div>
          </div>
        </CardContent>
      </Card>

      {/* POST /v1/responses */}
      <EndpointDoc
        id="responses"
        method="POST"
        path="/v1/responses"
        title={t('apiRef.responses.title')}
        description={t('apiRef.responses.desc')}
        apiKey={firstKey}
        baseUrl={baseUrl}
        allKeys={allKeys}
        defaultBody={`{
  "model": "gpt-5.4",
  "input": [{"role": "user", "content": [{"type": "input_text", "text": "Hello"}]}],
  "stream": false
}`}
        curlExample={`curl --request POST \\
  --url ${baseUrl}/v1/responses \\
  --header 'Authorization: Bearer <token>' \\
  --header 'Content-Type: application/json' \\
  --data '{
  "model": "gpt-5.4",
  "input": [
    {
      "role": "user",
      "content": [
        {"type": "input_text", "text": "Hello, what can you do?"}
      ]
    }
  ],
  "stream": true,
  "reasoning": {"effort": "high"}
}'`}
        responseExamples={[
          { code: 200, body: `{
  "id": "resp_abc123",
  "object": "response",
  "model": "gpt-5.4",
  "status": "completed",
  "output": [
    {
      "type": "message",
      "role": "assistant",
      "content": [
        {
          "type": "output_text",
          "text": "Hello! I'm an AI assistant..."
        }
      ]
    }
  ],
  "usage": {
    "input_tokens": 12,
    "output_tokens": 45,
    "total_tokens": 57
  }
}` },
          { code: 400, body: `{
  "error": {
    "code": "invalid_request",
    "message": "model is required",
    "type": "invalid_request_error"
  }
}` },
          { code: 401, body: `{
  "error": {
    "code": "invalid_api_key",
    "message": "Invalid API key provided",
    "type": "authentication_error"
  }
}` },
          { code: 429, body: `{
  "error": {
    "message": "Rate limit exceeded",
    "type": "server_error",
    "code": "account_pool_usage_limit_reached",
    "resets_in_seconds": 18000
  }
}` },
        ]}
      />

      {/* POST /v1/chat/completions */}
      <EndpointDoc
        id="chat"
        method="POST"
        path="/v1/chat/completions"
        title={t('apiRef.chat.title')}
        description={t('apiRef.chat.desc')}
        apiKey={firstKey}
        baseUrl={baseUrl}
        allKeys={allKeys}
        defaultBody={`{
  "model": "gpt-5.4",
  "messages": [{"role": "user", "content": "Hello"}],
  "stream": false
}`}
        curlExample={`curl --request POST \\
  --url ${baseUrl}/v1/chat/completions \\
  --header 'Authorization: Bearer <token>' \\
  --header 'Content-Type: application/json' \\
  --data '{
  "model": "gpt-5.4",
  "messages": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "Hello!"}
  ],
  "stream": true,
  "reasoning_effort": "high"
}'`}
        responseExamples={[
          { code: 200, body: `{
  "id": "chatcmpl-abc123",
  "object": "chat.completion",
  "model": "gpt-5.4",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "Hello! How can I help you today?"
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 18,
    "completion_tokens": 9,
    "total_tokens": 27
  }
}` },
          { code: 400, body: `{
  "error": {
    "code": "invalid_request",
    "message": "Request validation failed",
    "type": "invalid_request_error"
  }
}` },
          { code: 401, body: `{
  "error": {
    "code": "invalid_api_key",
    "message": "Invalid API key provided",
    "type": "authentication_error"
  }
}` },
        ]}
      />

      {/* POST /v1/messages */}
      <EndpointDoc
        id="messages"
        method="POST"
        path="/v1/messages"
        title={t('apiRef.messages.title')}
        description={t('apiRef.messages.desc')}
        apiKey={firstKey}
        baseUrl={baseUrl}
        allKeys={allKeys}
        defaultBody={`{
  "model": "claude-sonnet-4-5-20250514",
  "max_tokens": 1024,
  "messages": [{"role": "user", "content": "Hello"}]
}`}
        curlExample={`curl --request POST \\
  --url ${baseUrl}/v1/messages \\
  --header 'x-api-key: <token>' \\
  --header 'Content-Type: application/json' \\
  --header 'anthropic-version: 2023-06-01' \\
  --data '{
  "model": "claude-sonnet-4-5-20250514",
  "max_tokens": 1024,
  "messages": [
    {"role": "user", "content": "Hello, Claude!"}
  ]
}'`}
        responseExamples={[
          { code: 200, body: `{
  "id": "msg_abc123",
  "type": "message",
  "role": "assistant",
  "model": "claude-sonnet-4-5-20250514",
  "content": [
    {
      "type": "text",
      "text": "Hello! How can I assist you today?"
    }
  ],
  "stop_reason": "end_turn",
  "stop_sequence": null,
  "usage": {
    "input_tokens": 10,
    "output_tokens": 12,
    "cache_creation_input_tokens": 0,
    "cache_read_input_tokens": 0
  }
}` },
          { code: 400, body: `{
  "type": "error",
  "error": {
    "type": "invalid_request_error",
    "message": "model is required"
  }
}` },
          { code: 401, body: `{
  "type": "error",
  "error": {
    "type": "authentication_error",
    "message": "Invalid API key"
  }
}` },
          { code: 429, body: `{
  "type": "error",
  "error": {
    "type": "rate_limit_error",
    "message": "All accounts rate limited"
  }
}` },
        ]}
      />

      {/* GET /v1/models */}
      <EndpointDoc
        id="models"
        method="GET"
        path="/v1/models"
        title={t('apiRef.models.title')}
        description={t('apiRef.models.desc')}
        apiKey={firstKey}
        baseUrl={baseUrl}
        allKeys={allKeys}
        curlExample={`curl --request GET \\
  --url ${baseUrl}/v1/models \\
  --header 'Authorization: Bearer <token>'`}
        responseExamples={[
          { code: 200, body: `{
  "object": "list",
  "data": [
    {"id": "gpt-5.4", "object": "model", "owned_by": "openai"},
    {"id": "gpt-5.4-mini", "object": "model", "owned_by": "openai"},
    {"id": "gpt-5.3-codex", "object": "model", "owned_by": "openai"},
    {"id": "gpt-5.2-codex", "object": "model", "owned_by": "openai"},
    {"id": "gpt-5.1-codex", "object": "model", "owned_by": "openai"},
    {"id": "gpt-5.1-codex-mini", "object": "model", "owned_by": "openai"},
    {"id": "gpt-5.1-codex-max", "object": "model", "owned_by": "openai"}
  ]
}` },
          { code: 401, body: `{
  "error": {
    "code": "invalid_api_key",
    "message": "Invalid API key provided",
    "type": "authentication_error"
  }
}` },
        ]}
      />

      {/* GET /health */}
      <EndpointDoc
        id="health"
        method="GET"
        path="/health"
        title={t('apiRef.health.title')}
        description={t('apiRef.health.desc')}
        apiKey={firstKey}
        baseUrl={baseUrl}
        allKeys={allKeys}
        curlExample={`curl --request GET \\
  --url ${baseUrl}/health`}
        responseExamples={[
          { code: 200, body: `{
  "status": "ok",
  "available": 5,
  "total": 8
}` },
        ]}
      />

      {/* 账号管理分隔 */}
      <div className="mt-10 mb-6">
        <h2 className="text-lg font-bold text-foreground mb-1">{t('apiRef.accountSection')}</h2>
        <p className="text-sm text-muted-foreground">{t('apiRef.accountSectionDesc')}</p>
      </div>

      {/* POST /api/admin/accounts — RT 导入 */}
      <EndpointDoc
        id="add-account"
        method="POST"
        path="/api/admin/accounts"
        title={t('apiRef.addAccount.title')}
        description={t('apiRef.addAccount.desc')}
        apiKey={firstKey}
        baseUrl={baseUrl}
        allKeys={allKeys}
        defaultBody={`{
  "name": "my-account",
  "refresh_token": "rt_XPqsKO3Ld...\\nrt_H2qdhY",
  "proxy_url": ""
}`}
        curlExample={`curl --request POST \\
  --url ${baseUrl}/api/admin/accounts \\
  --header 'X-Admin-Key: <admin_secret>' \\
  --header 'Content-Type: application/json' \\
  --data '{
  "name": "my-account",
  "refresh_token": "rt_XPqsKO3Ld...\\nrt_H2qdhY",
  "proxy_url": ""
}'`}
        responseExamples={[
          { code: 200, body: `{
  "message": "成功添加 1 个账号",
  "success": 1,
  "failed": 0
}` },
          { code: 400, body: `{
  "error": "refresh_token 是必填字段"
}` },
          { code: 401, body: `{
  "error": "Unauthorized"
}` },
        ]}
      />

      {/* POST /api/admin/accounts — 批量 RT */}
      <EndpointDoc
        id="add-account-batch"
        method="POST"
        path="/api/admin/accounts"
        title={t('apiRef.addAccountBatch.title')}
        description={t('apiRef.addAccountBatch.desc')}
        apiKey={firstKey}
        baseUrl={baseUrl}
        allKeys={allKeys}
        defaultBody={`{
  "name": "batch",
  "refresh_token": "token_1\\ntoken_2\\ntoken_3",
  "proxy_url": ""
}`}
        curlExample={`curl --request POST \\
  --url ${baseUrl}/api/admin/accounts \\
  --header 'X-Admin-Key: <admin_secret>' \\
  --header 'Content-Type: application/json' \\
  --data '{
  "name": "batch",
  "refresh_token": "token_1\\ntoken_2\\ntoken_3",
  "proxy_url": ""
}'`}
        responseExamples={[
          { code: 200, body: `{
  "message": "成功添加 3 个账号",
  "success": 3,
  "failed": 0
}` },
        ]}
      />

      {/* POST /api/admin/accounts/at — AT 导入 */}
      <EndpointDoc
        id="add-account-at"
        method="POST"
        path="/api/admin/accounts/at"
        title={t('apiRef.addATAccount.title')}
        description={t('apiRef.addATAccount.desc')}
        apiKey={firstKey}
        baseUrl={baseUrl}
        allKeys={allKeys}
        defaultBody={`{
  "name": "at-account",
  "access_token": "eyJhbGciOi...",
  "proxy_url": ""
}`}
        curlExample={`curl --request POST \\
  --url ${baseUrl}/api/admin/accounts/at \\
  --header 'X-Admin-Key: <admin_secret>' \\
  --header 'Content-Type: application/json' \\
  --data '{
  "name": "at-account",
  "access_token": "eyJhbGciOi...",
  "proxy_url": ""
}'`}
        responseExamples={[
          { code: 200, body: `{
  "message": "成功添加 1 个 AT-only 账号",
  "success": 1,
  "failed": 0
}` },
          { code: 400, body: `{
  "error": "access_token 是必填字段"
}` },
        ]}
      />

      {/* POST /api/admin/accounts/import — 文件导入 */}
      <EndpointDoc
        id="import-accounts"
        method="POST"
        path="/api/admin/accounts/import"
        title={t('apiRef.importAccounts.title')}
        description={t('apiRef.importAccounts.desc')}
        apiKey={firstKey}
        baseUrl={baseUrl}
        allKeys={allKeys}
        curlExample={`# TXT 格式（每行一个 Refresh Token）
curl --request POST \\
  --url ${baseUrl}/api/admin/accounts/import \\
  --header 'X-Admin-Key: <admin_secret>' \\
  --form 'file=@tokens.txt' \\
  --form 'format=txt' \\
  --form 'proxy_url='

# JSON 格式（兼容 CLIProxyAPI 凭证导出）
curl --request POST \\
  --url ${baseUrl}/api/admin/accounts/import \\
  --header 'X-Admin-Key: <admin_secret>' \\
  --form 'file=@credentials.json' \\
  --form 'format=json' \\
  --form 'proxy_url='

# AT TXT 格式（每行一个 Access Token）
curl --request POST \\
  --url ${baseUrl}/api/admin/accounts/import \\
  --header 'X-Admin-Key: <admin_secret>' \\
  --form 'file=@access_tokens.txt' \\
  --form 'format=at_txt' \\
  --form 'proxy_url='`}
        responseExamples={[
          { code: 200, body: `{
  "message": "导入完成：成功 5，失败 0，重复 2",
  "total": 7,
  "success": 5,
  "failed": 0,
  "duplicate": 2
}` },
          { code: 400, body: `{
  "error": "请上传文件（字段名: file）"
}` },
        ]}
      />

      {/* DELETE /api/admin/accounts/:id */}
      <EndpointDoc
        id="delete-account"
        method="DELETE"
        path="/api/admin/accounts/:id"
        title={t('apiRef.deleteAccount.title')}
        description={t('apiRef.deleteAccount.desc')}
        apiKey={firstKey}
        baseUrl={baseUrl}
        allKeys={allKeys}
        curlExample={`curl --request DELETE \\
  --url ${baseUrl}/api/admin/accounts/1 \\
  --header 'X-Admin-Key: <admin_secret>'`}
        responseExamples={[
          { code: 200, body: `{
  "message": "账号已删除"
}` },
          { code: 404, body: `{
  "error": "账号不存在"
}` },
        ]}
      />

      {/* GET /api/admin/accounts */}
      <EndpointDoc
        id="list-accounts"
        method="GET"
        path="/api/admin/accounts"
        title={t('apiRef.listAccounts.title')}
        description={t('apiRef.listAccounts.desc')}
        apiKey={firstKey}
        baseUrl={baseUrl}
        allKeys={allKeys}
        curlExample={`curl --request GET \\
  --url ${baseUrl}/api/admin/accounts \\
  --header 'X-Admin-Key: <admin_secret>'`}
        responseExamples={[
          { code: 200, body: `{
  "accounts": [
    {
      "id": 1,
      "name": "my-account",
      "email": "user@example.com",
      "plan_type": "team",
      "status": "active",
      "proxy_url": "",
      "created_at": "2025-01-01T00:00:00Z",
      "total_requests": 128,
      "success_requests": 125
    }
  ]
}` },
        ]}
      />
    </>
  )
}
