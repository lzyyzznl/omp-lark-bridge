import { useEffect, useRef, useState } from 'react'
import { api } from '../api.js'
import { useEsc } from '../hooks/useEsc.js'

// 用指定专家执行一次任务：后端新建一个独立会话并把任务委托给该专家
// （首条 prompt 注入 delegationTemplate，见 agent/omp/session.go），
// 结果通过 SSE 流式返回；done 事件携带 fork 后的新会话 id，可供后续续聊。
export default function ExpertRunDialog({ open, agent, onClose }) {
  const [task, setTask] = useState('')
  const [running, setRunning] = useState(false)
  const [log, setLog] = useState([]) // [{kind:'text'|'thinking'|'tool'|'error'|'done', text}]
  const [newId, setNewId] = useState('')
  const aliveRef = useRef(true)
  useEsc(open, () => { if (!running) onClose() })

  useEffect(() => {
    if (!open) return
    setTask(''); setLog([]); setNewId(''); setRunning(false)
    aliveRef.current = true
    return () => { aliveRef.current = false }
  }, [open, agent])

  if (!open) return null

  const append = (kind, text) => setLog((ls) => [...ls, { kind, text }])

  const run = async () => {
    const msg = task.trim()
    if (!msg || running) return
    setRunning(true); setLog([]); setNewId('')
    try {
      const res = await api.runExpert(agent.name, { message: msg })
      if (!res.ok || !res.body) throw new Error(`HTTP ${res.status}`)
      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buf = ''
      outer:
      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        let idx
        while ((idx = buf.indexOf('\n\n')) >= 0) {
          const frame = buf.slice(0, idx)
          buf = buf.slice(idx + 2)
          const line = frame.split('\n').find((l) => l.startsWith('data: '))
          if (!line) continue
          let ev
          try { ev = JSON.parse(line.slice(6)) } catch { continue }
          if (ev.type === 'text' && ev.content) append('text', ev.content)
          else if (ev.type === 'thinking' && ev.content) append('thinking', ev.content)
          else if (ev.type === 'tool_use') append('tool', `🔧 ${ev.tool}：${String(ev.intent || '').slice(0, 140)}`)
          else if (ev.type === 'error') { append('error', ev.error); break outer }
          else if (ev.type === 'done') { if (ev.id) setNewId(ev.id); append('done', '完成'); break outer }
        }
      }
    } catch (e) {
      append('error', e.message || '执行失败')
    } finally {
      setRunning(false)
    }
  }

  return (
    <div className="overlay" onClick={() => { if (!running) onClose() }}>
      <div className="dialog" role="dialog" aria-modal="true" aria-label={`用专家执行 · ${agent.name}`}
        onClick={(e) => e.stopPropagation()} style={{ width: 720, maxWidth: '94vw' }}>
        <div className="dialog-head">
          用专家执行 · <span className="mono">{agent.name}</span>
          <button className="icon-close" onClick={onClose} aria-label="关闭" style={{ fontSize: 18 }}>×</button>
        </div>
        <div className="dialog-body">
          <div className="field">
            <label htmlFor="exp-task">任务</label>
            <textarea id="exp-task" className="input" rows={4} autoFocus value={task} disabled={running}
              onChange={(e) => setTask(e.target.value)}
              placeholder={`给 ${agent.name} 的任务：以全新独立会话执行，首条即注入该专家人格。`} />
          </div>
          <div className="field" style={{ marginBottom: 0 }}>
            <label>执行输出</label>
            <div style={{ maxHeight: '40vh', overflowY: 'auto', border: '1px solid var(--border)', borderRadius: 6, padding: 10, fontSize: 14 }}>
              {log.length === 0 ? (
                <div className="help">（提交任务后这里流式显示执行过程）</div>
              ) : log.map((l, i) =>
                l.kind === 'thinking' ? (
                  <div key={i} className="help" style={{ margin: '2px 0', whiteSpace: 'pre-wrap' }}>… {l.text}</div>
                ) : l.kind === 'tool' ? (
                  <div key={i} className="help mono" style={{ margin: '2px 0', fontSize: 12, whiteSpace: 'pre-wrap' }}>{l.text}</div>
                ) : l.kind === 'error' ? (
                  <div key={i} className="err-txt" style={{ margin: '2px 0', whiteSpace: 'pre-wrap' }}>{l.text}</div>
                ) : l.kind === 'done' ? (
                  <div key={i} className="help" style={{ margin: '4px 0' }}><b>✅ 完成</b>{newId ? ` · 新会话 ${newId}（可在「会话库」继续）` : ''}</div>
                ) : (
                  <div key={i} className="exp-text" style={{ margin: '2px 0', whiteSpace: 'pre-wrap' }}>{l.text}</div>
                ),
              )}
            </div>
          </div>
        </div>
        <div className="dialog-foot">
          <span className="help" style={{ marginRight: 'auto' }}>{agent.description ? agent.description.slice(0, 84) : ''}</span>
          <button className="btn btn-sm" onClick={onClose} disabled={running}>关闭</button>
          <button className="btn btn-sm btn-solid" onClick={run} disabled={running || !task.trim()}>
            {running ? '执行中…' : '执行'}
          </button>
        </div>
      </div>
    </div>
  )
}
