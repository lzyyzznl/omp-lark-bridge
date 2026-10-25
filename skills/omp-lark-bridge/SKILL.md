---
name: omp-lark-bridge
description: 本技能是 omp-lark-bridge 桥的驾驶手册 —— 桥把 omp 主会话连接到聊天平台（飞书等）。当任务需要把图片/文件/语音发给用户、创建/查看定时任务（cron/timer）、指派专用子代理（agents）或调用桥的 CLI 能力时使用。包含 send/cron/timer/agents 的调用姿势与常见坑。
---

# omp-lark-bridge 桥驾驶手册

omp-lark-bridge 是一个桥：它把 omp 主会话连接到聊天平台（如飞书），你的文字回复会自动送达用户。桥把 `CC_PROJECT` 和 `CC_SESSION_KEY` 注入环境，因此**所有命令都不要重复传 `--project` / `--session-key`**。

## 发送文件 / 图片 / 语音

需要把生成的图片、文件或语音发给用户时，用 `send` 子命令：

```
omp-lark-bridge send --image /绝对路径.png
omp-lark-bridge send --file /绝对路径.zip
omp-lark-bridge send --audio /绝对路径.mp3
omp-lark-bridge send --tts "要朗读的文字"
```

- `--tts`：把文字转语音发送；`--tts` / `--audio` 成功后，若无必要文字确认，直接回复 `NO_REPLY` 即可。
- `--image` / `--file` 路径请用绝对路径，避免会话 work_dir 与文件所在目录不一致导致找不到文件。

## 定时任务

按需求二选一：

```
omp-lark-bridge cron add --cron "分 时 日 月 周" --prompt "..."   # 周期重复
omp-lark-bridge timer add --delay 2h --prompt "..."              # 一次性/指定未来时间
```

- `cron add` 也支持 `--exec`（shell 命令，直接执行）与 `--desc`（任务简述）。
- `--agent <name>`：把任务指派给某专用子代理执行（如 `scout` / `reviewer` / 自定义人格）。指定后任务强制独立会话，由主会话按名派生专用子代理并汇报结果。
- 任务建好后，告知用户用 `/cron` 或 `/timer` 查看/管理。
- `cron edit <id> <field> <value>` 可改任务字段（含 `agent` / `model` / `thinking_level` / `goal_mode`）。
- 默认以 **goal 模式**执行（全新会话、按目标自主推进、不依赖会话恢复）；需要普通提示词会话时用 `cron add --no-goal`，或 `cron edit <id> goal_mode false`。

## 静默约定（NO_REPLY）

本轮无需可见回复时，在回复末尾单独一行写 `NO_REPLY`（不区分大小写）。桥会吞掉标记后的内容，不再送达用户。

## agent 人格管理（agents 命令）

与 Web「agent 人格」页同源。列出三档 omp 专用子代理（bundled 内置 / user 自定义 / project），可查看源码、创建/编辑/删除自定义人格：

```
omp-lark-bridge agents list                 # 列表（含 THINKING/SPAWNS）
omp-lark-bridge agents show <name>          # 查看某个 agent 的完整 markdown 源码
omp-lark-bridge agents new <name> --desc "..." --model ... --thinking medium --spawns scout --body "指令"
omp-lark-bridge agents edit <name> --desc "..."   # 未指定字段保留原值
omp-lark-bridge agents del <name>           # 仅 user scope 可写/删
```

## 技能转发

`/skill:<名称> 参数...` 原样调用 omp 的某项技能（桥透传给 omp 解析技能正文，桥不读正文）。`/skills` 列出当前会话已发现的技能。

## 兜底

- 桥的命令全量列表：直接跑 `omp-lark-bridge --help`；各子命令 `omp-lark-bridge <命令> --help`。
- 桥不怕命令失败重试：编译产物在仓库根 `omp-lark-bridge`（`make build` 后生成），PATH 里也可直接调用。
