import { useEffect, useState } from 'react'
import { api } from '../api.js'

// 项目管理页：多飞书通道 → 不同工作目录。每个项目独立 omp agent（独立
// work_dir）与独立飞书应用；定时任务挂靠项目。改参数需要重启生效。
export default function ProjectsPage({ onToast }) {
  const [projects, setProjects] = useState(null)
  const [error, setError] = useState('')
  const [selected, setSelected] = useState(null)
  const [detail, setDetail] = useState(null)
  const [busy, setBusy] = useState(false)
  const [showNew, setShowNew] = useState(false)
  const [newName, setNewName] = useState('')
  const [newWorkDir, setNewWorkDir] = useState('')
  const [draft, setDraft] = useState(null) // 编辑草稿 {work_dir, model, admin_from, disabled_commands, allow_from}
  const [saved, setSaved] = useState(false) // 最近保存 → 提示重启

  const load = async () => {
    try {
      const d = await api.listProjects()
      setProjects(d.projects || [])
      setError('')
    } catch (e) {
      setError(e.message)
    }
  }

  useEffect(() => { load() }, [])

  const select = async (p) => {
    setSelected(p.name)
    setDetail(null)
    try {
      const d = await api.getProject(p.name)
      setDetail(d)
      setDraft({
        work_dir: d.work_dir || '',
        model: d.model || '',
        admin_from: d.settings?.admin_from || '',
        disabled_commands: (d.settings?.disabled_commands || []).join(', '),
        allow_from: (d.platform_configs || [])[0]?.allow_from || '',
      })
      setSaved(false)
      setError('')
    } catch (e) {
      setError(e.message)
    }
  }

  const create = async () => {
    const name = newName.trim()
    if (!name) return
    setBusy(true)
    try {
      await api.addPlatform(name, { type: 'feishu', agent_type: 'omp', work_dir: newWorkDir.trim() || undefined })
      setNewName(''); setNewWorkDir(''); setShowNew(false)
      await load()
      const list = await api.listProjects()
      const created = list.projects.find((x) => x.name === name)
      if (created) select(created)
      onToast?.('ok', `项目 ${name} 已创建（重启后飞书机器人上线）`)
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  const save = async () => {
    if (!selected || !draft) return
    setBusy(true)
    try {
      const fields = {}
      fields.work_dir = draft.work_dir
      fields.model = draft.model
      fields.admin_from = draft.admin_from
      fields.disabled_commands = draft.disabled_commands.split(',').map((s) => s.trim()).filter(Boolean)
      fields.platform_allow_from = { feishu: draft.allow_from }
      await api.patchProject(selected, fields)
      setSaved(true)
      onToast?.('ok', `项目 ${selected} 设置已保存（重启后生效）`)
      await select(projects.find((x) => x.name === selected))
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  const removeProj = async (name) => {
    if (!window.confirm(`确定从配置移除项目 ${name} 吗？`)) return
    setBusy(true)
    try {
      await api.delProject(name)
      if (selected === name) { setSelected(null); setDetail(null) }
      await load()
      onToast?.('ok', `项目 ${name} 已移除（重启后生效）`)
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="prj-page">
      <div className="sess-list prj-list">
        <div className="sess-list-head">
          <span>项目（最大颗粒度）</span>
          <button className="btn btn-sm btn-green" onClick={() => setShowNew(true)} disabled={busy}>+ 新建</button>
        </div>
        {error && <div className="err-txt" style={{ padding: 8 }}>{error}</div>}
        {projects === null && <div className="sess-empty">加载中…</div>}
        {projects !== null && projects.length === 0 && <div className="sess-empty">暂无项目</div>}
        {projects && projects.map((p) => (
          <div key={p.name} className={`sess-item ${selected === p.name ? 'sess-item-on' : ''}`} onClick={() => select(p)} data-prj={p.name}>
            <div className="sess-item-name">{p.name}</div>
            <div className="sess-item-sub">
              {p.agent_type} · {(p.platforms || []).join(', ') || '未绑平台'} · {p.sessions_count || 0} 会话
            </div>
          </div>
        ))}

        {showNew && (
          <div className="prj-new">
            <input className="input" placeholder="项目名（如 my-ops）" value={newName} onChange={(e) => setNewName(e.target.value)} />
            <input className="input mono" placeholder="会话启动文件夹（work_dir，如 /srv/ops）" value={newWorkDir} onChange={(e) => setNewWorkDir(e.target.value)} />
            <div className="prj-new-actions">
              <button className="btn btn-sm btn-green" onClick={create} disabled={busy || !newName.trim()}>创建</button>
              <button className="btn btn-sm" onClick={() => setShowNew(false)}>取消</button>
            </div>
            <div className="help">每个项目 = 独立 omp（独立 work_dir）+ 独立飞书应用。创建后：<code>in-one-chat feishu bind --project {newName || '&lt;name&gt;'} --app app_id:app_secret</code></div>
          </div>
        )}
      </div>

      <div className="sess-detail prj-detail">
        {!selected && <div className="sess-empty">← 选择项目查看 / 编辑</div>}
        {selected && !detail && <div className="sess-empty">加载中…</div>}
        {selected && detail && (
          <>
            <div className="sess-detail-head">
              <div className="sess-detail-title">{selected}</div>
              <div className="sess-detail-sub">
                {detail.agent_type} · {detail.work_dir || '默认目录'}
                {saved && <span className="prj-saved">保存成功，重启生效</span>}
              </div>
            </div>
            <div className="prj-form">
              <div className="field">
                <label>会话启动文件夹（work_dir）</label>
                <input className="input mono" value={draft?.work_dir || ''} onChange={(e) => setDraft({ ...draft, work_dir: e.target.value })} spellCheck={false} />
              </div>
              <div className="field">
                <label>默认模型</label>
                <input className="input mono" value={draft?.model || ''} onChange={(e) => setDraft({ ...draft, model: e.target.value })} placeholder="如 deepseek/deepseek-flash；留空=default" spellCheck={false} />
              </div>
              <div className="field">
                <label>管理员（admin_from）</label>
                <input className="input mono" value={draft?.admin_from || ''} onChange={(e) => setDraft({ ...draft, admin_from: e.target.value })} placeholder="user id 逗号分隔，或 *" spellCheck={false} />
              </div>
              <div className="field">
                <label>允许使用用户（allow_from，应用到 feishu 平台）</label>
                <input className="input mono" value={draft?.allow_from || ''} onChange={(e) => setDraft({ ...draft, allow_from: e.target.value })} spellCheck={false} />
              </div>
              <div className="field">
                <label>禁用命令（disabled_commands）</label>
                <input className="input mono" value={draft?.disabled_commands || ''} onChange={(e) => setDraft({ ...draft, disabled_commands: e.target.value })} placeholder="逗号分隔，如 restart, upgrade" spellCheck={false} />
              </div>
              <div className="prj-form-actions">
                <button className="btn btn-sm btn-solid" onClick={save} disabled={busy}>保存设置</button>
                <button className="btn btn-sm btn-red" onClick={() => removeProj(selected)} disabled={busy}>删除项目</button>
              </div>
              <div className="help">飞书绑定：CLI <code>in-one-chat feishu bind --project {selected} --app app_id:app_secret</code>，或编辑 config.toml 的 platforms.options。（本项目页不落凭证）</div>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
