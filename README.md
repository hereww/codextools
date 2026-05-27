# CodexTools

[![README in Chinese](https://img.shields.io/badge/README-%E4%B8%AD%E6%96%87-0f766e)](./README.zh-CN.md)

CodexTools is a standalone Go + React desktop manager for Codex setup, launch, connection modes, UI enhancements, scripts, diagnostics, and repair workflows.
It keeps the task-oriented manager UI, relay and provider tooling, script management, local repair actions, packaged downloads, and support diagnostics in one repo that can be built and published independently.

## What it includes

- A Go backend that serves the web UI and keeps the command contract stable.
- A Gemini-inspired manager UI redesigned for non-technical users.
- Guided first-run setup, launch status, connection mode selection, relay profile management, provider sync, script market integration, logs, diagnostics, and repair tools.
- macOS and Windows desktop packages published from this repository.
- A self-contained repository layout so the manager can be built without the original monorepo.

## Repository layout

- `main.go`: binary entry point, build-time role switch, embedded assets, and shared constants.
- `manager.go`: local HTTP manager, static UI serving, command dispatch, Codex app discovery, and CCS provider import.
- `launcher.go`: silent launcher flow, Codex process startup, restart handling, and launch status updates.
- `helper.go`: helper HTTP server, local relay proxy, CORS responses, and image/text relay routing.
- `bridge.go`: Chrome DevTools Protocol integration, renderer bridge injection, and bridge request handling.
- `settings.go`: settings defaults, persistence, repository root detection, and embedded/web dist lookup.
- `relay.go`: relay profile application, auth/config status, relay file editing, and relay profile tests.
- `repair.go`: Codex config repair, plugin recovery, provider sync, SQLite/global-state maintenance, and TOML table repair helpers.
- `scripts.go`: script market loading, script install/delete, and user script inventory.
- `entrypoints.go`: desktop entry/app bundle/shortcut installation and Windows watcher support.
- `diagnostics.go`: diagnostic log writing, log tailing, and support report generation.
- `toml.go`: focused TOML string helpers used by relay and repair flows.
- `util.go`: small shared HTTP, JSON, path, argument, and type-conversion helpers.
- `types.go`: shared backend data structures.
- `desktop_darwin.go`, `desktop_other.go`: platform-specific manager window hooks.
- `web/`: React + Vite manager UI.
- `docs/`: GitHub Pages project introduction, download page, and public assets.

The Go backend is still one `package main` so release scripts can keep using `-ldflags "-X main.binaryRole=..."`, but the implementation is split by responsibility to reduce merge conflicts and make ownership clearer.

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

## Screenshots

The project introduction page now includes real manager screenshots with feature descriptions:

### Home dashboard

![CodexTools home dashboard showing launch status, connection mode, UI enhancement mode, and repair entry points](./docs/assets/screenshot-home.png)

The first screen shows whether the local setup is ready, exposes the primary launch button, and keeps connection, UI features, and repair entry points close together.

### Beginner guide

![CodexTools beginner guide showing system detection, Codex install status, CCSwitch import, mode selection, and launch steps](./docs/assets/screenshot-onboarding.png)

The guided flow checks platform and architecture, confirms Codex installation, imports CCSwitch providers, selects the connection mode, and ends at launch.

- Home dashboard: launch status, active connection, UI enhancement mode, entry paths, and key health checks.
- Beginner guide: system detection, Codex install check, CCSwitch import, mode selection, and Codex++ launch flow.
- Connection service: official login, mixed API mode, relay/API providers, CCSwitch import, and connectivity testing.
- UI features: session delete, Markdown export, project move, Timeline, plugin entry unlock, and forced plugin install controls.

Screenshot assets live in `docs/assets/` and are referenced directly by the public project page.

## Telegram community

Telegram: `https://t.me/wanai8`

## Community links

This open-source project is linked with and recognized by the LINUX DO community:

- LINUX DO: <https://linux.do/>

## Project philosophy

CodexTools is rebuilt so more people can actually use the manager. The original project created a useful foundation, but as the code, product direction, and community expectations evolved, there were differences in direction and practical reasons to explore a separate branch.

This project is that branch. It is built from my own thinking about the manager experience while keeping the open-source spirit that made the work possible. The goal is not to reject the original project, but to keep another path open for users who need a simpler, more accessible, and more actively shaped tool.

CodexTools does not accept sponsorships or donations. It is maintained as an open, community-oriented open-source project, with the code and direction kept public for anyone who wants to study, use, discuss, or fork it.

## Project origin and thanks

CodexTools is a standalone Go refactor and manager UI project based on the earlier Codex++ work.
Thanks to the original Codex++ project for the foundation, workflow ideas, and user-facing tool direction.

- Original project: <https://github.com/BigPizzaV3/CodexPlusPlus>
- Refactor source: <https://github.com/hereww/CodexPlusPlus>
- Standalone project: <https://github.com/hereww/codextools>

## Downloads

- GitHub Pages download page: [docs/downloads.html](./docs/downloads.html)
- Windows is published as setup installers for traditional Intel/AMD PCs (`windows-x64`) and Windows on ARM devices (`windows-arm64`). Portable zip builds are also attached for both architectures.
- macOS is published as installer packages for Apple Silicon (`macos-arm64`) and Intel Macs (`macos-x64`). Portable zip builds remain available as secondary artifacts.
- If macOS blocks the unsigned apps after installation, run:

```bash
xattr -cr "/Applications/Codex++ 管理工具.app"
xattr -cr "/Applications/Codex++.app"
```

## Project introduction page

The repository includes an English-first project page with a Chinese switch button:

- [Open project intro](./docs/index.html)

GitHub Pages can publish the `docs/` folder automatically through the included workflow after the repository is pushed.

## Notes

- The backend still targets the Codex/Codex++ local workflow and keeps compatibility-oriented command names where that reduces migration risk.
- Watcher install and removal are implemented for Windows. macOS shows an explicit platform limitation and keeps only the local enable/disable flag controls.
