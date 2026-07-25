import type { PromptGuardRolloutConfig, PromptGuardRolloutFallbackMode } from '../types'

export function createDefaultPromptGuardRollout(): PromptGuardRolloutConfig {
  return {
    enabled: false,
    percent: 0,
    fallback_mode: 'warn',
    newapi_user_allowlist: [],
    api_key_allowlist: [],
    protocols: [],
    providers: [],
  }
}

function normalizeStringList(value: unknown): string[] {
  if (!Array.isArray(value)) return []
  const seen = new Set<string>()
  const result: string[] = []
  for (const item of value) {
    if (typeof item !== 'string') continue
    const normalized = item.trim()
    if (!normalized || seen.has(normalized)) continue
    seen.add(normalized)
    result.push(normalized)
  }
  return result
}

function normalizeIdentifierList(value: unknown): string[] {
  if (!Array.isArray(value)) return []
  const seen = new Set<string>()
  const result: string[] = []
  for (const item of value) {
    let normalized = ''
    if (typeof item === 'string') {
      normalized = item.trim()
    } else if (typeof item === 'number' && Number.isSafeInteger(item) && item >= 0) {
      // Older/manual JSON may use numeric IDs. New saves use strings so IDs do
      // not lose precision when they eventually grow beyond JavaScript's safe
      // integer range.
      normalized = String(item)
    }
    if (!normalized || seen.has(normalized)) continue
    seen.add(normalized)
    result.push(normalized)
  }
  return result
}

export function parsePromptGuardRollout(value: unknown): PromptGuardRolloutConfig {
  const raw = value && typeof value === 'object' ? value as Record<string, unknown> : {}
  const numericPercent = typeof raw.percent === 'number' && Number.isFinite(raw.percent)
    ? raw.percent
    : 0
  const fallbackMode: PromptGuardRolloutFallbackMode = raw.fallback_mode === 'shadow' ? 'shadow' : 'warn'

  return {
    enabled: raw.enabled === true,
    percent: Math.min(100, Math.max(0, Math.round(numericPercent))),
    fallback_mode: fallbackMode,
    newapi_user_allowlist: normalizeIdentifierList(raw.newapi_user_allowlist),
    api_key_allowlist: normalizeIdentifierList(raw.api_key_allowlist),
    protocols: normalizeStringList(raw.protocols),
    providers: normalizeStringList(raw.providers),
  }
}
