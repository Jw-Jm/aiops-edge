import { useCallback, useEffect, useRef, useState } from 'react'

export interface UseAsyncDataResult<T> {
  data: T | null
  loading: boolean
  error: string | null
  retry: () => void
}

/**
 * 统一数据加载 Hook：封装 loading/error/retry 三态，
 * 替代页面内散落的 `.catch(() => setData([]))` 静默失败模式（见 F2）。
 *
 * - 挂载与 deps 变化时自动调用 fetcher；
 * - 卸载后不再 setState（mounted ref 守卫）；
 * - 并发请求仅保留最后一次（request-id ref 使过期请求失效）；
 * - 失败时提取后端错误信息（error.response?.data?.error || error.message || '加载失败'）。
 */
export function useAsyncData<T>(
  fetcher: () => Promise<T>,
  deps: unknown[] = []
): UseAsyncDataResult<T> {
  const [data, setData] = useState<T | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const mountedRef = useRef(true)
  const requestIdRef = useRef(0)
  const fetcherRef = useRef(fetcher)
  fetcherRef.current = fetcher

  const run = useCallback(() => {
    const requestId = ++requestIdRef.current
    setLoading(true)
    setError(null)
    fetcherRef.current().then(
      (result) => {
        if (!mountedRef.current || requestId !== requestIdRef.current) return
        setData(result)
        setLoading(false)
      },
      (err: unknown) => {
        if (!mountedRef.current || requestId !== requestIdRef.current) return
        const msg =
          (err as { response?: { data?: { error?: string } } })?.response?.data?.error ||
          (err as { message?: string })?.message ||
          '加载失败'
        setError(msg)
        setLoading(false)
      }
    )
  }, [])

  useEffect(() => {
    mountedRef.current = true
    run()
    return () => {
      mountedRef.current = false
      requestIdRef.current++ // 使在途请求失效，避免卸载后 setState
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)

  const retry = useCallback(() => {
    run()
  }, [run])

  return { data, loading, error, retry }
}