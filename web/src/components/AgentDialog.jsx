import { useEffect, useState } from 'react'
import { api } from '../api.js'
import { useEsc } from '../hooks/useEsc.js'

const NAME_RE = /^[A-Za-z0-9_-]{1,64}$/

// 新建 / 编辑 / 查看 omp 自定义 agent。
// - 新建（user scope）：写入 ~/.omp/agent/agents/<name>.md，所有项目可用。
// - 编辑：仅 user scope；重建 frontmatter（name/description/model/tools）+ body，
//   因此 prewalk/advisor 等高级 frontmatter 不在编辑范围内（查看源码可读）。
// - 查看：完整 markdown 源码只读；bundled/project 仅可查看。
export default function AgentDialog({ open, mode, agent, onClose, onSave, onDelete, saving }) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [model, setModel] = useState('')
  const [tools, setTools] = useState('')
  const [thinkingLevel, setThinkingLevel] = useState('')
  const [spawns, setSpawns] = useState('')
  const [body, setBody] = useState('')
  const [content, setContent] = useState('') // 查看模式下的完整源码
  const [touched, setTouched] = useState(false)
  useEsc(open, onClose)
  const [tab, setTab] = useState('form') // 'form' | 'source'

  useEffect(() => {
    if (!open) return
    let alive = true
    ;(async () => {
      setTouched(false)
      setTab('form')
      setContent('')
      if (!agent) {
        setName(''); setDescription(''); setModel(''); setTools(''); setThinkingLevel(''); setSpawns(''); setBody('')
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
    })()
    return () => { alive = false }
  }, [open, agent])

  if (!open) return null
  const isNew = mode === 'new'
  const isView = mode === 'view'
  const nameInvalid = touched && !NAME_RE.test(name)
  const canSubmit = NAME_RE.test(name)

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
      body: body.trim(),
    }
    if (isNew) {
      onSave(() => api.addAgent(payload), 'agent 已创建')
    } else {
      onSave(() => api.updateAgent(name.trim(), payload), 'agent 已保存')
    }
  }

  return (
    <div className="overlay" onClick={onClose}>
      <div className="dialog" role="dialog" aria-modal="true" aria-label={isNew ? '新建 agent' : `agent · ${name}`}
        onClick={(e) => e.stopPropagation()} style={{ width: 640, maxWidth: '94vw' }}>
        <div className="dialog-head">
          {isNew ? '新建自定义 agent' : isView ? `查看 agent · ${agent.name}` : `编辑 agent · ${agent.name}`}
          <button className="icon-close" onClick={onClose} aria-label="关闭" style={{ fontSize: 18 }}>×</button>
        </div>

        <div className="dialog-body">
          <div className="tab-row">
            <button className={`tab ${tab === 'form' ? 'tab-on' : ''}`} onClick={() => setTab('form')}>基础信息</button>
            {!isNew && (
              <button className={`tab ${tab === 'source' ? 'tab-on' : ''}`} onClick={() => setTab('source')}>源码</button>
            )}
          </div>

          {tab === 'form' ? (
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
                  onChange={(e) => setDescription(e.target.value)} placeholder="一句话说明这个 agent 什么时候用、做什么（omp 用它决定何时选中该 agent）" />
              </div>
              <div className="field">
                <label htmlFor="ag-model">模型（可选）</label>
                <input id="ag-model" className="input mono" value={model}
                  onChange={(e) => setModel(e.target.value)} placeholder="留空 = 继承主会话；如 deepseek/deepseek-flash 或 @slow" spellCheck={false} />
              </div>
              <div className="field">
                <label htmlFor="ag-tools">可用工具（可选，逗号分隔）</label>
                <input id="ag-tools" className="input mono" value={tools}
                  onChange={(e) => setTools(e.target.value)} placeholder="留空 = 全部；如 read, grep, glob, bash" spellCheck={false} />
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
              <div className="field">
                <label htmlFor="ag-spawns">可派生子代理（可选，逗号分隔）</label>
                <input id="ag-spawns" className="input mono" value={spawns}
                  onChange={(e) => setSpawns(e.target.value)} placeholder="留空 = 不限制；* = 任意；如 scout, task" spellCheck={false} />
                <div className="help">白名单：该 agent 执行时能派生的专用子代理。<code>*</code> 表示不限制。</div>
              </div>
              <div className="field">
                <label htmlFor="ag-body">指令（人格正文）<span className="req"> *</span></label>
                <textarea id="ag-body" className="input" rows={8} value={body}
                  onChange={(e) => setBody(e.target.value)} placeholder={'给这个 agent 的完整工作指令：它擅长什么、要避免什么、输出格式……'} />
              </div>
              {!isNew && !isView && (
                <div className="help">保存会用上述基础字段重建整个文件 —— prewalk / advisor 等高级 frontmatter 不在编辑范围内，请直接在 <code>~/.omp/agent/agents/{name}.md</code> 里维护（保存会覆盖）。</div>
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

        <div className="dialog-foot">
          {!isView && agent && !agent.read_only && tab === 'form' ? (
            <button className="btn btn-sm btn-danger" onClick={() => onDelete(name)} disabled={saving}>删除</button>
          ) : <span />}
          {!isView && (
            <button className="btn btn-sm btn-solid" onClick={submit} disabled={saving || !canSubmit}>
              {saving ? '保存中…' : isNew ? '创建' : '保存修改'}
            </button>
          )}
        </div>
      </div>
    </div>
  )
}

// extractBody 从 markdown frontmatter 中摘出指令正文（编辑器只编辑 body）。
function extractBody(content) {
  const s = String(content || '').trim()
  if (!s.startsWith('---')) return s
  const rest = s.slice(3).replace(/^\n/, '')
  const end = rest.indexOf('\n---')
  if (end < 0) return s
  return rest.slice(end + 4).trim()
}
