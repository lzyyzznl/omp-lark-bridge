# in-one-chat

Reference implementation of the **cc-connect** architecture (Go), trimmed to a single-agent, single-platform bridge: **Feishu/Lark ⇄ omp** (the `omp` coding harness).

## What this is

A multi-turn messaging bridge. You talk to the `omp` coding agent in Feishu; this project:

- Receives Feishu messages (long-lived WebSocket, card messages, inline actions)
- Maintains long-lived `omp --mode rpc` sessions (per chat, per workdir)
- Exposes a practical slash-command set: sessions, model/reasoning, memory, cron/timer, `/shell`
- Runs as a resident daemon, config via `config.toml`

Architecture and engineering rules are inherited from cc-connect (MIT). See `AGENTS.md`.

## Layout

```
cmd/in-one-chat      entry point, CLI, daemon, plugin hooks
config/              TOML config parsing
core/                engine, interfaces, i18n, cards, sessions, cron
agent/omp            the omp coding harness adapter (RPC long session)
platform/feishu      Feishu/Lark platform adapter
daemon/              systemd/launchd service
```

## Quick start

```bash
make build
./in-one-chat --config config.toml
```

See `config.example.toml` for the full configuration reference.
