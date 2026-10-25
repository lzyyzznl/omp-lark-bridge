// 任务状态 → 徽章。优先失败（红）> 静音（墨）> 启用（绿）> 停用（灰）。
export function jobStatus(job) {
  if (job.fail_streak > 0) return { cls: 'fail', text: `失败 ×${job.fail_streak}` }
  if (!job.enabled) return { cls: 'off', text: '停用' }
  if (job.mute) return { cls: 'muted', text: '静音' }
  return { cls: 'on', text: '启用' }
}

export default function StatusBadge({ job }) {
  const s = jobStatus(job)
  return <span className={`badge ${s.cls}`}>{s.text}</span>
}
