# CodexTools

[![README in Chinese](https://img.shields.io/badge/README-%E4%B8%AD%E6%96%87-0f766e)](./README.zh-CN.md)

CodexTools is a standalone Go + React manager extracted from the Codex++ refactor work.
It keeps the task-oriented manager UI, the relay and provider tooling, script management, diagnostics, and repair workflows in one repo that can be built and published independently.

## What it includes

- A Go backend that serves the web UI and keeps the command contract stable.
- A Gemini-inspired manager UI redesigned for non-technical users.
- Relay profile management, provider sync, script market integration, logs, diagnostics, and repair tools.
- A self-contained repository layout so the manager can be built without the original monorepo.

## Repository layout

- `main.go`: Go backend and local HTTP shell.
- `web/`: React + Vite manager UI.
- `docs/`: project introduction page and public assets.

## Run locally

```bash
npm --prefix web install
npm --prefix web run vite:build
go run .
```

## Build

```bash
npm --prefix web run check
npm --prefix web run vite:build
go build -o codextools .
```

## Feature overview

1. Simple launch surface
   The home screen exposes only the actions a normal user needs first: launch, connect service, inspect status, and repair paths.
2. Relay and API management
   Profiles support official login, compatible API mode, protocol switching, relay testing, and injection helpers.
3. UI enhancement controls
   The manager keeps feature toggles, launch mode selection, and script-related tooling in one place.
4. Script center
   Users can install, enable, disable, update, and remove user scripts without editing config files manually.
5. Recovery and diagnostics
   Built-in logs, diagnostics output, path repair, and shortcut repair reduce support friction.
6. Legacy conversation repair
   Provider sync tools help recover visibility for older local conversations.

## Telegram community

Telegram: `https://t.me/wanai8`

## Project introduction page

The repository includes an English-first project page with a Chinese switch button:

- [Open project intro](./docs/index.html)

GitHub Pages can publish the `docs/` folder automatically through the included workflow after the repository is pushed.

## Notes

- The backend still targets the Codex/Codex++ local workflow and keeps compatibility-oriented command names where that reduces migration risk.
- Watcher install and removal remain marked as not implemented in the Go backend, matching the current refactor state.
