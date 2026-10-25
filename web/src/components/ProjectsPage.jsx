import { useEffect, useState } from 'react'
import { api } from '../api.js'
import ProjectConfigPanel from './ProjectConfigPanel.jsx'

// 项目管理页：项目列表 + 右侧配置面板（面板同时被会话页的齿轮抽屉复用）。
export default function ProjectsPage({ onToast, initialProject }) {
  const [projects, setProjects] = useState(null)
  const [error, setError] = useState('')
  const [selected, setSelected] = useState(initialProject || null)
  const [busy, setBusy] = useState(false)
  const [showNew, setShowNew] = useState(false)
  const [newName, setNewName] = useState('')
  const [newWorkDir, setNewWorkDir] = useState('')
  const [bindMode, setBindMode] = useState('none') // none | manual | qr
  const [manualAppID, setManualAppID] = useState('')
  const [manualSecret, setManualSecret] = useState('')
  const [newBusy, setNewBusy] = useState(false)
  const [fsOpen, setFsOpen] = useState(false)
  const [fsPath, setFsPath] = useState('')
  const [fsParent, setFsParent] = useState('')
  const [fsDirs, setFsDirs] = useState([])
  const [fsLoading, setFsLoading] = useState(false)
  const [fsError, setFsError] = useState('')

  const load = async () => {
    try {
      setProjects((await api.listProjects()).projects)
      setError('')
    } catch (e) {
      setError(e.message)
    }
  }

  useEffect(() => { load() }, [])

  const waitServiceBack = async (timeoutMs = 20000) => {
    const t0 = Date.now()
    while (Date.now() - t0 < timeoutMs) {
      await new Promise((r) => setTimeout(r, 700))
      try {
        await api.listProjects()
        return true
      } catch { /* 重启窗口内继续等 */ }
    }
    return false
  }

  const browseFS = async (path) => {
    setFsLoading(true); setFsError('')
    try {
      const r = await api.listDirs(path)
      setFsPath(r.path); setFsParent(r.parent); setFsDirs(r.dirs || [])
    } catch (e) {
      setFsError(e.message)
    } finally {
      setFsLoading(false)
    }
  }
  const openNewFS = async () => {
    setFsOpen(true); setFsError(''); setFsPath(''); setFsParent(''); setFsDirs([])
    await browseFS(newWorkDir || '')
  }

  const openNew = () => {
    setNewName(''); setNewWorkDir(''); setBindMode('none')
    setManualAppID(''); setManualSecret(''); setNewBusy(false)
    setShowNew(true)
  }

  const submitNew = async () => {
    const name = newName.trim()
    if (!name) return
    setNewBusy(true)
    try {
      const opts = {}
      if (bindMode === 'manual') {
        if (manualAppID.trim()) opts.app_id = manualAppID.trim()
        if (manualSecret.trim()) opts.app_secret = manualSecret.trim()
      }
      const created = await api.createProject({
        name,
        type: 'feishu',
        agent_type: 'omp', // 当前仅支持 omp
        work_dir: newWorkDir.trim() || undefined,
        options: opts,
      })
      setShowNew(false)
      setSelected(created.id)
      onToast?.('ok', `项目 ${name} 已创建，正在自动重启生效…`)
      const back = await waitServiceBack()
      await load()
      if (!back) {
        onToast?.('err', `项目 ${name} 已创建，但服务未在预期时间内恢复，请稍后刷新`)
      } else if (bindMode === 'qr') {
        onToast?.('ok', `项目 ${name} 已上线，在右侧点「扫码绑定」完成机器人授权`)
      } else {
        onToast?.('ok', bindMode === 'manual'
          ? `项目 ${name} 已创建并写入凭证，机器人已上线`
          : `项目 ${name} 已创建，机器人已上线`)
      }
    } catch (e) {
      setError(e.message)
    } finally {
      setNewBusy(false)
    }
  }

  return (
    <div className="prj-page">
      <div className="sess-list prj-list">
        <div className="sess-list-head">
          <span>项目（最大颗粒度）</span>
          <button className="btn btn-sm btn-green" onClick={openNew} disabled={busy}>+ 新建</button>
        </div>
        {error && <div className="err-txt" style={{ padding: 8 }}>{error}</div>}
        {projects === null && <div className="sess-empty">加载中…</div>}
        {projects !== null && projects.length === 0 && <div className="sess-empty">暂无项目</div>}
        {projects && projects.map((p) => (
          <div key={p.name} className={`sess-item ${selected === p.name ? 'sess-item-on' : ''}`} onClick={() => setSelected(p.name)} data-prj={p.name}>
            <div className="sess-item-name">{p.display_name || p.name}{p.running === false && <span className="bot-tag">未运行</span>}</div>
            <div className="sess-item-sub">
              {p.agent_type || '—'} · {(p.platforms || []).join(', ') || '未绑平台'} · {p.sessions_count || 0} 会话
            </div>
          </div>
        ))}
      </div>

      <div className="sess-detail prj-detail">
        {!selected && <div className="sess-empty">← 选择项目查看 / 编辑</div>}
        {selected && (
          <ProjectConfigPanel
            project={selected}
            onToast={onToast}
            onSaved={load}
            onDeleted={() => { setSelected(null); load() }}
          />
        )}
      </div>

      {fsOpen && (
        <div className="prj-modal-mask" style={{ zIndex: 70 }} onClick={(e) => { if (e.target === e.currentTarget) setFsOpen(false) }}>
          <div className="prj-modal">
            <div className="sess-list-head">
              <span>选择文件夹（work_dir）</span>
              <button className="btn btn-sm" onClick={() => setFsOpen(false)}>×</button>
            </div>
            {fsError && <div className="err-txt">{fsError}</div>}
            <input className="input mono" value={fsPath} onChange={(e) => setFsPath(e.target.value)} onKeyDown={(e) => { if (e.key === 'Enter') browseFS(fsPath) }} placeholder="输入路径跳转（/ 开头）" spellCheck={false} />
            {fsLoading && <div className="sess-empty">加载中…</div>}
            <div className="fs-list">
              {fsParent && <div className="fs-item" onClick={() => browseFS(fsParent)}>📁 ..（上级）</div>}
              {fsDirs.map((d) => <div key={d} className="fs-item" onClick={() => browseFS(fsPath.endsWith('/') ? fsPath + d : fsPath + '/' + d)}>📁 {d}</div>)}
              {!fsLoading && fsDirs.length === 0 && <div className="sess-empty">无子目录</div>}
            </div>
            <div className="prj-new-actions">
              <button className="btn btn-sm btn-green" onClick={() => { if (fsPath) setNewWorkDir(fsPath); setFsOpen(false) }}>选择此目录</button>
              <button className="btn btn-sm" onClick={() => setFsOpen(false)}>取消</button>
            </div>
          </div>
        </div>
      )}

      {showNew && (
        <div className="prj-modal-mask" onClick={(e) => { if (e.target === e.currentTarget) setShowNew(false) }}>
          <div className="prj-modal">
            <div className="sess-list-head">
              <span>新建项目</span>
              <button className="btn btn-sm" onClick={() => setShowNew(false)}>×</button>
            </div>
            {error && <div className="err-txt">{error}</div>}
            <input className="input" placeholder="项目名称（显示名，如 my-ops；ID 自动生成）" value={newName} onChange={(e) => setNewName(e.target.value)} spellCheck={false} />
            <div style={{ display: 'flex', gap: 8 }}>
              <input className="input mono" placeholder="会话启动文件夹（work_dir，如 /srv/ops）" value={newWorkDir} onChange={(e) => setNewWorkDir(e.target.value)} spellCheck={false} />
              <button className="btn btn-sm" onClick={openNewFS}>浏览…</button>
            </div>

            <div className="bind-row">
              <span className="bind-label">飞书机器人</span>
              <label><input type="radio" name="bindMode" checked={bindMode === 'none'} onChange={() => setBindMode('none')} /> 暂不绑定</label>
              <label><input type="radio" name="bindMode" checked={bindMode === 'manual'} onChange={() => setBindMode('manual')} /> 手动填写</label>
              <label><input type="radio" name="bindMode" checked={bindMode === 'qr'} onChange={() => setBindMode('qr')} /> 创建后扫码</label>
            </div>

            {bindMode === 'manual' && (
              <div className="bind-manual">
                <input className="input mono" placeholder="app_id" value={manualAppID} onChange={(e) => setManualAppID(e.target.value)} spellCheck={false} />
                <input className="input mono" type="password" placeholder="app_secret" value={manualSecret} onChange={(e) => setManualSecret(e.target.value)} spellCheck={false} />
              </div>
            )}

            <div className="prj-new-actions">
              <button className="btn btn-sm btn-green" onClick={submitNew} disabled={newBusy || !newName.trim()}>{newBusy ? '创建中…' : '创建项目'}</button>
              <button className="btn btn-sm" onClick={() => setShowNew(false)}>取消</button>
            </div>
            <div className="help">每个项目 = 独立 omp（独立 work_dir）+ 独立飞书应用。项目 ID 自动生成，名称仅用于显示、可随时修改。</div>
          </div>
        </div>
      )}
    </div>
  )
}
