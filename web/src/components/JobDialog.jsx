import { useEffect, useMemo, useState } from 'react'
import { parseCron, humanizeCron, formatTime, formatCountdown } from '../lib/time.js'
import { api } from '../api.js'
import Combobox from './Combobox.jsx'
import { useEsc } from '../hooks/useEsc.js'

const SESSION_PLACEHOLDER = 'feishu:<open_chat_id>:<open_id>'

// 会话锚点下拉的友好化渲染：把后端 /sessions 的条目（platform/chat_name/
// user_name/active/live + 原始 session_key）渲染成「平台 · 会话类型 · 名称」，
// 避免用户面对一坨 platform:oc_xxx:ou_yyy 分不清。提交值仍是原始 session_key。
const PLAT_LABELS = {
  feishu: '飞书', telegram: 'Telegram', discord: 'Discord', slack: 'Slack',
  dingtalk: '钉钉', wecom: '企业微信', weixin: '微信', qq: 'QQ', qqbot: 'QQ',
  line: 'LINE', weibo: '微博',
}

function sessionOption(x) {
  const key = (x.session_key || '').trim()
  if (!key) return null
  // 后端标记为不属于本平台 bot 的会话（如跨应用 open_id 残留）不进候选。
  if (x.valid_for_bot === false) return null
  const parts = key.split(':')
  const tail = parts[2] || ''
  let kind = '群聊'
  if (tail.startsWith('root:') || tail.startsWith('thread:')) kind = '话题'
  else if (tail.startsWith('ou_')) kind = '私聊'
  const marks = []
  if (x.live) marks.push('在线')
  else if (x.active) marks.push('活跃')
  const plat = PLAT_LABELS[x.platform] || x.platform || ''
  // chat_name/user_name 未解析出来时后端会回填类 id 值（oc_/ou_/om_），这种不显示。
  const idish = (v) => !v || /^(oc_|ou_|om_|root:|thread:)/.test(v)
  const who = !idish(x.chat_name) ? x.chat_name : !idish(x.user_name) ? x.user_name : ''
  const label = [plat, kind, who, marks.join('/')].filter(Boolean).join(' · ')
  return { value: key, label: label || key }
}

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
  const [agent, setAgent] = useState('')
  const [goal, setGoal] = useState(true)
  const [expanded, setExpanded] = useState(false)
  const [modelOptions, setModelOptions] = useState([])
  const [agentOptions, setAgentOptions] = useState([])
  const [sessionOptions, setSessionOptions] = useState([])
  const [project, setProject] = useState('')
  const [projects, setProjects] = useState([]) // [{name, display_name}]；显示名，绑定 ID
  const [touched, setTouched] = useState(false)
  // Esc 分层关闭：先收展开的提示词弹窗，再关整个对话框。
  useEsc(open && !expanded, onClose)
  useEsc(expanded, () => setExpanded(false))

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
    setAgent(job?.agent || '')
    setGoal(job ? job.goal_mode !== false : true)
    setExpanded(false)
    setTouched(false)
  }, [open, job])

  // 打开时加载项目列表并选中当前（或第一个）项目。
  useEffect(() => {
    if (!open) return
    let alive = true
    ;(async () => {
      try {
        const projs = await api.listProjects()
        const list = (projs?.projects || []).filter((p) => p.name)
        if (!alive) return
        setProjects(list)
        setProject(job?.project || list[0]?.name || '')
      } catch {
        /* 忽略：只有下拉候选丢失，手输不受影响 */
      }
    })()
    return () => { alive = false }
  }, [open])

  // 按当前项目加载模型与会话锚点候选；切换「所属项目」时自动重新拉取，
  // 保证会话下拉只列所选中项目的 bot 名下会话。失败静默——仅丢失候选。
  useEffect(() => {
    if (!open || !project) return
    let alive = true
    ;(async () => {
      try {
        const [m, s, ag] = await Promise.all([api.getModels(project), api.getSessions(project), api.listAgents().catch(() => null)])
        if (!alive) return
        setModelOptions(m?.models || [])
        // 所有已发现 agent（bundled/user/project）都可被指派执行，read_only 只是禁止在管理页编辑。
        setAgentOptions(
          [...new Set(((ag?.agents) || []).map((a) => a.name).filter(Boolean))]
        )
        setSessionOptions(
          (s?.sessions || [])
            .map(sessionOption)
            .filter(Boolean)
            .filter((o, i, a) => a.findIndex((v) => v.value === o.value) === i)
        )
      } catch {
        /* 忽略：只有下拉候选丢失，手输不受影响 */
      }
    })()
    return () => { alive = false }
  }, [open, project])

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
      session_mode: goal ? 'new_per_run' : sessionMode || '',
      model: model.trim() || undefined,
      thinking_level: thinking || undefined,
      agent: agent.trim() || undefined,
      goal_mode: goal,
    }
    if (kind === 'prompt') {
      clean.prompt = prompt.trim()
    } else {
      clean.exec = exec.trim()
      clean.work_dir = workDir.trim() || undefined
    }
    clean.project = project
    if (mode === 'new') clean.session_key = sessionKey.trim()
    onSubmit(clean)
  }

  if (!open) return null
  const edit = mode === 'edit'

  return (
    <>
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
            <label htmlFor="jd-project">所属项目</label>
            <select id="jd-project" className="input" value={project} onChange={(e) => setProject(e.target.value)}>
              {projects.length === 0 && <option value={project}>{project || '…'}</option>}
              {projects.map((p) => (
                <option key={p.name} value={p.name}>{p.display_name || p.name}</option>
              ))}
            </select>
            <div className="help">运行时在所选项目的工作目录下启会话。换项目会清空旧项目下的执行记录（会话列表）。</div>
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
            <>
              <textarea className="input" value={prompt} onChange={(e) => setPrompt(e.target.value)} placeholder="例如：整理今天的 GitHub Trending，发简报给我" style={{ marginTop: 8 }} />
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 6 }}>
                <button type="button" className="btn btn-ghost btn-sm" onClick={() => setExpanded(true)}>展开编辑</button>
                <span className="help" style={{ margin: 0 }}>内容过长时可在弹窗里完整输入</span>
              </div>
            </>
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
            <div className="help">下拉仅列出当前「所属项目」下的会话（含平台/类型/活跃标记，选中即填完整 key）；也可手输；飞书发 <code>/whoami</code> 可查。</div>
          </div>

          <div className="field">
            <label>会话模式</label>
            <div className="radio-row">
              <label><input type="radio" checked={!goal && sessionMode === ''} disabled={goal} onChange={() => setSessionMode('')} /> 复用会话</label>
              <label><input type="radio" checked={sessionMode === 'new_per_run' || goal} onChange={() => setSessionMode('new_per_run')} /> 每次新建</label>
            </div>
            <div className="help">复用 = 每次都在该会话里继续；新建 = 每次全新会话。注意：开启「以 goal 模式执行」或绑定模型/子代理会强制每次新建会话（覆盖本选项）。</div>
          </div>
          <div className="field">
            <label htmlFor="jd-model">模型（可选）</label>
            <Combobox ariaLabel="模型" className="mono" value={model} onChange={setModel}
              options={modelOptions} placeholder="留空 = default；如 deepseek/deepseek-flash" />
            <div className="help">从候选选可用模型，或手输。指定后该任务每次以独立会话按此模型运行；模型不存在时自动回退 default。</div>
          </div>

          <div className="field">
            <label htmlFor="jd-agent">使用专用子代理（可选）</label>
            <Combobox ariaLabel="专用子代理" className="mono" value={agent} onChange={setAgent}
              options={agentOptions} placeholder="留空 = 主会话直接执行；如 scout、reviewer" />
            <div className="help">把任务指派给 omp 专用子代理（scout/reviewer/自定义…）执行并汇报结果。指定后该任务每次以独立会话按此代理运行，代理自带的模型/思考等级优先于上面两项。</div>
          </div>

          <div className="field">
            <label className="check-row">
              <input type="checkbox" checked={goal} onChange={(e) => setGoal(e.target.checked)} />
              以 goal 模式执行（默认开启）
            </label>
            <div className="help">以 omp 目标驱动模式运行：每次都用全新会话、按目标自主推进并回报，不依赖会话恢复——因此该模式与「复用会话」互斥，开启时每次运行都会在会话列表新增一条。要跨次复用同一个会话需关闭本项。不清楚时对工程类任务（初始化目录、把某任务做完）推荐开启。</div>
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
      {expanded && (
        <div className="overlay" onClick={() => setExpanded(false)}>
          <div className="dialog" role="dialog" aria-modal="true" aria-label="任务提示词"
            onClick={(e) => e.stopPropagation()} style={{ width: 860, maxWidth: '94vw' }}>
            <div className="dialog-head">
              任务提示词
              <button className="icon-close" onClick={() => setExpanded(false)} aria-label="关闭" style={{ fontSize: 18 }}>×</button>
            </div>
            <div className="dialog-body">
              <textarea className="input" rows={18} autoFocus value={prompt}
                onChange={(e) => setPrompt(e.target.value)}
                placeholder="输入任务提示词（agent 将按此执行）"
                style={{ fontFamily: 'var(--font-mono)', lineHeight: 1.6 }} />
            </div>
            <div className="dialog-foot">
              <button className="btn btn-sm btn-ghost" onClick={() => setExpanded(false)}>取消</button>
              <button className="btn btn-sm btn-solid" onClick={() => setExpanded(false)}>保存</button>
            </div>
          </div>
        </div>
      )}
    </>
  )
}
