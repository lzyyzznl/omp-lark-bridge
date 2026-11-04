import { useCallback, useEffect, useMemo, useState } from 'react'
import { api } from '../api.js'
import { useEsc } from '../hooks/useEsc.js'
import ProjectConfigPanel from './ProjectConfigPanel.jsx'
import NewProjectDialog from './NewProjectDialog.jsx'
import ConfirmDialog from './ConfirmDialog.jsx'

// 项目管理页：会话查看与 agent 编辑已迁到 ompweb，这里只保留定时任务的挂钩对象。
export default function ProjectsPage({ onToast }) {
  const [projects, setProjects] = useState(null)
  const [configProject, setConfigProject] = useState('')
  const [newOpen, setNewOpen] = useState(false)
  const [renaming, setRenaming] = useState('')
  const [renameVal, setRenameVal] = useState('')
  const [delProj, setDelProj] = useState('')
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    try {
      setProjects((await api.listProjects()).projects || [])
    } catch (e) {
      onToast?.('err', e.message)
      setProjects([])
    }
  }, [onToast])

  useEffect(() => { load() }, [load])
  useEsc(!!configProject, () => setConfigProject(''))

  const stats = useMemo(() => {
    const list = projects || []
    const platforms = new Set()
    for (const p of list) for (const x of p.platforms || []) platforms.add(x)
    return {
      total: list.length,
      running: list.filter((p) => p.running).length,
      platforms: platforms.size,
    }
  }, [projects])

  const rename = async (name) => {
    const val = renameVal.trim()
    setRenaming('')
    if (!val || val === (projects || []).find((p) => p.name === name)?.display_name) return
    setBusy(true)
    try {
      await api.patchProject(name, { display_name: val })
      onToast?.('ok', `项目已重命名为「${val}」`)
      await load()
    } catch (e) {
      onToast?.('err', e.message)
    } finally {
      setBusy(false)
    }
  }

  const remove = async () => {
    if (!delProj) return
    setBusy(true)
    try {
      await api.delProject(delProj)
      onToast?.('ok', '项目已删除')
      setDelProj('')
      await load()
    } catch (e) {
      onToast?.('err', e.message)
    } finally {
      setBusy(false)
    }
  }

  const label = (p) => p.display_name || p.name

  return (
    <div className="projects-page">
      <div className="projects-head">
        <div>
          <h2>项目</h2>
          <div className="hint-line">项目是定时任务的挂钩对象；会话查看与 agent 编辑请到 ompweb。</div>
        </div>
        <button className="btn btn-green" onClick={() => setNewOpen(true)}>+ 新建项目</button>
      </div>

      {projects !== null && projects.length > 0 && (
        <div className="stat-grid proj-stats">
          <div className="stat"><div className="label">项目总数</div><div className="value num">{stats.total}</div></div>
          <div className="stat ok"><div className="label">运行中</div><div className="value num">{stats.running}</div></div>
          <div className="stat"><div className="label">关联平台</div><div className="value num">{stats.platforms}</div></div>
        </div>
      )}

      {projects === null ? (
        <div className="sess-empty">加载中…</div>
      ) : projects.length === 0 ? (
        <div className="state-block">
          <div className="big">还没有项目</div>
          <div className="hint">新建项目后才能在它下面挂定时任务。</div>
          <div style={{ marginTop: 16 }}>
            <button className="btn btn-solid" onClick={() => setNewOpen(true)}>+ 新建项目</button>
          </div>
        </div>
      ) : (
        <div className="proj-grid">
          {projects.map((p) => (
            <article key={p.name} className="proj-card">
              <div className="proj-card-head">
                <div className="proj-title">
                  {renaming === p.name ? (
                    <input
                      className="proj-rename"
                      autoFocus
                      value={renameVal}
                      onChange={(e) => setRenameVal(e.target.value)}
                      onBlur={() => rename(p.name)}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter') rename(p.name)
                        if (e.key === 'Escape') setRenaming('')
                      }}
                    />
                  ) : (
                    <span className="proj-name" title={label(p)}>{label(p)}</span>
                  )}
                  <span className="proj-id mono">{p.name}</span>
                </div>
                <span className={`pill ${p.running ? 'pill-on' : 'pill-off'}`}>
                  {p.running ? '运行中' : '未启动'}
                </span>
              </div>

              <div className="proj-path mono" title={p.work_dir || ''}>
                {p.work_dir || '未设置工作目录'}
              </div>

              <div className="proj-meta">
                {(p.platforms || []).map((x) => (
                  <span key={x} className="chip">{x}</span>
                ))}
                {p.agent_type && <span className="chip chip-quiet">{p.agent_type}</span>}
                <span className="dim">{p.sessions_count || 0} 会话</span>
              </div>

              <div className="proj-actions">
                <button className="btn btn-sm btn-ghost" onClick={() => setConfigProject(p.name)}>设置</button>
                <button
                  className="btn btn-sm btn-ghost"
                  onClick={() => { setRenaming(p.name); setRenameVal(p.display_name || p.name) }}
                >重命名</button>
                <button className="btn btn-sm btn-ghost proj-del" disabled={busy} onClick={() => setDelProj(p.name)}>删除</button>
              </div>
            </article>
          ))}
        </div>
      )}

      {configProject && (
        <div className="prj-modal-mask" style={{ zIndex: 80 }} onClick={(e) => { if (e.target === e.currentTarget) setConfigProject('') }}>
          <div className="prj-modal prj-modal-tall">
            <ProjectConfigPanel
              project={configProject}
              onToast={onToast}
              onSaved={load}
              onClose={() => setConfigProject('')}
              onDeleted={() => { setConfigProject(''); load() }}
            />
          </div>
        </div>
      )}

      {newOpen && (
        <NewProjectDialog onClose={() => setNewOpen(false)} onToast={onToast} onCreated={load} />
      )}

      <ConfirmDialog
        open={!!delProj}
        title="删除项目"
        danger
        confirmLabel="删除"
        body={<>确定要删除项目 <b className="mono">{delProj}</b> 吗？</>}
        onConfirm={remove}
        onCancel={() => setDelProj('')}
      />
    </div>
  )
}
