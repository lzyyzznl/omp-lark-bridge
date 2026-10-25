import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, getToken, hasToken, setToken } from './api.js'
import { useCron } from './hooks/useCron.js'
import { formatTime } from './lib/time.js'
import CronTable from './components/CronTable.jsx'
import JobDialog from './components/JobDialog.jsx'
import ConfirmDialog from './components/ConfirmDialog.jsx'
import SettingsDialog from './components/SettingsDialog.jsx'
import SessionsPage from './components/SessionsPage.jsx'
import ProjectsPage from './components/ProjectsPage.jsx'
import LoginScreen from './components/LoginScreen.jsx'
import Toast from './components/Toast.jsx'
export default function App() {
  const [authed, setAuthed] = useState(() => hasToken())
  const [now, setNow] = useState(() => Date.now())
  const { jobs, loading, error, lastSync, reload } = useCron(null, () => setAuthed(false))
  const [toasts, setToasts] = useState([])
  const [dialog, setDialog] = useState(null) // {mode:'new'} | {mode:'edit', job}
  const [confDel, setConfDel] = useState(null) // job 待删除确认
  const [submitting, setSubmitting] = useState(false)
  const [busyId, setBusyId] = useState(null)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [tab, setTab] = useState('cron') // 'cron' | 'sessions' | 'projects'
  const [sessionsId, setSessionsId] = useState('')

  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 30000)
    return () => clearInterval(t)
  }, [])

  const push = useCallback((kind, text) => {
    const key = Math.random().toString(36).slice(2)
    setToasts((ts) => [...ts.slice(-2), { key, kind, text }])
  }, [])
  const dismissToast = useCallback((key) => setToasts((ts) => ts.filter((x) => x.key !== key)), [])

  const stats = useMemo(() => {
    const total = jobs.length
    const enabled = jobs.filter((j) => j.enabled && !(j.fail_streak > 0)).length
    const failing = jobs.filter((j) => j.fail_streak > 0).length
    const muted = jobs.filter((j) => j.mute).length
    return { total, enabled, failing, muted }
  }, [jobs])

  const withApi = useCallback(async (fn, okMsg) => {
    try {
      await fn()
      if (okMsg) push('ok', okMsg)
      reload()
    } catch (e) {
      push('err', e.message)
    }
  }, [push, reload])

  const runNow = (job) => {
    setBusyId(job.id)
    withApi(() => api.exec(job.id), `已触发「${job.id}」运行`).finally(() => setBusyId(null))
  }
  const toggleEnable = (job) => {
    setBusyId(job.id)
    withApi(() => api.patch(job.id, { enabled: !job.enabled }), job.enabled ? '已停用' : '已启用').finally(() => setBusyId(null))
  }
  const toggleMute = (job) => {
    setBusyId(job.id)
    withApi(() => api.patch(job.id, { mute: !job.mute }), job.mute ? '已取消静音' : '已静音').finally(() => setBusyId(null))
  }
  const submitJob = async (payload) => {
    setSubmitting(true)
    try {
      if (dialog.mode === 'new') {
        await api.add(payload)
        push('ok', '任务已创建')
      } else {
        await api.patch(dialog.job.id, payload)
        push('ok', '修改已保存')
      }
      setDialog(null)
      reload()
    } catch (e) {
      push('err', e.message)
    } finally {
      setSubmitting(false)
    }
  }
  const confirmDelete = async () => {
    setConfDel(null)
    setSubmitting(true)
    try {
      await api.del(confDel.id)
      push('ok', `已删除「${confDel.id}」`)
      reload()
    } catch (e) {
      push('err', e.message)
    } finally {
      setSubmitting(false)
    }
  }
  const handleLogin = (t) => {
    setToken(t)
    setAuthed(true)
    setTab('cron')
    reload()
  }
  const saveSettings = (t) => {
    setToken(t)
    setAuthed(true)
    push('ok', '设置已保存')
    reload()
  }

  const TABS = [
    { id: 'cron', label: '定时任务' },
    { id: 'sessions', label: '会话查看' },
    { id: 'projects', label: '项目管理' },
  ]
  const tabTitle = TABS.find((t) => t.id === tab)?.label || ''

  if (!authed) return <LoginScreen onLogin={handleLogin} />

  return (
    <div>
      <header className="masthead">
        <div>
          <h1>omp·lark·bridge <span className="dot">▮</span> {tabTitle}</h1>
          <div className="sub">Cron Scheduler Console</div>
        </div>
        <div className="masthead-right">
          <span className="sub">同步 {lastSync ? formatTime(lastSync) : '—'}</span>
          <button className="btn btn-ghost" style={{ color: 'var(--cream)', borderColor: 'var(--cream)' }} onClick={() => reload()}>刷新</button>
          <button className="btn btn-ghost" style={{ color: 'var(--cream)', borderColor: 'var(--cream)' }} onClick={() => setSettingsOpen(true)}>设置</button>
          {!loading && jobs.length > 0 && (
            <button className="btn btn-green" onClick={() => setDialog({ mode: 'new' })}>+ 新建任务</button>
          )}
        </div>
      </header>
      <nav className="tabs">
        {TABS.map((t) => (
          <button key={t.id} className={`tab ${tab === t.id ? 'tab-on' : ''}`} onClick={() => { setTab(t.id); if (t.id === 'sessions') setSessionsId('') }}>
            {t.label}
          </button>
        ))}
      </nav>

      {tab === 'cron' && (
      <div className="shell">
        <div className="stat-grid">
          <div className="stat"><div className="label">全部任务</div><div className="value num">{loading ? '…' : stats.total}</div></div>
          <div className="stat ok"><div className="label">正常启用</div><div className="value num">{loading ? '…' : stats.enabled}</div></div>
          <div className="stat danger"><div className="label">失败中</div><div className="value num">{loading ? '…' : stats.failing}</div></div>
          <div className="stat"><div className="label">静音</div><div className="value num">{loading ? '…' : stats.muted}</div></div>
        </div>

        {error && !loading && (
          <div className="state-block">
            <div className="big">连不上 management API</div>
            <div className="hint">{error}</div>
            <div style={{ marginTop: 16 }}>
              <button className="btn" onClick={() => setSettingsOpen(true)}>打开设置</button>
            </div>
          </div>
        )}

        <div className="section-title">任务列表</div>

        {loading ? (
          <div>
            <div className="skeleton-line" style={{ marginBottom: 10 }} />
            <div className="skeleton-line" style={{ marginBottom: 10 }} />
            <div className="skeleton-line" style={{ marginBottom: 10 }} />
          </div>
        ) : (
          <>
            {!error && jobs.length === 0 ? (
              <div className="state-block">
                <div className="big">还没有定时任务</div>
                <div className="hint">创建第一个任务：每天早上自动汇总热点、定时跑脚本……</div>
                <div style={{ marginTop: 16 }}>
                  <button className="btn btn-solid" onClick={() => setDialog({ mode: 'new' })}>+ 新建任务</button>
                </div>
              </div>
            ) : (
              <CronTable
                jobs={jobs}
                now={now}
                busy={busyId}
                onRun={runNow}
                onToggleEnable={toggleEnable}
                onToggleMute={toggleMute}
                onEdit={(job) => setDialog({ mode: 'edit', job })}
                onDelete={(job) => setConfDel(job)}
                onViewSession={(id) => { setSessionsId(id); setTab('sessions') }}
              />
            )}
          </>
        )}
      </div>
      )}

      {tab === 'sessions' && <SessionsPage initialId={sessionsId} />}

      {tab === 'projects' && <ProjectsPage onToast={push} />}

      <JobDialog
        open={!!dialog}
        mode={dialog?.mode}
        job={dialog?.job}
        onClose={() => { if (!submitting) setDialog(null) }}
        onSubmit={submitJob}
        submitting={submitting}
      />

      <ConfirmDialog
        open={!!confDel}
        title="删除任务"
        danger
        confirmLabel="删除"
        body={confDel ? <>确定要删除任务 <b className="mono">{confDel.id}</b> 吗？<br />（{confDel.description || confDel.prompt || confDel.exec || ''}）</> : null}
        onConfirm={confirmDelete}
        onCancel={() => setConfDel(null)}
      />

      <SettingsDialog open={settingsOpen} onClose={() => setSettingsOpen(false)} onSave={saveSettings} />

      <Toast items={toasts} onDismiss={dismissToast} />
    </div>
  )
}
