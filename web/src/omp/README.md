# src/omp —— ompweb 渲染栈副本

会话历史的解析与渲染直接用 [ompweb](https://www.npmjs.com/package/@kahme247/ompweb) 的源码，
不重写一份：`MessageView`、`MarkdownBody`、工具卡/任务面板/diff、折叠规则、
`globals.css` 都来自它，所以两边看到的会话长得一样。

## 文件来源

| 这里 | 来源（ompweb 仓库根） |
| --- | --- |
| `components/**` | `components/**`（`MessageView*`、`MarkdownBody`、`MarkdownCode`、`MermaidBlock`、`ImageLightbox`、`TableChart`、`SubagentStatusIcon`、`MessageCopyActions`、`ui/primitives`、`ui/field`） |
| `hooks/**` | `hooks/**`（`useCopyFeedback`、`useSpeechSynthesis`、`useTheme`） |
| `lib/**` | `lib/**`（`types`、`chat-transcript-plan`、`message-display`、`ansi`、`markdown`、`i18n` … 全是纯浏览器代码） |
| `fonts/**` | `app/fonts/**` |
| `omp-globals.css` | `app/globals.css`（转换见下） |

`@/` 在 `vite.config.js` 里被指向 `src/omp`，所以副本内部 import 不用改。

## 本仓库自己加的两个文件

- `lib/sessionView.ts`：从 ompweb 的 `lib/session-reader.ts` + `lib/omp/session-files.ts`
  中裁出的**纯逻辑**（去掉了 fs 部分）：JSONL 解析、v1/v2→v3 迁移、leaf→root 分支选择、
  compaction 折叠、entry → UI message。olb 后端把 jsonl 原文直接发给浏览器
  （`GET /api/v1/transcripts/{id}?raw=1`），所以这里不需要读文件。
- `components/TranscriptBody.tsx`：`ChatWindow` 里只读的那部分（turn 分组 +
  「过程详情」折叠），去掉了流式、搜索、编辑/分叉、窗口化。

## omp-globals.css 的转换

`app/globals.css` 原本是 Next/Tailwind 的全局表，导入了三项改动：

1. 删 `@import "tailwindcss"`、`@source*`、`@theme {}`（组件里零 Tailwind utility，`var(--color-*)` 也无人使用）。
2. 展开 `@layer base {}`。
3. 全局选择器收进容器作用域：`:root` / `html.omp` → `.omp-render`，`html[data-*]` → `.omp-render[data-*]`；
   深色主题块（`omp.sh midnight`）→ `.omp-render[data-theme="dark"]`。

所以页面里只需给容器加 `class="omp-render"`；想切深色就再加 `data-theme="dark"`。

## 升级 ompweb 时

按上表把对应文件重新复制一遍（保留 `lib/sessionView.ts`、`components/TranscriptBody.tsx`），
CSS 重新跑一遍上面三项转换即可。
