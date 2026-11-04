// 会话历史只读视图：解析与渲染都复用 ompweb 的源码（见 src/omp/README.md），
// 所以样式、折叠行为、工具卡与 ompweb 完全一致。
// 与 ompweb 的差别只有一处：这里不发消息，只展示已经落盘的对话。

import { useCallback, useEffect, useRef, useState } from 'react'
import { getTranscriptRaw } from '../api'
import { buildSessionContext, parseSessionFile } from '../omp/lib/sessionView'
import { setLocale } from '../omp/lib/i18n'
import { TranscriptBody } from '../omp/components/TranscriptBody'
import '../omp/omp-globals.css'

const EMPTY = { loading: true, error: '', messages: [], cwd: undefined }
const LIVE_REFRESH_MS = 15000

export default function OmpTranscript({ id, live = false }) {
  const [state, setState] = useState(EMPTY)
  // 切会话时旧请求可能后到；用 ref 丢掉过期结果。
  const idRef = useRef(id)
  useEffect(() => { idRef.current = id }, [id])

  // ompweb 的文案默认按浏览器语言取；这里固定中文，和 olb 面板一致。
  useEffect(() => { setLocale('zh-CN') }, [])

  const load = useCallback(async (background) => {
    if (!background) setState(EMPTY)
    try {
      const text = await getTranscriptRaw(id)
      if (idRef.current !== id) return
      const { header, entries } = parseSessionFile(text)
      if (!header) {
        setState({ loading: false, error: '无法解析会话文件（缺少 session 头）', messages: [], cwd: undefined })
        return
      }
      const ctx = buildSessionContext(entries)
      setState({ loading: false, error: '', messages: ctx.messages, cwd: header.cwd })
    } catch (e) {
      if (idRef.current !== id) return
      setState({ loading: false, error: e.message || '读取会话失败', messages: [], cwd: undefined })
    }
  }, [id])

  useEffect(() => { void load(false) }, [load])

  // 正在跑的会话每 15 秒重读一次；组件位置不变，展开状态会保留。
  useEffect(() => {
    if (!live) return undefined
    const timer = setInterval(() => { void load(true) }, LIVE_REFRESH_MS)
    return () => clearInterval(timer)
  }, [live, load])

  return (
    <div className="omp-render omp-transcript-scroll">
      {state.loading && <div className="omp-transcript-note">读取会话…</div>}
      {state.error && <div className="omp-transcript-note is-error">{state.error}</div>}
      {!state.loading && !state.error && state.messages.length === 0 && (
        <div className="omp-transcript-note">这条会话还没有可展示的消息。</div>
      )}
      {!state.loading && !state.error && state.messages.length > 0 && (
        <TranscriptBody messages={state.messages} cwd={state.cwd} />
      )}
    </div>
  )
}
