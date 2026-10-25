import { useState } from 'react'
import { api } from '../api.js'

// 新建项目弹窗：名称（显示名）+ work_dir 浏览 + 飞书绑定方式。
// 项目 ID 由服务端生成；创建后服务自动重启使新项目上线。
export default function NewProjectDialog({ onClose, onToast, onCreated }) {
  const [name, setName] = useState('')
  const [workDir, setWorkDir] = useState('')
  const [bindMode, setBindMode] = useState('none') // none | manual | qr
  const [appID, setAppID] = useState('')
  const [secret, setSecret] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [fsOpen, setFsOpen] = useState(false)
  const [fsPath, setFsPath] = useState('')
  const [fsParent, setFsParent] = useState('')
  const [fsDirs, setFsDirs] = useState([])
  const [fsLoading, setFsLoading] = useState(false)
  const [fsError, setFsError] = useState('')

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
  const openFS = async () => {
    setFsOpen(true); setFsError(''); setFsPath(''); setFsParent(''); setFsDirs([])
    await browseFS(workDir || '')
  }

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

  const submit = async () => {
    const n = name.trim()
    if (!n) return
    setBusy(true)
    try {
      const opts = {}
      if (bindMode === 'manual') {
        if (appID.trim()) opts.app_id = appID.trim()
        if (secret.trim()) opts.app_secret = secret.trim()
      }
      const created = await api.createProject({
        name: n,
        type: 'feishu',
        agent_type: 'omp',
        work_dir: workDir.trim() || undefined,
        options: opts,
      })
      onToast?.('ok', `项目 ${n} 已创建，正在自动重启生效…`)
      const back = await waitServiceBack()
      onCreated?.(created.id)
      if (!back) {
        onToast?.('err', `项目 ${n} 已创建，但服务未在预期时间内恢复，请稍后刷新`)
      } else if (bindMode === 'qr') {
        onToast?.('ok', `项目 ${n} 已上线，在项目齿轮里点「扫码绑定」完成授权`)
      } else {
        onToast?.('ok', `项目 ${n} 已上线${bindMode === 'manual' ? '（已写入凭证）' : ''}`)
      }
      onClose()
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="prj-modal-mask" style={{ zIndex: 85 }} onClick={(e) => { if (e.target === e.currentTarget) onClose() }}>
      <div className="prj-modal">
        <div className="sess-list-head">
          <span>新建项目</span>
          <button className="btn btn-sm" onClick={onClose}>×</button>
        </div>
        {error && <div className="err-txt">{error}</div>}
        <input className="input" placeholder="项目名称（显示名，如 my-ops；ID 自动生成）" value={name} onChange={(e) => setName(e.target.value)} spellCheck={false} />
        <div style={{ display: 'flex', gap: 8 }}>
          <input className="input mono" placeholder="会话启动文件夹（work_dir，如 /srv/ops）" value={workDir} onChange={(e) => setWorkDir(e.target.value)} spellCheck={false} />
          <button className="btn btn-sm" onClick={openFS}>浏览…</button>
        </div>

        <div className="bind-row">
          <span className="bind-label">飞书机器人</span>
          <label><input type="radio" name="bindMode" checked={bindMode === 'none'} onChange={() => setBindMode('none')} /> 暂不绑定</label>
          <label><input type="radio" name="bindMode" checked={bindMode === 'manual'} onChange={() => setBindMode('manual')} /> 手动填写</label>
          <label><input type="radio" name="bindMode" checked={bindMode === 'qr'} onChange={() => setBindMode('qr')} /> 创建后扫码</label>
        </div>

        {bindMode === 'manual' && (
          <div className="bind-manual">
            <input className="input mono" placeholder="app_id" value={appID} onChange={(e) => setAppID(e.target.value)} spellCheck={false} />
            <input className="input mono" type="password" placeholder="app_secret" value={secret} onChange={(e) => setSecret(e.target.value)} spellCheck={false} />
          </div>
        )}

        <div className="prj-new-actions">
          <button className="btn btn-sm btn-green" onClick={submit} disabled={busy || !name.trim()}>{busy ? '创建中…' : '创建项目'}</button>
          <button className="btn btn-sm" onClick={onClose}>取消</button>
        </div>
        <div className="help">每个项目 = 独立 omp（独立 work_dir）+ 独立飞书应用。项目 ID 自动生成，名称仅用于显示、可随时修改。</div>

        {fsOpen && (
          <div className="prj-modal-mask" style={{ zIndex: 90 }} onClick={(e) => { if (e.target === e.currentTarget) setFsOpen(false) }}>
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
                <button className="btn btn-sm btn-green" onClick={() => { if (fsPath) setWorkDir(fsPath); setFsOpen(false) }}>选择此目录</button>
                <button className="btn btn-sm" onClick={() => setFsOpen(false)}>取消</button>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
