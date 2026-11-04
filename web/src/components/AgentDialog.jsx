import { useEffect, useState } from 'react'
import { api } from '../api.js'
import { useEsc } from '../hooks/useEsc.js'

const NAME_RE = /^[A-Za-z0-9_-]{1,64}$/

// 新建 / 编辑 / 查看 omp 自定义 agent，四步向导：
//   1 基础 -> 2 能力 -> 3 行为 -> 4 输出与知识
// - 工具白名单勾选（/api/v1/omp-tools）；技能勾选只显示名字。
// - output/knowledge/body 大文本在独立弹窗里编辑。
// - 新建（user scope）：写入 ~/.omp/agent/agents/<name>.md，所有项目可用。
// - 编辑：仅 user scope；按分步字段重建 frontmatter + body。
export default function AgentDialog({ open, mode, agent, onClose, onSave, onDelete, saving }) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [model, setModel] = useState('')
  const [tools, setTools] = useState('')
  const [thinkingLevel, setThinkingLevel] = useState('')
  const [spawns, setSpawns] = useState('')
  const [skills, setSkills] = useState('')
  const [output, setOutput] = useState('')
  const [prewalk, setPrewalk] = useState('')
  const [advisor, setAdvisor] = useState('')
  const [blocking, setBlocking] = useState(false)
  const [structured, setStructured] = useState(false)
  const [readSummarize, setReadSummarize] = useState(false)
  const [knowledge, setKnowledge] = useState('')
  const [body, setBody] = useState('')
  const [content, setContent] = useState('') // 查看模式下的完整源码
  const [touched, setTouched] = useState(false)
  const [step, setStep] = useState(1)
  const [tab, setTab] = useState('form') // 'form' | 'source'
  const [skillsCatalog, setSkillsCatalog] = useState(null) // null=未加载
  const [skillsOpen, setSkillsOpen] = useState(false)
  const [skillsDetail, setSkillsDetail] = useState(null) // 展开详情的技能名 | null
  const [spawnsCatalog, setSpawnsCatalog] = useState(null) // 可派生子代理列表（agent 名）
  const [spawnsOpen, setSpawnsOpen] = useState(false)
  const [toolsCatalog, setToolsCatalog] = useState(null)
  const [toolsOpen, setToolsOpen] = useState(false)
  const [editField, setEditField] = useState(null) // null | {key,label,placeholder,rows,mono}
  useEsc(open, onClose)

  useEffect(() => {
    if (!open) return
    let alive = true
    ;(async () => {
      setTouched(false)
      setTab('form')
      setStep(1)
      setEditField(null)
      setSkillsOpen(false)
      setToolsOpen(false)
      setSpawnsOpen(false)
      setSkillsDetail(null)
      setContent('')
      if (!agent) {
        setName(''); setDescription(''); setModel(''); setTools(''); setThinkingLevel(''); setSpawns('')
        setSkills(''); setOutput(''); setPrewalk(''); setAdvisor('')
        setBlocking(false); setStructured(false); setReadSummarize(false)
        setKnowledge(''); setBody('')
        return
      }
      setName(agent.name || '')
      setDescription(agent.description || '')
      setModel(agent.model || '')
      setTools(agent.tools || '')
      setThinkingLevel(agent.thinking_level || '')
      setSpawns(agent.spawns || '')
      setBody('')
      const a = agent.detail || (await api.getAgent(agent.name).catch(() => null))
      if (!alive || !a) return
      const d = a.agent || a
      setContent(d.content || '')
      // 详情优先：description/model/tools 以最新文件为准
      if (d.description !== undefined) setDescription(d.description || '')
      if (d.model !== undefined) setModel(d.model || '')
      if (d.thinking_level !== undefined) setThinkingLevel(d.thinking_level || '')
      if (d.spawns !== undefined) setSpawns(d.spawns || '')
      if (d.skills !== undefined) setSkills(d.skills || '')
      if (d.output !== undefined) setOutput(d.output || '')
      if (d.prewalk !== undefined) setPrewalk(d.prewalk || '')
      if (d.advisor !== undefined) setAdvisor(d.advisor || '')
      if (d.blocking !== undefined) setBlocking(!!d.blocking)
      if (d.structured !== undefined) setStructured(!!d.structured)
      if (d.read_summarize !== undefined) setReadSummarize(!!d.read_summarize)
      if (d.knowledge !== undefined) setKnowledge(d.knowledge || '')
    })()
    return () => { alive = false }
  }, [open, agent])

  if (!open) return null
  const isNew = mode === 'new'
  const isView = mode === 'view'
  const nameInvalid = touched && !NAME_RE.test(name)
  const canSubmit = NAME_RE.test(name)

  const skillNames = skills.split(',').map((s) => s.trim()).filter(Boolean)
  const toggleSkill = (n) => {
    const cur = skillNames.includes(n) ? skillNames.filter((x) => x !== n) : [...skillNames, n]
    setSkills(cur.join(', '))
  }
  const toggleSkillsPanel = () => {
    setSkillsOpen((v) => !v)
    if (!skillsCatalog) {
      setSkillsCatalog([])
      api.listSkills().then((res) => {
        const list = res?.skills || []
        setSkillsCatalog(list)
        if (!list.length) setSkillsOpen(false)
      }).catch(() => { setSkillsOpen(false); setSkillsCatalog(null) })
    }
  }

  const toolNames = tools.split(',').map((s) => s.trim()).filter(Boolean)
  const toggleTool = (n) => {
    const cur = toolNames.includes(n) ? toolNames.filter((x) => x !== n) : [...toolNames, n]
    setTools(cur.join(', '))
  }
  const toggleToolsPanel = () => {
    setToolsOpen((v) => !v)
    if (!toolsCatalog) {
      setToolsCatalog([])
      api.listTools().then((res) => {
        const list = res?.tools || []
        setToolsCatalog(list)
        if (!list.length) setToolsOpen(false)
      }).catch(() => { setToolsOpen(false); setToolsCatalog(null) })
    }
  }

  const spawnNames = spawns.split(',').map((s) => s.trim()).filter(Boolean)
  const toggleSpawn = (n) => {
    const cur = spawnNames.includes(n) ? spawnNames.filter((x) => x !== n) : [...spawnNames, n]
    setSpawns(cur.join(', '))
  }
  const toggleSpawnsPanel = () => {
    setSpawnsOpen((v) => !v)
    if (!spawnsCatalog) {
      setSpawnsCatalog([])
      api.listAgents().then((res) => {
        const list = res?.agents || []
        setSpawnsCatalog(list.map((a) => a.name))
        if (!list.length) setSpawnsOpen(false)
      }).catch(() => { setSpawnsOpen(false); setSpawnsCatalog(null) })
    }
  }

  const fieldLabels = {
    output: { label: '输出格式 · 答题卡模板', help: '规定 agent 必须按什么结构交结果。像答题卡，答案按格子填，方便程序自动解析。若勾了「结构化输出」则强制遵守。', mono: true, rows: 10, placeholder: 'properties:\n  结论:\n    type: string\n  严重级别:\n    enum:\n      - 低\n      - 中\n      - 高\nrequired:\n  - 结论' },
    knowledge: { label: '背景资料 · 随身参考资料', help: '给 agent 的参考资料，会插在指令前面一起喂给它。和「指令」的区别：这栏是背景资料，不是行为要求。', mono: false, rows: 10, placeholder: '粘贴团队规范、术语表、历史背景……将作为该 agent 的持久化知识注入上下文' },
    body: { label: '指令 · 岗位说明书', help: 'agent 的核心，越具体越好。建议写清「目标 / 工作流程 / 输出要求 / 红线（不能做什么）」。', mono: false, rows: 14, placeholder: '写清它是谁、干什么、按什么流程、要避免什么……整个 agent 的个性都来自这一段', required: true },
  }
  const currentField = editField ? { key: editField, ...fieldLabels[editField] } : null
  const setFieldValue = (key, v) => {
    if (key === 'output') setOutput(v)
    else if (key === 'knowledge') setKnowledge(v)
    else setBody(v)
  }

  const submit = () => {
    setTouched(true)
    if (!canSubmit || isView) return
    const payload = {
      name: name.trim(),
      description: description.trim(),
      model: model.trim(),
      tools: tools.trim(),
      thinking_level: thinkingLevel,
      spawns: spawns.trim(),
      skills: skills.trim(),
      output: output.trim(),
      prewalk: prewalk.trim(),
      advisor: advisor.trim(),
      blocking: !!blocking,
      structured: !!structured,
      read_summarize: !!readSummarize,
      knowledge: knowledge.trim(),
      body: body.trim(),
    }
    if (isNew) {
      onSave(() => api.addAgent(payload), 'agent 已创建')
    } else {
      onSave(() => api.updateAgent(name.trim(), payload), 'agent 已保存')
    }
  }

  const fieldValue = (key) => (key === 'output' ? output : key === 'knowledge' ? knowledge : body)
  const stepTitles = ['基础', '能力', '行为', '输出与知识']

  return (
    <div className="overlay" onClick={onClose}>
      <div className="dialog" role="dialog" aria-modal="true" aria-label={isNew ? '新建 agent' : `agent · ${name}`}
        onClick={(e) => e.stopPropagation()} style={{ width: 680, maxWidth: '94vw' }}>
        <div className="dialog-head">
          {isNew ? '新建自定义 agent' : isView ? `查看 agent · ${agent.name}` : `编辑 agent · ${agent.name}`}
          <button className="icon-close" onClick={onClose} aria-label="关闭" style={{ fontSize: 18 }}>×</button>
        </div>

        <div className="dialog-body">
          <div className="tab-row">
            {tab === 'form' && (
              <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
                {stepTitles.map((t, i) => (
                  <button key={t} className={`tab ${step === i + 1 ? 'tab-on' : ''}`}
                    onClick={() => setStep(i + 1)}>
                    {i + 1} · {t}
                  </button>
                ))}
              </div>
            )}
            {!isNew && (
              <button className={`tab ${tab === 'source' ? 'tab-on' : ''}`} style={{ marginLeft: 'auto' }}
                onClick={() => setTab(tab === 'source' ? 'form' : 'source')}>源码</button>
            )}
          </div>

          {tab === 'form' ? (
            <>
              {step === 1 && (
                <>
                  <div className="field">
                    <label htmlFor="ag-name">Agent 名称{isNew && <span className="req"> *</span>}</label>
                    {isNew ? (
                      <input id="ag-name" className={`input mono ${nameInvalid ? 'input-err' : ''}`} value={name}
                        onChange={(e) => { setName(e.target.value); setTouched(true) }}
                        placeholder="如 release-auditor（仅字母数字 - _ ，≤64）" spellCheck={false} />
                    ) : (
                      <div className="input mono" style={{ background: 'var(--surface-dim)' }}>{name}</div>
                    )}
                    {nameInvalid && <div className="err-txt">名称只能含字母、数字、-、_（1–64 字符）</div>}
                  </div>
                  <div className="field">
                    <label htmlFor="ag-desc">描述<span className="req"> *</span></label>
                    <textarea id="ag-desc" className="input" rows={3} value={description}
                      onChange={(e) => setDescription(e.target.value)} placeholder="一句话说清：这个 agent 什么时候用、能干什么（omp 靠这段描述决定何时派它上场）" />
                  </div>
                  <div className="field">
                    <label htmlFor="ag-model">模型（可选）</label>
                    <input id="ag-model" className="input mono" value={model}
                      onChange={(e) => setModel(e.target.value)} placeholder="留空 = 沿用主会话模型；可填具体模型名或 @fast/@slow 等角色" spellCheck={false} />
                    <div className="help">留空就继承主会话的模型；想给这个 agent 单独指定模型时填，如 deepseek/deepseek-flash 或 @slow。</div>
                  </div>
                  <div className="field">
                    <label htmlFor="ag-tl">思考强度（可选）</label>
                    <select id="ag-tl" className="input" value={thinkingLevel} onChange={(e) => setThinkingLevel(e.target.value)}>
                      <option value="">继承主会话（默认）</option>
                      <option value="auto">auto · 自动</option>
                      <option value="low">low · 低</option>
                      <option value="medium">medium · 中</option>
                      <option value="high">high · 高</option>
                    </select>
                    <div className="help">omp 的思考强度档位；低成本机械任务选 low，复杂推理选 high。</div>
                  </div>
                </>
              )}

              {step === 2 && (
                <>
                  <div className="field">
                    <label htmlFor="ag-tools">可用工具（白名单勾选）</label>
                    <div style={{ display: 'flex', gap: 8 }}>
                      <input id="ag-tools" className="input mono" value={tools} style={{ flex: 1 }}
                        readOnly placeholder="留空 = 不限制；勾选下方工具加入白名单" spellCheck={false} />
                      <button className="btn btn-sm" onClick={toggleToolsPanel} type="button">
                        {toolsOpen ? '收起' : '选择工具'}
                      </button>
                    </div>
                    {toolsOpen && (
                      <div style={{ marginTop: 8, border: '1px solid var(--border)', borderRadius: 6, padding: 8, maxHeight: 200, overflowY: 'auto' }}>
                        {!toolsCatalog ? (
                          <div className="help">工具列表加载中…</div>
                        ) : (
                          toolsCatalog.map((t) => (
                            <label key={t.name} style={{ display: 'flex', gap: 8, alignItems: 'baseline', padding: '3px 0', cursor: 'pointer' }}>
                              <input type="checkbox" checked={toolNames.includes(t.name)} onChange={() => toggleTool(t.name)} />
                              <span className="mono">{t.name}</span>
                              {t.description ? <span className="help" style={{ flex: 1 }}>{t.description}</span> : null}
                            </label>
                          ))
                        )}
                      </div>
                    )}
                    <div className="help">白名单：只允许它用勾选的工具；留空 = 不限制。写代码的 agent 建议至少勾 bash、read。</div>
                  </div>
                  <div className="field">
                    <label htmlFor="ag-spawns">可派生子代理（白名单勾选）</label>
                    <div style={{ display: 'flex', gap: 8 }}>
                      <input id="ag-spawns" className="input mono" value={spawns} style={{ flex: 1 }}
                        readOnly placeholder="留空 = 不限制；勾选下方子代理加入白名单" spellCheck={false} />
                      <button className="btn btn-sm" onClick={toggleSpawnsPanel} type="button">
                        {spawnsOpen ? '收起' : '选择子代理'}
                      </button>
                    </div>
                    {spawnsOpen && (
                      <div style={{ marginTop: 8, border: '1px solid var(--border)', borderRadius: 6, padding: 8, maxHeight: 180, overflowY: 'auto' }}>
                        {!spawnsCatalog ? (
                          <div className="help">子代理列表加载中…</div>
                        ) : (
                          [
                            { name: '*', label: '* · 任意子代理' },
                            ...spawnsCatalog.map((n) => ({ name: n, label: n })),
                          ].map((opt) => (
                            <label key={opt.name} style={{ display: 'flex', gap: 8, alignItems: 'center', padding: '3px 0', cursor: 'pointer' }}>
                              <input type="checkbox" checked={spawnNames.includes(opt.name)} onChange={() => toggleSpawn(opt.name)} />
                              <span className="mono">{opt.label}</span>
                            </label>
                          ))
                        )}
                      </div>
                    )}
                    <div className="help">白名单：该 agent 执行时能派生的专用子代理（取自既有 agent 列表）。<code>*</code> = 任意子代理。</div>
                  </div>
                  <div className="field">
                    <label htmlFor="ag-skills">技能白名单（可选，勾选）</label>
                    <div style={{ display: 'flex', gap: 8 }}>
                      <input id="ag-skills" className="input mono" value={skills} style={{ flex: 1 }}
                        onChange={(e) => setSkills(e.target.value)} placeholder="如 gh, grilling" spellCheck={false} />
                      <button className="btn btn-sm" onClick={toggleSkillsPanel} type="button">
                        {skillsOpen ? '收起' : '勾选技能'}
                      </button>
                    </div>
                    {skillsOpen && (
                      <div style={{ marginTop: 8, border: '1px solid var(--border)', borderRadius: 6, padding: 8, maxHeight: 180, overflowY: 'auto' }}>
                        {!skillsCatalog ? (
                          <div className="help">技能列表加载中…</div>
                        ) : (
                          skillsCatalog.map((s) => (
                            <div key={s.name} style={{ padding: '3px 0' }}>
                              <label style={{ display: 'flex', gap: 8, alignItems: 'center', cursor: 'pointer' }}>
                                <input type="checkbox" checked={skillNames.includes(s.name)} onChange={() => toggleSkill(s.name)} />
                                <span className="mono">{s.name}</span>
                                {s.description ? (
                                  <button className="btn btn-sm" style={{ padding: '0 6px' }} type="button"
                                    onClick={(e) => { e.stopPropagation(); setSkillsDetail(skillsDetail === s.name ? null : s.name) }}>
                                    {skillsDetail === s.name ? '收起' : '详情'}
                                  </button>
                                ) : null}
                              </label>
                              {skillsDetail === s.name && s.description ? (
                                <div className="help" style={{ margin: '2px 0 4px 26px' }}>{s.description}</div>
                              ) : null}
                            </div>
                          ))
                        )}
                      </div>
                    )}
                    <div className="help">勾选的技能是该专家的白名单：它的会话（以及派生的子代理）只看到这些技能；留空 = 全部技能。</div>
                  </div>
                </>
              )}

              {step === 3 && (
                <>
                  <div className="field">
                    <label htmlFor="ag-prewalk">Prewalk · 动手前先扫一遍（可选）</label>
                    <input id="ag-prewalk" className="input mono" value={prewalk}
                      onChange={(e) => setPrewalk(e.target.value)} placeholder="true = 开启 / false = 关闭 / 留空继承；可指定仅某模型开启，如 @fast" spellCheck={false} />
                    <div className="help">开启后，agent 正式干活前先把相关代码/材料快速扫一遍，先摸清全局再动手。适合大任务，避免上来就埋头改错地方。</div>
                  </div>
                  <div className="field">
                    <label htmlFor="ag-advisor">Advisor · 顾问模式：只出主意不动手（可选）</label>
                    <input id="ag-advisor" className="input mono" value={advisor}
                      onChange={(e) => setAdvisor(e.target.value)} placeholder="true = 开启 / false = 关闭 / 留空继承；也可仅对某几个子代理开，如 scout, task" spellCheck={false} />
                    <div className="help">开启后 agent 以顾问身份干活：只分析、给建议，不实际改动文件、不执行变更。</div>
                  </div>
                  <div className="field">
                    <label>执行行为（开关）</label>
                    <div style={{ display: 'grid', gap: 6 }}>
                      <label style={{ display: 'flex', gap: 8, alignItems: 'center', cursor: 'pointer' }}>
                        <input type="checkbox" checked={blocking} onChange={(e) => setBlocking(e.target.checked)} />
                        阻塞型（blocking）· 派它干活必须等它交完差才继续，适合要立刻拿结果的场景
                      </label>
                      <label style={{ display: 'flex', gap: 8, alignItems: 'center', cursor: 'pointer' }}>
                        <input type="checkbox" checked={structured} onChange={(e) => setStructured(e.target.checked)} />
                        结构化输出（structured）· 强制按「输出格式」那一栏交卷，不许自由发挥
                      </label>
                      <label style={{ display: 'flex', gap: 8, alignItems: 'center', cursor: 'pointer' }}>
                        <input type="checkbox" checked={readSummarize} onChange={(e) => setReadSummarize(e.target.checked)} />
                        先总结再读（readSummarize）· 读长文档先出摘要再继续，防止一次读爆上下文
                      </label>
                    </div>
                    <div className="help">默认都不勾。blocking 决定「等不等它完事」，structured 决定「交卷是否严格按格式」，readSummarize 决定「长文档怎么读」。</div>
                  </div>
                </>
              )}

              {step === 4 && (
                <>
                  {['output', 'knowledge', 'body'].map((key) => {
                    const meta = fieldLabels[key]
                    const v = fieldValue(key)
                    return (
                      <div className="field" key={key}>
                        <label>{meta.label}{meta.required && <span className="req"> *</span>}</label>
                        <div style={{ display: 'flex', gap: 8, alignItems: 'stretch' }}>
                          <div className="input" style={{ flex: 1, whiteSpace: 'pre-wrap', maxHeight: 72, overflowY: 'auto', cursor: 'pointer', background: 'var(--surface-dim)' }}
                            onClick={() => setEditField(key)} title="点击编辑">
                            {v ? (v.length > 160 ? v.slice(0, 160) + '…' : v) : '（未填写，点击编辑）'}
                          </div>
                          <button className="btn btn-sm" onClick={() => setEditField(key)} type="button">编辑</button>
                        </div>
                        <div className="help">{meta.help}</div>
                      </div>
                    )
                  })}
                </>
              )}
            </>
          ) : (
            <div className="field" style={{ marginBottom: 0 }}>
              <label>完整源码（frontmatter + 指令，只读）</label>
              <pre className="agent-source">{content || '（无法读取源码）'}</pre>
              <div className="help">{agent?.read_only ? '这是内置/项目 agent，不可编辑；如需定制可基于它新建一个自定义 agent。' : `源文件：${agent?.source || ''}`}</div>
            </div>
          )}
        </div>

        {tab === 'form' && !isView && (
          <div className="dialog-foot">
            {!isNew && agent && !agent.read_only ? (
              <button className="btn btn-sm btn-danger" onClick={() => onDelete(name)} disabled={saving}>删除</button>
            ) : <span />}
            <div style={{ flex: 1 }} />
            {step > 1 && (
              <button className="btn btn-sm" onClick={() => setStep((s) => s - 1)} disabled={saving}>上一步</button>
            )}
            {step < 4 ? (
              <button className="btn btn-sm btn-solid" onClick={() => setStep(step + 1)}>下一步</button>
            ) : (
              <button className="btn btn-sm btn-solid" onClick={submit} disabled={saving || !canSubmit}>
                {saving ? '保存中…' : isNew ? '创建' : '保存修改'}
              </button>
            )}
          </div>
        )}

        {currentField && (
          <div className="overlay" onClick={() => setEditField(null)}>
            <div className="dialog" role="dialog" aria-modal="true" aria-label={currentField.label}
              onClick={(e) => e.stopPropagation()} style={{ width: 600, maxWidth: '94vw' }}>
              <div className="dialog-head">
                {currentField.label}
                <button className="icon-close" onClick={() => setEditField(null)} aria-label="关闭" style={{ fontSize: 18 }}>×</button>
              </div>
              <div className="dialog-body">
                <div className="field" style={{ marginBottom: 0 }}>
                  <textarea className={`input ${currentField.mono ? 'mono' : ''}`} rows={currentField.rows}
                    value={fieldValue(currentField.key)}
                    onChange={(e) => setFieldValue(currentField.key, e.target.value)}
                    placeholder={currentField.placeholder} spellCheck={false} autoFocus />
                  <div className="help" style={{ marginTop: 8 }}>{currentField.help}</div>
                </div>
              </div>
              <div className="dialog-foot">
                <span style={{ flex: 1 }} />
                <button className="btn btn-sm btn-solid" onClick={() => setEditField(null)}>完成</button>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
