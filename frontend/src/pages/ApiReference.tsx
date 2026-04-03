import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Copy, Check } from 'lucide-react'
import PageHeader from '../components/PageHeader'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'

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

// 单个端点文档
function EndpointDoc({ id, method, path, title, description, curlExample, responseExamples }: {
  id?: string
  method: string
  path: string
  title: string
  description: string
  curlExample: string
  responseExamples: { code: number; body: string }[]
}) {
  const [activeStatus, setActiveStatus] = useState(responseExamples[0]?.code ?? 200)
  const activeBody = responseExamples.find(r => r.code === activeStatus)?.body ?? ''

  return (
    <Card id={id} className="mb-6 scroll-mt-20">
      <CardContent className="p-6">
        {/* 标题 */}
        <h3 className="text-xl font-bold text-foreground mb-1">{title}</h3>
        <p className="text-sm text-muted-foreground mb-4">{description}</p>

        {/* 端点路径栏 */}
        <div className="flex items-center gap-3 p-3 rounded-xl border border-border bg-muted/30 mb-5">
          <MethodBadge method={method} />
          <code className="font-mono text-sm font-semibold text-foreground">{path}</code>
        </div>

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
    const parent = el.parentElement
    if (!parent) return
    setIndicator({
      left: el.offsetLeft,
      width: el.offsetWidth,
    })
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
        <div className="relative flex flex-wrap items-center gap-x-0.5 gap-y-1 px-3 py-2 rounded-2xl border border-border bg-background/80 backdrop-blur-lg shadow-sm">
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
        curlExample={`curl --request POST \\
  --url ${baseUrl}/api/admin/accounts \\
  --header 'X-Admin-Key: <admin_secret>' \\
  --header 'Content-Type: application/json' \\
  --data '{
  "name": "my-account",
  "refresh_token": "eyJhbGciOi...",
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
