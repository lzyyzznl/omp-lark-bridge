import { useEffect, useRef, useState } from 'react'
import QRCode from 'qrcode'
import { api } from '../api.js'

// 项目管理页：多飞书通道 → 不同工作目录。每个项目独立 omp agent（独立
// work_dir）与独立飞书应用；定时任务挂靠项目。新建项目走弹窗，可手填飞书
// app_id/app_secret 或直接扫码绑定；平台参数保存后支持进程内热换。
export default function ProjectsPage({ onToast }) {
  const [projects, setProjects] = useState(null)
  const [error, setError] = useState('')
  const [selected, setSelected] = useState(null)
  const [detail, setDetail] = useState(null)
  const [busy, setBusy] = useState(false)
  const [showNew, setShowNew] = useState(false)
  const [newName, setNewName] = useState('')
  const [newWorkDir, setNewWorkDir] = useState('')
  const [newAgentType, setNewAgentType] = useState('')
  const [bindMode, setBindMode] = useState('none') // none | manual | qr
  const [manualAppID, setManualAppID] = useState('')
  const [manualSecret, setManualSecret] = useState('')
  const [newBusy, setNewBusy] = useState(false)
  const [qrModal, setQrModal] = useState(false) // 二维码显示在新建弹窗内
  const [draft, setDraft] = useState(null) // 编辑草稿 {work_dir, model, admin_from, disabled_commands, allow_from}
  const [saved, setSaved] = useState(false) // 最近保存 → 提示
  const [botDraft, setBotDraft] = useState({}) // { [platformType]: {app_id, app_secret} }；secret 仅输入用，不回显
  const [qr, setQr] = useState(null) // {platform, device_code, interval, expires_in}
  const [qrImg, setQrImg] = useState('')
  const [qrStatus, setQrStatus] = useState('') // '' | loading | pending | ok | expired | denied | error:…
  const cancelQR = useRef(null)
  const [models, setModels] = useState([]) // omp 模型列表（下拉）
  const [fsOpen, setFsOpen] = useState(false) // work_dir 目录选择弹窗
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

  const load = async () => {
    try {
      setProjects((await api.listProjects()).projects)
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
      setBotDraft(initBotDraft(d))
      setSaved(false)
      setQr(null); setQrImg(''); setQrStatus('')
      setError('')
      // 模型下拉：omp model 列表（未运行/不支持时留空，退回手填）
      try {
        const mm = await api.getModels(p.name)
        setModels((mm && mm.models) || [])
      } catch {
        setModels([])
      }
    } catch (e) {
      setError(e.message)
    }
  }

  const openFS = async () => {
    setFsOpen(true); setFsError('')
    setFsPath(''); setFsParent(''); setFsDirs([])
    await browseFS(draft?.work_dir || '')
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

  const chooseFS = () => {
    if (fsPath && draft) setDraft({ ...draft, work_dir: fsPath })
    setFsOpen(false)
  }
  const openNew = () => {
    stopQR()
    setNewName(''); setNewWorkDir(''); setNewAgentType(''); setBindMode('none')
    setManualAppID(''); setManualSecret(''); setNewBusy(false)
    setQrModal(false); setQr(null); setQrImg(''); setQrStatus('')
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
      await api.addPlatform(name, {
        type: 'feishu',
        agent_type: newAgentType.trim() || 'omp',
        work_dir: newWorkDir.trim() || undefined,
        options: opts,
      })
      setShowNew(false)
      await load()
      const created = (await api.listProjects()).projects.find((x) => x.name === name)
      if (created) await select(created)
      if (bindMode === 'qr') {
        setQrModal(true)
        onToast?.('ok', `项目 ${name} 已创建，请用飞书 App 扫下面的二维码绑定机器人`)
        startQR('feishu', '', true)
      } else {
        onToast?.('ok', bindMode === 'manual'
          ? `项目 ${name} 已创建并写入凭证（重启后机器人上线）`
          : `项目 ${name} 已创建（重启后机器人上线，可在右侧编辑或扫码绑定）`)
      }
    } catch (e) {
      setError(e.message)
    } finally {
      setNewBusy(false)
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
      const resp = await api.patchProject(selected, fields)
      setSaved(true)
      onToast?.('ok', `项目 ${selected} 设置已保存${resp?.hot_swapped ? '（已热换生效）' : '（重启后生效）'}`)
      await select(projects.find((x) => x.name === selected))
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
    if (!secret) { setError(`app_secret 必填（本项目也要求：仅改 app_id 无法热换且会覆盖 secret，故留空不触发保存）`); return }
    setBusy(true)
    try {
      const resp = await api.patchProject(selected, {
        platform_app_id: { [type]: appID },
        platform_app_secret: { [type]: secret },
      })
      setSaved(true)
      onToast?.('ok', `平台 ${type} 已保存${resp?.hot_swapped ? '，已热换生效' : '（重启后生效）'}`)
      const d = await api.getProject(selected)
      setDetail(d)
      setBotDraft(initBotDraft(d))
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  const stopQR = () => { cancelQR.current?.(); cancelQR.current = null }

  const startQR = async (type, curAppId, fromModal = false) => {
    stopQR()
    setQrStatus('loading')
    try {
      const r = await api.beginBotQR(selected, { platform: type, app_id: curAppId || undefined })
      const img = await QRCode.toDataURL(r.qr_url, { width: 220, margin: 1 })
      setQrImg(img)
      setQr({ platform: type, device_code: r.device_code, interval: r.interval || 5, expires_in: r.expires_in || 300 })
      pollQR(r, type, fromModal)
    } catch (e) {
      setQrStatus(`error: ${e.message}`)
    }
  }

  const pollQR = (r, type, fromModal) => {
    const interval = r.interval || 5
    const deadline = Date.now() + (r.expires_in || 300) * 1000
    let cancelled = false
    cancelQR.current = () => { cancelled = true }
    setQrStatus('pending')
    const step = async () => {
      if (cancelled) return
      if (Date.now() > deadline) { setQrStatus('expired'); return }
      try {
        const pr = await api.pollBotQR(selected, { device_code: r.device_code, platform: type })
        if (cancelled) return
        if (pr.status === 'pending') { setTimeout(step, Math.max(interval, 1) * 1000); return }
        if (pr.status === 'ok') {
          setQrStatus('ok')
          onToast?.('ok', `扫码成功：${type} 已绑定 ${pr.app_id}${pr.hot_swapped ? '，已热换生效' : '（重启后生效）'}`)
          if (fromModal) {
            setQrModal(false); setShowNew(false)
          }
          const d = await api.getProject(selected)
          setDetail(d)
          setBotDraft(initBotDraft(d))
        } else {
          setQrStatus(pr.status)
        }
      } catch (e) {
        if (!cancelled) setQrStatus(`error: ${e.message}`)
      }
    }
    setTimeout(step, Math.max(interval, 1) * 1000)
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

  // 运行中项目用 platforms（连接状态），未运行项目退回 platform_configs
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
          <div key={p.name} className={`sess-item ${selected === p.name ? 'sess-item-on' : ''}`} onClick={() => select(p)} data-prj={p.name}>
            <div className="sess-item-name">{p.name}{p.running === false && <span className="bot-tag">未运行</span>}</div>
            <div className="sess-item-sub">
              {p.agent_type || '—'} · {(p.platforms || []).join(', ') || '未绑平台'} · {p.sessions_count || 0} 会话
            </div>
          </div>
        ))}
      </div>

      <div className="sess-detail prj-detail">
        {!selected && <div className="sess-empty">← 选择项目查看 / 编辑</div>}
        {selected && !detail && <div className="sess-empty">加载中…</div>}
        {selected && detail && (
          <>
            <div className="sess-detail-head">
              <div className="sess-detail-title">{selected}</div>
              <div className="sess-detail-sub">
                {detail.agent_type || '—'} · {detail.work_dir || '默认目录'}
                {saved && <span className="prj-saved">保存成功，已热换或重启生效</span>}
              </div>
            </div>
            {detail.running === false && (
              <div className="prj-note">项目未运行：仅配置已写入，重启服务后启动该通道。下方可继续编辑配置或扫码绑定。</div>
            )}
            <div className="prj-form">
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
                        <button className="btn btn-sm btn-green" onClick={() => startQR(p.type, bd.app_id, false)} disabled={busy || !!qr}>扫码绑定{bd.app_id ? '/换绑' : ''}</button>
                      </div>
                    </div>

                    {qr && qr.platform === p.type && (
                      <div className="qr-box">
                        {qrImg ? <img src={qrImg} alt="扫码绑定二维码" className="qr-img" /> : <div className="sess-empty">生成二维码…</div>}
                        <div className="qr-status mono">{qrStatusText()}</div>
                        <div className="prj-new-actions">
                          <button className="btn btn-sm" onClick={() => { stopQR(); setQr(null); setQrImg(''); setQrStatus('') }}>取消</button>
                          <button className="btn btn-sm" onClick={() => startQR(p.type, bd.app_id, false)}>重新生成</button>
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
                <button className="btn btn-sm btn-red" onClick={() => removeProj(selected)} disabled={busy}>删除项目</button>
              </div>
              <div className="help">凭证：app_secret 永不出现在本页；保存会立即热换（新 bot 生效无需重启）。也可 CLI <code>omp-lark-bridge feishu bind --project {selected} --app app_id:app_secret</code>。换 bot 后飞书会话 key 会变，历史会话不可达。</div>
            </div>
          </>
        )}
      </div>


      {fsOpen && (
        <div className="prj-modal-mask" onClick={(e) => { if (e.target === e.currentTarget) setFsOpen(false) }}>
          <div className="prj-modal">
            <div className="sess-list-head">
              <span>选择文件夹（work_dir）</span>
              <button className="btn btn-sm" onClick={() => setFsOpen(false)}>×</button>
            </div>
            <div className="bot-row">
              <input className="input mono" value={fsPath} onChange={(e) => browseFS(e.target.value)} placeholder="输入路径跳转（/ 开头）" spellCheck={false} />
            </div>
            {fsError && <div className="err-txt">{fsError}</div>}
            {fsLoading ? (
              <div className="sess-empty">加载中…</div>
            ) : (
              <div className="fs-list mono">
                <div className={`fs-item ${fsParent === fsPath ? 'fs-disabled' : ''}`} onClick={() => fsParent !== fsPath && browseFS(fsParent)}>⬆ 上级目录：{fsParent || '/'}</div>
                {fsDirs.map((d) => (
                  <div key={d.path} className="fs-item" onClick={() => browseFS(d.path)}>📁 {d.name}</div>
                ))}
                {fsDirs.length === 0 && <div className="sess-empty">（无子目录）</div>}
              </div>
            )}
            <div className="prj-new-actions">
              <button className="btn btn-sm btn-solid" onClick={chooseFS} disabled={!fsPath || fsLoading}>选择此目录</button>
              <button className="btn btn-sm" onClick={() => setFsOpen(false)}>取消</button>
            </div>
          </div>
        </div>
      )}
      {showNew && (
        <div className="prj-modal-mask" onClick={(e) => { if (e.target === e.currentTarget && !qrModal) { stopQR(); setShowNew(false) } }}>
          <div className="prj-modal">
            <div className="sess-list-head">
              <span>新建项目</span>
              {!qrModal && <button className="btn btn-sm" onClick={() => setShowNew(false)}>×</button>}
            </div>
            {error && <div className="err-txt">{error}</div>}
            <input className="input" placeholder="项目名（如 my-ops）" value={newName} onChange={(e) => setNewName(e.target.value)} disabled={qrModal} spellCheck={false} />
            <input className="input mono" placeholder="会话启动文件夹（work_dir，如 /srv/ops）" value={newWorkDir} onChange={(e) => setNewWorkDir(e.target.value)} disabled={qrModal} spellCheck={false} />
            <input className="input mono" placeholder="agent 类型（默认 omp）" value={newAgentType} onChange={(e) => setNewAgentType(e.target.value)} disabled={qrModal} spellCheck={false} />

            <div className="bind-row">
              <span className="bind-label">飞书机器人</span>
              <label><input type="radio" name="bindMode" checked={bindMode === 'none'} onChange={() => setBindMode('none')} disabled={qrModal} /> 暂不绑定</label>
              <label><input type="radio" name="bindMode" checked={bindMode === 'manual'} onChange={() => setBindMode('manual')} disabled={qrModal} /> 手动填写</label>
              <label><input type="radio" name="bindMode" checked={bindMode === 'qr'} onChange={() => setBindMode('qr')} disabled={qrModal} /> 扫码绑定</label>
            </div>

            {bindMode === 'manual' && !qrModal && (
              <div className="bind-manual">
                <input className="input mono" placeholder="app_id" value={manualAppID} onChange={(e) => setManualAppID(e.target.value)} spellCheck={false} />
                <input className="input mono" type="password" placeholder="app_secret" value={manualSecret} onChange={(e) => setManualSecret(e.target.value)} spellCheck={false} />
              </div>
            )}

            {bindMode === 'qr' && qrModal && (
              <div className="qr-box" style={{ maxWidth: '100%', alignItems: 'center' }}>
                {qrImg ? <img src={qrImg} alt="扫码绑定二维码" className="qr-img" /> : <div className="sess-empty">生成二维码…</div>}
                <div className="qr-status mono">{qrStatusText()}</div>
                <div className="prj-new-actions">
                  <button className="btn btn-sm" onClick={() => { stopQR(); setQrModal(false); setShowNew(false) }}>关闭</button>
                  <button className="btn btn-sm" onClick={() => startQR('feishu', '', true)}>重新生成</button>
                </div>
              </div>
            )}

            {!qrModal && (
              <div className="prj-new-actions">
                <button className="btn btn-sm btn-green" onClick={submitNew} disabled={newBusy || !newName.trim()}>{newBusy ? '创建中…' : '创建项目'}</button>
                <button className="btn btn-sm" onClick={() => setShowNew(false)}>取消</button>
              </div>
            )}
            <div className="help">每个项目 = 独立 omp（独立 work_dir）+ 独立飞书应用。扫码绑定：创建后弹出二维码，用飞书 App 扫码即完成授权并自动写凭证。</div>
          </div>
        </div>
      )}
    </div>
  )
}
