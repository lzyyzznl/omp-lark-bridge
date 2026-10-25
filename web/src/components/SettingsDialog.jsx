import { useState } from 'react'
import { getToken } from '../api.js'

// management API token 设置。token 存 cookie（30 天自动续期，见 api.js）。
export default function SettingsDialog({ open, onClose, onSave }) {
  const [val, setVal] = useState(() => getToken())
  if (!open) return null
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
