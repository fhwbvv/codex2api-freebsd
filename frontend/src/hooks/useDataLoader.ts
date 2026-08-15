import { useCallback, useEffect, useRef, useState } from 'react'
import { getErrorMessage } from '../utils/error'

export interface LoadOptions {
  silent?: boolean
}

interface UseDataLoaderOptions<T> {
  initialData: T
  load: (options?: LoadOptions) => Promise<T>
  onError?: (message: string, error: unknown) => void
  /**
   * 为 false 时不自动加载(手动 reload 仍可用)。用于同一组件承载多个视图时,
   * 让隐藏视图的数据链整体停摆,而不是在后台空转打请求。
   */
  enabled?: boolean
}

export function useDataLoader<T>({ initialData, load, onError, enabled = true }: UseDataLoaderOptions<T>) {
  const [data, setData] = useState<T>(initialData)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const requestIdRef = useRef(0)

  const run = useCallback(async (options: LoadOptions = {}) => {
    const { silent = false } = options
    const requestId = ++requestIdRef.current

    if (!silent) {
      setLoading(true)
      setError(null)
    }

    try {
      const nextData = await load(options)
      if (requestId === requestIdRef.current) {
        setData(nextData)
        setError(null)
      }
      return nextData
    } catch (err) {
      if (err instanceof DOMException && err.name === 'AbortError') {
        return null
      }
      const message = getErrorMessage(err)
      if (requestId === requestIdRef.current) {
        if (!silent) {
          setError(message)
        }
        onError?.(message, err)
      }
      return null
    } finally {
      if (requestId === requestIdRef.current) {
        setLoading(false)
      }
    }
  }, [load, onError])

  useEffect(() => {
    if (!enabled) return
    void run()
  }, [run, enabled])

  const reload = useCallback(() => run(), [run])
  const reloadSilently = useCallback(() => run({ silent: true }), [run])

  return {
    data,
    setData,
    loading,
    error,
    reload,
    reloadSilently,
  }
}
