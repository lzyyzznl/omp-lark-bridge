import { useEsc } from '../hooks/useEsc.js'

// 通用确认对话框（删除等破坏性操作）。
export default function ConfirmDialog({ open, title, body, danger, confirmLabel, onConfirm, onCancel }) {
  useEsc(open, onCancel)
  if (!open) return null
  return (
    <div className="overlay" onClick={onCancel}>
      <div className={`dialog ${danger ? 'danger' : ''}`} role="dialog" aria-modal="true" aria-label={title} onClick={(e) => e.stopPropagation()}>
        <div className="dialog-head">{title}</div>
        <div className="dialog-body">{body}</div>
        <div className="dialog-foot">
          <button className="btn btn-sm" onClick={onCancel}>取消</button>
          <button className={`btn btn-sm ${danger ? 'btn-red' : 'btn-solid'}`} onClick={onConfirm} autoFocus>
            {confirmLabel || '确认'}
          </button>
        </div>
      </div>
    </div>
  )
}
