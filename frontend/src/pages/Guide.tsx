import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Copy, Check } from 'lucide-react'
import PageHeader from '../components/PageHeader'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'

// 带复制按钮的代码块
function CodeBlock({ path, content }: { path: string; content: string }) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(content)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      const textarea = document.createElement('textarea')
      textarea.value = content
      textarea.style.cssText = 'position:fixed;left:-9999px'
      document.body.appendChild(textarea)
      textarea.select()
      document.execCommand('copy')
      document.body.removeChild(textarea)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    }
  }

  return (
    <div className="bg-zinc-900 dark:bg-zinc-950 rounded-xl overflow-hidden">
      <div className="flex items-center justify-between px-4 py-2 bg-zinc-800 dark:bg-zinc-900 border-b border-zinc-700">
        <span className="text-xs text-zinc-400 font-mono">{path}</span>
        <button
          onClick={() => void handleCopy()}
          className={`flex items-center gap-1.5 px-2.5 py-1 text-xs font-medium rounded-lg transition-colors ${
            copied
              ? 'bg-emerald-500/20 text-emerald-400'
              : 'bg-zinc-700 hover:bg-zinc-600 text-zinc-300 hover:text-white'
          }`}
        >
          {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
          {copied ? t('guide.copied') : t('guide.copy')}
        </button>
      </div>
      <pre className="p-4 text-sm font-mono text-zinc-100 overflow-x-auto leading-relaxed">
        <code>{content}</code>
      </pre>
    </div>
  )
}

// macOS/Linux 和 Windows 的 Tab 切换
function OsTabs({ active, onChange }: { active: 'unix' | 'windows'; onChange: (v: 'unix' | 'windows') => void }) {
  return (
    <div className="border-b border-border mb-4">
      <nav className="-mb-px flex space-x-4">
        <button
          onClick={() => onChange('unix')}
          className={`whitespace-nowrap py-2.5 px-1 border-b-2 font-medium text-sm transition-colors flex items-center gap-2 ${
            active === 'unix'
              ? 'border-primary text-primary'
              : 'border-transparent text-muted-foreground hover:text-foreground hover:border-border'
          }`}
        >
          <svg fill="currentColor" viewBox="0 0 24 24" className="size-4"><path d="M18.71 19.5c-.83 1.24-1.71 2.45-3.05 2.47-1.34.03-1.77-.79-3.29-.79-1.53 0-2 .77-3.27.82-1.31.05-2.3-1.32-3.14-2.53C4.25 17 2.94 12.45 4.7 9.39c.87-1.52 2.43-2.48 4.12-2.51 1.28-.02 2.5.87 3.29.87.78 0 2.26-1.07 3.81-.91.65.03 2.47.26 3.64 1.98-.09.06-2.17 1.28-2.15 3.81.03 3.02 2.65 4.03 2.68 4.04-.03.07-.42 1.44-1.38 2.83M13 3.5c.73-.83 1.94-1.46 2.94-1.5.13 1.17-.34 2.35-1.04 3.19-.69.85-1.83 1.51-2.95 1.42-.15-1.15.41-2.35 1.05-3.11z"/></svg>
          macOS / Linux
        </button>
        <button
          onClick={() => onChange('windows')}
          className={`whitespace-nowrap py-2.5 px-1 border-b-2 font-medium text-sm transition-colors flex items-center gap-2 ${
            active === 'windows'
              ? 'border-primary text-primary'
              : 'border-transparent text-muted-foreground hover:text-foreground hover:border-border'
          }`}
        >
          <svg fill="currentColor" viewBox="0 0 24 24" className="size-4"><path d="M3 12V6.75l6-1.32v6.48L3 12zm17-9v8.75l-10 .15V5.21L20 3zM3 13l6 .09v6.81l-6-1.15V13zm7 .25l10 .15V21l-10-1.91v-5.84z"/></svg>
          Windows
        </button>
      </nav>
    </div>
  )
}

export default function Guide() {
  const { t } = useTranslation()
  const [codexOs, setCodexOs] = useState<'unix' | 'windows'>('unix')
  const [claudeOs, setClaudeOs] = useState<'unix' | 'windows'>('unix')

  // 动态获取当前服务地址
  const baseUrl = useMemo(() => window.location.origin, [])

  const codexConfigDir = codexOs === 'windows' ? '%userprofile%\\.codex' : '~/.codex'
  const claudeConfigDir = claudeOs === 'windows' ? '%userprofile%\\.claude' : '~/.claude'

  const codexConfigToml = `model_provider = "OpenAI"
model = "gpt-5.4"
review_model = "gpt-5.4"
model_reasoning_effort = "xhigh"
disable_response_storage = true
network_access = "enabled"
model_context_window = 1000000
model_auto_compact_token_limit = 900000

[model_providers.OpenAI]
name = "OpenAI"
base_url = "${baseUrl}"
wire_api = "responses"
requires_openai_auth = true`

  const codexAuthJson = `{
  "OPENAI_API_KEY": "YOUR_API_KEY"
}`

  const claudeSettingsJson = `{
  "env": {
    "ANTHROPIC_BASE_URL": "${baseUrl}",
    "ANTHROPIC_AUTH_TOKEN": "YOUR_API_KEY",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"
  }
}`

  const claudeEnvUnix = `export ANTHROPIC_BASE_URL="${baseUrl}"
export ANTHROPIC_AUTH_TOKEN="YOUR_API_KEY"
export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`

  const claudeEnvWindows = `set ANTHROPIC_BASE_URL=${baseUrl}
set ANTHROPIC_AUTH_TOKEN=YOUR_API_KEY
set CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`

  return (
    <>
      <PageHeader
        title={t('guide.title')}
        description={t('guide.description')}
      />

      {/* API 端点总览 */}
      <Card className="mb-4">
        <CardContent className="p-6">
          <h3 className="text-base font-semibold text-foreground mb-2">{t('guide.endpoints')}</h3>
          <p className="text-sm text-muted-foreground mb-4">{t('guide.endpointsDesc')}</p>

          <div className="space-y-4">
            <div className="p-4 rounded-xl border border-border bg-white/40 dark:bg-white/5">
              <div className="flex items-center gap-2 mb-2">
                <Badge variant="default" className="text-[13px]">POST</Badge>
                <code className="font-mono text-sm font-semibold">/v1/responses</code>
              </div>
              <p className="text-sm text-muted-foreground">{t('guide.responsesDesc')}</p>
            </div>

            <div className="p-4 rounded-xl border border-border bg-white/40 dark:bg-white/5">
              <div className="flex items-center gap-2 mb-2">
                <Badge variant="default" className="text-[13px]">POST</Badge>
                <code className="font-mono text-sm font-semibold">/v1/chat/completions</code>
              </div>
              <p className="text-sm text-muted-foreground">{t('guide.chatDesc')}</p>
            </div>

            <div className="p-4 rounded-xl border border-border bg-white/40 dark:bg-white/5">
              <div className="flex items-center gap-2 mb-2">
                <Badge variant="default" className="text-[13px]">POST</Badge>
                <code className="font-mono text-sm font-semibold">/v1/messages</code>
              </div>
              <p className="text-sm text-muted-foreground">{t('guide.messagesDesc')}</p>
            </div>
          </div>
        </CardContent>
      </Card>

      {/* Codex CLI 配置 */}
      <Card className="mb-4">
        <CardContent className="p-6">
          <h3 className="text-base font-semibold text-foreground mb-2">{t('guide.codexTitle')}</h3>
          <p className="text-sm text-muted-foreground mb-4">{t('guide.codexDesc')}</p>

          <OsTabs active={codexOs} onChange={setCodexOs} />

          <p className="text-xs text-amber-600 dark:text-amber-400 mb-3 flex items-center gap-1">
            ⓘ {t('guide.codexConfigHint')}
          </p>

          <div className="space-y-4">
            <CodeBlock path={`${codexConfigDir}/config.toml`} content={codexConfigToml} />
            <CodeBlock path={`${codexConfigDir}/auth.json`} content={codexAuthJson} />
          </div>

          <p className="text-xs text-muted-foreground mt-3">
            {codexOs === 'windows' ? t('guide.codexNoteWindows') : t('guide.codexNoteUnix')}
          </p>
        </CardContent>
      </Card>

      {/* Claude Code 配置 */}
      <Card className="mb-4">
        <CardContent className="p-6">
          <h3 className="text-base font-semibold text-foreground mb-2">{t('guide.claudeCodeTitle')}</h3>
          <p className="text-sm text-muted-foreground mb-4">{t('guide.claudeCodeDesc')}</p>

          <OsTabs active={claudeOs} onChange={setClaudeOs} />

          <div className="space-y-4">
            <CodeBlock
              path={claudeOs === 'unix' ? 'Terminal' : 'Command Prompt'}
              content={claudeOs === 'unix' ? claudeEnvUnix : claudeEnvWindows}
            />
            <p className="text-xs text-muted-foreground">{t('guide.claudeEnvNote')}</p>

            <CodeBlock
              path={`${claudeConfigDir}/settings.json`}
              content={claudeSettingsJson}
            />
            <p className="text-xs text-muted-foreground">{t('guide.claudeSettingsNote')}</p>
          </div>
        </CardContent>
      </Card>

      {/* 认证方式 */}
      <Card className="mb-4">
        <CardContent className="p-6">
          <h3 className="text-base font-semibold text-foreground mb-2">{t('guide.authTitle')}</h3>
          <p className="text-sm text-muted-foreground mb-3">{t('guide.authDesc')}</p>
          <ul className="space-y-2 text-sm">
            <li className="flex items-start gap-2">
              <span className="text-primary font-bold mt-0.5">1.</span>
              <code className="font-mono text-[13px] bg-muted px-2 py-0.5 rounded">{t('guide.authBearer')}</code>
            </li>
            <li className="flex items-start gap-2">
              <span className="text-primary font-bold mt-0.5">2.</span>
              <code className="font-mono text-[13px] bg-muted px-2 py-0.5 rounded">{t('guide.authXApiKey')}</code>
            </li>
            <li className="flex items-start gap-2">
              <span className="text-primary font-bold mt-0.5">3.</span>
              <code className="font-mono text-[13px] bg-muted px-2 py-0.5 rounded">{t('guide.authAnthropicToken')}</code>
            </li>
          </ul>
        </CardContent>
      </Card>

      {/* 模型映射说明 */}
      <Card className="mb-4">
        <CardContent className="p-6">
          <h3 className="text-base font-semibold text-foreground mb-2">{t('guide.modelMappingTitle')}</h3>
          <p className="text-sm text-muted-foreground">{t('guide.modelMappingDesc')}</p>
        </CardContent>
      </Card>

      {/* 请求示例 */}
      <Card>
        <CardContent className="p-6">
          <h3 className="text-base font-semibold text-foreground mb-4">{t('guide.exampleTitle')}</h3>

          <div className="space-y-6">
            <div>
              <h4 className="text-sm font-semibold text-muted-foreground mb-2">{t('guide.responsesExample')}</h4>
              <CodeBlock path="curl" content={`curl -X POST http://your-server:8080/v1/responses \\
  -H "Authorization: Bearer YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "gpt-5.4",
    "input": [{"role": "user", "content": [{"type": "input_text", "text": "Hello"}]}],
    "stream": true
  }'`} />
            </div>

            <div>
              <h4 className="text-sm font-semibold text-muted-foreground mb-2">{t('guide.chatExample')}</h4>
              <CodeBlock path="curl" content={`curl -X POST http://your-server:8080/v1/chat/completions \\
  -H "Authorization: Bearer YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "gpt-5.4",
    "messages": [{"role": "user", "content": "Hello"}],
    "stream": true
  }'`} />
            </div>

            <div>
              <h4 className="text-sm font-semibold text-muted-foreground mb-2">{t('guide.messagesExample')}</h4>
              <CodeBlock path="curl" content={`curl -X POST http://your-server:8080/v1/messages \\
  -H "x-api-key: YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -H "anthropic-version: 2023-06-01" \\
  -d '{
    "model": "claude-sonnet-4-5-20250514",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "Hello"}]
  }'`} />
            </div>
          </div>
        </CardContent>
      </Card>
    </>
  )
}
