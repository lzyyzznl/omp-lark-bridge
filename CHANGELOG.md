# Changelog

All notable changes to this project are documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/).

## [Unreleased]

### Added

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

### Fixed

- `go test ./cmd/in-one-chat` no longer hangs for 10 minutes:
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

Initial release: a slim cc-connect bridge for Feishu/Lark ⇄ omp.

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

- Module path `github.com/lzyyzznl/in-one-chat`; binary name `in-one-chat`;
  default data dir `~/.in-one-chat`.
- Branded CLI help text and version output.

[v0.1.0]: https://github.com/lzyyzznl/in-one-chat/releases/tag/v0.1.0
