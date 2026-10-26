import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { marked } from 'marked'
import { api } from '../api.js'
import { formatTime } from '../lib/time.js'
import ProjectConfigPanel from './ProjectConfigPanel.jsx'
import NewProjectDialog from './NewProjectDialog.jsx'
import { useEsc } from '../hooks/useEsc.js'

const ROLE_LABEL = { user: '你', assistant: 'Harness', tool: '工具', system: '系统' }

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
// ── 页面：会话库（文件夹 → 会话 → 对话） ─────────────────────────
// 会话文件全局共享，按 transcript 内的 session.cwd 归入文件夹；来源
// （定时任务 / 对话）只能由 cron 任务的 session_ids 反查得出。
const ALL = '__all__'

// 时间范围 → 时间戳下界。today = 本地今天 00:00。
function rangeCutoff(range) {
  const now = Date.now()
  if (range === '1h') return now - 3600 * 1000
  if (range === 'today') {
    const d = new Date()
    d.setHours(0, 0, 0, 0)
    return d.getTime()
  }
  if (range === '7d') return now - 7 * 86400 * 1000
  if (range === '30d') return now - 30 * 86400 * 1000
  if (range === '90d') return now - 90 * 86400 * 1000
  return 0
}

// 会话状态（后端推断）：进行中 / 提问待处理 / 已完成 / 静默
const STATUS_LABEL = {
  running: '进行中',
  asking: '提问待处理',
  done: '已完成',
  idle: '静默',
}
const STATUS_HINT = {
  running: '最近 5 分钟内有活动且本轮未收尾',
  asking: '有 ask 提问在等回答',
  done: '本轮已正常结束',
  idle: '没有收尾记录且超过 5 分钟无活动（进程可能被中断）',
}

export default function SessionsPage({ initialTx, onToast }) {
  const [library, setLibrary] = useState(null)      // 全局会话（含 cwd/job_id/archived）
  const [projects, setProjects] = useState([])
  const [archivedCount, setArchivedCount] = useState(0)
  const [view, setView] = useState('active')        // active | archived
  const [folder, setFolder] = useState(ALL)         // cwd / ''（未归类）/ ALL
  const [source, setSource] = useState('all')       // all | cron | chat
  const [qInput, setQInput] = useState('')
  const [q, setQ] = useState('')                    // 提交后的关键词（后端搜正文）
  const [range, setRange] = useState('today')       // 1h | today | 7d | 30d | 90d | all（默认今天）
  const [checked, setChecked] = useState([])
  const [error, setError] = useState('')
  // 刷新后保持选中的会话：#/sessions?tx=<id>
  const [selectedTx, setSelectedTx] = useState(() => {
    const qs = window.location.hash.split('?')[1] || ''
    return new URLSearchParams(qs).get('tx') || ''
  })
  const [txDetail, setTxDetail] = useState(null)
  const [txLoading, setTxLoading] = useState(false)
  const [titleBusy, setTitleBusy] = useState('')
  const [copied, setCopied] = useState({})
  const [sending, setSending] = useState(false)
  const [composerText, setComposerText] = useState('')
  const [modelCtrl, setModelCtrl] = useState({ models: [], current: '' })    // 发送左侧：可用模型 + 当前
  const [reasonCtrl, setReasonCtrl] = useState({ efforts: [], current: '' })  // 发送左侧：思考强度级别 + 当前
  const [attachments, setAttachments] = useState([])
  const [live, setLive] = useState([])
  const [sendErr, setSendErr] = useState('')
  const [stopping, setStopping] = useState(false)
  const [typingTick, setTypingTick] = useState(0)
  const pendingRef = useRef([])
  const flowRef = useRef(null)
  const thinkingShownRef = useRef('')
  const [stickBottom, setStickBottom] = useState(true)
  const [configProject, setConfigProject] = useState('')  // 非空 → 打开配置弹窗
  const [newOpen, setNewOpen] = useState(false)
  // 会话状态（后端从 transcript 尾部推断）：running/asking/done/idle
  useEsc(!!configProject, () => setConfigProject(''))

  const loadLibrary = useCallback(async () => {
    try {
      const d = await api.library(view === 'archived' ? 'archived' : '', q, rangeCutoff(range))
      const items = d.transcripts || []
      setLibrary(items)
      setArchivedCount(d.archived_count || 0)
      setError('')
    } catch (e) {
      setError(e.message)
    }
  }, [view, q, range])

  const loadProjects = useCallback(async () => {
    try {
      setProjects((await api.listProjects()).projects || [])
    } catch { /* 列表失败时仍可浏览会话 */ }
  }, [])

  useEffect(() => { loadProjects() }, [loadProjects])
  useEffect(() => { loadLibrary() }, [loadLibrary])

  // 列表实时刷新：只要在会话页，就每 3s 重拉一次（正在写入的会话会显示「实时」）。
  useEffect(() => {
    const t = setInterval(() => { loadLibrary() }, 3000)
    return () => clearInterval(t)
  }, [loadLibrary])

  // 同页内 hash 变化（例如从任务执行记录跳转）也要打开对应会话。
  useEffect(() => {
    const onHash = () => {
      const tx = new URLSearchParams(window.location.hash.split('?')[1] || '').get('tx') || ''
      setSelectedTx((prev) => (prev === tx ? prev : tx))
    }
    window.addEventListener('hashchange', onHash)
    return () => window.removeEventListener('hashchange', onHash)
  }, [])

  // 直链/站内跳转进来时，hashchange 不一定触发（页面刚加载），库到手后按 URL 再对一次。
  useEffect(() => {
    if (library === null) return
    const tx = new URLSearchParams(window.location.hash.split('?')[1] || '').get('tx') || ''
    if (tx) setSelectedTx((prev) => (prev === tx ? prev : tx))
  }, [library])

  // 选中会话写进 URL，刷新后自动回到同一会话并继续实时刷新。
  useEffect(() => {
    const h = selectedTx ? `#/sessions?tx=${encodeURIComponent(selectedTx)}` : '#/sessions'
    if (window.location.hash !== h) window.history.replaceState(null, '', h)
  }, [selectedTx])

  // 文件夹分组：项目配置 + 会话 cwd 的并集。有会话的按最近更新排序，
  // 只有配置的排在后面，未归类固定最后。
  const folders = useMemo(() => {
    const map = new Map()
    const ensure = (cwd) => {
      const key = cwd || ''
      let f = map.get(key)
      if (!f) { f = { cwd: key, items: [], projects: [] }; map.set(key, f) }
      return f
    }
    for (const p of projects) ensure(p.work_dir || '').projects.push(p)
    for (const it of library || []) ensure(it.cwd || '').items.push(it)
    const list = [...map.values()].filter((f) => f.items.length || f.projects.length)
    for (const f of list) {
      f.label = f.cwd || '未归类'
      f.latest = f.items.length ? new Date(f.items[0].updated_at).getTime() : 0
    }
    list.sort((a, b) => {
      if (!!a.cwd !== !!b.cwd) return a.cwd ? -1 : 1
      if (a.items.length && b.items.length) return b.latest - a.latest
      if (a.items.length !== b.items.length) return b.items.length - a.items.length
      return a.label.localeCompare(b.label)
    })
    return list
  }, [projects, library])

  // 中栏过滤：文件夹 + 来源 + 时间（关键词已由后端处理），默认按最后更新时间倒序。
  const visible = useMemo(() => {
    let arr = library || []
    if (folder !== ALL) arr = arr.filter((it) => (it.cwd || '') === folder)
    if (source === 'cron') arr = arr.filter((it) => it.job_id)
    if (source === 'chat') arr = arr.filter((it) => !it.job_id)
    if (range !== 'all') {
      const cut = rangeCutoff(range)
      arr = arr.filter((it) => new Date(it.updated_at).getTime() >= cut)
    }
    return arr.slice().sort((a, b) => new Date(b.updated_at) - new Date(a.updated_at))
  }, [library, folder, source, range])

  // 会话详情/对话需要一个项目名（后端按项目取 session dir；目录全局共享，
  // 任意运行中的项目都可用）。
  const projectFor = useCallback((it) => {
    const cwd = it?.cwd || ''
    if (cwd) {
      const hit = projects.find((p) => (p.work_dir || '') === cwd)
      if (hit) return hit.name
    }
    const running = projects.find((p) => p.running !== false)
    return running?.name || projects[0]?.name || ''
  }, [projects])

  const currentTx = useMemo(() => (library || []).find((x) => x.id === selectedTx), [library, selectedTx])
  const activeProject = useMemo(() => projectFor(currentTx), [currentTx, projectFor])

  // 发送旁的下拉：按当前项目引擎读取可切换的模型 / 思考强度与当前值；
  // agent 不支持（接口报错）时静默隐藏对应控件。
  useEffect(() => {
    if (!activeProject) { setModelCtrl({ models: [], current: '' }); setReasonCtrl({ efforts: [], current: '' }); return }
    let alive = true
    api.getModels(activeProject)
      .then((d) => { if (alive) setModelCtrl({ models: (d && d.models) || [], current: (d && d.current) || '' }) })
      .catch(() => { if (alive) setModelCtrl({ models: [], current: '' }) })
    api.getReasoning(activeProject)
      .then((d) => { if (alive) setReasonCtrl({ efforts: (d && d.efforts) || [], current: (d && d.current) || '' }) })
      .catch(() => { if (alive) setReasonCtrl({ efforts: [], current: '' }) })
    return () => { alive = false }
  }, [activeProject])

  const switchModel = useCallback(async (m) => {
    if (!activeProject || m === modelCtrl.current) return
    try {
      await api.setModel(activeProject, m)
      setModelCtrl((c) => ({ ...c, current: m }))
      onToast?.('ok', m ? `模型已切换：${m}（下次发送生效）` : '已恢复默认模型（下次发送生效）')
    } catch (e) { onToast?.('err', e.message || '模型切换失败') }
  }, [activeProject, modelCtrl.current, onToast])

  const switchReasoning = useCallback(async (level) => {
    if (!activeProject || level === reasonCtrl.current) return
    try {
      await api.setReasoning(activeProject, level)
      setReasonCtrl((c) => ({ ...c, current: level }))
      onToast?.('ok', `思考强度已切换：${level}（下次发送生效）`)
    } catch (e) { onToast?.('err', e.message || '思考强度切换失败') }
  }, [activeProject, reasonCtrl.current, onToast])

  // 从任务执行记录点「查看对话」跳进来：定位到该会话所在文件夹。
  useEffect(() => {
    if (!initialTx || !library) return
    const it = library.find((x) => x.id === initialTx)
    if (!it) return
    setFolder(it.cwd || ALL)
    setSelectedTx(initialTx)
  }, [initialTx, library])

  const onDetailScroll = useCallback(() => {
    const el = flowRef.current
    if (!el) return
    setStickBottom(el.scrollHeight - el.scrollTop - el.clientHeight < 140)
  }, [])

  // 会话详情：选中后拉取，2s 轮询（会话可能正被网页/终端/飞书写入）。
  // 走全局端点，不依赖项目列表，刷新后直接链接也能打开。
  useEffect(() => {
    if (!selectedTx) { setTxDetail(null); return }
    if (sending) return
    let alive = true
    const fetchOne = async () => {
      try {
        const d = await api.libraryTranscript(selectedTx)
        if (!alive) return
        const arr = d.transcript || []
        // 没变化就不重建整棵消息树（大文件每 2s 全量重渲染会卡）。
        setTxDetail((prev) => {
          if (prev && prev.length === arr.length
            && JSON.stringify(prev[prev.length - 1]) === JSON.stringify(arr[arr.length - 1])) return prev
          return arr
        })
        setLive([])
        setTxLoading(false)
      } catch (e) {
        if (alive) { setError(e.message); setTxLoading(false) }
      }
    }
    setTxLoading(true)
    fetchOne()
    const timer = setInterval(fetchOne, 2000)
    return () => { alive = false; clearInterval(timer) }
  }, [selectedTx, sending])

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

  // 复制终端续接命令：cd 到会话的工作目录后用 omp --resume 续上同一份 transcript。
  // 桥进程正在对话时不要并发使用（两边会同时写一个会话文件）。
  const copyResume = useCallback((it) => {
    if (!it?.resume_cmd) return
    navigator.clipboard?.writeText(it.resume_cmd).then(() => {
      setCopied((p) => ({ ...p, [it.id]: true }))
      setTimeout(() => setCopied((p) => ({ ...p, [it.id]: false })), 1500)
      onToast?.('ok', '已复制：粘贴到终端即可继续该会话')
    }).catch(() => onToast?.('err', '复制失败，请手动选择命令'))
  }, [onToast])

  const genTitle = async (id) => {
    if (titleBusy) return
    setTitleBusy(id)
    try {
      const data = await api.regenerateTitle(activeProject, id)
      setLibrary((prev) => (prev || []).map((x) => (x.id === id ? { ...x, title: data.title } : x)))
      onToast?.('ok', '会话标题已更新')
    } catch (e) {
      setError(e.message || '标题生成失败')
    } finally {
      setTitleBusy('')
    }
  }

  // ── 网页继续对话：SSE 事件流（与旧实现一致） ──
  const pushPending = useCallback((type, text) => {
    const q2 = pendingRef.current
    const last = q2[q2.length - 1]
    if (last && last.type === type) last.text += text
    else q2.push({ type, text })
    setTypingTick((t) => t + 1)
  }, [])

  const appendLive = useCallback((ev) => {
    if (ev.type === 'text') {
      if (ev.content) pushPending('text', ev.content)
      return
    }
    if (ev.type === 'thinking') {
      const full = ev.content || ''
      if (!full) return
      let expressed = thinkingShownRef.current
      for (let k = pendingRef.current.length - 1; k >= 0; k--) {
        if (pendingRef.current[k].type !== 'thinking') break
        expressed += pendingRef.current[k].text
      }
      let inc = ''
      if (full.startsWith(expressed)) {
        inc = full.slice(expressed.length)
      } else {
        inc = full
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
  }, [pushPending])

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
      const q2 = pendingRef.current
      const headEv = q2[0]
      if (!headEv) return
      const n = Math.min(headEv.text.length, 2)
      const part = headEv.text.slice(0, n)
      headEv.text = headEv.text.slice(n)
      if (!headEv.text) q2.shift()
      appendChunk(headEv.type, part)
      setTypingTick((t2) => t2 + 1)
    }, 25)
    return () => clearInterval(t)
  }, [sending, typingTick, appendChunk])

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

  const sendComposer = useCallback(async () => {
    const text = composerText.trim()
    if (sending || (!text && attachments.length === 0) || !activeProject) return
    const blocks = []
    if (text) blocks.push({ type: 'text', text })
    attachments.forEach((a) => blocks.push({ type: 'text', text: a.kind === 'image' ? `🖼 ${a.name}` : `📎 ${a.name}` }))
    if (blocks.length === 0) blocks.push({ type: 'text', text: '' })
    setTxDetail((prev) => [...(prev || []), { type: 'message', message: { role: 'user', content: blocks } }])
    setLive([])
    thinkingShownRef.current = ''
    setSendErr('')
    setSending(true)
    setComposerText('')
    const sent = attachments
    setAttachments([])

    try {
      const res = await api.chatTranscript(activeProject, selectedTx, {
        message: text,
        images: sent.filter((a) => a.kind === 'image').map((a) => ({ mime_type: a.mime, filename: a.name, data: a.b64 })),
        files: sent.filter((a) => a.kind === 'file').map((a) => ({ mime_type: a.mime, filename: a.name, data: a.b64 })),
      })
      if (!res.ok || !res.body) throw new Error(`HTTP ${res.status}`)
      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buf = ''
      let newTxId = ''
      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        let idx
        while ((idx = buf.indexOf('\n\n')) >= 0) {
          const chunk = buf.slice(0, idx)
          buf = buf.slice(idx + 2)
          const line = chunk.split('\n').find((l) => l.startsWith('data: '))
          if (!line) continue
          let ev
          try { ev = JSON.parse(line.slice(6)) } catch { continue }
          if (ev.type === 'done') { newTxId = ev.id || ''; continue }
          if (ev.type === 'error') { setSendErr(ev.message || 'stream error'); continue }
          appendLive(ev)
        }
      }
      // omp resume 会 fork 新文件：done 携带新 id 就切换过去。
      if (newTxId && newTxId !== selectedTx) {
        setSelectedTx(newTxId)
        await loadLibrary()
      }
    } catch (err) {
      setSendErr(err.message || '发送失败')
    } finally {
      setSending(false)
      setLive([])
      pendingRef.current = []
      thinkingShownRef.current = ''
    }
  }, [sending, composerText, attachments, selectedTx, activeProject, appendLive, loadLibrary])

  // 停止本轮对话：请求后端 abort（agent 进程保留），SSE 会以 done 结束并由
  // sendComposer 的 finally 复位 sending/stopping。
  const stopTurn = useCallback(async () => {
    if (!activeProject || !selectedTx) return
    setStopping(true)
    try {
      await api.stopChat(activeProject, selectedTx)
    } catch (e) {
      onToast?.('err', e.message || '停止失败')
    } finally {
      setStopping(false)
    }
  }, [activeProject, selectedTx, onToast])
  const histToolResults = useMemo(() => indexToolResults(txDetail || []), [txDetail])
  const histRendered = useMemo(() => (txDetail || []).map((ev, i) => renderEvent(ev, i, histToolResults, false)), [txDetail, histToolResults])
  const liveToolResults = useMemo(() => indexToolResults(live), [live])
  const liveRendered = useMemo(() => live.map((ev, i) => renderEvent(ev, (txDetail?.length || 0) + i, liveToolResults, sending)), [live, liveToolResults, txDetail, sending])
  const rendered = [...histRendered, ...liveRendered]

  useEffect(() => {
    const el = flowRef.current
    if (el && stickBottom && (sending || live.length)) el.scrollTop = el.scrollHeight
  }, [live, sending, stickBottom])

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

  const toggleChecked = (id) => {
    setChecked((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]))
  }

  // 单条归档 / 恢复（列表项上的按钮）
  const toggleArchive = async (id, restore) => {
    try {
      if (restore) await api.unarchiveSessions([id])
      else await api.archiveSessions([id])
      onToast?.('ok', restore ? '已恢复该会话' : '已归档，可在「归档」中查看和恢复')
      if (!restore && id === selectedTx) { setSelectedTx(''); setTxDetail(null) }
      setChecked((prev) => prev.filter((x) => x !== id))
      await loadLibrary()
    } catch (e) {
      setError(e.message)
    }
  }

  const archiveChecked = async () => {
    if (checked.length === 0) return
    try {
      await api.archiveSessions(checked)
      onToast?.('ok', `已归档 ${checked.length} 个会话，可在「归档」中恢复`)
      setChecked([])
      if (checked.includes(selectedTx)) { setSelectedTx(''); setTxDetail(null) }
      await loadLibrary()
    } catch (e) { setError(e.message) }
  }

  const restoreChecked = async () => {
    if (checked.length === 0) return
    try {
      await api.unarchiveSessions(checked)
      onToast?.('ok', `已恢复 ${checked.length} 个会话`)
      setChecked([])
      await loadLibrary()
    } catch (e) { setError(e.message) }
  }

  const folderStats = (f) => {
    const parts = []
    if (f.items.length) parts.push(`${f.items.length} 会话`)
    if (f.projects.length) parts.push(`${f.projects.length} 项目`)
    if (!f.items.length) parts.push('无会话')
    return parts.join(' · ')
  }

  return (
    <div className="sess-page lib-page">
      {/* 左栏：文件夹（含其下项目）；齿轮打开配置抽屉 */}
      <div className="sess-list lib-folders">
        <div className="sess-list-head">
          <span>文件夹 / 项目</span>
          <button
            className={`btn btn-sm ${view === 'archived' ? 'btn-solid' : 'btn-ghost'}`}
            onClick={() => { setView(view === 'archived' ? 'active' : 'archived'); setChecked([]); setSelectedTx('') }}
            title="查看已归档的会话"
          >
            归档{archivedCount ? ` (${archivedCount})` : ''}
          </button>
        </div>
        {error && <div className="err-txt" style={{ padding: 8 }}>{error}</div>}
        <div
          className={`sess-item ${folder === ALL ? 'sess-item-on' : ''}`}
          onClick={() => setFolder(ALL)}
        >
          <div className="sess-item-name">全部会话</div>
          <div className="sess-item-sub">{(library || []).length} 个</div>
        </div>
        {library === null && <div className="sess-empty">加载中…</div>}
        {folders.map((f) => (
          <div key={f.cwd || '__none__'}>
            <div
              className={`sess-item ${folder === f.cwd ? 'sess-item-on' : ''}`}
              onClick={() => setFolder(f.cwd)}
              title={f.cwd || '没有记录 cwd 的会话'}
            >
              <div className="sess-item-name">
                {f.cwd ? `📁 ${f.label.split('/').filter(Boolean).pop() || f.label}` : '未归类'}
                {!f.cwd && <span className="bot-tag">无 cwd</span>}
              </div>
              <div className="sess-item-sub" title={f.cwd || ''}>{folderStats(f)}</div>
            </div>
            {f.projects.length > 0 && (
              <div className="lib-badges">
                {f.projects.map((p) => (
                  <button
                    key={p.name}
                    type="button"
                    className={`lib-badge ${p.running === false ? 'lib-badge-off' : ''}`}
                    title="项目配置（点击打开）"
                    onClick={() => setConfigProject(p.name)}
                  >
                    <span>{p.display_name || p.name}</span>
                    <span className="lib-badge-icon" aria-hidden>⚙</span>
                  </button>
                ))}
              </div>
            )}
          </div>
        ))}
        <div className="lib-foot">
          <button className="btn btn-sm btn-green" onClick={() => setNewOpen(true)}>+ 新建项目</button>
        </div>
      </div>

      {/* 中栏：会话列表；来源徽标 + 过滤 + 多选归档 */}
      <div className="sess-list lib-txs">
        <div className="sess-list-head">
          <span>{view === 'archived' ? '已归档' : '会话'}</span>
          {checked.length > 0 && (
            view === 'archived'
              ? <button className="btn btn-sm btn-green" onClick={restoreChecked}>恢复 {checked.length}</button>
              : <button className="btn btn-sm btn-red" onClick={archiveChecked}>归档 {checked.length}</button>
          )}
        </div>
        <div className="lib-filters">
          <input
            className="input"
            placeholder="搜索标题或正文，回车"
            value={qInput}
            onChange={(e) => setQInput(e.target.value)}
            onKeyDown={(e) => { if (e.key === 'Enter') setQ(qInput.trim()) }}
            spellCheck={false}
          />
          <div className="lib-filter-row">
            <select className="input" value={source} onChange={(e) => setSource(e.target.value)}>
              <option value="all">全部来源</option>
              <option value="cron">定时任务</option>
              <option value="chat">对话</option>
            </select>
            <select className="input" value={range} onChange={(e) => setRange(e.target.value)}>
              <option value="1h">最近 1 小时</option>
              <option value="today">今天</option>
              <option value="7d">最近 7 天</option>
              <option value="30d">最近 30 天</option>
              <option value="90d">最近 90 天</option>
              <option value="all">全部时间</option>
            </select>
          </div>
          {q && (
            <div className="lib-query">
              关键词：<code>{q}</code>
              <button className="btn btn-sm btn-ghost" onClick={() => { setQ(''); setQInput('') }}>清除</button>
            </div>
          )}
        </div>
        {library === null && <div className="sess-empty">加载中…</div>}
        {library !== null && visible.length === 0 && (
          <div className="sess-empty">
            {view === 'archived' ? '归档里还没有会话。' : '没有匹配的会话。'}
          </div>
        )}
        <div className="lib-tx-scroll">
          {visible.map((t) => (
            <div
              key={t.id}
              className={`sess-tx-item ${selectedTx === t.id ? 'sess-tx-on' : ''}`}
              onClick={() => setSelectedTx(t.id)}
            >
              <input
                type="checkbox"
                className="lib-check"
                checked={checked.includes(t.id)}
                onChange={(e) => { e.stopPropagation(); toggleChecked(t.id) }}
                onClick={(e) => e.stopPropagation()}
              />
              <div className="lib-tx-main">
                <div className="sess-tx-name">
                  {t.job_id
                    ? <span className="badge badge-cron" title={`定时任务：${t.job_title || t.job_id}`}>定时任务</span>
                    : <span className="badge badge-chat">对话</span>}
                  <span className="lib-tx-title" title={t.title || t.id}>{t.title || t.id}</span>
                </div>
                <div className="sess-tx-sub">
                  <span className="lib-tx-meta">
                    {STATUS_LABEL[t.status] && (
                      <span className={`lib-status st-${t.status}`} title={STATUS_HINT[t.status] || ''}>
                        {STATUS_LABEL[t.status]}
                      </span>
                    )}
                    <span>{formatTime(t.updated_at)} · {formatBytes(t.size)}</span>
                    {t.cwd && folder === ALL && <span className="lib-cwd" title={t.cwd}>{t.cwd.split('/').filter(Boolean).pop()}</span>}
                  </span>
                  <span className="lib-tx-ops">
                    <button
                      className="btn btn-sm btn-ghost sess-title-btn"
                      disabled={titleBusy === t.id}
                      title="根据最近对话重新生成标题"
                      onClick={(e) => { e.stopPropagation(); genTitle(t.id) }}
                    >
                      {titleBusy === t.id ? '生成中…' : '生成标题'}
                    </button>
                    <button
                      className={`btn btn-sm ${view === 'archived' ? 'btn-green' : 'btn-ghost'}`}
                      title={view === 'archived' ? '从归档恢复' : '归档（软删除，可随时恢复）'}
                      onClick={(e) => { e.stopPropagation(); toggleArchive(t.id, view === 'archived') }}
                    >
                      {view === 'archived' ? '恢复' : '归档'}
                    </button>
                    <button
                      className="btn btn-sm btn-ghost"
                      disabled={!t.resume_cmd}
                      title="复制终端命令：在终端里继续这个会话"
                      onClick={(e) => { e.stopPropagation(); copyResume(t) }}
                    >
                      {copied[t.id] ? '已复制' : '终端'}
                    </button>
                  </span>
                </div>
              </div>
            </div>
          ))}
        </div>
      </div>

      {/* 右栏：会话详情 / 继续对话 */}
      <div className="sess-detail" ref={flowRef} onScroll={onDetailScroll}>
        {!selectedTx && (
          <div className="sess-empty">← 选择文件夹与会话；定时任务产生的会话带「定时任务」徽标</div>
        )}
        {selectedTx && (
          <>
            <div className="sess-detail-head">
              <button className="btn btn-sm btn-ghost" onClick={() => { setSelectedTx(''); setTxDetail(null) }}>← 返回列表</button>
              <button className="btn btn-sm btn-ghost sess-copy" onClick={copyTx}>{copied.tx ? '已复制' : '复制文本'}</button>
              <button
                className="btn btn-sm btn-ghost sess-copy"
                disabled={!currentTx?.resume_cmd}
                title="复制命令：在终端里继续这个会话（cd 到工作目录后 omp --resume）"
                onClick={() => copyResume(currentTx)}
              >
                {copied[selectedTx] ? '已复制' : '复制终端命令'}
              </button>
              <div className="sess-detail-title">{txDetail ? (currentTx?.title || selectedTx) : selectedTx}</div>
              <div className="sess-detail-sub">
                {currentTx?.job_id
                  ? `定时任务 · ${currentTx.job_title || currentTx.job_id}`
                  : '对话'}
                {currentTx?.cwd ? ` · ${currentTx.cwd}` : ''}
                {` · ${selectedTx}`}
              </div>
              {STATUS_LABEL[currentTx?.status] && (
                <div className={`lib-status-row st-${currentTx.status}`}>
                  ● {STATUS_LABEL[currentTx.status]} · 自动刷新中
                </div>
              )}
            </div>
            <div className="t-flow">
              {txLoading && !txDetail && <div className="sess-empty">加载中…</div>}
              {txDetail && rendered.length === 0 && <div className="sess-empty">此会话没有可渲染的事件</div>}
              {txDetail && rendered}
              {sending && (
                <div className="sess-typing"><span className="sess-spin" aria-hidden />Harness 正在回复…</div>
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
                placeholder={sending ? 'Harness 正在回复…' : '输入消息继续对话（Enter 发送，Shift+Enter 换行）'}
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
                {modelCtrl.models.length > 0 && (
                  <select
                    className="sess-sel"
                    value={modelCtrl.current}
                    disabled={sending}
                    aria-label="切换模型"
                    title="切换模型（下次发送生效）"
                    onChange={(e) => switchModel(e.target.value)}
                  >
                    <option value="">默认模型</option>
                    {modelCtrl.current && !modelCtrl.models.includes(modelCtrl.current) && (
                      <option value={modelCtrl.current}>{modelCtrl.current}（当前）</option>
                    )}
                    {modelCtrl.models.map((m) => <option key={m} value={m}>{m}</option>)}
                  </select>
                )}
                {reasonCtrl.efforts.length > 0 && (
                  <select
                    className="sess-sel"
                    value={reasonCtrl.current}
                    disabled={sending}
                    aria-label="切换思考强度"
                    title="切换思考强度（下次发送生效）"
                    onChange={(e) => switchReasoning(e.target.value)}
                  >
                    <option value="">默认强度</option>
                    {reasonCtrl.current && !reasonCtrl.efforts.includes(reasonCtrl.current) && (
                      <option value={reasonCtrl.current}>{reasonCtrl.current}（当前）</option>
                    )}
                    {reasonCtrl.efforts.map((l) => <option key={l} value={l}>{l}</option>)}
                  </select>
                )}
                {sending && (
                  <button className="btn btn-sm btn-red" onClick={stopTurn} disabled={stopping}>
                    {stopping ? '停止中…' : '停止 ⏹'}
                  </button>
                )}
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

      {/* 项目配置弹窗：齿轮打开，不离开会话列表 */}
      {configProject && (
        <div className="prj-modal-mask" style={{ zIndex: 80 }} onClick={(e) => { if (e.target === e.currentTarget) setConfigProject('') }}>
          <div className="prj-modal prj-modal-tall">
            <ProjectConfigPanel
              project={configProject}
              onToast={onToast}
              onSaved={loadProjects}
              onClose={() => setConfigProject('')}
              onDeleted={() => { setConfigProject(''); loadProjects(); loadLibrary() }}
            />
          </div>
        </div>
      )}

      {newOpen && (
        <NewProjectDialog
          onClose={() => setNewOpen(false)}
          onToast={onToast}
          onCreated={() => { loadProjects(); loadLibrary() }}
        />
      )}
    </div>
  )
}
