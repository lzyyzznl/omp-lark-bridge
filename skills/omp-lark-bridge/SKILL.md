---
name: omp-lark-bridge
description: 本技能是 omp-lark-bridge 桥的驾驶手册 —— 桥把 omp 主会话连接到聊天平台（飞书等）。当任务需要把图片/文件/语音发给用户、创建/查看定时任务（cron/timer）、指派专用子代理（agents）或调用桥的 CLI 能力时使用。包含 send/cron/timer/agents 的调用姿势与常见坑。
---

# omp-lark-bridge 桥驾驶手册

omp-lark-bridge 是一个桥：它把 omp 主会话连接到聊天平台（如飞书），你的文字回复会自动送达用户。桥把 `CC_PROJECT` 和 `CC_SESSION_KEY` 注入环境（**仅注入给桥派发的 agent 会话**），因此**所有命令都不要重复传 `--project` / `--session-key`**。若在普通 shell 直接调用桥、报 `project is required (multiple projects configured)`，说明环境变量未注入——改用环境前缀等价注入：`CC_PROJECT=<项目名> CC_SESSION_KEY=<会话key> omp-lark-bridge <cmd>`（项目名列在 config.toml `[[projects]]`，会话 key 从 `~/.omp-lark-bridge/crons/jobs.json` 或 `sessions list` 查）。

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
- send 还支持 `--video`（视频）与 `--stdin`（从标准输入读消息，适合长文/特殊符号）；文件路径类一律用绝对路径。

## 定时任务

按需求二选一：

```
omp-lark-bridge cron add --cron "分 时 日 月 周" --prompt "..."   # 周期重复
omp-lark-bridge timer add --delay 2h --prompt "..."              # 一次性/指定未来时间
```

- `cron add` 也支持 `--exec`（shell 命令，直接执行）与 `--desc`（任务简述）。`cron add --help` 可看全量 flag（含 `--timeout-mins` / `--no-goal` 等）。
- 静音要分清两档：`--silent` 只**抑制「开始执行」通知**，运行结果仍会推送；要**完全静音**（开始+结果都不发），建好后执行 `cron edit <id> mute true`（等价 `/cron` 卡片上的「静音」按钮，job 字段 `mute: true`）。
- 想让 --exec/--prompt 任务连输出都不产生时，可把命令输出重定向到日志文件（`... >> 日志 2>&1`），这样即使结果推送也是空的。
- 定时器参数与 cron 基本一致，但静音不对称：`timer add` 直接支持 `--mute`（本次完全静音）与 `--at "2026-05-16T09:00"`（指定未来时刻）；`cron add` 只有 `--silent`，完全静音需事后 `cron edit <id> mute true`。
- `--agent <name>`：把任务指派给某专用子代理执行（如 `scout` / `reviewer` / 自定义人格）。指定后任务强制独立会话，由主会话按名派生专用子代理并汇报结果。
- 任务建好后，告知用户用 `/cron` 或 `/timer` 查看/管理。
- `cron edit <id> <field> <value>` 可改任务字段：字符串 `project` / `session_key` / `cron_expr` / `prompt` / `exec` / `work_dir` / `description` / `session_mode` / `model` / `thinking_level` / `agent`，布尔 `enabled` / `mute` / `silent` / `goal_mode`，整数 `timeout_mins`。（`result_only` 仅 Web 面板可设，edit 命令不支持。）
- 默认以 **goal 模式**执行（全新会话、按目标自主推进、不依赖会话恢复）；需要普通提示词会话时用 `cron add --no-goal`，或 `cron edit <id> goal_mode false`。

## 静默约定（NO_REPLY）

本轮无需可见回复时，在回复末尾单独一行写 `NO_REPLY`（不区分大小写）。桥会吞掉标记后的内容，不再送达用户。

## agent 人格管理（agents 命令）

与 Web「agent 人格」页同源。列出三档 omp 专用子代理（bundled 内置 / user 自定义 / project），可查看源码、创建/编辑/删除自定义人格：

### 先查可用资源，再创建

```
omp-lark-bridge agents tools                    # 可用工具白名单（--tools 取值来源，含中文说明）
omp-lark-bridge agents skills [--dir <项目目录>] # 当前项目可用技能（--skills 取值来源，默认当前目录）
omp-lark-bridge agents list                     # 列表（含 THINKING/SPAWNS）
omp-lark-bridge agents show <name>              # 查看某 agent 的完整 markdown 源码（参考既有写法）
```

### 创建 / 编辑 / 删除（user scope，所有项目可用）

```
omp-lark-bridge agents new <name> --desc "..." --model ... --thinking medium --spawns scout \
  --tools "read, bash" --skills "gh, edw-zmail" --knowledge "..." --body "指令"
omp-lark-bridge agents new <name> --from reviewer   # 从内置/自定义 fork 再改
omp-lark-bridge agents edit <name> --field value    # 未指定字段保留原值；--output/--knowledge/--body 传 "^" 清除
omp-lark-bridge agents del <name>                   # 仅 user scope 可写/删
```

### 参数值速查表（默认 = 继承主会话，除非特别说明）

| 参数 | 取值 | 说明 |
|---|---|---|
| `--desc` | 任意文本 | 一句话描述，列表/卡片显示 |
| `--model` | 如 `@slow` / `deepseek/deepseek-flash` | 空 = 继承 |
| `--tools` | 逗号分隔工具名 | 工具白名单；留空 = omp 默认集。取自 `agents tools` |
| `--thinking` | `off\|minimal\|low\|medium\|high\|xhigh\|max\|auto` | 思考强度；空 = 继承 |
| `--spawns` | 逗号分隔子代理名；`*` = 任意 | 可派生子代理白名单；空 = 不限制 |
| `--skills` | 逗号分隔技能名 | 技能白名单：勾选 = 该人格会话（含它派生的子代理）只看到这些技能；留空 = 全部技能 |
| `--output` | YAML schema（多行 / @file / -） | 结构化输出 schema |
| `--prewalk` | `true\|false\|模型模式` | 预检/探测；空 = 继承 |
| `--advisor` | `true\|false\|模型模式` | 顾问角色；空 = 继承 |
| `--blocking` | `true\|false` | 阻塞型子代理 |
| `--structured` | `true\|false` | 强制结构化输出 |
| `--read-summarize` | `true\|false` | 长文档自动摘要 |
| `--knowledge` | markdown（多行 / @file / -） | 背景知识文档 |
| `--body` | 指令正文（多行 / @file / -） | 人格 instructions |
| `--from` | agent 名 | 仅 new：从既有 agent fork |

### 创建专家（专用子代理人格）的流程

1. `agents tools` + `agents skills --dir <项目>` 先看可选工具与技能（技能名以 `agents skills` 列出的为准，含 hidden 标注）。
2. `agents show scout`（或 reviewer）参考内置人格的 frontmatter 与指令写法。
3. `agents new <名称> ...` 按需填上面表格：`--desc` 给介绍、`--model` 指定模型或留空继承、`--tools`/`--spawns`/`--skills` 收窄白名单、`--knowledge` 挂背景资料、`--body` 写角色指令。
4. `agents list` 确认创建；此后该人格即可在 Web「agent 人格」页「用专家执行」，或由 `cron`/`timer` 的 `--agent <name>` 指派（见上「定时任务」），执行时桥会把人格的技能白名单注入该会话的 `--skills`。

> 语义提示：`--skills` 是**技能白名单**（列表隔离），不是简单的「自动加载」——勾选后该专家会话的可用技能列表就只剩这几项，未勾选的技能不会出现；因此创建时只勾该专家真正需要那几个技能更能聚焦任务。

## 技能转发

`/skill:<名称> 参数...` 原样调用 omp 的某项技能（桥透传给 omp 解析技能正文，桥不读正文）。`/skills` 列出当前会话已发现的技能。
- 对 cron/timer 等独立会话任务，prompt 里直接写 `/<技能名> 参数...` 也能触发技能（桥会重写为 `/skill:` 协议）；技能名与描述以 `/skills` 列出的为准。

## 兜底

- 桥不怕命令失败重试：直接跑 `omp-lark-bridge --help` 即可；产物源码在仓库根（`make build` 生成）。

## 环境要求（首次使用先装二进制）

本技能假设 `omp-lark-bridge` 二进制在 PATH 中（agent 执行命令的 shell 看不到就报「路径不存在」）。

**新机器 / 报路径不存在时，先自动安装** —— 下载匹配本机平台的预编译二进制并加入 PATH：

```bash
cd ~/.agents/skills/omp-lark-bridge   # 或技能实际所在目录
bash install.sh                       # 需 gh 已认证（私有仓库）；go install 为兜底
```

装好后开新 shell（PATH 已写入 rc），`omp-lark-bridge --version` 确认。手动方式：源码 `make build` 后把产物软链到 `~/.local/bin`，或 `go install github.com/lzyyzznl/omp-lark-bridge/cmd/omp-lark-bridge`。
