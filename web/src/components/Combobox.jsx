import { useEffect, useRef, useState } from 'react'

// 通用「下拉选 + 自由输入」组件：聚焦展开选项列表，支持关键字过滤、
// 方向键选择、Enter 确认、Esc 关闭；仍可自由输入任意值（不受限于 options）。
export default function Combobox({ value, onChange, options = [], placeholder, className = '', disabled, readOnly, ariaLabel, emptyText = '无匹配项' }) {
  const [open, setOpen] = useState(false)
  const [hi, setHi] = useState(0)
  const wrapRef = useRef(null)

  const q = (value || '').toLowerCase()
  const filtered = options.filter((o) => (o || '').toLowerCase().includes(q))

  useEffect(() => {
    if (!open) return
    const onDoc = (e) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target)) setOpen(false)
    }
    document.addEventListener('mousedown', onDoc)
    return () => document.removeEventListener('mousedown', onDoc)
  }, [open])

  // 列表滚动到高亮项，避免方向键移出可视区
  useEffect(() => {
    if (!open) return
    const el = wrapRef.current?.querySelector('.combobox-item-hi')
    el?.scrollIntoView({ block: 'nearest' })
  }, [hi, open])

  const pick = (o) => { onChange(o); setOpen(false) }

  return (
    <div className="combobox" ref={wrapRef}>
      <input
        aria-label={ariaLabel}
        className={`input ${className}`}
        value={value}
        disabled={disabled}
        readOnly={readOnly}
        placeholder={placeholder}
        spellCheck={false}
        onFocus={() => { setOpen(true); setHi(0) }}
        onChange={(e) => { onChange(e.target.value); setOpen(true); setHi(0) }}
        onKeyDown={(e) => {
          if (e.key === 'Escape') { setOpen(false); return }
          if (e.key === 'ArrowDown') { e.preventDefault(); setHi((h) => Math.min(h + 1, filtered.length - 1)); return }
          if (e.key === 'ArrowUp') { e.preventDefault(); setHi((h) => Math.max(h - 1, 0)); return }
          if (e.key === 'Enter' && open && filtered[hi]) { e.preventDefault(); pick(filtered[hi]) }
        }}
      />
      {open && (
        <ul className="combobox-list" role="listbox">
          {filtered.length === 0 && <li className="combobox-empty">{emptyText}</li>}
          {filtered.map((o, i) => (
            <li
              key={o}
              role="option"
              aria-selected={i === hi}
              className={`combobox-item ${i === hi ? 'combobox-item-hi' : ''}`}
              onMouseEnter={() => setHi(i)}
              onMouseDown={(e) => { e.preventDefault(); pick(o) }}
            >{o}</li>
          ))}
        </ul>
      )}
    </div>
  )
}
