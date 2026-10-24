# Changelog

All notable changes to this project are documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/).

## [Unreleased]

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
