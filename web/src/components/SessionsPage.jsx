import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
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

function ToolCallRow({ call, result, streaming = false }) {
  if (!call || call.type !== 'toolCall') return null
  const args = call.arguments
  const hasArgs = args && (!Array.isArray(args) || args.length > 0) && JSON.stringify(args) !== '{}'
  const intent = call.intent || (call.name ? '' : '') || summarizeArgs(args)
  const dot = result ? (result.isError ? 'err' : 'ok') : 'run'
  // 执行中（无结果）展开显示参数；完成后折叠（可在历史中点开）。
  const open = streaming && !result
  return (
    <details className={`t-call ${result && result.isError ? 't-call-err' : ''}`} open={open}>
      <summary className="t-call-sum">
        <span className={`t-call-dot ${dot}`} aria-hidden="true" />
        <span className="t-call-name">{call.name || 'tool'}</span>
        {intent ? <span className="t-call-intent">{intent}</span> : null}
        {result && formatBytes(result.bytes) && (
          <span className={`t-call-size ${result.isError ? 't-call-size-err' : ''}`}>
            {result.isError ? '失败 · ' : ''}{formatBytes(result.bytes)}
          </span>
        )}
        {!result && <span className="t-call-size"><span className="t-call-spin" aria-hidden />执行中</span>}
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

function renderEvent(ev, i, toolResults, streaming = false) {
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
              return <ToolCallRow key={bi} call={b} result={toolResults.get(b.id)} streaming={streaming} />
            }
            if (b && b.type === 'thinking') {
              // 正在流式输出时展开，输出完成后折叠。
              const live = streaming && b.type === 'thinking'
              return (
                <details className="t-tool" key={bi} open={live}>
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
              }} result={null} streaming={streaming} />
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
  const [typingTick, setTypingTick] = useState(0)  // 打字机队列节拍（驱动 flush 重挂）
  const pendingRef = useRef([])                    // [{type:'text'|'thinking', text}] 待逐字刷出的内容
  const flowRef = useRef(null)                     // 消息流容器（自动滚动用）
  const thinkingShownRef = useRef('')   // 已刷到界面的 thinking 全量（用于 diff SSE 快照取增量）
  const [stickBottom, setStickBottom] = useState(true)  // 是否跟随底部（用户上滑历史则暂停自动滚动）

  // 滚动容器的滚动监听：离开底部区域时暂停自动跟随并显示「回到底部」，滚回底部后恢复。
  const onDetailScroll = useCallback(() => {
    const el = flowRef.current
    if (!el) return
    setStickBottom(el.scrollHeight - el.scrollTop - el.clientHeight < 140)
  }, [])


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
        if (alive) { setTxDetail(d.transcript); setLive([]); setTxLoading(false) }
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

  // activeProject 必须先于 sendComposer 等 hook 声明（useCallback deps 会在
  // 每次 render 求值它，置后再引用会触发 TDZ：Cannot access before init）。
  const activeProject = useMemo(
    () => jobs?.find((j) => j.id === selectedJob)?.project || '',
    [jobs, selectedJob],
  )
  // ── 网页继续对话：SSE 流式事件转成 transcript 事件行，实时追加渲染 ──
  // 注意协议语义：EventText.content 是增量（直接入队逐字）；EventThinking.content
  // 是「当前累积全量」快照（同一 turn 会多次 flush），需与已显示量 diff 出增量，
  // 否则快照整段重复追加（见首帧孤立「用户」bug）。工具调用直接插入。
  const appendLive = useCallback((ev) => {
    if (ev.type === 'text') {
      const c = ev.content
      if (c) pushPending('text', c)
      return
    }
    if (ev.type === 'thinking') {
      const full = ev.content || ''
      if (!full) return
      // 基线 = 已刷到界面的 thinking + 队列中尚未 flush 的 thinking（快照是
      // 全量累积，需与「已表达+表达中」diff，避免快照背靠背时增量重复）。
      let expressed = thinkingShownRef.current
      for (let k = pendingRef.current.length - 1; k >= 0; k--) {
        if (pendingRef.current[k].type !== 'thinking') break
        expressed += pendingRef.current[k].text
      }
      let inc = ''
      if (full.startsWith(expressed)) {
        inc = full.slice(expressed.length)   // 快照较上次多出新增 delta
      } else {
        inc = full                           // 快照与基线不连续（新 turn 等）→ 重置
        thinkingShownRef.current = ''
        pendingRef.current = pendingRef.current.filter((x) => x.type !== 'thinking')
      }
      if (inc) pushPending('thinking', inc)
      return
    }
    if (ev.type === 'tool_use') {
      setLive((prev) => {
        const next = prev.slice()
        next.push({
          type: 'message',
          message: { role: 'assistant', content: [{ type: 'toolCall', id: 'live' + next.length, name: ev.tool, arguments: {}, intent: ev.intent || '' }] },
        })
        return next
      })
    }
  }, [])

  const pushPending = useCallback((type, text) => {
    const q = pendingRef.current
    const last = q[q.length - 1]
    if (last && last.type === type) last.text += text
    else q.push({ type, text })
    setTypingTick((t) => t + 1)
  }, [])

  // 把一小段字符追加到当前 assistant 消息的 text / thinking 块。
  const appendChunk = useCallback((type, part) => {
    if (type === 'thinking') thinkingShownRef.current += part
    setLive((prev) => {
      const next = prev.slice()
      let last = next[next.length - 1]
      if (!last || last.type !== 'message' || last.message?.role !== 'assistant') {
        next.push({ type: 'message', message: { role: 'assistant', content: [] } })
        last = next[next.length - 1]
      }
      const blocks = last.message.content || []
      if (type === 'thinking') {
        const b = blocks.find((x) => x.type === 'thinking')
        if (b) b.thinking = (b.thinking || '') + part
        else blocks.push({ type: 'thinking', thinking: part })
      } else {
        const b = blocks[blocks.length - 1]
        if (b && b.type === 'text') b.text = (b.text || '') + part
        else blocks.push({ type: 'text', text: part })
      }
      return next
    })
  }, [])

  useEffect(() => {
    if (!sending) { pendingRef.current = []; return }
    if (pendingRef.current.length === 0) return
    const t = setInterval(() => {
      const q = pendingRef.current
      const head = q[0]
      if (!head) return
      const n = Math.min(head.text.length, 2)   // 每拍 2 字符，约 40 字符/秒
      const part = head.text.slice(0, n)
      head.text = head.text.slice(n)
      if (!head.text) q.shift()
      appendChunk(head.type, part)
      setTypingTick((t2) => t2 + 1)
    }, 25)
    return () => clearInterval(t)
  }, [sending, typingTick, appendChunk])

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
    thinkingShownRef.current = ''
    setSendErr('')
    setSending(true)
    // 立即清空输入区，界面马上反馈「已发送」，不必等整轮回复结束。
    setComposerText('')
    setAttachments([])

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
      // omp 每次 resume 会 fork 新 session 文件，原始 transcript 不会更新；
      // done 携带新 id 时切换到续写后的会话，详情页显示完整续篇。
      let newTxId = ''
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
            else if (msg.type === 'done') newTxId = msg.id || ''
            else appendLive(msg)
          }
        }
      }
      if (newTxId && newTxId !== selectedTx) setSelectedTx(newTxId)
    } catch (err) {
      setSendErr(err.message || '发送失败')
    } finally {
      // live 保留到轮询取回权威 transcript 后再清空（见 fetchOne），
      // 避免发送结束到刷新之间回复内容断档。
      setSending(false)
    }
  }, [sending, composerText, attachments, selectedTx, activeProject, appendLive])

  // 历史（权威）与流式 live 拆分渲染：流式期间只重建 live 尾部，避免
  // 整棵消息树每次重算导致的卡顿；结束后 live 由轮询刷新取代并清空。
  const histToolResults = useMemo(() => indexToolResults(txDetail || []), [txDetail])
  const histRendered = useMemo(() => (txDetail || []).map((ev, i) => renderEvent(ev, i, histToolResults, false)), [txDetail, histToolResults])
  const liveToolResults = useMemo(() => indexToolResults(live), [live])
  const liveRendered = useMemo(() => live.map((ev, i) => renderEvent(ev, (txDetail?.length || 0) + i, liveToolResults, sending)), [live, liveToolResults, txDetail, sending])
  const rendered = [...histRendered, ...liveRendered]

  // 流式逐字追加时自动滚动到底，保证新内容可见。
  useEffect(() => {
    const el = flowRef.current
    if (el && stickBottom && (sending || live.length)) el.scrollTop = el.scrollHeight
  }, [live, sending, stickBottom])

  // 进入/切换会话时滚到最新消息（底部），而非停在最早消息。
  // 需等 txDetail 渲染就绪后再滚（fetch 返回后才有实际高度）。
  const [scrollOnLoad, setScrollOnLoad] = useState(false)
  useEffect(() => { setScrollOnLoad(!!selectedTx) }, [selectedTx])
  useEffect(() => {
    if (!scrollOnLoad || !txDetail) return
    const t = setTimeout(() => {
      const el = flowRef.current
      if (el) { el.scrollTop = el.scrollHeight; setScrollOnLoad(false) }
    }, 30)
    return () => clearTimeout(t)
  }, [scrollOnLoad, txDetail])

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
      <div className="sess-detail" ref={flowRef} onScroll={onDetailScroll}>
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
              {sending && (
                <div className="sess-typing"><span className="sess-spin" aria-hidden />助手正在回复…</div>
              )}
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
            {!stickBottom && (
              <button
                className="sess-back-btn"
                aria-label="回到最新消息"
                title="回到最新消息"
                onClick={() => {
                  const el = flowRef.current
                  if (el) el.scrollTop = el.scrollHeight
                  setStickBottom(true)
                }}
              >
                ↓
              </button>
            )}
          </>
        )}
      </div>
    </div>
  )
}
