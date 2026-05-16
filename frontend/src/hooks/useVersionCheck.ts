import { useCallback, useEffect, useRef, useState } from 'react'

const GITHUB_API = 'https://api.github.com/repos/james-6-23/codex2api/releases/latest'
const CACHE_KEY = 'codex2api_latest_version'
const CACHE_TTL = 10 * 60 * 1000 // 10 分钟缓存
const POLL_INTERVAL = 30 * 60 * 1000 // 30 分钟轮询

interface CachedVersion {
  version: string
  checkedAt: number
}

function readCachedVersion(ignoreTTL = false): string | null {
  try {
    const raw = localStorage.getItem(CACHE_KEY)
    if (!raw) return null
    const cached: CachedVersion = JSON.parse(raw)
    if (!cached.version) return null
    if (!ignoreTTL && Date.now() - cached.checkedAt >= CACHE_TTL) return null
    return cached.version
  } catch {
    return null
  }
}

/** 解析版本号字符串为数字数组，如 "v1.0.5" → [1, 0, 5] */
function parseVersion(tag: string): number[] | null {
  const m = tag.replace(/^v/i, '').match(/^(\d+)\.(\d+)\.(\d+)/)
  if (!m) return null
  return [Number(m[1]), Number(m[2]), Number(m[3])]
}

/** 判断 remote 是否比 local 更新 */
function isNewer(remote: number[], local: number[]): boolean {
  for (let i = 0; i < 3; i++) {
    if (remote[i] > local[i]) return true
    if (remote[i] < local[i]) return false
  }
  return false
}

async function fetchLatestVersion(forceNetwork = false): Promise<string | null> {
  // 优先读取未过期的缓存
  if (!forceNetwork) {
    const cached = readCachedVersion()
    if (cached) return cached
  }

  try {
    const res = await fetch(GITHUB_API, {
      headers: { Accept: 'application/vnd.github.v3+json' },
      signal: AbortSignal.timeout(10000),
    })
    if (!res.ok) return null
    const data = await res.json()
    const version = data.tag_name as string
    if (version) {
      localStorage.setItem(CACHE_KEY, JSON.stringify({ version, checkedAt: Date.now() }))
    }
    return version || null
  } catch {
    return readCachedVersion(true)
  }
}

export function useVersionCheck(triggerKey?: string) {
  const [latestVersion, setLatestVersion] = useState<string | null>(null)
  const [hasUpdate, setHasUpdate] = useState(false)
  const lastTriggerRef = useRef<string | undefined>(undefined)

  const check = useCallback(async (forceNetwork = false) => {
    const currentVersion = __APP_VERSION__
    // 开发模式不检查
    if (currentVersion === 'dev') return

    const localParsed = parseVersion(currentVersion)
    if (!localParsed) return

    const remote = await fetchLatestVersion(forceNetwork)
    if (!remote) return
    const remoteParsed = parseVersion(remote)
    if (!remoteParsed) return

    setLatestVersion(remote)
    setHasUpdate(isNewer(remoteParsed, localParsed))
  }, [])

  useEffect(() => {
    void check()
    const timer = setInterval(() => void check(), POLL_INTERVAL)
    return () => clearInterval(timer)
  }, [check])

  useEffect(() => {
    if (triggerKey === undefined) return
    if (lastTriggerRef.current === undefined) {
      lastTriggerRef.current = triggerKey
      return
    }
    if (lastTriggerRef.current === triggerKey) return
    lastTriggerRef.current = triggerKey
    void check(true)
  }, [check, triggerKey])

  return { hasUpdate, latestVersion }
}
