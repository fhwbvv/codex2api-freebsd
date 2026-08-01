import { useEffect, useState } from 'react'

/**
 * useMediaQuery 订阅一个 CSS media query 的匹配状态。
 * 用于"桌面表格 / 移动卡片"这类互斥视图按视口只渲染一份,
 * 替代两份都渲染再用 CSS 隐藏的写法(大列表下是双倍渲染成本)。
 */
export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() =>
    typeof window !== 'undefined' ? window.matchMedia(query).matches : false,
  )

  useEffect(() => {
    const mql = window.matchMedia(query)
    setMatches(mql.matches)
    const onChange = (event: MediaQueryListEvent) => setMatches(event.matches)
    mql.addEventListener('change', onChange)
    return () => mql.removeEventListener('change', onChange)
  }, [query])

  return matches
}

/** Tailwind lg 断点(1024px),与 data-table-shell 的 hidden lg:block 保持一致。 */
export function useIsDesktop(): boolean {
  return useMediaQuery('(min-width: 1024px)')
}
