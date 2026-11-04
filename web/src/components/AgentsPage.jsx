import { useEffect, useMemo, useState } from 'react'
import { api } from '../api.js'
import AgentDialog from './AgentDialog.jsx'
import ExpertRunDialog from './ExpertRunDialog.jsx'
import ConfirmDialog from './ConfirmDialog.jsx'
import Toast from './Toast.jsx'

const SCOPE_LABEL = { bundled: '内置', user: '自定义', project: '项目' }
const SCOPE_CLASS = { bundled: 'badge badge-muted', user: 'badge badge-ok', project: 'badge badge-warn' }

// omp agent persona 管理页：
// 列出内置 + 自定义 + 项目三档 agent，支持创建 / 编辑 / 删除自定义（user scope）。
export default function AgentsPage({ onToast }) {
  const [agents, setAgents] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [dialog, setDialog] = useState(null) // {mode:'new'} | {mode:'view'|'edit', agent}
  const [confDel, setConfDel] = useState(null) // agent 待删除
  const [saving, setSaving] = useState(false)
  const [copyFrom, setCopyFrom] = useState(null) // 待复制的源 agent
  const [copyName, setCopyName] = useState('')
  const [toasts, setToasts] = useState([])
  const [runFor, setRunFor] = useState(null) // 待执行 agent（ExpertRunDialog）
  const [hub, setHub] = useState(null) // {devices:[...]} | null（null = 未启用 hub）
  const [hubLoading, setHubLoading] = useState(false)
  const [hubFwd, setHubFwd] = useState(null) // {device, agent, msg, busy, op, result}

  const push = (msg) => setToasts((ts) => [...ts, { key: Math.random(), kind: 'ok', text: msg }])
  const dismissToast = (key) => setToasts((ts) => ts.filter((x) => x.key !== key))


  const load = async () => {
    setLoading(true)
    try {
      const data = await api.listAgents()
      setAgents(data?.agents || [])
      setError('')
    } catch (e) {
      setError(e.message || '加载 agents 失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  // hub 全网视图：未启用 hub（端点 404）时 api.hubOverview() 返回 null → 隐藏该区。
  useEffect(() => {
    (async () => {
      setHubLoading(true)
      try {
        setHub(await api.hubOverview())
      } catch {
        setHub(null)
      } finally {
        setHubLoading(false)
      }
    })()
  }, [])

  // 把「用专家执行」转发给远端 hub 设备，并轮询其结果回执。
  const startHubRun = async (device, agent, msg) => {
    if (!msg.trim() || !agent) return
    const mark = (p) => setHubFwd((f) => ({ ...f, device, agent, msg, ...p }))
    mark({ busy: true, op: null, result: null, error: '' })
    try {
      const { queued } = await api.hubForwardRun(device.token, { agent, message: msg })
      mark({ busy: false, op: queued })
      pollHubResult(queued)
    } catch (e) {
      mark({ busy: false, error: e.message || '转发失败' })
    }
  }
  const pollHubResult = (op, tries = 0) => {
    setTimeout(async () => {
      try {
        const d = await api.hubResult(op)
        if (d?.result) {
          setHubFwd((f) => (f?.op === op ? { ...f, result: d.result } : f))
          return
        }
        if (tries < 60) pollHubResult(op, tries + 1)
      } catch {
        if (tries < 60) pollHubResult(op, tries + 1)
      }
    }, 2000)
  }

  const stats = useMemo(() => {
    const s = { total: agents.length, custom: 0, bundled: 0 }
    for (const a of agents) {
      if (a.scope === 'user') s.custom++
      if (a.scope === 'bundled') s.bundled++
    }
    return s
  }, [agents])

  const handleSave = async (fn, okMsg) => {
    setSaving(true)
    try {
      await fn()
      setDialog(null)
      push(okMsg)
      await load()
      onToast?.(okMsg)
    } catch (e) {
      push(e.message || '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const confirmDelete = async () => {
    if (!confDel) return
    setSaving(true)
    try {
      await api.delAgent(confDel.name)
      setConfDel(null)
      push(`agent ${confDel.name} 已删除`)
      await load()
    } catch (e) {
      push(e.message || '删除失败')
    } finally {
      setSaving(false)
    }
  }

  const viewAgent = (a) => setDialog({ mode: 'view', agent: a })
  const editAgent = (a) => setDialog({ mode: 'edit', agent: a })

  const openCopy = (a) => { setCopyFrom(a); setCopyName(`${a.name}-copy`) }
  const confirmCopy = async () => {
    if (!copyFrom) return
    const n = copyName.trim()
    if (!/^[A-Za-z0-9_-]{1,64}$/.test(n)) {
      push('名称只能含字母、数字、-、_（1–64 字符）')
      return
    }
    setSaving(true)
    try {
      await api.copyAgent(copyFrom.name, n)
      setCopyFrom(null)
      push(`已复制为自定义 agent ${n}`)
      await load()
    } catch (e) {
      push(e.message || '复制失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="shell">
      <div className="stat-grid">
        <div className="stat"><div className="label">全部 agent</div><div className="value num">{loading ? '…' : stats.total}</div></div>
        <div className="stat ok"><div className="label">自定义（user）</div><div className="value num">{loading ? '…' : stats.custom}</div></div>
        <div className="stat"><div className="label">内置（bundled）</div><div className="value num">{loading ? '…' : stats.bundled}</div></div>
        <div className="stat"><div className="label">项目 agent</div><div className="value num">{loading ? '…' : agents.filter((a) => a.scope === 'project').length}</div></div>
      </div>

      <div className="section-title">
        omp 专用子代理（agent personas）
        <button className="btn btn-sm btn-green" style={{ marginLeft: 12 }} onClick={() => setDialog({ mode: 'new' })}>新建 agent</button>
      </div>

      {error && <div className="state-block"><div className="big">加载失败</div><div className="hint">{error}</div></div>}

      {loading ? (
        <div>
          <div className="skeleton-line" style={{ marginBottom: 10 }} />
          <div className="skeleton-line" style={{ marginBottom: 10 }} />
          <div className="skeleton-line" style={{ marginBottom: 10 }} />
        </div>
      ) : (
        <div className="agent-grid">
          {agents.length === 0 && (
            <div className="state-block">
              <div className="big">还没有可用 agent</div>
              <div className="hint">点击右上角「新建 agent」创建一个自定义专用子代理，即可在定时任务里指派它执行。</div>
            </div>
          )}
          {agents.map((a) => (
            <div key={`${a.scope}:${a.name}`} className="agent-card">
              <div className="agent-card-head">
                <span className="mono agent-name">{a.name}</span>
                <span className={SCOPE_CLASS[a.scope] || 'badge'}>{SCOPE_LABEL[a.scope] || a.scope}</span>
              </div>
              {a.description ? (
                <div className="agent-desc">{a.description}</div>
              ) : (
                <div className="help">（无描述）</div>
              )}
              <div className="agent-meta mono">
                {a.model ? <span>{a.model}</span> : <span className="help">模型：继承</span>}
                {a.thinking_level ? <span className="help">思考 {a.thinking_level}</span> : null}
                {a.spawns ? <span className="help">派生 {a.spawns}</span> : null}
                {a.project ? <span className="help">@{a.project}</span> : null}
              </div>
              <div className="agent-card-actions">
                <button className="btn btn-sm" onClick={() => viewAgent(a)}>查看</button>
                <button className="btn btn-sm btn-green" onClick={() => setRunFor(a)} title="用该 agent 新建独立会话执行">执行</button>
                <button className="btn btn-sm" onClick={() => openCopy(a)} title="复制为新的自定义 agent">复制</button>
                {!a.read_only && <button className="btn btn-sm" onClick={() => editAgent(a)}>编辑</button>}
                {!a.read_only && (
                  <button className="btn btn-sm btn-danger" onClick={() => setConfDel({ name: a.name })}>删除</button>
                )}
              </div>
            </div>
          ))}
        </div>
      )}

      {hub && (
        <div style={{ marginTop: 28 }}>
          <div className="section-title">
            hub 全网设备
            {hubLoading && <span className="help" style={{ marginLeft: 8 }}>加载中…</span>}
          </div>
          {hub.devices.length === 0 ? (
            <div className="state-block">
              <div className="big">暂无注册设备</div>
              <div className="hint">其他 olb 实例以 client 模式向本 hub 注册后，会在下方列出，可把「用专家执行」转发过去。</div>
            </div>
          ) : (
            <div className="agent-grid">
              {hub.devices.map((d) => (
                <div key={d.token} className="agent-card">
                  <div className="agent-card-head">
                    <span className="agent-name">{d.name}</span>
                    <span className="badge badge-ok">在线</span>
                  </div>
                  <div className="agent-meta mono">
                    <span>{d.token}</span>
                    <span className="help">{d.agents?.length || 0} agents · {d.sessions?.length || 0} 会话</span>
                  </div>
                  {hubFwd?.device?.token === d.token ? (
                    <div className="hub-fwd" style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 8 }}>
                      <select className="input mono" value={hubFwd.agent}
                        onChange={(e) => setHubFwd((f) => ({ ...f, agent: e.target.value }))}>
                        {(d.agents && d.agents.length ? d.agents : [{ name: 'scout' }]).map((a) => (
                          <option key={a.name} value={a.name}>{a.name}</option>
                        ))}
                      </select>
                      <input className="input" value={hubFwd.msg}
                        onChange={(e) => setHubFwd((f) => ({ ...f, msg: e.target.value }))}
                        placeholder="要转发执行的指令" />
                      <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                        {hubFwd.op ? (
                          <span className="help mono">{hubFwd.result ? (hubFwd.result.ok ? '已执行完成' : '执行失败') : `已转发 ${hubFwd.op}，等待远端执行…`}</span>
                        ) : (
                          <button className="btn btn-sm btn-green" disabled={hubFwd.busy || !hubFwd.msg.trim() || !hubFwd.agent}
                            onClick={() => startHubRun(d, hubFwd.agent, hubFwd.msg)}>{hubFwd.busy ? '转发中…' : '转发执行'}</button>
                        )}
                        <button className="btn btn-sm" onClick={() => setHubFwd(null)}>收起</button>
                      </div>
                      {hubFwd.result && (
                        <div className="agent-desc" style={{ whiteSpace: 'pre-wrap' }}>{(hubFwd.result.message || hubFwd.result.error || '').slice(0, 500)}</div>
                      )}
                      {hubFwd.error && <div className="help" style={{ color: 'var(--error)' }}>{hubFwd.error}</div>}
                    </div>
                  ) : (
                    <div className="agent-card-actions">
                      <button className="btn btn-sm" title="转发专家执行到该设备"
                        onClick={() => setHubFwd({ device: d, agent: d.agents?.[0]?.name || '', msg: '', busy: false, op: null, result: null, error: '' })}>执行</button>
                    </div>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      <AgentDialog
        open={!!dialog}
        mode={dialog?.mode}
        agent={dialog?.mode === 'new' ? null : dialog?.agent}
        onClose={() => { if (!saving) setDialog(null) }}
        onSave={handleSave}
        onDelete={(name) => { setDialog(null); setConfDel({ name }) }}
        saving={saving}
      />

      <ConfirmDialog
        open={!!confDel}
        title="删除 agent"
        danger
        confirmLabel="删除"
        body={confDel ? <>确定要删除自定义 agent <b className="mono">{confDel.name}</b> 吗？<br />对应文件 <code className="mono">~/.omp/agent/agents/{confDel.name}.md</code> 会被移除。</> : null}
        onConfirm={confirmDelete}
        onCancel={() => setConfDel(null)}
      />

      <ConfirmDialog
        open={!!copyFrom}
        title="复制为自定义 agent"
        confirmLabel="复制"
        body={copyFrom ? (
          <div className="field" style={{ marginBottom: 0 }}>
            <label htmlFor="cp-name">新 agent 名称</label>
            <input id="cp-name" className="input mono" value={copyName} autoFocus
              onChange={(e) => setCopyName(e.target.value)}
              placeholder="如 release-auditor（仅字母数字 - _ ，≤64）" spellCheck={false} />
            <div className="help">从 <b className="mono">{copyFrom.name}</b> 复制一份完整人格（含 frontmatter 与指令正文）到一个新的自定义 agent。</div>
          </div>
        ) : null}
        onConfirm={confirmCopy}
        onCancel={() => setCopyFrom(null)}
      />

      <ExpertRunDialog
        open={!!runFor}
        agent={runFor}
        onClose={() => { if (!saving) setRunFor(null) }}
      />

      <Toast items={toasts} onDismiss={dismissToast} />
    </div>
  )
}
