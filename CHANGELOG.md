# Changelog

All notable changes to this project are documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/).

## [Unreleased]

### Changed
- 每轮对话固定输出**一张**卡片（Card 2.0 rich），思考、工具调用、工具结果与正文都在同一张卡里原地更新：
  - 写死 `card_mode = "rich"`：`config.EffectiveCardMode` 恒返回 "rich"，引擎默认 `DisplayCfg.CardMode`
    同为 rich，`[display] card_mode` 仅保留解析（值被忽略），不再有 legacy 分片路径。
  - 删掉最终回答的 MarkdownTableSplitter 溢出发送与错误文本的二次发送：卡片已带 Error 状态，
    不再重复发一条消息。
  - 超长识别 `core.RichCardBodyBudgeter`（feishu 实现为 8000 rune）：正文超预算时首卡按换行截断
    并以 `…` 收尾，剩余内容**新开一张卡**续写，循环直到发完，不丢内容也不混发纯文本。
  - 工具结果硬截断：引擎侧每行最多 `richCardToolResultMaxRunes = 300` 字符、最多保留最近
    `richCardMaxSteps = 24` 行；飞书侧每行文本截到 `maxRichCardStepTextRunes = 200`，
    每个面板只留最近 `maxRichCardStepsPerLane = 10` 行并显示"隐藏 N 步"提示。
  - 卡片体积硬保证：`buildRichCard` 在压缩面板后仍超 `maxRichCardJSONBytes = 29500` 时，
    对正文做二分截断并加 `…（内容过长，已截断）` 标记，避免飞书拒收整条消息导致回复丢失。
  - cardkit 流式更新文本请求带 `uuid` 幂等键：同一帧（同一 body）在 token 刷新重放/瞬时
    重试时复用同一个 uuid，飞书按 `200770` 去重而不重复渲染该帧。
  - 引擎侧流式推送改为**异步单飞合并**（`core.richCardPusher`，latest-value conflation）：
    `EventText` 只更新最新累计文本、永不阻塞事件循环；后台 goroutine 每 ≥150ms 推送一次
    最新快照，被取代的中间帧直接丢弃（每帧都是累计全文，不丢内容）。推送滞后恒为一个
    flush 周期 + 一次请求往返，不再随 chunk 数量串行叠加；最终帧强制 flush 后才切 Done
    卡，迟到帧不会覆盖终态。仅实现 `core.RichCardTextCapable` 且 handle 带卡片实体的
    平台启用，其余保持原同步路径。
  - 飞书打字机提速：`streaming_config` 从 40ms/4 字（约 100 字/秒）改为 20ms/8 字
    （约 400 字/秒），长回答不再在 agent 完成后继续"打字"十几秒。
  - 工具执行改为**每次调用一个独立容器**：每个工具调用渲染为带标题（工具名 + 状态，如
    `Bash · exit 1`）、描边和浅灰底的折叠面板，内部按"命令 / 状态 / 结果行"拆成一条条小容器，
    不再把所有工具行平铺在同一个面板里。最新或仍在执行的调用默认展开，更早的已完成调用
    折叠，避免长 turn 把正文挤下去；单次结果最多 6 行，其余提示"还有 N 行"。

### Fixed
- 会话页停止按钮不再只认"本页发起的那一轮"：进入页面或刷新后按会话状态
  （`running` / `asking`）显示，停止成功后立即刷新该行状态。后端停止接口也能
  停掉页面没发起过的 turn——omp resume 会 fork 新会话文件（页面持有的是旧 id）
  时按当前文件匹配；飞书 / 终端 / cron 发起的 turn 由引擎按会话文件查到 live
  session 并 `CancelTurn`，不再一律 409。
- Windows 上管理后台改配置不再需要手动重启：`daemon.RestartServiceAsync` 原先
  直接返回 `async restart unsupported on windows`，导致网页保存/扫码绑定/新建项目
  后 `restart_required` 永不生效（前端仍提示"正在自动重启生效…"），表现为新建或
  换绑应用后飞书断连。现改为写一个 `%TEMP%\omp-lark-bridge-restart.ps1` 助手：
  等 2s 让 HTTP 响应落地 → `schtasks /End` → 等 2s → `schtasks /Run`。
  助手必须不在任务的进程树里，否则 `/End` 会连它一起杀掉（计划任务把进程放进
  job，终止 job 即全灭）——主路径用 WMI `Invoke-CimMethod Win32_Process Create`
  （父进程是 WmiPrvSE，天然在树外），后备是
  `CREATE_NO_WINDOW|CREATE_BREAKAWAY_FROM_JOB` 直接派生；两者都不可用时返回错误、
  留给人手重启，绝不保留"job 内 End→Run"这种会自杀的路径。副产物：助手输出落
  `%TEMP%\omp-lark-bridge-restart.log`。仅在计划任务守护模式下生效
  （`CC_LOG_FILE` 存在），前台运行仍明确报错，避免误停任务。

### Added
- cron 任务新增 `goal_mode`（默认开启）：以 omp goal 模式执行 —— 全新会话、
  按目标自主推进，不依赖会话恢复。Web JobDialog 勾选；API `POST/GET/PATCH`
  透传；CLI `cron add --no-goal` 关闭、`cron edit <id> goal_mode true|false`、
  `cron list` 显示 `[no-goal]`。执行路径：桥在 cron 会话 enter 后发 omp RPC
  `goal`（op: create）—— omp 拒绝 `--goal` 启动参数配 rpc-ui，RPC goal 是
  桥内唯一的 per-job goal 通道（best-effort，goal.enabled 关闭时降级普通会话）。
- CLI 桥驾驶手册技能 `skills/omp-lark-bridge/SKILL.md`：沉淀 send/cron/timer/
  agents（含 `cron add --agent`）的调用姿势与坑，供 omp 会话按需取用；
  `npx skills add ./skills/omp-lark-bridge -g` 全局安装到 `~/.agents/skills`
  （omp 以 `agents:user` 发现，`/skill:omp-lark-bridge` 或 `skill://` 读取正文）。
- omp agent 人格目录 + 定时任务指派 agent：
  - Web 新增「agent 人格」页：列出三档 omp 专用子代理 —— 内置（bundled，
    `omp agents unpack` 实时物化）、自定义（user，`~/.omp/agent/agents/*.md`）、
    项目（`.omp/agents/*.md`）；可查看完整源码（frontmatter + 指令），
    并创建 / 编辑 / 删除自定义 agent（name/description/model/tools/body
    重建 markdown，prewalk/advisor 等高级 frontmatter 不在编辑范围）。
  - 管理 API：`GET /omp-agents`（目录）、`GET /omp-agents/{name}`（源码）、
    `POST /omp-agents`（创建）、`PUT|DELETE /omp-agents/{name}`（仅 user scope 可写）。
  - cron 任务新增 `agent` 字段：Web/API 指派给定 omp 专用子代理执行（如
    `scout`/`reviewer`/自定义）；指定后任务强制独立会话，omp 会话把委派指令
    注入该任务的**首条** prompt，由主会话按名派生专用子代理并汇报结果
    （omp RPC 无会话级 agent 命令，此为官方"按名使用专用子代理"路径）。
    代理自带的 model/thinking 优先于任务级覆盖。

  - CLI 补齐 agent 人格能力：`omp-lark-bridge agents list|show|new|edit|del`
    （与 Web 人格页同源后端函数，工作目录映射来自 config 的项目 work_dir；
    new/edit/del 仅允许 user scope）；`cron add` 支持 `--agent` 指派专用
    子代理，`cron list` 显示 agent 标签。
- 多项目管理（模型 A：每项目 = 独立 omp agent/工作目录 + 独立飞书应用）：
  - CLI `project list|create|show|set|remove`（`--config` 可放 root 位，子命令继承，
    不再回落默认配置污染真实 config；`project create`/`set` 支持 model，
    `remove` 需确认或 `--yes`）。
  - 管理 API `GET/PATCH/DELETE /projects/{name}`、`POST /projects/{name}/add-platform`：
    PATCH 支持 model，`GET` 详情含 model/work_dir/settings/platform_configs。
  - Web 项目管理页（两栏：项目列表 + 详情/编辑）：新建（feishu 槽位 + work_dir）、
    编辑 work_dir/model/admin_from/allow_from/disabled_commands、删除；改配置重启生效。
- 定时任务通知模式：cron 任务新增 `result_only`（web/API「仅发结果」）——
  执行过程静音（复用 mutePlatform 丢弃过程推送与开始通知），结束后把最终
  assistant 结果投递一次；与 `mute`（全静音）互斥。shell 型任务 result_only
  等效 mute。（提示：新增 type 为 omp 的项目不会破坏「项目是最大颗粒度下挂 n 个定时任务」模型。）
- omp `ask` tool dialogs now render in chat: the adapter enables
  `set_ask_dialog`, turns `extension_ui_request{method:"ask"}` into engine
  question cards (Feishu buttons; multi-select answers by numbers) and replies
  with `extension_ui_response{answers}`. The per-option `select` fallback and
  free-text answers (`customInput`) are handled too.
- Feishu Slash Command menu: the bot syncs its command list into the client's
  `/` panel via `application/v7/app_slash_commands` (create missing, patch
  changed descriptions, prune entries it no longer advertises). Needs the
  `application:app_slash_command:write`/`:read` scopes and a published app
  version; opt out with `slash_commands = false`.
- `/cron` and `/timer` are now a paged dashboard: per-job status badge, next-run
  countdown, last run, failure streak and last error, with **Run now**,
  enable/disable, mute and delete buttons plus a *this chat / all project jobs*
  scope toggle (`nav:/cron all <page>`).
- Failed scheduled jobs push an alert card into the job's chat on the 1st
  failure and every 5th consecutive one; the streak (new `fail_streak` field on
  cron/timer records) resets on success and muted jobs stay silent.
- `/retry` (alias `/regenerate`): replays the last non-command user message in
  the active session (typed command; no card button).
- Chat-list preview: settled cards set `config.summary` from the answer's first
  body line (Feishu otherwise shows `生成中` forever).
- 会话查看页改为「定时任务 → 该任务的会话」两级视图：左侧列出全部定时任务
  （显示名下会话数），选中后右侧只显示该任务执行过的会话（`CronJob` 每次
  执行累积 `session_ids`，旧任务回退 `last_session_id`），普通聊天的
  transcript 不再混入；任务名下的每个会话可展开查看完整足迹并可一键复制文本。
- 会话足迹里的工具调用重新设计（借鉴 agentsview）：`toolCall` 与其后的
  `toolResult` 按 `toolCallId` 配对，渲染为一行紧凑卡片——结果状态点
  （成功绿/失败红/运行中）+ 工具名 + 意图/参数摘要 + 结果大小，点击展开
  才显示参数与结果全文；`toolResult` 消息不再作为散落的大段纯文本输出。
- 技能（skills）重新接通，数据源改用 omp 协议：`/skills` 列出项目可解析的技能，
  `/<技能名> [参数]` 直接调用。桥通过 `omp skill list <cwd> --json` 发现技能——
  与 agent 自身加载完全一致（user + project 的 SKILL.md），随后把 `/<技能名> args`
  转发为 omp 原生技能调用 `/skill:<技能名> args`，由 omp 自行构建技能 prompt，
  桥不读技能正文。cron/timer 任务 prompt 以 `/<技能名>` 开头时同样展开。上一版
  因 omp 未实现旧 `SkillProvider` 扫描而删除的框架，现按 omp 协议重新接通
  （`SkillProvider{ListSkills}`；未实现该接口的 agent 自动隐藏 `/skills`）。
### Changed
- 技能跨机可自举：`skills/omp-lark-bridge/install.sh` 一键下载匹配本机平台
  （linux/darwin × amd64/arm64）的 GitHub Release 预编译二进制并注册进
  PATH（~/.profile/.bashrc/.zshrc），go install 为兜底；`make release-publish`
  发版上传资产。SKILL.md 增加「环境要求（首次使用先装二进制）」。

### Fixed

- `go test ./cmd/omp-lark-bridge` no longer hangs for 10 minutes:
  `captureStderr` read the pipe only after `printUsage` returned, so the usage
  text (larger than the pipe buffer) deadlocked the writer. It now drains
  concurrently — the package runs in ~1.2s. Path assertions in the core/config
  tests were also made drive- and separator-independent (Windows `C:\tmp\…`
  vs `/tmp/…`, `USERPROFILE` for the home dir).
- Commands the agent cannot serve are hidden instead of failing on click:
  `/help`, `/commands` and the platform `/` panel consult agent capabilities
  (`ContextCompressor` with a non-empty command, `SessionDeleter`,
  `ModeSwitcher`, `ReasoningEffortSwitcher`, `ProviderSwitcher`,
  `UsageReporter`, `HistoryProvider`, `ModelSwitcher`, `WorkDirSwitcher`,
  `MemoryFileProvider`), so omp's panel dropped from 43 to 36 entries and the 7
  stale ones are pruned from Feishu on the next start.

- omp conversations now survive a daemon restart. `new_session` returns only
  `{cancelled}` (omp 18.12.6), so the adapter had been persisting an empty
  `AgentSessionID` while deriving `--session-dir` from it — every restart landed
  in a fresh directory and silently began a blank conversation. Now: the store
  directory is a stable container, the id/session-file come from `get_state`,
  `CurrentSessionID()` reports the `.jsonl` path, and resume uses
  `switch_session{sessionPath}` (falling back to a fresh session when the file
  is gone). Verified across two processes with a recalled codeword.
- Feishu `/cron` 卡片每任务的 5 个按钮原挤在一行等宽（`width:"fill"`），窄屏下
  每个按钮标签溢出被飞书截断显示 "…"，看不出按钮用途。现拆为两行（立即运行 ·
  启用/停用 · 新建/复用会话 ｜ 静音/取消静音 · 删除），会话按钮文案缩短
  （"每次新建会话"→"新建会话"），所有按钮标签在手机上完整可读。

### Changed

- Chinese-only build: `ParseLanguage` resolves every `language` value to
  Simplified Chinese, `/lang` reports the fixed choice, and message lookup falls
  back requested → zh.
- Cron/timer jobs take a per-job session policy, **default reuse** — a job keeps
  running in the same chat session forever, so its history and omp session file
  stay stable. `/cron add --new-session`, `/cron session <id> <reuse|new>` and the
  card toggle switch a job to a fresh session per run.

- Feishu card chrome follows `[language]` from config: header verbs, panel
  titles (`思考`/`工具`), placeholders and done/error titles. Platforms receive
  `language` via their options map.
- Card size ceiling raised to 29500 bytes with a finer degradation ladder
  (10/120, 8/100, 6/80, 3/60) so long turns keep more tool history.

## [v0.1.0] - 2026-10-24

Initial release: a slim omp-lark-bridge bridge for Feishu/Lark ⇄ omp.

### Added

- Port `core/`, `config/`, `daemon/`, `platform/feishu/` from
  [chenhg5/cc-connect](https://github.com/chenhg5/cc-connect) (MIT).
- `agent/omp`: long-lived `omp --mode rpc` sessions — streaming events,
  steer/abort (`/stop`), session new/switch/close, `set_model` plumbing.
- omp sessions stored under `<data_dir>/omp-sessions` (kept out of `~/.omp`).
- Fixed yolo approval (`--approval-mode yolo`) — no permission prompting, no
  mode switcher.
- Slash commands, cron/timer, memory (`/memory`), model (`/model`), `/shell`.
- Daemon + CLI: `send`, `sessions`, `cron`, `timer`, `feishu setup`,
  `doctor`, `daemon`.

### Removed (vs cc-connect)

- All platforms except Feishu/Lark; all agents except omp.
- Multi-provider / model switching; permission modes; web admin console.
- Gitee update mirror (GitHub releases only).

### Retained (compat)

- `provider` CLI commands remain binary-compatible but are inert for omp
  (omp has no `ProviderSwitcher`).

### Changed

- Module path `github.com/lzyyzznl/omp-lark-bridge`; binary name `omp-lark-bridge`;
  default data dir `~/.omp-lark-bridge`.
- Branded CLI help text and version output.

[v0.1.0]: https://github.com/lzyyzznl/omp-lark-bridge/releases/tag/v0.1.0
