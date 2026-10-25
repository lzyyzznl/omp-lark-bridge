import { useEffect } from 'react'

// 监听 Escape 关闭弹窗。open 为 false 或未传 onClose 时不挂监听。
// 登录页等不允许 Esc 关闭的强制层不要用它。
export function useEsc(open, onClose) {
  useEffect(() => {
    if (!open || typeof onClose !== 'function') return
    const onKey = (e) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])
}
