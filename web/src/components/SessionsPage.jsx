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
  const [sending, setSending] = useState(false)     // 网页「继续对话」进行中
  const [composerText, setComposerText] = useState('')
  const [attachments, setAttachments] = useState([]) // {kind:'image'|'file', name, mime, b64}
  const [live, setLive] = useState([])               // 流式对话期间追加的事件（transcript 格式）
  const [sendErr, setSendErr] = useState('')


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
    if (sending) return
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
  }, [selectedTx, selectedJob, jobs, sending])

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

  // ── 网页继续对话：SSE 流式事件转成 transcript 事件行，实时追加渲染 ──
  // 文本/思考按 delta 累积进当前一条 assistant 消息；工具调用按独立行显示，
  // 其结果由发送结束后的 transcript 刷新补齐（与 toolCall 配对渲染）。
  const appendLive = useCallback((ev) => {
    setLive((prev) => {
      const next = prev.slice()
      if (ev.type === 'text' && ev.content) {
        const last = next[next.length - 1]
        if (last && last.type === 'message' && last.message?.role === 'assistant') {
          const blocks = last.message.content || []
          const b = blocks[blocks.length - 1]
          if (b && b.type === 'text') b.text = (b.text || '') + ev.content
          else blocks.push({ type: 'text', text: ev.content })
        } else {
          next.push({ type: 'message', message: { role: 'assistant', content: [{ type: 'text', text: ev.content }] } })
        }
      } else if (ev.type === 'thinking' && ev.content) {
        const last = next[next.length - 1]
        if (last && last.type === 'message' && last.message?.role === 'assistant') {
          const b = (last.message.content || []).find((x) => x.type === 'thinking')
          if (b) b.thinking = (b.thinking || '') + ev.content
          else last.message.content.push({ type: 'thinking', thinking: ev.content })
        }
      } else if (ev.type === 'tool_use') {
        next.push({
          type: 'message',
          message: { role: 'assistant', content: [{ type: 'toolCall', id: 'live' + next.length, name: ev.tool, arguments: {}, intent: ev.intent || '' }] },
        })
      }
      return next
    })
  }, [])

  // 选择图片/文件附件：本地读为 base64 内联提交（不另走上传接口）。
  const onPickFile = useCallback((e) => {
    const files = Array.from(e.target.files || [])
    if (files.length === 0) return
    Promise.all(files.map((f) => new Promise((resolve) => {
      const fr = new FileReader()
      fr.onload = () => {
        const dataUrl = String(fr.result || '')
        resolve({
          kind: f.type.startsWith('image/') ? 'image' : 'file',
          name: f.name,
          mime: f.type || 'application/octet-stream',
          b64: dataUrl.split(',')[1] || '',
        })
      }
      fr.readAsDataURL(f)
    }))).then((list) => setAttachments((prev) => [...prev, ...list]))
    e.target.value = ''
  }, [])

  // 发送：立即回显用户消息，再经 SSE 流式接收助手的回复。
  const sendComposer = useCallback(async () => {
    const text = composerText.trim()
    if (sending || (!text && attachments.length === 0)) return
    const blocks = []
    if (text) blocks.push({ type: 'text', text })
    attachments.forEach((a) => blocks.push({ type: 'text', text: a.kind === 'image' ? `🖼 ${a.name}` : `📎 ${a.name}` }))
    if (blocks.length === 0) blocks.push({ type: 'text', text: '' })
    setTxDetail((prev) => [...(prev || []), { type: 'message', message: { role: 'user', content: blocks } }])
    setLive([])
    setSendErr('')
    setSending(true)
    try {
      const res = await api.chatTranscript(activeProject, selectedTx, {
        message: text,
        images: attachments.filter((a) => a.kind === 'image').map((a) => ({ mime_type: a.mime, filename: a.name, data: a.b64 })),
        files: attachments.filter((a) => a.kind === 'file').map((a) => ({ mime_type: a.mime, filename: a.name, data: a.b64 })),
      })
      if (!res.ok || !res.body) throw new Error(`HTTP ${res.status}`)
      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buf = ''
      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        let nl
        while ((nl = buf.indexOf('\n\n')) !== -1) {
          const frame = buf.slice(0, nl)
          buf = buf.slice(nl + 2)
          for (const line of frame.split('\n')) {
            if (!line.startsWith('data:')) continue
            const data = line.slice(5).trim()
            if (!data) continue
            let msg
            try { msg = JSON.parse(data) } catch { continue }
            if (msg.type === 'error') setSendErr(msg.error || '对话出错')
            else if (msg.type !== 'done') appendLive(msg)
          }
        }
      }
    } catch (err) {
      setSendErr(err.message || '发送失败')
    } finally {
      setSending(false)
      setLive([])
      setComposerText('')
      setAttachments([])
    }
  }, [sending, composerText, attachments, selectedTx, activeProject, appendLive])

  // 流式对话期间把 live 事件追加在历史之后一起渲染；结束后 txDetail 会被
  // 轮询刷新为 omp 写入的权威 transcript，live 随即清空。
  const viewEvents = (sending || live.length > 0) ? [...(txDetail || []), ...live] : txDetail
  const toolResults = useMemo(() => (viewEvents ? indexToolResults(viewEvents) : new Map()), [viewEvents])
  const rendered = useMemo(() => (viewEvents || []).map((ev, i) => renderEvent(ev, i, toolResults)), [viewEvents, toolResults])

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
            <div className="sess-composer">
              {sendErr && <div className="sess-stream-err">⚠ {sendErr}</div>}
              {attachments.length > 0 && (
                <div className="sess-picked">
                  {attachments.map((a, i) => (
                    <span key={i}>
                      {a.kind === 'image' ? '🖼' : '📎'} {a.name}
                      {!sending && (
                        <button onClick={() => setAttachments((arr) => arr.filter((_, j) => j !== i))}>×</button>
                      )}
                    </span>
                  ))}
                </div>
              )}
              <textarea
                className="input"
                rows={2}
                placeholder={sending ? '助手正在回复…' : '输入消息继续对话（Enter 发送，Shift+Enter 换行）'}
                value={composerText}
                disabled={sending}
                onChange={(e) => setComposerText(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
                    e.preventDefault()
                    sendComposer()
                  }
                }}
              />
              <div className="sess-composer-actions">
                <label className="btn btn-sm btn-ghost sess-attach">
                  附件
                  <input type="file" multiple hidden onChange={onPickFile} />
                </label>
                <span className="sess-streaming">{sending ? '对话中…' : '可继续对话'}</span>
                <button
                  className="btn btn-sm btn-solid"
                  onClick={sendComposer}
                  disabled={sending || (!composerText.trim() && attachments.length === 0)}
                >
                  发送 ⏎
                </button>
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
