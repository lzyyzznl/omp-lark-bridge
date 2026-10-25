import { useCallback, useEffect, useRef, useState } from 'react'
import { api } from '../api.js'

// 定时任务列表 hook：默认 15s 轮询 + 手动刷新。
// token 变化时重拉；silent 刷新失败不打断已有数据（仅记录 error 供静默提示）。
export function useCron(token) {
  const [jobs, setJobs] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [lastSync, setLastSync] = useState(null)
  const alive = useRef(true)

  const load = useCallback(async (silent = false) => {
    try {
      const data = await api.list()
      if (!alive.current) return
      setJobs(data.jobs || [])
      setError(null)
      setLastSync(new Date())
    } catch (e) {
      if (!alive.current) return
      if (!silent) setError(e.message)
      setLoading(false)
    } finally {
      if (!silent && alive.current) setLoading(false)
    }
  }, [])

  useEffect(() => {
    alive.current = true
    setLoading(true)
    load(false)
    let iv = setInterval(() => load(true), document.hidden ? 60000 : 15000)
    const onVis = () => {
      clearInterval(iv)
      iv = setInterval(() => load(true), document.hidden ? 60000 : 15000)
    }
    document.addEventListener('visibilitychange', onVis)
    return () => {
      alive.current = false
      clearInterval(iv)
      document.removeEventListener('visibilitychange', onVis)
    }
  }, [load])

  return { jobs, loading, error, lastSync, reload: () => load(false), refetch: () => load(true) }
}
