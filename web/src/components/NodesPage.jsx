import { useCallback, useEffect, useState } from 'react'
import { api } from '../api.js'
import { formatTime } from '../lib/time.js'

// 节点页：hub server 聚合的设备目录。每行直达该节点的 ompweb（新标签打开，
// ompweb 拒绝被 iframe 嵌入）。节点只出网，页面流量经中心 relay 转发。
export default function NodesPage({ onToast }) {
  const [nodes, setNodes] = useState(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [pairOpen, setPairOpen] = useState(false)
  const [pairForm, setPairForm] = useState({ device_token: '', name: '', host: '' })
  const [pairing, setPairing] = useState(false)
  const [pairResult, setPairResult] = useState(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const r = await api.nodes()
      setNodes(r.nodes || [])
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

  const submitPair = async (e) => {
    e.preventDefault()
    if (!pairForm.device_token.trim()) {
      onToast?.('err', 'device_token 必填')
      return
    }
    setPairing(true)
    setPairResult(null)
    try {
      const r = await api.pairNode({
        device_token: pairForm.device_token.trim(),
        name: pairForm.name.trim(),
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
        <button className="btn btn-ghost" onClick={() => setPairOpen(v => !v)}>
          {pairOpen ? '收起配对' : '添加节点'}
        </button>
        <button className="btn btn-ghost" onClick={load} disabled={loading}>
          {loading ? '刷新中…' : '刷新'}
        </button>
      </div>

      {pairOpen && (
        <div className="pair-panel">
          <div className="pair-title">添加节点</div>
          <form className="pair-form" onSubmit={submitPair}>
            <input
              className="input"
              placeholder="device_token，例如 pc1"
              value={pairForm.device_token}
              onChange={e => setPairForm(f => ({ ...f, device_token: e.target.value }))}
            />
            <input
              className="input"
              placeholder="显示名称，例如 zte-linux"
              value={pairForm.name}
              onChange={e => setPairForm(f => ({ ...f, name: e.target.value }))}
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
            留空时自动生成 omp-&lt;token&gt;.&lt;中心域名&gt;；泛解析已开启，添加第 N 个节点不需要再建 DNS/ingress。
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

      {error ? (
        <div className="state-block">
          <div className="big">无法读取节点列表</div>
          <div className="hint">{error}</div>
          <div className="hint">确认 config.toml 已启用 [hub] server 模式，并重启服务。</div>
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
          <div className="hint">点「添加节点」生成一次性配对命令，在节点上执行即可接入中心。</div>
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
                <th>OMP Web</th>
              </tr>
            </thead>
            <tbody>
              {nodes.map((n, i) => (
                <tr key={`${n.name}-${i}`}>
                  <td className="mono">{n.name}</td>
                  <td><span className={`badge ${n.online ? 'on' : 'off'}`}>{n.online ? '在线' : '离线'}</span></td>
                  <td className="mono">{formatTime(n.last_seen)}</td>
                  <td className="num">{n.agent_count ?? 0}</td>
                  <td className="num">{n.session_count ?? 0}</td>
                  <td>
                    <div className="node-actions">
                      <button className="btn btn-ghost" onClick={() => open(n)} disabled={!n.web_url}>
                        {n.web_url ? '打开 ↗' : '未上报'}
                      </button>
                      <button className="btn btn-ghost node-remove" onClick={() => remove(n)}>
                        移除
                      </button>
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
