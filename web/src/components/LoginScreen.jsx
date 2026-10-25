import { useState } from 'react'
import { api, setToken } from '../api.js'

// 进站门禁：必须提供 management token 才能进入看板。
// token 校验通过后由父级写入 cookie（30 天自动续期，见 api.js）。
export default function LoginScreen({ onLogin }) {
  const [val, setVal] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  const submit = async () => {
    const t = val.trim()
    if (!t) { setErr('请输入 token'); return }
    setBusy(true)
    setErr('')
    setToken(t) // 先写入 cookie，才能用该 token 请求校验
    try {
      await api.list() // 校验 token：401 会抛错
      onLogin(t)
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="overlay" style={{ display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
      <div className="dialog" role="dialog" aria-modal="true" aria-label="登录" style={{ width: 380, maxWidth: '92vw' }}>
        <div className="dialog-head">omp-lark-bridge · 定时任务看板</div>
        <div className="dialog-body setup-form">
          <div className="field">
            <label htmlFor="login-token">Management token</label>
            <input
              id="login-token"
              className={`input mono ${err ? 'input-err' : ''}`}
              value={val}
              onChange={(e) => setVal(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Enter') submit() }}
              placeholder="请输入 token"
              spellCheck={false}
              autoFocus
            />
            {err ? <div className="err-txt">{err}</div> : null}
            <div className="help">
              首次使用默认 token 为 <code>1</code>；如需修改，编辑 config.toml 里
              <code>[management] token</code> 后重启 omp-lark-bridge。
            </div>
          </div>
        </div>
        <div className="dialog-foot">
          <button className="btn btn-sm btn-solid" onClick={submit} disabled={busy}>
            {busy ? '验证中…' : '进入'}
          </button>
        </div>
      </div>
    </div>
  )
}
