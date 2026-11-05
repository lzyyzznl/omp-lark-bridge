import { useCallback, useEffect, useState } from 'react'
import { api } from '../api.js'
import { formatTime } from '../lib/time.js'

// 节点页：hub server 聚合的设备目录。每行直达该节点的 ompweb（新标签打开，
// ompweb 拒绝被 iframe 嵌入）。节点只出网，页面流量经中心 relay 转发。
export default function NodesPage({ onToast }) {
  const [nodes, setNodes] = useState(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [role, setRole] = useState('')
  const [canEdit, setCanEdit] = useState(false)
  const [pairOpen, setPairOpen] = useState(false)
  const [pairForm, setPairForm] = useState({ device_id: '', device_name: '', host: '' })
  const [pairing, setPairing] = useState(false)
  const [pairResult, setPairResult] = useState(null)
  const [editing, setEditing] = useState(null)
  const [editForm, setEditForm] = useState({ device_name: '', secret: '', login_code: '', clear_login_code: false })
  const [saving, setSaving] = useState(false)
  const [accessNode, setAccessNode] = useState(null)
  const [accessRecords, setAccessRecords] = useState(null)
  const [accessLoading, setAccessLoading] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const r = await api.nodes()
      setNodes(r?.nodes || [])
      setRole(r?.role || '')
      setCanEdit(r?.can_edit === true)
    } catch (e) {
      setNodes(null)
      setError(e.message || String(e))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { load() }, [load])

  const open = (n) => {
    if (!n.web_url) {
      onToast?.('err', `${n.name} 还没有可用的 OMP Web 地址`)
      return
    }
    const tab = window.open('', '_blank')
    if (!tab) {
      onToast?.('err', '浏览器拦截了新标签，请允许弹窗')
      return
    }
    tab.opener = null
    tab.location.href = n.web_url
  }

  const remove = async (n) => {
    if (!window.confirm(`移除节点 ${n.name}？节点本机仍会继续运行，但中心不再能访问它。`)) return
    try {
      const r = await api.unpairNode(n.id || n.name)
      if (!r?.removed) throw new Error('移除失败')
      onToast?.('ok', '已移除')
      load()
    } catch (err) {
      onToast?.('err', err.message || String(err))
    }
  }

  const loadAccess = async (n) => {
    setAccessNode(n)
    setAccessRecords(null)
    setAccessLoading(true)
    try {
      const r = await api.access(n.id)
      setAccessRecords(r?.records || [])
    } catch (err) {
      onToast?.('err', err.message || String(err))
    } finally {
      setAccessLoading(false)
    }
  }

  const unlockIP = async (rec) => {
    if (!window.confirm(`解锁 IP ${rec.ip}？会清除该 IP 的登录锁定。`)) return
    try {
      await api.unlockAccess(rec.ip)
      onToast?.('ok', `已解锁 ${rec.ip}`)
      if (accessNode) await loadAccess(accessNode)
      await load()
    } catch (err) {
      onToast?.('err', err.message || String(err))
    }
  }

  const startEdit = (n) => {
    setEditing(n)
    setEditForm({ device_name: n.name || '', secret: '', login_code: '', clear_login_code: false })
  }

  const saveEdit = async (e) => {
    e.preventDefault()
    if (!editing) return
    const fields = {}
    const name = editForm.device_name.trim()
    if (name !== (editing.name || '')) fields.device_name = name
    if (editForm.secret.trim()) fields.secret = editForm.secret.trim()
    if (editForm.login_code.trim()) fields.login_code = editForm.login_code.trim()
    if (editForm.clear_login_code) fields.clear_login_code = true
    if (Object.keys(fields).length === 0) {
      onToast?.('err', '没有修改')
      return
    }
    setSaving(true)
    try {
      await api.updateNode(editing.id, fields)
      onToast?.('ok', '已保存')
      setEditing(null)
      await load()
    } catch (err) {
      onToast?.('err', err.message || String(err))
    } finally {
      setSaving(false)
    }
  }

  const submitPair = async (e) => {
    e.preventDefault()
    setPairing(true)
    setPairResult(null)
    try {
      const r = await api.pairNode({
        device_id: pairForm.device_id.trim(),
        device_name: pairForm.device_name.trim(),
        host: pairForm.host.trim(),
      })
      if (!r?.code) throw new Error('生成失败：检查 device_token 是否已配对')
      setPairResult(r)
      onToast?.('ok', '配对命令已生成')
    } catch (err) {
      onToast?.('err', err.message || String(err))
    } finally {
      setPairing(false)
    }
  }

  const copyCommand = async () => {
    if (!pairResult?.command) return
    try {
      await navigator.clipboard.writeText(pairResult.command)
      onToast?.('ok', '已复制')
    } catch {
      onToast?.('err', '复制失败，请手动选中命令')
    }
  }

  return (
    <div className="shell">
      <div className="section-title">节点</div>
      <div className="nodes-toolbar">
        {canEdit && (
          <button className="btn btn-ghost" onClick={() => setPairOpen(v => !v)}>
            {pairOpen ? '收起配对' : '添加节点'}
          </button>
        )}
        <button className="btn btn-ghost" onClick={load} disabled={loading}>
          {loading ? '刷新中…' : '刷新'}
        </button>
        {!canEdit && role === 'client' && (
          <span className="hint">客户端只读：可查看并打开所有节点</span>
        )}
      </div>

      {pairOpen && canEdit && (
        <div className="pair-panel">
          <div className="pair-title">添加节点</div>
          <form className="pair-form" onSubmit={submitPair}>
            <input
              className="input"
              placeholder="设备 ID（留空自动生成，例如 node-7f3k9m2q）"
              value={pairForm.device_id}
              onChange={e => setPairForm(f => ({ ...f, device_id: e.target.value }))}
            />
            <input
              className="input"
              placeholder="设备名称，例如 zte-linux（可选）"
              value={pairForm.device_name}
              onChange={e => setPairForm(f => ({ ...f, device_name: e.target.value }))}
            />
            <input
              className="input"
              placeholder="公网主机（可留空，自动生成）"
              value={pairForm.host}
              onChange={e => setPairForm(f => ({ ...f, host: e.target.value }))}
            />
            <button className="btn" type="submit" disabled={pairing}>
              {pairing ? '生成中…' : '生成配对命令'}
            </button>
          </form>
          <div className="hint" style={{ marginTop: 8 }}>
            设备 ID 留空会自动生成；公网主机留空时使用 omp-&lt;设备ID&gt;.&lt;中心域名&gt;。泛解析已开启，添加第 N 个节点不需要再建 DNS/ingress。
          </div>
          {pairResult && (
            <div className="pair-result">
              <div className="hint">
                在节点上执行，命令 {new Date(pairResult.expires_at).toLocaleString()} 前有效：
              </div>
              <pre className="pair-command">{pairResult.command}</pre>
              <div className="pair-actions">
                <button className="btn btn-ghost" type="button" onClick={copyCommand}>复制命令</button>
                <span className="hint">配对成功后节点会出现在列表里，点「打开 ↗」经中心进入。</span>
              </div>
            </div>
          )}
        </div>
      )}

      {editing && canEdit && (
        <div className="pair-panel">
          <div className="pair-title">编辑节点 {editing.name}</div>
          <form className="pair-form" onSubmit={saveEdit}>
            <input
              className="input"
              placeholder="设备名称"
              value={editForm.device_name}
              onChange={e => setEditForm(f => ({ ...f, device_name: e.target.value }))}
            />
            <input
              className="input mono"
              type="password"
              placeholder="新密钥（留空保持）"
              value={editForm.secret}
              onChange={e => setEditForm(f => ({ ...f, secret: e.target.value }))}
              spellCheck={false}
            />
            <input
              className="input mono"
              type="password"
              placeholder={editing.login_code_set ? '新登录码（留空保持）' : '登录码（留空不启用）'}
              value={editForm.login_code}
              onChange={e => setEditForm(f => ({ ...f, login_code: e.target.value, clear_login_code: false }))}
              spellCheck={false}
            />
            <label className="node-edit-check">
              <input
                type="checkbox"
                checked={editForm.clear_login_code}
                onChange={e => setEditForm(f => ({ ...f, clear_login_code: e.target.checked, login_code: '' }))}
              />
              清除登录码
            </label>
            <div className="pair-actions">
              <button className="btn" type="submit" disabled={saving}>
                {saving ? '保存中…' : '保存'}
              </button>
              <button className="btn btn-ghost" type="button" onClick={() => setEditing(null)} disabled={saving}>
                取消
              </button>
            </div>
          </form>
          <div className="hint" style={{ marginTop: 8 }}>
            密钥用于节点和中心认证，修改后节点下次同步会自动采用；登录码用于浏览器经中心访问该节点（HTTP Basic 密码），留空保持原值。
          </div>
        </div>
      )}

      {accessNode && canEdit && (
        <div className="pair-panel">
          <div className="pair-title">
            访问记录 · {accessNode.name}
            <button className="btn btn-ghost" type="button" style={{ float: 'right' }} onClick={() => setAccessNode(null)}>
              关闭
            </button>
          </div>
          {accessLoading ? (
            <div className="hint">读取中…</div>
          ) : (accessRecords || []).length === 0 ? (
            <div className="hint">还没有访问记录。</div>
          ) : (
            <div className="nodes-table-wrap" style={{ marginTop: 8 }}>
              <table className="nodes-table">
                <thead>
                  <tr>
                    <th>IP</th>
                    <th>次数</th>
                    <th>最后访问</th>
                    <th>路径</th>
                    <th>状态</th>
                    <th>锁定</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {accessRecords.map((a) => {
                    const locked = a.locked_until && new Date(a.locked_until) > new Date()
                    const status = a.last_status === 429 ? '已锁' : a.last_status === 401 ? '登录失败' : '正常'
                    return (
                      <tr key={`${a.ip}-${a.last_seen}`}>
                        <td className="mono">{a.ip}</td>
                        <td className="num">{a.count}</td>
                        <td className="mono">{formatTime(a.last_seen)}</td>
                        <td className="mono">{a.last_path || '/'}</td>
                        <td><span className={`badge ${a.last_status === 200 ? 'on' : 'off'}`}>{status}</span></td>
                        <td className="mono">{locked ? formatTime(a.locked_until) : '—'}</td>
                        <td>
                          <button className="btn btn-ghost" onClick={() => unlockIP(a)} disabled={!locked}>
                            解锁
                          </button>
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          )}
          <div className="hint" style={{ marginTop: 8 }}>
            锁定只影响当前服务进程；重启会清空。SSH 兜底：POST http://127.0.0.1:31990/api/v1/hub/access/unlock
            {' '}（带中心 management token，body 为 {'{'} "ip": "x.x.x.x" {'}'}）。
          </div>
        </div>
      )}

      {error ? (
        <div className="state-block">
          <div className="big">无法读取节点列表</div>
          <div className="hint">{error}</div>
          <div className="hint">
            确认 config.toml 已启用 [hub] 并完成配对；客户端模式需要能同步中心，且为只读。
          </div>
        </div>
      ) : loading && nodes === null ? (
        <div>
          <div className="skeleton-line" style={{ marginBottom: 10 }} />
          <div className="skeleton-line" style={{ marginBottom: 10 }} />
          <div className="skeleton-line" />
        </div>
      ) : (nodes || []).length === 0 ? (
        <div className="state-block">
          <div className="big">还没有节点</div>
          <div className="hint">
            {role === 'client'
              ? '还没有从中心同步到节点目录；确认本节点已配对并等待一次同步。'
              : '点「添加节点」生成一次性配对命令，在节点上执行即可接入中心。'}
          </div>
        </div>
      ) : (
        <div className="nodes-table-wrap">
          <table className="nodes-table">
            <thead>
              <tr>
                <th>节点</th>
                <th>状态</th>
                <th>最后心跳</th>
                <th>Agents</th>
                <th>会话</th>
                <th>凭证</th>
                <th>OMP Web</th>
              </tr>
            </thead>
            <tbody>
              {nodes.map((n) => (
                <tr key={n.id || n.name}>
                  <td>
                    <div className="mono">
                      {n.name}
                      {n.is_self ? <span className="badge on" style={{ marginLeft: 6 }}>本机</span> : null}
                    </div>
                    <div className="hint mono">{n.id}</div>
                  </td>
                  <td><span className={`badge ${n.online ? 'on' : 'off'}`}>{n.online ? '在线' : '离线'}</span></td>
                  <td className="mono">{formatTime(n.last_seen)}</td>
                  <td className="num">{n.agent_count ?? 0}</td>
                  <td className="num">{n.session_count ?? 0}</td>
                  <td>
                    <div className="node-cred">
                      <span className={`badge ${n.secret_set ? 'on' : 'off'}`}>
                        {n.secret_set ? '独立密钥' : '默认密钥'}
                      </span>
                      {n.login_code_set ? <span className="badge on">登录码已设</span> : null}
                    </div>
                  </td>
                  <td>
                    <div className="node-actions">
                      <button className="btn btn-ghost" onClick={() => open(n)} disabled={!n.web_url}>
                        {n.web_url ? '打开 ↗' : '未上报'}
                      </button>
                      {canEdit && (
                        <button className="btn btn-ghost" onClick={() => startEdit(n)}>
                          编辑
                        </button>
                      )}
                      {canEdit && (
                        <button className="btn btn-ghost" onClick={() => loadAccess(n)}>
                          访问记录
                        </button>
                      )}
                      {canEdit && (
                        <button className="btn btn-ghost node-remove" onClick={() => remove(n)}>
                          移除
                        </button>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
