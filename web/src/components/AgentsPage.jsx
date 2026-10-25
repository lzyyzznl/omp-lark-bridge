import { useEffect, useMemo, useState } from 'react'
import { api } from '../api.js'
import AgentDialog from './AgentDialog.jsx'
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
  const [toasts, setToasts] = useState([])

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
                <button className="btn btn-sm" onClick={() => viewAgent(a)}>{a.read_only ? '查看' : '查看'}</button>
                {!a.read_only && <button className="btn btn-sm" onClick={() => editAgent(a)}>编辑</button>}
                {!a.read_only && (
                  <button className="btn btn-sm btn-danger" onClick={() => setConfDel({ name: a.name })}>删除</button>
                )}
              </div>
            </div>
          ))}
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

      <Toast items={toasts} onDismiss={dismissToast} />
    </div>
  )
}
