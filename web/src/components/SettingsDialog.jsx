import { useEffect, useState } from 'react'
import { api, getToken } from '../api.js'
import { useEsc } from '../hooks/useEsc.js'

// 设置：management token + 会话自动归档策略。token 存 cookie（30 天自动续期）。
export default function SettingsDialog({ open, onClose, onSave }) {
  useEsc(open, onClose)
  const [val, setVal] = useState(() => getToken())
  const [arc, setArc] = useState(null)
  const [arcMsg, setArcMsg] = useState('')
  const [arcBusy, setArcBusy] = useState(false)

  useEffect(() => {
    if (!open) return
    setArcMsg('')
    api.archiveSettings()
      .then((d) => setArc({ ...(d.settings || {}), archived_count: d.archived_count || 0 }))
      .catch(() => setArc(null))
  }, [open])

  if (!open) return null

  const patchArc = async (fields) => {
    setArcBusy(true)
    try {
      const d = await api.saveArchiveSettings(fields)
      setArc((prev) => ({ ...(prev || {}), ...(d.settings || {}) }))
      setArcMsg('已保存，自动归档每天检查一次')
    } catch (e) {
      setArcMsg(e.message)
    } finally {
      setArcBusy(false)
    }
  }

  const runArchiveNow = async () => {
    setArcBusy(true)
    try {
      const r = await api.runArchive()
      const d = await api.archiveSettings()
      setArc({ ...(d.settings || {}), archived_count: d.archived_count || 0 })
      setArcMsg(`已按当前设置归档 ${r.archived} 个会话`)
    } catch (e) {
      setArcMsg(e.message)
    } finally {
      setArcBusy(false)
    }
  }

  return (
    <div className="overlay" onClick={onClose}>
      <div className="dialog" role="dialog" aria-modal="true" aria-label="API 设置" onClick={(e) => e.stopPropagation()}>
        <div className="dialog-head">
          API 设置
          <button className="icon-close" onClick={onClose} aria-label="关闭" style={{ fontSize: 18 }}>×</button>
        </div>
        <div className="dialog-body setup-form">
          <div className="field">
            <label htmlFor="set-token">Management token</label>
            <input id="set-token" className="input mono" value={val} onChange={(e) => setVal(e.target.value)}
              placeholder="config.toml 里 [management] token 的值（默认 1）" spellCheck={false} />
            <div className="help">
              对应 <code>[management]</code> 的 <code>token</code>，默认 <code>1</code>。
              保存后写入 cookie（30 天自动续期）。
            </div>
          </div>

          <div className="dialog-section">会话归档</div>
          {!arc && <div className="help">归档设置不可用（management 未就绪）。</div>}
          {arc && (
            <>
              <div className="field">
                <label>
                  <input
                    type="checkbox"
                    checked={!!arc.enabled}
                    disabled={arcBusy}
                    onChange={(e) => patchArc({ enabled: e.target.checked })}
                  />{' '}
                  自动归档早于指定天数的会话
                </label>
                <div className="help">归档是软删除：会话从列表隐藏，可在「归档」里查看和恢复，文件不会删除。</div>
              </div>
              <div className="field">
                <label>保留天数（超过则归档）</label>
                <input
                  className="input mono"
                  type="number"
                  min={1}
                  value={arc.older_than_days || 90}
                  disabled={arcBusy}
                  onChange={(e) => setArc({ ...arc, older_than_days: Number(e.target.value) })}
                  onBlur={(e) => patchArc({ older_than_days: Math.max(1, Number(e.target.value) || 90) })}
                />
              </div>
              <div className="field">
                <label>
                  <input
                    type="checkbox"
                    checked={!!arc.exclude_cron}
                    disabled={arcBusy}
                    onChange={(e) => patchArc({ exclude_cron: e.target.checked })}
                  />{' '}
                  不归档定时任务的会话
                </label>
              </div>
              <div className="field">
                <div className="prj-new-actions">
                  <button className="btn btn-sm btn-solid" onClick={runArchiveNow} disabled={arcBusy}>立即执行一次</button>
                  <span className="help">已归档 {arc.archived_count || 0} 个会话</span>
                </div>
                {arcMsg && <div className="help">{arcMsg}</div>}
              </div>
            </>
          )}
        </div>
        <div className="dialog-foot">
          <button className="btn btn-sm" onClick={onClose}>取消</button>
          <button
            className="btn btn-sm btn-solid"
            onClick={() => { onSave(val.trim()); onClose() }}
          >保存并重连</button>
        </div>
      </div>
    </div>
  )
}
