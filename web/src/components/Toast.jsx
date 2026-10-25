import { useEffect } from 'react'

// 轻量 toast：transition 而非 keyframes（可中断、堆叠不闪烁）。
export default function Toast({ items, onDismiss }) {
  useEffect(() => {
    if (items.length === 0) return
    const t = setTimeout(() => onDismiss(items[0].key), 3500)
    return () => clearTimeout(t)
  }, [items, onDismiss])

  return (
    <div className="toast-wrap" role="status" aria-live="polite">
      {items.map((it) => (
        <div key={it.key} className={`toast ${it.kind}`}>
          {it.text}
        </div>
      ))}
    </div>
  )
}
