import { useCallback, useEffect, useMemo, useState } from 'react'
import { api } from '../api.js'
import { formatTime } from '../lib/time.js'
import { useEsc } from '../hooks/useEsc.js'

// 任务详情弹窗：子 tab「执行记录」（会话/失败/重跑按时间混排）与
// 「历史对话」（该 cron 跑出的全部 transcript，一键跳会话库查看）。
export default function CronRunsPanel({ job, onClose, onToast, onViewTx }) {
  const [tab, setTab] = useState('exec') // 'exec' | 'convos'
  const [runs, setRuns] = useState(null)
  const [convos, setConvos] = useState(null) // 该任务历史会话（库按 job_id 过滤）
  const [convosJob, setConvosJob] = useState('')
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
  // 历史对话：全局会话库带 job_id（后端按 job.SessionIDs 打标），过滤出本任务
  // 的全部 transcript。首次切到该 tab 才拉取；换任务后重新拉取。
  const loadConvos = useCallback(async () => {
    if (!job) return
    try {
      const d = await api.library('')
      setConvos((d.transcripts || []).filter((t) => t.job_id === job.id))
      setError('')
    } catch (e) {
      setError(e.message)
    }
  }, [job])

  useEffect(() => {
    if (!job || tab !== 'convos') return
    if (convosJob === job.id && convos !== null) return
    setConvos(null)
    setConvosJob(job.id)
    loadConvos()
  }, [job, tab, convos, convosJob, loadConvos])

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
          <span>任务详情 · {job?.description || job?.id}</span>
          <button className="btn btn-sm" onClick={onClose}>×</button>
        </div>
        <div className="tab-row">
          <button className={`tab ${tab === 'exec' ? 'tab-on' : ''}`} onClick={() => setTab('exec')}>执行记录</button>
          <button className={`tab ${tab === 'convos' ? 'tab-on' : ''}`} onClick={() => setTab('convos')}>历史对话</button>
        </div>
        {error && <div className="err-txt">{error}</div>}
        {tab === 'exec' && (
        <>
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
        </>
        )}
        {tab === 'convos' && (
        <>
          <div className="runs-actions">
            <span className="help">该任务跑出的全部会话，点「打开」一键跳到会话库查看。</span>
            <button className="btn btn-sm" onClick={loadConvos}>刷新</button>
          </div>
          {convos === null && <div className="sess-empty">加载中…</div>}
          {convos !== null && convos.length === 0 && <div className="sess-empty">还没有历史对话，任务运行成功后这里会出现。</div>}
          {convos !== null && convos.length > 0 && (
            <table className="runs-table">
              <thead>
                <tr>
                  <th>对话</th>
                  <th>更新时间</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {convos.map((c) => (
                  <tr key={c.id}>
                    <td>
                      <div className="runs-title">{c.title || c.id}</div>
                      <div className="mono runs-tx">{c.id}</div>
                    </td>
                    <td className="mono">{formatTime(c.updated_at)}</td>
                    <td className="runs-ops">
                      <button className="btn btn-sm btn-ghost" onClick={() => { onViewTx?.(c.id); onClose() }}>
                        打开
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </>
        )}
      </div>
    </div>
  )
}
