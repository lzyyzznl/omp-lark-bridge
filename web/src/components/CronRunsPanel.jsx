import { useCallback, useEffect, useMemo, useState } from 'react'
import { api } from '../api.js'
import { formatTime } from '../lib/time.js'
import { useEsc } from '../hooks/useEsc.js'

// 任务执行记录：会话（可点进对话）与失败/异常退出（红色 + 重跑）按时间混排。
export default function CronRunsPanel({ job, onClose, onToast, onViewTx }) {
  const [runs, setRuns] = useState(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')
  useEsc(!!job, onClose)

  const load = useCallback(async () => {
    if (!job) return
    try {
      const d = await api.cronRuns(job.id)
      setRuns(d.runs || [])
      setError('')
    } catch (e) {
      setError(e.message)
    }
  }, [job])

  useEffect(() => { setRuns(null); load() }, [load])

  // 有执行中的记录就轮询，结束后停止。
  const hasRunning = useMemo(() => (runs || []).some((r) => r.status === 'running'), [runs])
  useEffect(() => {
    if (!hasRunning) return
    const t = setInterval(load, 2000)
    return () => clearInterval(t)
  }, [hasRunning, load])

  const rerun = async () => {
    setBusy('rerun')
    try {
      await api.exec(job.id)
      onToast?.('ok', '已触发重跑')
      setTimeout(load, 800)
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy('')
    }
  }

  if (!job) return null

  const statusText = (r) => {
    if (r.status === 'running') return '运行中'
    if (r.status === 'failed') return '失败'
    return '成功'
  }

  return (
    <div className="prj-modal-mask" style={{ zIndex: 75 }} onClick={(e) => { if (e.target === e.currentTarget) onClose() }}>
      <div className="prj-modal runs-modal">
        <div className="sess-list-head">
          <span>执行记录 · {job?.description || job?.id}</span>
          <button className="btn btn-sm" onClick={onClose}>×</button>
        </div>
        {error && <div className="err-txt">{error}</div>}
        <div className="runs-actions">
          <button className="btn btn-sm btn-solid" onClick={rerun} disabled={busy === 'rerun'}>
            {busy === 'rerun' ? '触发中…' : '立即重跑'}
          </button>
          <button className="btn btn-sm" onClick={load}>刷新</button>
          <span className="help">保留最近 50 条；失败执行没有会话文件，靠这里回溯。</span>
        </div>
        {runs === null && <div className="sess-empty">加载中…</div>}
        {runs !== null && runs.length === 0 && <div className="sess-empty">还没有执行记录。</div>}
        {runs !== null && runs.length > 0 && (
          <table className="runs-table">
            <thead>
              <tr>
                <th>开始时间</th>
                <th>状态</th>
                <th>耗时</th>
                <th>触发</th>
                <th>错误 / 会话</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {runs.map((r) => (
                <tr key={r.id} className={r.status === 'failed' ? 'runs-row-failed' : (r.status === 'running' ? 'runs-row-running' : '')}>
                  <td className="mono">{formatTime(r.started_at)}</td>
                  <td>
                    <span className={`runs-status runs-${r.status}`}>{statusText(r)}</span>
                  </td>
                  <td className="mono">{r.status === 'running' ? '—' : `${(r.duration_ms / 1000).toFixed(1)}s`}</td>
                  <td>{r.trigger === 'manual' ? '手动' : '定时'}</td>
                  <td className="runs-detail">
                    {r.status === 'failed'
                      ? <span className="runs-err" title={r.error}>{r.error || '（无错误信息）'}</span>
                      : (r.transcript_id ? <span className="mono runs-tx">{r.transcript_id}</span> : <span className="muted">—</span>)}
                  </td>
                  <td className="runs-ops">
                    {r.transcript_id && r.status !== 'failed' && (
                      <button className="btn btn-sm btn-ghost" onClick={() => { onViewTx?.(r.transcript_id); onClose() }}>
                        查看对话
                      </button>
                    )}
                    {r.status === 'failed' && (
                      <button className="btn btn-sm btn-green" onClick={rerun} disabled={busy === 'rerun'}>
                        重跑
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}
