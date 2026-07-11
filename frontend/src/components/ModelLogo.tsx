import { useEffect, useMemo, useState } from 'react'
import { cn } from '@/lib/utils'

/**
 * LobeHub Icons via npm: `@lobehub/icons-static-svg`
 * https://lobehub.com/icons
 *
 * Icons are resolved from node_modules and hashed into the Vite build.
 * Prefer this over `@lobehub/icons` to avoid antd / @lobehub/ui peer deps.
 *
 * Only the icon files we actually reference are listed below so the bundle
 * stays small (import.meta.glob requires static path literals).
 */

// ?url → hashed asset URL strings. Keep this list in sync with MODEL_MAPPINGS + Docs.
const ICON_URLS = import.meta.glob(
  [
    '../../node_modules/@lobehub/icons-static-svg/icons/openai.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/codex.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/codex-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/sora.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/sora-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/dalle.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/dalle-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/claude.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/claude-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/claudecode.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/claudecode-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/cherrystudio.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/cherrystudio-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/anthropic.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/gemini.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/gemini-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/gemma.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/gemma-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/deepmind.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/deepmind-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/deepseek.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/deepseek-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/qwen.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/qwen-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/moonshot.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/mistral.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/mistral-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/meta.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/meta-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/llava.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/llava-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/grok.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/perplexity.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/perplexity-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/yi.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/yi-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/minimax.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/minimax-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/zai.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/chatglm.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/chatglm-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/doubao.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/doubao-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/hunyuan.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/hunyuan-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/wenxin.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/wenxin-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/baichuan.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/baichuan-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/spark.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/spark-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/stepfun.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/stepfun-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/cohere.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/cohere-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/nvidia.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/nvidia-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/microsoft.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/microsoft-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/stability.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/stability-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/flux.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/openrouter.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/aws.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/aws-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/google.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/google-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/xiaomimimo.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/internlm.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/internlm-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/jina.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/voyage.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/voyage-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/fireworks.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/fireworks-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/together.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/together-color.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/ollama.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/huggingface.svg',
    '../../node_modules/@lobehub/icons-static-svg/icons/huggingface-color.svg',
  ],
  { eager: true, query: '?url', import: 'default' },
) as Record<string, string>

const ICON_BY_FILE = (() => {
  const map = new Map<string, string>()
  for (const [path, url] of Object.entries(ICON_URLS)) {
    const file = path.split('/').pop()
    if (file) map.set(file, url)
  }
  return map
})()

type Mapping = {
  /** LobeHub static icon id (lowercase), e.g. "openai", "claude" */
  id: string
  /** Prefer color variant when available */
  color?: boolean
  /** Regex keywords matched against the lowercased model id */
  keywords: string[]
}

// Order matters: more specific patterns first (mirrors @lobehub/icons modelMappings).
const MODEL_MAPPINGS: Mapping[] = [
  { id: 'sora', color: true, keywords: ['sora'] },
  { id: 'dalle', color: true, keywords: ['dalle', 'dall-e'] },
  { id: 'codex', color: true, keywords: ['codex'] },
  { id: 'openai', color: false, keywords: ['gpt-oss', 'text-embedding-', 'tts-', 'whisper-', 'davinci', 'babbage', 'omni-moderation', 'computer-use'] },
  { id: 'openai', color: false, keywords: ['o1-', '^o1', '/o1', 'o3-', '^o3', '/o3', 'o4-', '^o4', '/o4'] },
  { id: 'openai', color: false, keywords: ['gpt-3', 'gpt-4', 'gpt-5', '^gpt-', '/gpt-', 'openai'] },
  { id: 'claude', color: true, keywords: ['claude'] },
  { id: 'anthropic', color: false, keywords: ['anthropic'] },
  { id: 'gemini', color: true, keywords: ['gemini'] },
  { id: 'gemma', color: true, keywords: ['gemma'] },
  { id: 'deepmind', color: true, keywords: ['^imagen-', '/imagen-', 'deepmind'] },
  { id: 'deepseek', color: true, keywords: ['deepseek'] },
  { id: 'qwen', color: true, keywords: ['qwen', 'qwq', 'qvq', 'tongyi'] },
  { id: 'moonshot', color: false, keywords: ['kimi', 'moonshot'] },
  { id: 'mistral', color: true, keywords: ['mistral', 'mixtral', 'codestral', 'pixtral', 'ministral', 'magistral', 'devstral', 'voxtral'] },
  { id: 'meta', color: true, keywords: ['llama', '/l3'] },
  { id: 'llava', color: true, keywords: ['llava'] },
  { id: 'grok', color: false, keywords: ['^grok-', '/grok-', 'grok'] },
  { id: 'perplexity', color: true, keywords: ['pplx', 'sonar', 'perplexity'] },
  { id: 'yi', color: true, keywords: ['^yi-', '/yi-', '-yi-'] },
  { id: 'minimax', color: true, keywords: ['minimax', 'abab'] },
  { id: 'zai', color: false, keywords: ['^glm-5', '/glm-5', '-glm-5', '^glm-4', '/glm-4', '-glm-4', '/glm4', '/glm5'] },
  { id: 'chatglm', color: true, keywords: ['chatglm', '^glm-', '/glm-', '-glm-'] },
  { id: 'doubao', color: true, keywords: ['doubao-', '^ep-'] },
  { id: 'hunyuan', color: true, keywords: ['hunyuan'] },
  { id: 'wenxin', color: true, keywords: ['ernie', 'wenxin'] },
  { id: 'baichuan', color: true, keywords: ['baichuan'] },
  { id: 'spark', color: true, keywords: ['spark', 'xinghuo'] },
  { id: 'stepfun', color: true, keywords: ['^step', '/step'] },
  { id: 'cohere', color: true, keywords: ['command', 'cohere'] },
  { id: 'nvidia', color: true, keywords: ['nemotron', 'nv-', 'nvidia'] },
  { id: 'microsoft', color: true, keywords: ['wizardlm', '/phi-', '^phi-', '-phi-', 'mai-', 'microsoft'] },
  { id: 'stability', color: true, keywords: ['stable-diffusion', 'sdxl', 'stablelm', '^sd3', '^sd2', '^sd1'] },
  { id: 'flux', color: false, keywords: ['flux'] },
  { id: 'openrouter', color: false, keywords: ['^openrouter', 'openrouter'] },
  { id: 'aws', color: true, keywords: ['titan', 'bedrock', 'amazon'] },
  { id: 'google', color: true, keywords: ['google', 'learnlm'] },
  { id: 'xiaomimimo', color: false, keywords: ['^mimo-', '/mimo-'] },
  { id: 'internlm', color: true, keywords: ['internlm', 'internvl'] },
  { id: 'jina', color: false, keywords: ['^jina', '/jina'] },
  { id: 'voyage', color: true, keywords: ['voyage'] },
  { id: 'fireworks', color: true, keywords: ['fireworks'] },
  { id: 'together', color: true, keywords: ['together'] },
  { id: 'ollama', color: false, keywords: ['ollama'] },
  { id: 'huggingface', color: true, keywords: ['huggingface', 'hf.co', 'hugging'] },
]

function resolveIconUrl(id: string, preferColor: boolean): string | null {
  if (preferColor) {
    const color = ICON_BY_FILE.get(`${id}-color.svg`)
    if (color) return color
  }
  return ICON_BY_FILE.get(`${id}.svg`) ?? null
}

export function resolveLobeIconId(model: string): { id: string; color: boolean } {
  const value = model.trim().toLowerCase()
  if (!value) return { id: 'openai', color: false }

  for (const item of MODEL_MAPPINGS) {
    if (item.keywords.some((kw) => {
      try {
        return new RegExp(kw, 'i').test(value)
      } catch {
        return value.includes(kw.toLowerCase())
      }
    })) {
      const color = Boolean(item.color) && ICON_BY_FILE.has(`${item.id}-color.svg`)
      return { id: item.id, color }
    }
  }

  return { id: 'openai', color: false }
}

export function getLobeIconUrl(model: string, preferColor = true): string | null {
  const resolved = resolveLobeIconId(model)
  return resolveIconUrl(resolved.id, preferColor && resolved.color)
}

/** Explicit file lookup for non-model brand icons (e.g. claudecode-color.svg). */
export function getLobeIconFileUrl(fileName: string): string | null {
  const name = fileName.endsWith('.svg') ? fileName : `${fileName}.svg`
  return ICON_BY_FILE.get(name) ?? null
}

function modelInitials(model: string): string {
  const parts = model
    .replace(/^gpt-?/i, '')
    .split(/[-_.\s/]+/)
    .filter(Boolean)
  if (parts.length === 0) return 'AI'
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase()
  return (parts[0][0] + parts[1][0]).toUpperCase()
}

function modelAccent(model: string): string {
  let hash = 0
  for (let i = 0; i < model.length; i += 1) hash = (hash * 31 + model.charCodeAt(i)) >>> 0
  const hues = [214, 198, 262, 160, 32, 280, 190]
  return `hsl(${hues[hash % hues.length]} 72% 48%)`
}

interface ModelLogoProps {
  model: string
  size?: number
  className?: string
  /** 'soft' = rounded square with muted bg; 'plain' = bare icon; 'ring' = bordered tile */
  variant?: 'soft' | 'plain' | 'ring'
  title?: string
}

export default function ModelLogo({
  model,
  size = 28,
  className,
  variant = 'soft',
  title,
}: ModelLogoProps) {
  const [failed, setFailed] = useState(false)
  const [triedMono, setTriedMono] = useState(false)

  const resolved = useMemo(() => resolveLobeIconId(model), [model])

  useEffect(() => {
    setFailed(false)
    setTriedMono(false)
  }, [model, resolved.id])

  const src = useMemo(() => {
    if (failed && triedMono) return null
    if (failed || triedMono || !resolved.color) {
      return resolveIconUrl(resolved.id, false)
    }
    return resolveIconUrl(resolved.id, true)
  }, [failed, resolved.color, resolved.id, triedMono])

  const pad = variant === 'plain' ? 0 : Math.max(4, Math.round(size * 0.18))
  const iconSize = size - pad * 2

  if (!src || (failed && triedMono)) {
    const accent = modelAccent(model)
    return (
      <span
        title={title ?? model}
        className={cn(
          'inline-flex shrink-0 items-center justify-center font-bold tracking-wide text-white',
          variant === 'plain' ? 'rounded-md' : 'rounded-xl',
          className,
        )}
        style={{
          width: size,
          height: size,
          fontSize: Math.max(10, Math.round(size * 0.32)),
          background: `linear-gradient(145deg, ${accent}, color-mix(in oklab, ${accent} 55%, #0f172a))`,
        }}
        aria-hidden
      >
        {modelInitials(model)}
      </span>
    )
  }

  return (
    <span
      title={title ?? model}
      className={cn(
        'inline-flex shrink-0 items-center justify-center overflow-hidden',
        variant === 'soft' && 'rounded-xl bg-muted/70 ring-1 ring-border/70',
        variant === 'ring' && 'rounded-xl bg-card ring-1 ring-border shadow-sm',
        variant === 'plain' && 'rounded-md',
        className,
      )}
      style={{ width: size, height: size }}
      aria-hidden
    >
      <img
        src={src}
        alt=""
        width={iconSize}
        height={iconSize}
        draggable={false}
        className={cn(
          'object-contain',
          // Mono OpenAI / Grok marks are dark; invert in dark mode for contrast.
          resolved.id === 'openai' && 'dark:invert dark:opacity-90',
          resolved.id === 'grok' && 'dark:invert dark:opacity-90',
        )}
        style={{ width: iconSize, height: iconSize }}
        loading="lazy"
        decoding="async"
        onError={() => {
          if (!triedMono && resolved.color) {
            setTriedMono(true)
            return
          }
          setFailed(true)
        }}
      />
    </span>
  )
}
