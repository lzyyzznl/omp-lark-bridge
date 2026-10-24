# in-one-chat

Reference implementation of the **cc-connect** architecture (Go), trimmed to a
single-agent, single-platform bridge: **Feishu/Lark ⇄ omp** (the `omp` coding
harness).

## What this is

A multi-turn messaging bridge. You talk to the `omp` coding agent in Feishu;
this project:

- Receives Feishu messages over long-lived WebSocket (rich cards, inline
  actions, image/file attachments)
- Maintains **long-lived `omp --mode rpc` sessions** — one per chat, real
  streaming, steerable (`/stop`), resumable

- Runs omp at **maximum permission** (`--approval-mode yolo`) — no permission
  prompting, no mode switcher. Engineered for a personal single-user bot.
- Stores omp sessions under `<data_dir>/omp-sessions`, so your personal
  `~/.omp` store is never polluted
- Ships a practical slash-command set: sessions (`/new` `/switch` `/list`
  `/history` …), model/reasoning (`/model` …), memory (`/memory`), cron/timer,
  `/shell`, plus system-command equivalents (`send`, `sessions`, `cron`,
  `timer`, `daemon`, `doctor`)
- Runs as a resident daemon (`systemd`/`launchd`)

Architecture and engineering rules are inherited from cc-connect (MIT). See
`AGENTS.md`. License: MIT — see `LICENSE` (portions derived from
[chenhg5/cc-connect](https://github.com/chenhg5/cc-connect)).

## What was cut vs cc-connect

| Removed | Kept |
|---|---|
| All platforms except Feishu/Lark | `core/` engine, registries, i18n, cards, sessions |
| All agents except omp | Slash commands, cron/timer/heartbeat, `/model` `/memory` `/shell` |
| Multi-provider / model switching | Feishu cards, streaming preview, compact progress |
| Permission modes (`/mode` `/permit`) — fixed yolo | Daemon + CLI (send/sessions/cron/timer/…) |
| Web admin console | `doctor`, instance lock, log rotation |

## Layout

```
cmd/in-one-chat      entry point, CLI, daemon, plugin hooks
config/              TOML config parsing
core/                engine, interfaces, i18n, cards, sessions, cron
agent/omp            the omp coding harness adapter (RPC long session)
platform/feishu      Feishu/Lark platform adapter
daemon/              systemd/launchd service
```

## Prerequisites

- Go 1.25+
- `omp` CLI in PATH (install from https://omp.sh)
- A Feishu app with bot capability and the `im.message.receive_v1` event on
  **WebSocket long-connection** mode (see the setup notes in
  `config.example.toml`)

## Quick start

```bash
make build
cp config.example.toml config.toml   # fill in app_id / app_secret / work_dir
./in-one-chat --config config.toml
```

Point Feishu at the http endpoint, or bootstrap interactively:

```bash
./in-one-chat feishu setup --project my-project
```

Run as a service:

```bash
./in-one-chat daemon install --work-dir /path/to/config-dir
```

## Configuration

See `config.example.toml` for the full reference. The essential
`[[projects]]` block:

```toml
[[projects]]
name = "my-project"

[projects.agent]
type = "omp"

[projects.agent.options]
work_dir = "/path/to/your/workspace"   # where omp operates (AGENTS.md, sessions)
# model = "router"                     # optional default model (next session)
# cmd = "omp"                          # optional CLI override / extra args
# env = ["MY_VAR=value"]

[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "cli_xxx"
app_secret = "sec_xxx"
# allow_from = "*"                     # who may talk to the bot (default: all)
```

Global `data_dir` (default `~/.in-one-chat`) holds conversation history and
the `omp-sessions/` stores.

## Slash commands (in chat)

Sessions: `/new` `/switch` `/list` `/delete` `/history` `/retry`
Agent: `/model` `/reasoning` `/memory` `/shell` `/skill`
System: `/help` `/status` `/whoami` `/doctor` `/mode` no-op (fixed yolo),
plus cron/timer via `/cron` `/timer` and CLI equivalents
(`in-one-chat cron add`, `in-one-chat timer add`, `in-one-chat send -m "…"`).

Permission prompts never occur (yolo); use `/stop` to abort a running turn.

The bot speaks Simplified Chinese only: `language` is accepted but every value
(`en`, `auto`, …) renders Chinese and `/lang` no longer switches languages.

Commands that the active agent cannot serve are hidden everywhere — the `/`
panel, `/help` and `/commands` all ask the engine for capabilities before
listing an entry. omp therefore shows no `/compress` (no compaction command),
`/delete` (no `SessionDeleter`), `/mode`, `/reasoning`, `/provider`, `/usage`
or `/history`.

On Feishu the bot also syncs these commands into the client's `/` panel
(`application/v7/app_slash_commands`): grant the `application:app_slash_command:write`
and `:read` scopes, publish a new app version, and use client 7.70+. Each start
creates missing entries, patches changed descriptions and **deletes** entries the
bot no longer advertises (stale capabilities, renamed commands), so the panel
never shows dead commands. The client caches the list for a few minutes. Set
`slash_commands = false` to opt out.

## Cron & timer dashboard

`/cron` and `/timer` render a paged card (8 jobs per page) with a per-job status
line: `✅`/`❗`/`⏸`, schedule + next-run countdown, last run, consecutive-failure
streak and the last error. Buttons per job: **立即运行**, **启用/停用**,
**静音/取消静音**, **删除** and the session toggle (**每次新建会话** /
**复用本会话**); the footer toggles between *this chat only* and
*all jobs in the project*.

### Cron session policy

Each job carries `session_mode`:

| 值 | 行为 |
|---|---|
| `""` / `reuse`（默认） | 每次触发都在创建任务时的那个会话里继续——同一个任务永远是同一个会话 |
| `new_per_run` | 每次触发新建会话（触发消息落在 `<session>#cron:<id>` 侧会话里） |

Set it at creation (`/cron add --new-session 0 9 * * * 每日报告`, or the CLI's
`cron add --session-mode new-per-run`) or flip it later with
`/cron session <id> <reuse|new>` / the card button. `[cron] session_mode` in the
config file only changes the *default* for new jobs; existing jobs keep their
own value. A failing job pushes a failure card into its chat on
the 1st failure and then every 5th (`streak % 5 == 0`), so a permanently broken
job is loud once and then quiet; the streak resets on the next success. Muted
jobs stay silent.

## How the omp adapter works

`agent/omp` spawns `omp --mode rpc-ui --approval-mode yolo --session-dir
<data_dir>/omp-sessions/<session> --cwd <work_dir>` once per chat and speaks
omp's JSONL RPC protocol (v1):

- `new_session` / `switch_session` to start or resume; the resumed identity is
  the omp session *file* (`get_state.sessionFile`, since `new_session` reports
  no id), persisted as `Session.AgentSessionID` so a daemon restart continues the
  same conversation; `set_model` to apply the configured model
- `prompt` → streamed `message_update` (text/thinking deltas),
  `tool_execution_start/end`, then `prompt_result` (completed/aborted/error)
- `/stop` sends `abort`; session lifecycle (new/switch/close) maps to
  `session_settled`
- `set_ask_dialog` is enabled, so the `ask` tool's dialog arrives as one
  `extension_ui_request` per call and is rendered as chat question cards
  (buttons for single-select, numbered reply for multi-select); the answer goes
  back as `extension_ui_response{answers}` (`select` fallback and free-text
  `customInput` included)
- images/files are persisted to disk and referenced by path in the prompt

## Build & test

```bash
make build         # in-one-chat binary
make test-full     # go test -race ./...
go test ./...      # quick
```

Selective compile: `make build EXCLUDE=feishu` / `AGENTS=omp
PLATFORMS_INCLUDE=feishu` (tags `no_omp`, `no_feishu`).
