import { useEffect, useMemo, useState } from 'react'
import { parseCron, humanizeCron, formatTime, formatCountdown } from '../lib/time.js'
import { api } from '../api.js'
import Combobox from './Combobox.jsx'

const SESSION_PLACEHOLDER = 'feishu:<open_chat_id>:<open_id>'

// 新建 / 编辑定时任务对话框。
// 新建提交 CronAddRequest；编辑仅提交可改字段（session_key 锚点不可改）。
export default function JobDialog({ open, mode, job, onClose, onSubmit, submitting }) {
  const [desc, setDesc] = useState('')
  const [cronExpr, setCronExpr] = useState('0 9 * * *')
  const [kind, setKind] = useState('prompt')
  const [prompt, setPrompt] = useState('')
  const [exec, setExec] = useState('')
  const [workDir, setWorkDir] = useState('')
  const [sessionKey, setSessionKey] = useState('')
  const [mute, setMute] = useState(false)
  const [resultOnly, setResultOnly] = useState(false)
  const [sessionMode, setSessionMode] = useState('')
  const [model, setModel] = useState('')
  const [thinking, setThinking] = useState('')
  const [modelOptions, setModelOptions] = useState([])
  const [sessionOptions, setSessionOptions] = useState([])
  const [project, setProject] = useState('')
  const [touched, setTouched] = useState(false)

  useEffect(() => {
    if (!open) return
    const isExec = !!(job && (job.exec || typeof job.IsShellJob === 'function' && job.IsShellJob()))
    setDesc(job?.description || '')
    setCronExpr(job?.cron_expr || '0 9 * * *')
    setKind(job && isExec ? 'exec' : 'prompt')
    setPrompt(job?.prompt || '')
    setExec(job?.exec || '')
    setWorkDir(job?.work_dir || '')
    setSessionKey(job?.session_key || '')
    setMute(!!job?.mute)
    setResultOnly(!!job?.result_only)
    setSessionMode(job?.session_mode || '')
    setModel(job?.model || '')
    setThinking(job?.thinking_level || '')
    setTouched(false)
  }, [open, job])

  // 打开时加载可用模型与会话锚点（下拉候选）。失败静默——仅丢失候选，仍可手输。
  useEffect(() => {
    if (!open) return
    let alive = true
    ;(async () => {
      try {
        const projs = await api.listProjects()
        const proj = projs?.projects?.[0]?.name
        if (!alive || !proj) return
        if (!job?.project) setProject(proj) // 新建任务 → 显示默认项目
        const [m, s] = await Promise.all([api.getModels(proj), api.getSessions(proj)])
        if (!alive) return
        setModelOptions(m?.models || [])
        setSessionOptions(
          (s?.sessions || []).map((x) => x.session_key).filter(Boolean).filter((v, i, a) => a.indexOf(v) === i)
        )
      } catch {
        /* 忽略：只有下拉候选丢失，手输不受影响 */
      }
    })()
    return () => { alive = false }
  }, [open])

  const parsed = useMemo(() => parseCron(cronExpr), [cronExpr])
  const cronInvalid = touched && !parsed
  const taskSpecified = kind === 'prompt' ? prompt.trim() !== '' : exec.trim() !== ''

  const submit = () => {
    setTouched(true)
    if (!parsed || !taskSpecified || (mode === 'new' && sessionKey.trim() === '')) return
    const clean = {
      description: desc.trim(),
      cron_expr: cronExpr.trim(),
      mute,
      result_only: resultOnly,
      session_mode: sessionMode || '',
      model: model.trim() || undefined,
      thinking_level: thinking || undefined,
    }
    if (kind === 'prompt') {
      clean.prompt = prompt.trim()
    } else {
      clean.exec = exec.trim()
      clean.work_dir = workDir.trim() || undefined
    }
    if (mode === 'new') clean.session_key = sessionKey.trim()
    onSubmit(clean)
  }

  if (!open) return null
  const edit = mode === 'edit'

  return (
    <div className="overlay" onClick={onClose}>
      <div className="dialog" role="dialog" aria-modal="true" aria-label={edit ? '编辑任务' : '新建任务'} onClick={(e) => e.stopPropagation()}>
        <div className="dialog-head">
          {edit ? `编辑任务 · ${job.id}` : '新建任务'}
          <button className="icon-close" onClick={onClose} aria-label="关闭" style={{ fontSize: 18 }}>×</button>
        </div>

        <div className="dialog-body">
          <div className="field">
            <label htmlFor="jd-desc">任务描述</label>
            <input id="jd-desc" className="input" value={desc} onChange={(e) => setDesc(e.target.value)} placeholder="例如：每天早上 9 点汇总 GitHub 热点" />
          </div>

          <div className="field">
            <label>所属项目（会话启动文件夹）</label>
            <div className="input" style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <span className="mono">{project || '…'}</span>
              <span className="help" style={{ margin: 0 }}>任务始终挂靠项目；运行时在项目工作目录下启会话</span>
            </div>
          </div>

          <div className="field">
            <label htmlFor="jd-cron">Cron 表达式<span className="req"> *</span></label>
            <input id="jd-cron" className={`input mono ${cronInvalid ? 'input-err' : ''}`} value={cronExpr}
              onChange={(e) => setCronExpr(e.target.value)} spellCheck={false}
              placeholder="分 时 日 月 周 · 如 0 9 * * 1" />
            <div className="help">分 时 日 月 周 5 段。例：<code>0 9 * * 1</code> = 每周一 09:00；<code>*/30 * * * *</code> = 每 30 分钟。</div>
            {parsed && (
              <div className="preview mono">
                <span className="ok">✓ {humanizeCron(cronExpr)}</span> · 下次 {formatTime(parsed.next)}（{formatCountdown(parsed.next)}）
              </div>
            )}
            {cronInvalid && <div className="err-txt">表达式不合法</div>}
          </div>

          <div className="field">
            <label>执行方式</label>
            <div className="radio-row">
              <label><input type="radio" checked={kind === 'prompt'} onChange={() => setKind('prompt')} /> Agent 提示词</label>
              <label><input type="radio" checked={kind === 'exec'} onChange={() => setKind('exec')} /> Shell 命令</label>
            </div>
            {kind === 'prompt' ? (
              <textarea className="input" value={prompt} onChange={(e) => setPrompt(e.target.value)} placeholder="例如：整理今天的 GitHub Trending，发简报给我" style={{ marginTop: 8 }} />
            ) : (
              <>
                <input className="input mono" value={exec} onChange={(e) => setExec(e.target.value)} placeholder="要执行的 shell 命令" style={{ marginTop: 8 }} spellCheck={false} />
                <input className="input mono" value={workDir} onChange={(e) => setWorkDir(e.target.value)} placeholder="工作目录（可选，默认 agent 工作目录）" style={{ marginTop: 8 }} spellCheck={false} />
              </>
            )}
          </div>

          <div className="field">
            <label htmlFor="jd-session">{edit ? '会话锚点（不可改）' : '会话锚点'}{!edit && <span className="req"> *</span>}</label>
            <Combobox ariaLabel="会话锚点" className="mono" value={sessionKey} disabled={edit}
              onChange={setSessionKey} options={sessionOptions} placeholder={SESSION_PLACEHOLDER} />
            <div className="help">job 触发时的平台会话（platform:chat:user）。可从候选选已有会话，或手输；飞书发 <code>/whoami</code> 可查。</div>
          </div>

          <div className="field">
            <label>会话模式</label>
            <div className="radio-row">
              <label><input type="radio" checked={sessionMode === ''} onChange={() => setSessionMode('')} /> 复用会话</label>
              <label><input type="radio" checked={sessionMode === 'new_per_run'} onChange={() => setSessionMode('new_per_run')} /> 每次新建</label>
            </div>
            <div className="help">复用 = 每次都在该会话里继续；新建 = 每次全新会话。</div>
          </div>
          <div className="field">
            <label htmlFor="jd-model">模型（可选）</label>
            <Combobox ariaLabel="模型" className="mono" value={model} onChange={setModel}
              options={modelOptions} placeholder="留空 = default；如 deepseek/deepseek-flash" />
            <div className="help">从候选选可用模型，或手输。指定后该任务每次以独立会话按此模型运行；模型不存在时自动回退 default。</div>
          </div>

          <div className="field">
            <label htmlFor="jd-thinking">思考等级（可选）</label>
            <select id="jd-thinking" className="input" value={thinking} onChange={(e) => setThinking(e.target.value)}>
              <option value="">继承默认</option>
              <option value="off">关闭（off）</option>
              <option value="minimal">最低（minimal）</option>
              <option value="low">低（low）</option>
              <option value="medium">中（medium）</option>
              <option value="high">高（high）</option>
              <option value="xhigh">较高（xhigh）</option>
              <option value="max">最高（max）</option>
            </select>
            <div className="help">omp 思考等级（effort）。设置了模型或思考等级，任务即以独立会话执行。</div>
          </div>

          <div className="field" style={{ marginBottom: 0 }}>
            <label style={{ display: 'inline-flex', gap: 8, alignItems: 'center', textTransform: 'none', letterSpacing: 0, fontWeight: 600, cursor: 'pointer' }}>
              <input type="checkbox" checked={resultOnly} onChange={(e) => { setResultOnly(e.target.checked); if (e.target.checked) setMute(false) }} />
              仅发结果（过程静音，结束后发送最终结果一次）
            </label>
            <div className="help" style={{ marginTop: 2 }}>适合定时汇总/报告类任务——执行过程不打扰，只看最终产出。</div>
          </div>

          <div className="field" style={{ marginBottom: 0 }}>
            <label style={{ display: 'inline-flex', gap: 8, alignItems: 'center', textTransform: 'none', letterSpacing: 0, fontWeight: 600, cursor: 'pointer' }}>
              <input type="checkbox" checked={mute} onChange={(e) => { setMute(e.target.checked); if (e.target.checked) setResultOnly(false) }} />
              静音运行（不推送开始与结果消息）
            </label>
          </div>
        </div>

        <div className="dialog-foot">
          <button className="btn btn-sm" onClick={onClose} disabled={submitting}>取消</button>
          <button className="btn btn-sm btn-solid" onClick={submit} disabled={submitting}>
            {submitting ? '提交中…' : edit ? '保存修改' : '创建任务'}
          </button>
        </div>
      </div>
    </div>
  )
}
