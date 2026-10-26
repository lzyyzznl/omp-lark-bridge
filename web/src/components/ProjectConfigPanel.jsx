import { useEffect, useRef, useState } from 'react'
import QRCode from 'qrcode'
import { api } from '../api.js'

// 项目配置面板：名称 / work_dir / 模型 / 凭证 / 权限 / 扫码换绑。
// 项目管理页与合并后的会话页（齿轮抽屉）共用这一份实现。
export default function ProjectConfigPanel({ project, onToast, onDeleted, onSaved, onClose }) {
  const [detail, setDetail] = useState(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [saved, setSaved] = useState(false)
  const [draft, setDraft] = useState(null)
  const [baseline, setBaseline] = useState(null) // 载入快照：保存只提交改动字段
  const [botDraft, setBotDraft] = useState({})
  const [qr, setQr] = useState(null)
  const [qrImg, setQrImg] = useState('')
  const [qrStatus, setQrStatus] = useState('')
  const cancelQR = useRef(null)
  const [models, setModels] = useState([])
  const [fsOpen, setFsOpen] = useState(false)
  const [fsPath, setFsPath] = useState('')
  const [fsParent, setFsParent] = useState('')
  const [fsDirs, setFsDirs] = useState([])
  const [fsLoading, setFsLoading] = useState(false)
  const [fsError, setFsError] = useState('')

  const initBotDraft = (d) => {
    const out = {}
    for (const pc of d.platform_configs || []) {
      if (pc.type !== 'feishu' && pc.type !== 'lark') continue
      out[pc.type] = { app_id: pc.app_id || '', app_secret: '' }
    }
    return out
  }

  const draftFrom = (d) => ({
    display_name: d.display_name || '',
    work_dir: d.work_dir || '',
    model: d.model || '',
    admin_from: d.settings?.admin_from || '',
    disabled_commands: (d.settings?.disabled_commands || []).join(', '),
    allow_from: (d.platform_configs || [])[0]?.allow_from || '',
  })

  const load = async (name) => {
    const id = name || project
    if (!id) return
    try {
      const d = await api.getProject(id)
      const dd = draftFrom(d)
      setDetail(d)
      setDraft(dd)
      setBaseline(dd)
      setBotDraft(initBotDraft(d))
      setSaved(false)
      setQr(null); setQrImg(''); setQrStatus('')
      setError('')
      try {
        const mm = await api.getModels(id)
        setModels((mm && mm.models) || [])
      } catch {
        setModels([])
      }
    } catch (e) {
      setError(e.message)
    }
  }

  useEffect(() => {
    setDetail(null); setDraft(null); setBaseline(null)
    if (project) load(project)
  }, [project]) // eslint-disable-line react-hooks/exhaustive-deps

  // 配置变更会触发服务自动重启：轮询直到 API 恢复。
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
  const openFS = async () => {
    setFsOpen(true); setFsError(''); setFsPath(''); setFsParent(''); setFsDirs([])
    await browseFS(draft?.work_dir || '')
  }
  const chooseFS = () => {
    if (fsPath && draft) setDraft({ ...draft, work_dir: fsPath })
    setFsOpen(false)
  }

  const save = async () => {
    if (!project || !draft) return
    setBusy(true)
    try {
      // 只提交实际改动过的字段：整表单提交会把别人刚改的值覆盖回去。
      const fields = {}
      const base = baseline || {}
      const execCmds = draft.disabled_commands.split(',').map((s) => s.trim()).filter(Boolean)
      const baseCmds = (base.disabled_commands || '').split(',').map((s) => s.trim()).filter(Boolean)
      if ((draft.display_name || '').trim() !== (base.display_name || '')) fields.display_name = (draft.display_name || '').trim()
      if ((draft.work_dir || '').trim() !== (base.work_dir || '')) fields.work_dir = draft.work_dir
      if ((draft.model || '').trim() !== (base.model || '')) fields.model = draft.model
      if ((draft.admin_from || '').trim() !== (base.admin_from || '')) fields.admin_from = draft.admin_from
      if (JSON.stringify(execCmds) !== JSON.stringify(baseCmds)) fields.disabled_commands = execCmds
      if ((draft.allow_from || '') !== (base.allow_from || '')) fields.platform_allow_from = { feishu: draft.allow_from }
      if (Object.keys(fields).length === 0) {
        onToast?.('ok', '没有改动')
        return
      }
      const resp = await api.patchProject(project, fields)
      setSaved(true)
      if (resp?.restart_required) {
        onToast?.('ok', '设置已保存，正在自动重启生效…')
        const back = await waitServiceBack()
        await load(project)
        onToast?.(back ? 'ok' : 'err', back ? '设置已生效' : '服务未在预期时间内恢复，请稍后刷新')
      } else {
        await load(project)
        onToast?.('ok', `项目设置已保存${resp?.hot_swapped ? '（已热换生效）' : ''}`)
      }
      onSaved?.()
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  // 手动填写 app_id/app_secret → 保存并热换
  const saveBot = async (type) => {
    const b = botDraft[type] || {}
    const appID = (b.app_id || '').trim()
    const secret = (b.app_secret || '').trim()
    if (!appID) { setError('app_id 必填'); return }
    if (!secret) { setError('app_secret 必填（留空无法触发保存，避免覆盖现有 secret）'); return }
    setBusy(true)
    try {
      const resp = await api.patchProject(project, {
        platform_app_id: { [type]: appID },
        platform_app_secret: { [type]: secret },
      })
      setSaved(true)
      if (resp?.restart_required) {
        onToast?.('ok', `平台 ${type} 已保存，正在自动重启生效…`)
        await waitServiceBack()
      } else {
        onToast?.('ok', `平台 ${type} 已保存${resp?.hot_swapped ? '，已热换生效' : ''}`)
      }
      await load(project)
      onSaved?.()
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  const stopQR = () => {
    if (cancelQR.current) cancelQR.current()
    cancelQR.current = null
  }

  const startQR = async (type, curAppId) => {
    stopQR()
    setQrStatus('loading')
    try {
      const r = await api.beginBotQR(project, { platform: type, app_id: curAppId || undefined })
      const img = await QRCode.toDataURL(r.qr_url, { width: 220, margin: 1 })
      setQrImg(img)
      setQr({ platform: type, device_code: r.device_code, interval: r.interval || 5, expires_in: r.expires_in || 300 })
      pollQR(r, type)
    } catch (e) {
      setQrStatus(`error: ${e.message}`)
    }
  }

  const pollQR = (r, type) => {
    const interval = r.interval || 5
    const deadline = Date.now() + (r.expires_in || 300) * 1000
    let cancelled = false
    cancelQR.current = () => { cancelled = true }
    setQrStatus('pending')
    const step = async () => {
      if (cancelled) return
      if (Date.now() > deadline) { setQrStatus('expired'); return }
      try {
        const pr = await api.pollBotQR(project, { device_code: r.device_code, platform: type })
        if (cancelled) return
        if (pr.status === 'pending') { setTimeout(step, Math.max(interval, 1) * 1000); return }
        if (pr.status === 'ok') {
          setQrStatus('ok')
          onToast?.('ok', `扫码成功：${type} 已绑定 ${pr.app_id}${pr.hot_swapped ? '，已热换生效' : ''}`)
          if (pr.restart_required) await waitServiceBack()
          await load(project)
          setQr(null); setQrImg(''); setQrStatus('')
          onSaved?.()
        } else if (pr.status === 'expired' || pr.status === 'expired_token') {
          setQrStatus('expired')
        } else if (pr.status === 'denied' || pr.status === 'access_denied') {
          setQrStatus('denied')
        } else {
          setQrStatus(`error: ${pr.status || 'unknown'}`)
        }
      } catch (e) {
        if (!cancelled) setQrStatus(`error: ${e.message}`)
      }
    }
    setTimeout(step, Math.max(interval, 1) * 1000)
  }
  useEffect(() => () => stopQR(), [])

  const removeProj = async () => {
    if (!window.confirm(`确定从配置移除项目 ${project} 吗？`)) return
    setBusy(true)
    try {
      await api.delProject(project)
      onToast?.('ok', `项目 ${project} 已移除`)
      onDeleted?.()
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  const botPlats = (detail?.platforms && detail.platforms.length
    ? detail.platforms.filter((p) => p.type === 'feishu' || p.type === 'lark')
    : (detail?.platform_configs || []).filter((p) => p.type === 'feishu' || p.type === 'lark'))

  const qrStatusText = () => {
    if (qrStatus === 'loading') return '正在生成…'
    if (qrStatus === 'pending') return '等待扫码授权（用飞书 App 扫二维码）…'
    if (qrStatus === 'ok') return '✔ 绑定成功，已热换或重启后生效'
    if (qrStatus === 'expired' || qrStatus === 'expired_token') return '✘ 二维码已过期，请重新生成'
    if (qrStatus === 'denied' || qrStatus === 'access_denied') return '✘ 已拒绝授权'
    if (qrStatus.startsWith('error')) return qrStatus
    return ''
  }

  if (!project) return <div className="sess-empty">← 选择一个项目</div>
  if (!detail) {
    return (
      <div className="prj-form">
        {error ? <div className="err-txt">{error}</div> : <div className="sess-empty">加载中…</div>}
      </div>
    )
  }

  return (
    <div className="prj-form">
      {onClose && (
        <div className="prj-drawer-head">
          <span>{draft?.display_name || project}</span>
          <button className="btn btn-sm" onClick={onClose}>×</button>
        </div>
      )}
      {error && <div className="err-txt">{error}</div>}
      {detail.running === false && (
        <div className="prj-note">项目未运行：配置已写入，服务重启后启动该通道。</div>
      )}
      <div className="field">
        <label>项目名称</label>
        <input
          className="input mono"
          value={draft?.display_name || ''}
          onChange={(e) => setDraft({ ...draft, display_name: e.target.value })}
          spellCheck={false}
          placeholder={project}
        />
        <div className="help">仅影响显示，保存后立即生效；内部 ID <code>{project}</code> 不变，定时任务与会话不受影响。留空则显示 ID。</div>
      </div>
      <div className="field">
        <label>会话启动文件夹（work_dir）</label>
        <div className="bot-row">
          <input className="input mono" value={draft?.work_dir || ''} onChange={(e) => setDraft({ ...draft, work_dir: e.target.value })} spellCheck={false} />
          <button className="btn btn-sm" onClick={openFS} disabled={busy}>浏览…</button>
        </div>
      </div>
      <div className="field">
        <label>默认模型</label>
        {models.length > 0 ? (
          <select className="input" value={draft?.model || ''} onChange={(e) => setDraft({ ...draft, model: e.target.value })}>
            <option value="">默认（default）</option>
            {draft?.model && !models.includes(draft.model) && <option value={draft.model}>{draft.model}（当前）</option>}
            {models.map((m) => <option key={m} value={m}>{m}</option>)}
          </select>
        ) : (
          <input className="input mono" value={draft?.model || ''} onChange={(e) => setDraft({ ...draft, model: e.target.value })} placeholder="如 deepseek/deepseek-flash；留空=default" spellCheck={false} />
        )}
      </div>

      {botPlats.map((p) => {
        const bd = botDraft[p.type] || { app_id: '', app_secret: '' }
        return (
          <div className="field" key={p.type}>
            <label>
              {p.type} 平台机器人{p.connected ? <span className="bot-tag bot-ok">已连接</span> : <span className="bot-tag">未连接</span>}
            </label>
            <div className="bot-meta mono">
              {p.bot_name || '（bot 未识别）'}
              {bd.app_id ? ` · app_id: ${bd.app_id}` : ''}
              {p.bot_open_id ? ` · bot_open_id: ${p.bot_open_id}` : ''}
            </div>
            <div className="bot-row">
              <div className="bot-col">
                <input className="input mono" placeholder="app_id（新应用 ID）" value={bd.app_id} onChange={(e) => setBotDraft({ ...botDraft, [p.type]: { ...bd, app_id: e.target.value } })} spellCheck={false} />
                <input className="input mono" type="password" placeholder="app_secret（不回显，必填以触发保存）" value={bd.app_secret} onChange={(e) => setBotDraft({ ...botDraft, [p.type]: { ...bd, app_secret: e.target.value } })} spellCheck={false} />
              </div>
              <div className="bot-col bot-actions">
                <button className="btn btn-sm btn-solid" onClick={() => saveBot(p.type)} disabled={busy || !!qr}>保存并换绑</button>
                <button className="btn btn-sm btn-green" onClick={() => startQR(p.type, bd.app_id)} disabled={busy || !!qr}>扫码绑定{bd.app_id ? '/换绑' : ''}</button>
              </div>
            </div>

            {qr && qr.platform === p.type && (
              <div className="qr-box">
                {qrImg ? <img src={qrImg} alt="扫码绑定二维码" className="qr-img" /> : <div className="sess-empty">生成二维码…</div>}
                <div className="qr-status mono">{qrStatusText()}</div>
                <div className="prj-new-actions">
                  <button className="btn btn-sm" onClick={() => { stopQR(); setQr(null); setQrImg(''); setQrStatus('') }}>取消</button>
                  <button className="btn btn-sm" onClick={() => startQR(p.type, bd.app_id)}>重新生成</button>
                </div>
              </div>
            )}
          </div>
        )
      })}
      <div className="field">
        <label>管理员（admin_from）</label>
        <input className="input mono" value={draft?.admin_from || ''} onChange={(e) => setDraft({ ...draft, admin_from: e.target.value })} placeholder="* = 不限制特权命令；user id 逗号分隔；留空 = 禁用特权命令" spellCheck={false} />
      </div>
      <div className="field">
        <label>允许使用用户（allow_from，应用到 feishu 平台）</label>
        <input className="input mono" value={draft?.allow_from || ''} onChange={(e) => setDraft({ ...draft, allow_from: e.target.value })} placeholder="留空=所有用户可用；或 user id 逗号分隔" spellCheck={false} />
      </div>
      <div className="field">
        <label>禁用命令（disabled_commands）</label>
        <input className="input mono" value={draft?.disabled_commands || ''} onChange={(e) => setDraft({ ...draft, disabled_commands: e.target.value })} placeholder="逗号分隔，如 restart, upgrade" spellCheck={false} />
      </div>
      <div className="prj-form-actions">
        <button className="btn btn-sm btn-solid" onClick={save} disabled={busy}>保存设置</button>
        <button className="btn btn-sm btn-red" onClick={removeProj} disabled={busy}>删除项目</button>
        {saved && <span className="prj-saved">已保存</span>}
      </div>
      <div className="help">凭证：app_secret 永不出现在本页；保存会立即热换（新 bot 生效无需重启）。也可 CLI <code>omp-lark-bridge feishu bind --project {project} --app app_id:app_secret</code>。换 bot 后飞书会话 key 会变，历史会话不可达。</div>

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
              {fsDirs.map((d) => (
                <div key={d.path} className="fs-item" onClick={() => browseFS(d.path)}>📁 {d.name}</div>
              ))}
              {!fsLoading && fsDirs.length === 0 && <div className="sess-empty">无子目录</div>}
            </div>
            <div className="prj-new-actions">
              <button className="btn btn-sm btn-green" onClick={chooseFS}>选择此目录</button>
              <button className="btn btn-sm" onClick={() => setFsOpen(false)}>取消</button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
