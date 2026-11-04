import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api } from '../api.js'
import { formatTime } from '../lib/time.js'
import ProjectConfigPanel from './ProjectConfigPanel.jsx'
import NewProjectDialog from './NewProjectDialog.jsx'
import OmpTranscript from './OmpTranscript.jsx'
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


function formatBytes(n) {
  if (!n || n < 1024) return n ? `${n} B` : ''
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(2)} MB`
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
  // ompweb 入口地址（可达时才给出跳转）：会话属于 olb 自有 session-dir，
  // ompweb 看不到这些记录，但能按 cwd 打开同一工作区继续干活。
  const [ompwebUrl, setOmpwebUrl] = useState('')

  const flowRef = useRef(null)
  const [stickBottom, setStickBottom] = useState(true)
  const [configProject, setConfigProject] = useState('')  // 非空 → 打开配置弹窗
  const [newOpen, setNewOpen] = useState(false)
  // 左侧项目行的 ⋯ 菜单：设置 / 重命名（就地编辑）/ 删除（二次确认）
  const [projMenu, setProjMenu] = useState('')
  const [renaming, setRenaming] = useState('')
  const [renameVal, setRenameVal] = useState('')
  const [delProj, setDelProj] = useState('')
  const [projBusy, setProjBusy] = useState(false)
  const renameBusyRef = useRef(false)
  // 会话状态（后端从 transcript 尾部推断）：running/asking/done/idle
  useEsc(!!configProject, () => setConfigProject(''))

  // 点空白处收起 ⋯ 菜单
  useEffect(() => {
    if (!projMenu) return
    const close = () => setProjMenu('')
    document.addEventListener('click', close)
    return () => document.removeEventListener('click', close)
  }, [projMenu])

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
  // 探测 ompweb 是否在线（只探一次，失败静默：入口按钮保持禁用）。
  useEffect(() => {
    let alive = true
    api.ompWeb().then((st) => { if (alive && st?.reachable) setOmpwebUrl(st.url) }).catch(() => {})
    return () => { alive = false }
  }, [])

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

        setTxLoading(false)
      } catch (e) {
        if (alive) { setError(e.message); setTxLoading(false) }
      }
    }
    setTxLoading(true)
    fetchOne()
    const timer = setInterval(fetchOne, 2000)
    return () => { alive = false; clearInterval(timer) }
  }, [selectedTx])

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

  // 只读视图：历史消息即全部内容（网页不再续聊，续聊走 ompweb / 终端 resume）。
  // ompweb 用 jsonl 里的 session.id（uuid）定位会话；我们的列表 id 是文件名，
  // 所以从详情事件里取。取不到时退回 cwd 级跳转（同工作区、不同记录）。
  const ompSessionId = useMemo(
    () => (txDetail || []).find((e) => e.type === 'session')?.id || '',
    [txDetail],
  )

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

  // 就地重命名：Enter/失焦保存，Esc 取消。空值或未改动不发请求。
  const commitRename = async (p) => {
    if (renameBusyRef.current) return
    const val = renameVal.trim()
    setRenaming('')
    if (!val || val === (p.display_name || p.name)) return
    renameBusyRef.current = true
    try {
      await api.patchProject(p.name, { display_name: val })
      onToast?.('ok', `项目已重命名为「${val}」`)
      await loadProjects()
    } catch (e) {
      onToast?.('err', e.message)
    } finally {
      renameBusyRef.current = false
    }
  }

  // 弹窗与提示优先用显示名，显示名只是别名，删除请求仍按项目 ID
  const projLabel = (name) => projects.find((p) => p.name === name)?.display_name || name
  const doDeleteProj = async () => {
    if (!delProj) return
    setProjBusy(true)
    try {
      await api.delProject(delProj)
      onToast?.('ok', `项目「${projLabel(delProj)}」已删除`)
      setDelProj('')
      await loadProjects()
      await loadLibrary()
    } catch (e) {
      onToast?.('err', e.message)
    } finally {
      setProjBusy(false)
    }
  }

  // 项目子节点：名称 + ⋯ 菜单。同一 cwd 只有一个项目时内联到文件夹行，
  // 多个项目时按树形逐行列出（文件夹 = 根，项目 = 子节点）。
  const projectRow = (p) => (
    <div key={p.name} className="lib-project-row">
      {renaming === p.name ? (
        <input
          className="input lib-rename-input"
          value={renameVal}
          autoFocus
          onChange={(e) => setRenameVal(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') commitRename(p)
            if (e.key === 'Escape') setRenaming('')
          }}
          onBlur={() => commitRename(p)}
        />
      ) : (
        <span
          className={`lib-project-name ${p.running === false ? 'lib-project-off' : ''}`}
          title={p.display_name ? `${p.display_name}（${p.name}）` : p.name}
        >
          {p.display_name || p.name}
        </span>
      )}
      <div className="lib-proj-menu-wrap">
        <button
          className="lib-dots"
          title="项目操作"
          onClick={(e) => { e.stopPropagation(); setProjMenu(projMenu === p.name ? '' : p.name) }}
        >⋯</button>
        {projMenu === p.name && (
          <div className="lib-proj-menu" onClick={(e) => e.stopPropagation()}>
            <button onClick={() => { setProjMenu(''); setConfigProject(p.name) }}>设置</button>
            <button onClick={() => { setProjMenu(''); setRenaming(p.name); setRenameVal(p.display_name || p.name) }}>重命名</button>
            <button className="danger" onClick={() => { setProjMenu(''); setDelProj(p.name) }}>删除</button>
          </div>
        )}
      </div>
    </div>
  )

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
              <div className="sess-item-head">
                <div className="sess-item-name">
                  {f.cwd ? `📁 ${f.label.split('/').filter(Boolean).pop() || f.label}` : '未归类'}
                  {!f.cwd && <span className="bot-tag">无 cwd</span>}
                </div>
                {f.projects.length === 1 && <div className="lib-projects">{projectRow(f.projects[0])}</div>}
              </div>
              {f.projects.length > 1 && (
                <div className="lib-projects lib-projects-col">{f.projects.map(projectRow)}</div>
              )}
              <div className="sess-item-sub" title={f.cwd || ''}>{folderStats(f)}</div>
            </div>
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
              <button
                className="btn btn-sm btn-ghost sess-copy"
                disabled={!ompwebUrl}
                title="在 ompweb 打开这个会话继续对话（会话记录已同步；拿不到会话 id 时退回按工作区打开）"
                onClick={() => {
                  const q = ompSessionId
                    ? `session=${encodeURIComponent(ompSessionId)}`
                    : `cwd=${encodeURIComponent(currentTx?.cwd || '')}`
                  window.open(`${ompwebUrl}/?${q}`, '_blank', 'noopener')
                }}
              >
                在 ompweb 继续 ↗
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
            {/* 历史正文：解析与渲染复用 ompweb 的源码副本（src/omp），视觉与它一致。 */}
            <div className="t-flow t-flow-omp">
              <OmpTranscript
                id={selectedTx}
                live={currentTx?.status === 'running' || currentTx?.status === 'asking'}
              />
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

      {delProj && (
        <div className="prj-modal-mask" style={{ zIndex: 90 }} onClick={(e) => { if (e.target === e.currentTarget) setDelProj('') }}>
          <div className="prj-modal prj-confirm">
            <div className="sess-list-head">
              <span>删除项目</span>
              <button className="btn btn-sm" onClick={() => setDelProj('')}>×</button>
            </div>
            <div className="help">
              确定删除「{projLabel(delProj)}」？该项目的平台连接与配置会被移除，服务重启后生效；会话文件不会被删除。
            </div>
            <div className="prj-form-actions">
              <button className="btn btn-sm btn-red" onClick={doDeleteProj} disabled={projBusy}>
                {projBusy ? '删除中…' : '确认删除'}
              </button>
              <button className="btn btn-sm" onClick={() => setDelProj('')}>取消</button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
