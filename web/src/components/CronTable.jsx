import StatusBadge from './StatusBadge.jsx'
import { formatTime, formatCountdown, humanizeCron, parseCron } from '../lib/time.js'

const MAX_DESC = 46

function trunc(s, n) {
  if (!s) return ''
  return s.length > n ? s.slice(0, n - 1) + '…' : s
}

function JobSummary(job) {
  if (job.description) return job.description
  if (job.exec) return `$ ${job.exec}`
  if (job.prompt) return job.prompt
  return job.id
}

export default function CronTable({ jobs, now, busy, onRun, onToggleEnable, onToggleMute, onEdit, onDelete, onViewSession }) {
  if (jobs.length === 0) return null
  return (
    <div className="table-wrap">
      <table className="cron">
        <thead>
          <tr>
            <th>状态</th>
            <th>任务</th>
            <th>调度</th>
            <th>下次运行</th>
            <th>上次运行</th>
            <th>静音</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          {jobs.map((j) => {
            const d = j.last_error ? j.last_error : ''
            const cr = parseCron(j.cron_expr)
            const next = cr ? cr.next : null
            return (
              <tr key={j.id}>
                <td><StatusBadge job={j} /></td>
                <td>
                  <button className="job-desc job-title" disabled={busy === j.id} onClick={() => onEdit(j)}
                    title="查看详情 / 编辑">{trunc(JobSummary(j), MAX_DESC)}</button>
                  <div className="job-meta mono">{j.id}{j.exec ? ' · exec' : ''}{j.model ? ` · ${j.model}` : ''}{j.thinking_level ? ` · 思考:${j.thinking_level}` : ''}</div>
                </td>
                <td>
                  <div className="mono num">{j.cron_expr || '—'}</div>
                  <div className="job-meta">{humanizeCron(j.cron_expr)}</div>
                </td>
                <td className="mono num">
                  {next ? `${formatCountdown(next, now)} · ${formatTime(next)}` : '—'}
                </td>
                <td className="mono num" title={d} style={{ maxWidth: 220, overflowWrap: 'anywhere' }}>
                  {formatTime(j.last_run)}
                  {j.fail_streak > 0 && d ? <div className="job-meta err">{trunc(d, 40)}</div> : null}
                </td>
                <td className="mono num">{j.mute ? '是' : '否'}</td>
                <td>
                  <div className="actions">
                    <button className="btn btn-sm btn-green" disabled={busy === j.id} onClick={() => onRun(j)} title="立即运行">运行</button>
                    <button className="btn btn-sm" disabled={busy === j.id} onClick={() => onToggleEnable(j)} title={j.enabled ? '停用' : '启用'}>
                      {j.enabled ? '停用' : '启用'}
                    </button>
                    <button className="btn btn-sm" disabled={busy === j.id} onClick={() => onToggleMute(j)} title={j.mute ? '取消静音' : '静音'}>
                      {j.mute ? '取消静音' : '静音'}
                    </button>
                    <button className="btn btn-sm" disabled={busy === j.id} onClick={() => onEdit(j)}>编辑</button>
                    <button className="btn btn-sm" title="查看该任务执行过的会话（定时任务会话页）" onClick={() => onViewSession(j.id)}>会话</button>
                    <button className="btn btn-sm btn-red" disabled={busy === j.id} onClick={() => onDelete(j)}>删除</button>
                  </div>
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
