import { useEffect, useState } from 'react'
import { api } from '../api'

// 「显示完整用量数字」是全局设置，同一屏往往有十几个组件要格式化数字。
// 这里做模块级缓存 + 共享在途请求：首帧直接用上次的值（不闪一下紧凑单位），
// 挂载时再拉一次校准，多个组件同时挂载也只打一个请求。
let cachedValue = false
let inFlight: Promise<boolean> | null = null

function fetchShowFullUsageNumbers(): Promise<boolean> {
  if (inFlight) return inFlight
  inFlight = api
    .getSettings()
    .then((settings) => {
      cachedValue = Boolean(settings.show_full_usage_numbers)
      return cachedValue
    })
    .catch(() => cachedValue)
    .finally(() => {
      inFlight = null
    })
  return inFlight
}

/**
 * 读取「显示完整用量数字」设置。开启后数字走完整千分位，关闭时用 K/M/B/T 紧凑单位。
 */
export function useShowFullUsageNumbers(): boolean {
  const [value, setValue] = useState(cachedValue)

  useEffect(() => {
    let cancelled = false
    void fetchShowFullUsageNumbers().then((next) => {
      if (!cancelled) setValue(next)
    })
    return () => {
      cancelled = true
    }
  }, [])

  return value
}
