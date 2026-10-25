// cron 表达式解析 + 时间格式化工具（粗野看板用，全部 mono/tabular-nums）。
import parser from 'cron-parser'

// 解析合法的 5 段 cron 表达式；非法返回 null。
export function parseCron(expr) {
  if (!expr || !expr.trim()) return null
  try {
    const it = parser.parseExpression(expr.trim())
    return { next: it.next().toDate(), next5: (() => { const out = []; for (let i = 0; i < 5 && it.hasNext(); i++) out.push(it.next().toDate()); return out })() }
  } catch { return null }
}

// ±5 分钟内用相对表述，其余用绝对 "MM-DD HH:mm"。
export function formatTime(iso) {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  if (d.getFullYear() < 100) return '从未' // Go time.Time 零值 0001-01-01
  const p = (n) => String(n).padStart(2, '0')
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

export function formatCountdown(target, now = Date.now()) {
  if (!target) return '—'
  const ms = new Date(target).getTime() - now
  const min = Math.round(ms / 60000)
  if (ms < 0) return '即将'
  if (min < 1) return '不足 1 分钟'
  if (min < 60) return `${min} 分钟后`
  const h = Math.floor(min / 60)
  if (h < 24) return `${h} 小时 ${min % 60} 分后`
  const d = Math.floor(h / 24)
  return `${d} 天后 ${h % 24} 时`
}

export function formatDuration(ms) {
  if (!Number.isFinite(ms) || ms < 0) return '—'
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ${s % 60}s`
  return `${Math.floor(m / 60)}h ${m % 60}m`
}

export function humanizeCron(expr) {
  const raw = (expr || '').trim()
  const parts = raw.split(/\s+/)
  if (parts.length !== 5 || !parseCron(raw)) return raw
  const [min, hour, dom, mon, dow] = parts
  const time = hour === '*' ? '' : `${hour.padStart(2, '0')}:${min === '*' ? '00' : min.padStart(2, '0')}`
  // 分钟频率：* * * * * / */N * * * * / M * * * *
  if (hour === '*' && dom === '*' && mon === '*' && dow === '*') {
    if (min === '*') return '每分钟'
    if (min.startsWith('*/')) return `每 ${min.slice(2)} 分钟`
    return `每小时第 ${min} 分`
  }
  if (dom === '*' && mon === '*') {
    // 每天固定时刻
    if (dow === '*') return `每天 ${time}`.trim()
    // 每周几
    const dows = ['日', '一', '二', '三', '四', '五', '六']
    const dn = Number(dow)
    return `每${Number.isInteger(dn) && dn >= 0 && dn <= 6 ? '周' + dows[dn] : dow} ${time}`.trim()
  }
  return raw
}
