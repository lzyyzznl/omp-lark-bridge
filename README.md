# omp-lark-bridge

> **在飞书里,直接指挥最强的开源编码智能体。**
> 把 omp 接到飞书/Lark —— 随时随地,一句话干活:改代码、跑任务、定时巡检、拉周报。实时流式输出,全程无需打开终端。

![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)
![License](https://img.shields.io/badge/License-MIT-brightgreen)
![Platform](https://img.shields.io/badge/Linux%20%7C%20macOS-✓-blue)
![Release](https://img.shields.io/github/v/release/lzyyzznl/omp-lark-bridge)

---

## 一句话

omp-lark-bridge 是一个**消息桥**:让 omp(那个极强力的编码 agent harness)住进你的飞书。你像发消息一样使用它 —— 不用开终端、不用管环境,它自己带着完整的 AGENTS.md、技能、会话记忆在工作。

## 能做什么

- **实时流式对话**:长连接 WebSocket,回复像聊天一样一行行流出来,不是等一整屏。
- **永不丢失的会话**:每个聊天一个 `omp --mode rpc` 长会话,可 `在新/旧会话间切换`,重启服务也能接着聊(会话文件持久化,重启续上同一对话)。
- **yolo 全权限**:`--approval-mode yolo` 直接干,不打断你。`.omp` 会话目录隔离,不污染本机日常环境。
- **定时任务全家桶**:`/cron` `/timer` 分页看板 —— 定时巡检、日报草稿、定时发图,一键 `立即运行`。新任务**默认以 goal 模式执行**(RPC `goal create`):按目标自主推进,不靠聊天恢复。
- **指派专用子代理**:内置 `scout` / `reviewer` / 自定义人格,让正确的"员工"干正确的活。
- **技能即命令**:`/skills` 列出会话可用技能,每个技能都是一个斜杠命令 —— `/技能名 参数` 原样转给 omp 原生技能协议。
- **多项目隔离**:每项目 = 独立 omp agent + 工作目录 + 独立飞书应用,配置、会话、任务互不干扰。
- **后台常驻**:一键 `daemon install`,systemd / launchd 守护,开机自启,日志自动轮转。

## 长这样

```mermaid
flowchart LR
    U[飞书 / Lark 聊天框] <-->|WebSocket 长连接<br/>实时双向流式| B[omp-lark-bridge]
    subgraph B[omp-lark-bridge]
        C[core/ 引擎 · 会话 · slash 命令 · cron/timer]
        D[技能 · 子代理人格 · 多项目管理]
    end
    B <-->|JSONL RPC v1<br/>每聊天一会话 · 可恢复| O[omp --mode rpc-ui<br/>yolo 全权限]
```

## 快速开始

```bash
make build                            # 编译出 omp-lark-bridge
./omp-lark-bridge feishu setup --project my-project   # 交互式完成飞书应用接入
```

或纯配置文件流:

```bash
cp config.example.toml config.toml    # 填 app_id / app_secret / work_dir
./omp-lark-bridge --config config.toml
./omp-lark-bridge daemon install --work-dir .
```

> 还没装二进制?技能目录自带一键安装:`bash install.sh` 自动下载匹配
> 平台(linux/darwin × amd64/arm64)的 Release 预编译产物并注册进 PATH —— 新机器秒装。

## 在聊天框里能玩什么

| 类 | 命令 |
|---|---|
| 会话 | `/new` `/switch` `/list` `/history` `/delete` `/retry` |
| 智能体 | `/model` `/memory` `/shell` `/skills` |
| 定时 | `/cron` `/timer`(分页看板 + 立即运行/静音/删除) |
| 系统 | `/help` `/status` `/doctor`、`send` / `sessions` / `cron` 等 CLI 等价物 |

权限打断永不出现(yolo);想停就用 `/stop`。

## 飞书端细节

- 富文本卡片、inline 按钮、图片/文件、流式预览、紧凑进度
- `/` 面板自动同步可用命令(`application/v7/app_slash_commands`),过期命令自动清理
- 对话默认简体中文;agent 无法提供的命令(`/` 面板、`/help`、`/commands`)一律隐藏

## Cron & timer 看板

`/cron` `/timer` 渲染分页卡片(每页 8 条):状态徽标(`✅`/`❗`/`⏸`)、下次运行倒计时、上次运行、连续失败次数与最后错误。按钮分两行排布:行 1 **立即运行 · 启用/停用 · 新建会话/复用会话**,行 2 **静音/取消静音 · 删除**;底部在 *仅本聊天* 与 *项目全部任务* 之间切换。

### 会话策略(goal 模式默认开启)

每个任务携带 `goal_mode`(默认开):开启 = 以 omp goal 模式执行,新任务强制全新会话、按目标自主推进,执行前经 omp RPC `goal create` 建目标——`--no-goal` 关闭、`cron edit <id> goal_mode false` 改回。还需要 `session_mode`:

| 值 | 行为 |
|---|---|
| `""` / `reuse`(默认) | 每次触发都在创建任务时的那个会话里继续 —— 同一任务永远是同一会话 |
| `new_per_run` | 每次触发新建会话(触发消息落在 `<session>#cron:<id>` 侧会话) |

创建时指定(`/cron add --new-session 0 9 * * * 每日报告`,或 CLI `cron add --session-mode new-per-run`),之后用 `/cron session <id> <reuse\|new>` 或卡片按钮改。失败任务在**首次失败**及**每第 5 次连续失败**时推送失败卡片,成功后归零;静音任务保持安静。

## 系统提示注入

每个 live omp 会话以 `--append-system-prompt` 注入一份精简的桥能力简报(每会话仅一次),中文,告诉 agent 用 `omp-lark-bridge send` / `cron` / `timer` 与 `/skill:<name>` 调用能力;不会重复技能列表(omp 自己会发现)。每项目可用 `append_system_prompt = "…"` 覆盖,设为空串则完全禁用注入。

桥同时把自身目录前置到子进程 PATH(`prependSelfDirToPath`),所以即使桥不在系统 PATH,agent 也能解析 `omp-lark-bridge`(linux / windows x64 通用)。

## 它是怎么工作的

`agent/omp` 为每个聊天拉起 `omp --mode rpc-ui --approval-mode yolo --session-dir <data>/omp-sessions/<session> --cwd <work_dir>`,说 omp 的 JSONL RPC 协议(v1):

- `new_session` / `switch_session` 开始或恢复;恢复身份 = omp 会话 *文件*(`get_state.sessionFile`),存为 `Session.AgentSessionID`,守护进程重启后继同一对话;`set_model` 应用配置模型
- `prompt` → 流式 `message_update`(文本/思考增量)、`tool_execution_start/end`,再 `prompt_result`(completed/aborted/error)
- `/stop` 发 `abort`;会话生命周期(new/switch/close)映射到 `session_settled`
- `set_ask_dialog` 开启,`ask` 工具的对话框以 `extension_ui_request` 到达,渲染成聊天问题卡片(单选按钮、多选按数字回复),答复以 `extension_ui_response{answers}` 回写(含 `select` 兜底与自由文本 `customInput`)
- 图片/文件落盘,以路径形式进 prompt

### goal 模式执行路径

omp 拒绝 `--goal` 启动参数配 `--mode rpc-ui`;桥因此在会话启动后发 RPC `goal`(op: `create`, objective = 任务 prompt)—— 与交互式 `/goal` 同一生命周期,但**best-effort**:若 omp `goal.enabled` 关闭或已有活跃目标,`create` 失败仅告警,任务降级为普通会话继续跑,不会挂起。

## 项目结构

```
cmd/omp-lark-bridge      入口 · CLI · daemon · 插件注册
core/                   引擎 · 接口 · i18n · 卡片 · 会话 · cron/timer
agent/omp               omp harness 适配器(RPC 长会话 · goal 通道)
platform/feishu         飞书/Lark 平台适配器
daemon/                 systemd / launchd 服务
```

- **内核干净**:`core/` 不 import 任何 agent/platform;agent 与平台经注册表 `RegisterAgent` / `RegisterPlatform` 插件式接入 —— 加新平台/新 agent 不动内核。
- **可裁剪编译**:`make build EXCLUDE=feishu` 或 `AGENTS=omp PLATFORMS_INCLUDE=feishu`(tags `no_omp` / `no_feishu`)。
- **用户视角测试**:`core/cuj_test.go` 的 Critical User Journey 测试从"用户看到什么"出发,保证真流程不坏。

## 构建与测试

```bash
make build         # 编译 omp-lark-bridge
make test-full     # go test -race ./...
go test ./...      # 快速验证
make release-publish   # 全平台产物发布到 GitHub Release(新机器 install.sh 自动拉取)
```

## 授权

MIT License,见 [LICENSE](./LICENSE)。本项目派生自
[chenhg5/cc-connect](https://github.com/chenhg5/cc-connect)(MIT License)——
原作者的版权与许可声明已保留。

## 致谢

本项目站在 [chenhg5/cc-connect](https://github.com/chenhg5/cc-connect) 的肩膀上——
一个把编码 agent 接入 IM 的开源桥梁。感谢 **chenhg5** 及其贡献者:引擎内核、
可插拔的平台/agent 架构、卡片与会话设计,都让 omp-lark-bridge 得以快速落地。
Kudos, cc-connect ❤️
