export const DEFAULT_CLAUDE_MODEL_MAP: Record<string, string> = {
  'claude-opus-4-6': 'gpt-5.4',
  'claude-opus-4-6-20250610': 'gpt-5.4',
  'claude-haiku-4-5-20251001': 'gpt-5.4-mini',
  'claude-haiku-4-5': 'gpt-5.4-mini',
  'claude-sonnet-4-6': 'gpt-5.3-codex',
  'claude-sonnet-4-5-20250929': 'gpt-5.2',
  'claude-opus-4-5-20251101': 'gpt-5.3-codex',
  'claude-sonnet-4-5-20250514': 'gpt-5.4',
  'claude-sonnet-4-5': 'gpt-5.4',
  'claude-sonnet-4.5': 'gpt-5.4',
  'claude-sonnet-4-20250514': 'gpt-5.4',
  'claude-sonnet-4': 'gpt-5.4',
  'claude-opus-4-20250514': 'gpt-5.4',
  'claude-opus-4': 'gpt-5.4',
  'claude-3-5-sonnet-20241022': 'gpt-5.4',
  'claude-3-5-haiku-20241022': 'gpt-5.4-mini',
}

export type ModelMappingEntry = {
  from: string
  to: string
}

export type ModelMappingEntriesParseResult =
  | { ok: true; entries: ModelMappingEntry[] }
  | { ok: false }

export type ModelMappingSerializeResult =
  | { ok: true; value: string }
  | { ok: false }

export function emptyModelMappingEntries(): ModelMappingEntry[] {
  return [{ from: '', to: '' }]
}

export function parseModelMappingEntries(
  value: string,
): ModelMappingEntriesParseResult {
  const trimmed = value.trim()
  if (!trimmed) {
    return { ok: true, entries: emptyModelMappingEntries() }
  }

  let parsed: unknown
  try {
    parsed = JSON.parse(trimmed)
  } catch {
    return { ok: false }
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return { ok: false }
  }

  const entries = Object.entries(parsed as Record<string, unknown>)
  if (
    entries.some(
      ([from, to]) => !from.trim() || typeof to !== 'string' || !to.trim(),
    )
  ) {
    return { ok: false }
  }
  return {
    ok: true,
    entries:
      entries.length > 0
        ? entries.map(([from, to]) => ({ from, to: String(to) }))
        : emptyModelMappingEntries(),
  }
}

export function serializeModelMappingEntries(
  entries: ModelMappingEntry[],
): ModelMappingSerializeResult {
  const mapping: Record<string, string> = {}
  const seen = new Set<string>()
  for (const entry of entries) {
    const from = entry.from.trim()
    const to = entry.to.trim()
    if (!from && !to) continue
    if (!from || !to || seen.has(from.toLowerCase())) return { ok: false }
    seen.add(from.toLowerCase())
    mapping[from] = to
  }
  return {
    ok: true,
    value: Object.keys(mapping).length > 0 ? JSON.stringify(mapping) : '',
  }
}
