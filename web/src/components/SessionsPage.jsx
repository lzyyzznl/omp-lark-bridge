import { useCallback, useEffect, useMemo, useState } from 'react'
import { marked } from 'marked'
import { api } from '../api.js'
import { formatTime } from '../lib/time.js'

const ROLE_LABEL = { user: '你', assistant: '助手', tool: '工具', system: '系统' }

// ── 辅助：内容/参数/大小的可读化 ───────────────────────────────

function flattenText(content) {
  // omp toolResult content 可以是 ["文本", {text|type:"text"}...] 或字符串
  if (content == null) return ''
  if (typeof content === 'string') return content
  if (!Array.isArray(content)) return String(content)
  return content.map((x) => (typeof x === 'string' ? x : (x && (x.text || x.content)) || ''))
    .filter(Boolean).join('\n')
}

function prettyJSON(v) {
  try {
    return JSON.stringify(v, null, 2)
  } catch {
    return String(v)
  }
}

function formatBytes(n) {
  if (!n || n < 1024) return n ? `${n} B` : ''
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(2)} MB`
}

function summarizeArgs(args) {
  if (!args) return ''
  if (typeof args === 'string') return args.length > 80 ? args.slice(0, 80) + '…' : args
  if (Array.isArray(args)) return prettyJSON(args).slice(0, 80)
  const keys = Object.keys(args)
  if (keys.length === 0) return ''
  const first = args[keys[0]]
  const v = typeof first === 'object' ? prettyJSON(first) : String(first)
  const label = `${keys[0]}=${v}`
  return label.length > 84 ? label.slice(0, 84) + '…' : label
}

// 把 role=toolResult 的 message 按 toolCallId 建成查询表，供 toolCall 行配对。
function indexToolResults(events) {
  const map = new Map()
  for (const ev of events) {
    if (ev.type !== 'message') continue
    const m = ev.message || {}
    if (m.role === 'toolResult' && m.toolCallId) {
      const text = flattenText(m.content)
      map.set(m.toolCallId, {
        toolName: m.toolName || '',
        text,
        isError: !!m.isError,
        bytes: text.length,
      })
    }
  }
  return map
}

// ── 渲染：一条工具调用（toolCall → toolResult 配对） ──────────────
// 借鉴 agentsview 的 ToolCallRow：结果状态点 + 工具名 + 意图/参数摘要 + 结果大小，
// 展开才显示参数与结果全文。连续多个排成一行行，便于扫读。

function ToolCallRow({ call, result }) {
  if (!call || call.type !== 'toolCall') return null
  const args = call.arguments
  const hasArgs = args && (!Array.isArray(args) || args.length > 0) && JSON.stringify(args) !== '{}'
  const intent = call.intent || (call.name ? '' : '') || summarizeArgs(args)
  const dot = result ? (result.isError ? 'err' : 'ok') : 'run'
  return (
    <details className={`t-call ${result && result.isError ? 't-call-err' : ''}`}>
      <summary className="t-call-sum">
        <span className={`t-call-dot ${dot}`} aria-hidden="true" />
        <span className="t-call-name">{call.name || 'tool'}</span>
        {intent ? <span className="t-call-intent">{intent}</span> : null}
        {result && formatBytes(result.bytes) && (
          <span className={`t-call-size ${result.isError ? 't-call-size-err' : ''}`}>
            {result.isError ? '失败 · ' : ''}{formatBytes(result.bytes)}
          </span>
        )}
        {!result && <span className="t-call-size">…</span>}
      </summary>
      <div className="t-call-body">
        {hasArgs && (
          <>
            <div className="t-call-k">参数</div>
            <pre className="t-code">{prettyJSON(args)}</pre>
          </>
        )}
        {result && result.text && (
          <>
            <div className="t-call-k">{result.toolName ? `结果（${result.toolName}）` : '结果'}</div>
            <pre className="t-code">{result.text}</pre>
          </>
        )}
      </div>
    </details>
  )
}

// ── 渲染：一条事件 ─────────────────────────────────────────────

function renderEvent(ev, i, toolResults) {
  if (ev.type === 'message') {
    const m = ev.message || {}
    const role = m.role || 'unknown'
    // toolResult 已被对应的 toolCall 行吸收，不单独重复显示。
    if (role === 'toolResult') return null
    const blocks = Array.isArray(m.content) ? m.content : [{ type: 'text', text: String(m.content ?? '') }]
    return (
      <div className="t-msg" key={i} data-role={role}>
        <div className="t-role">{ROLE_LABEL[role] || role}</div>
        <div className="t-body">
          {blocks.map((b, bi) => {
            if (b && b.type === 'text') {
              return <div className="t-md" key={bi} dangerouslySetInnerHTML={{ __html: marked.parse(b.text || '') }} />
            }
            if (b && b.type === 'toolCall') {
              return <ToolCallRow key={bi} call={b} result={toolResults.get(b.id)} />
            }
            if (b && b.type === 'thinking') {
              return (
                <details className="t-tool" key={bi}>
                  <summary>🧠 思考{b.summary ? '（摘要）' : ''}</summary>
                  <pre className="t-code">{b.thinking || prettyJSON(b)}</pre>
                </details>
              )
            }
            // Claude 风格命名（tool_use / tool_result / thinking）兜底
            if (b && b.type === 'tool_use') {
              return <ToolCallRow key={bi} call={{
                type: 'toolCall', ...b,
                intent: b.intent || b.name, arguments: b.input ?? b.arguments,
              }} result={null} />
            }
            if (b && b.type === 'tool_result') {
              const text = flattenText(b.content)
              return (
                <details className="t-call t-call-err" key={bi}>
                  <summary className="t-call-sum">
                    <span className={`t-call-dot ${b.is_error ? 'err' : 'ok'}`} />
                    <span className="t-call-name">tool_result</span>
                  </summary>
                  <pre className="t-code">{text || prettyJSON(b)}</pre>
                </details>
              )
            }
            return (
              <details className="t-tool" key={bi}>
                <summary>🧩 {b?.type || 'block'}</summary>
                <pre className="t-code">{prettyJSON(b)}</pre>
              </details>
            )
          })}
        </div>
      </div>
    )
  }
  if (ev.type === 'thinking_level_change') {
    return <div className="t-meta" key={i}>🧠 思考等级 → <code>{ev.thinkingLevel ?? ev.configured}</code></div>
  }
  if (ev.type === 'model_change') {
    return <div className="t-meta" key={i}>🤖 模型 → <code>{ev.model}</code>{ev.resolvedModelIsFallback ? '（回退默认）' : ''}</div>
  }
  if (ev.type === 'session') {
    return <div className="t-meta" key={i}>💡 会话开始 · cwd <code>{ev.cwd}</code></div>
  }
  if (ev.type === 'title' && ev.title) {
    return <div className="t-meta" key={i}>🏷 {ev.title}</div>
  }
  return null
}

// ── 页面：左侧定时任务 → 右侧该任务的会话 ──────────────────────
// 只展示定时任务的会话；普通聊天 transcript 不出现。
export default function SessionsPage({ initialId }) {
  const [jobs, setJobs] = useState(null)
  const [error, setError] = useState('')
  const [selectedJob, setSelectedJob] = useState(initialId || '')
  const [jobTxs, setJobTxs] = useState(null)   // 当前任务的 transcripts（已按 session_ids 过滤）
  const [selectedTx, setSelectedTx] = useState('')
  const [txDetail, setTxDetail] = useState(null)
  const [txLoading, setTxLoading] = useState(false)
  const [copied, setCopied] = useState({})


  const loadJobs = useCallback(async () => {
    try {
      const d = await api.list()
      setJobs(d.jobs || [])
      setError('')
    } catch (e) {
      setError(e.message)
    }
  }, [])

  useEffect(() => { loadJobs() }, [loadJobs])

  // 选中任务：拉该项目全部 transcripts，过滤出该任务 session_ids 对应的
  // （旧任务无 session_ids → 回退 [last_session_id]）。
  useEffect(() => {
    if (!selectedJob) { setJobTxs(null); return }
    let alive = true
    ;(async () => {
      try {
        const job = (await api.list()).jobs?.find((x) => x.id === selectedJob)
        if (!alive || !job) return
        setJobTxs(null)
        const proj = job.project
        const wanted = (job.session_ids && job.session_ids.length ? job.session_ids : (job.last_session_id ? [job.last_session_id] : []))
        const set = new Set(wanted)
        const d = await api.listTranscripts(proj)
        if (!alive) return
        setSelectedTx('')
        setTxDetail(null)
        setJobTxs((d.transcripts || []).filter((t) => set.has(t.id)))
      } catch (e) {
        if (alive) setError(e.message)
      }
    })()
    return () => { alive = false }
  }, [selectedJob])

  // 会话详情：选中后拉取，3s 轮询刷新
  useEffect(() => {
    if (!selectedTx) { setTxDetail(null); return }
    const project = jobs?.find((j) => j.id === selectedJob)?.project || ''
    if (!project) return
    let alive = true
    const fetchOne = async () => {
      try {
        const d = await api.getTranscript(project, selectedTx)
        if (alive) { setTxDetail(d.transcript); setTxLoading(false) }
      } catch (e) {
        if (alive) { setError(e.message); setTxLoading(false) }
      }
    }
    setTxLoading(true)
    fetchOne()
    const timer = setInterval(fetchOne, 3000)
    return () => { alive = false; clearInterval(timer) }
  }, [selectedTx, selectedJob, jobs])

  // 复制会话文本（dictating 会话内容：全部 message 文本）
  const copyTx = useCallback(() => {
    if (!txDetail) return
    const lines = []
    for (const ev of txDetail) {
      if (ev.type !== 'message') continue
      const m = ev.message || {}
      if (!m.content) continue
      if (m.role === 'toolResult') {
        lines.push(`[工具结果 · ${m.toolName || ''}]\n${flattenText(m.content)}`)
        continue
      }
      const txt = Array.isArray(m.content) ? flattenText(m.content) : String(m.content)
      if (txt.trim()) lines.push(`[${ROLE_LABEL[m.role] || m.role}]\n${txt}`)
    }
    navigator.clipboard?.writeText(lines.join('\n\n')).then(() => {
      setCopied({ ...copied, tx: true })
      setTimeout(() => setCopied({ ...copied, tx: false }), 1500)
    }).catch(() => {})
  }, [txDetail, copied])

  const toolResults = useMemo(() => (txDetail ? indexToolResults(txDetail) : new Map()), [txDetail])
  const rendered = useMemo(() => (txDetail || []).map((ev, i) => renderEvent(ev, i, toolResults)), [txDetail, toolResults])

  const activeProject = useMemo(
    () => jobs?.find((j) => j.id === selectedJob)?.project || '',
    [jobs, selectedJob],
  )

  return (
    <div className="sess-page">
      {/* 左栏：定时任务列表 */}
      <div className="sess-list">
        <div className="sess-list-head"><span>定时任务</span></div>
        {error && <div className="err-txt" style={{ padding: 8 }}>{error}</div>}
        {jobs === null && <div className="sess-empty">加载中…</div>}
        {jobs !== null && jobs.length === 0 && <div className="sess-empty">暂无定时任务。创建后这里会出现，可查看它名下每次执行的会话。</div>}
        {jobs && jobs.map((j) => {
          const n = j.session_ids?.length ?? (j.last_session_id ? 1 : 0)
          return (
            <div
              key={j.id}
              className={`sess-item ${j.id === selectedJob ? 'sess-item-on' : ''}`}
              onClick={() => { setSelectedJob(j.id) }}
            >
              <div className="sess-item-name">{j.description || j.id}</div>
              <div className="sess-item-sub">{j.id} · {n} 会话</div>
            </div>
          )
        })}
      </div>

      {/* 右栏：该任务的会话列表 / 会话详情 */}
      <div className="sess-detail">
        {!selectedJob && <div className="sess-empty">← 选择一个定时任务，查看它名下所有会话</div>}

        {selectedJob && !selectedTx && (
          <>
            <div className="sess-detail-head">
              <div className="sess-detail-title">{jobs?.find((j) => j.id === selectedJob)?.description || selectedJob}</div>
              <div className="sess-detail-sub">{activeProject} · 该任务执行的会话（只列定时任务）</div>
            </div>
            <div className="t-flow">
              {jobTxs === null && <div className="sess-empty">加载中…</div>}
              {jobTxs !== null && jobTxs.length === 0 && (
                <div className="sess-empty">该任务还没有执行记录。运行一次后（定时触发或手动「运行」），这里会出现它的会话。</div>
              )}
              {jobTxs && jobTxs.map((t) => (
                <div key={t.id} className="sess-tx-item" onClick={() => setSelectedTx(t.id)}>
                  <div className="sess-tx-name">{t.title || t.name}</div>
                  <div className="sess-tx-sub">{formatTime(t.updated_at)} · {(t.size / 1024).toFixed(1)} KB</div>
                </div>
              ))}
            </div>
          </>
        )}

        {selectedTx && (
          <>
            <div className="sess-detail-head">
              <button className="btn btn-sm btn-ghost" onClick={() => { setSelectedTx(''); setTxDetail(null) }}>← 返回任务会话</button>
              <button className="btn btn-sm btn-ghost sess-copy" onClick={copyTx}>{copied.tx ? '已复制' : '复制文本'}</button>
              <div className="sess-detail-title">{txDetail ? (jobTxs?.find((t) => t.id === selectedTx)?.title || selectedTx) : selectedTx}</div>
              <div className="sess-detail-sub">{activeProject} · {selectedTx}</div>
            </div>
            <div className="t-flow">
              {txLoading && !txDetail && <div className="sess-empty">加载中…</div>}
              {txDetail && rendered.length === 0 && <div className="sess-empty">此会话没有可渲染的事件</div>}
              {txDetail && rendered}
            </div>
          </>
        )}
      </div>
    </div>
  )
}
