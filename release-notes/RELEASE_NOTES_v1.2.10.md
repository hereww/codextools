## CodexTools 1.2.10

### Changes

- Adds configurable proxy routing by request purpose, covering relay traffic, Remote Control, official sign-in, realtime voice, model catalog, audio, VLM, and Stepwise without changing system proxy settings.
- Preserves provider-specific proxy precedence and direct-connection bypass rules, and improves proxy configuration and relay-mode status reporting.
- Adds VLM profile testing with image preview, bounded uploads and responses, proxy support, and API-key redaction.
- Adds GPT-6 Astra model-catalog compatibility and per-model context-window and automatic-compaction settings.
- Hardens TOML configuration merging so provider repair and synchronization preserve unrelated settings.
- Adds regression coverage for proxy routing, relay modes, model metadata, TOML semantics, and vision-model testing.

### macOS unsigned build notice

The macOS packages are unsigned community builds, including the pkg installers. If macOS blocks the first launch, run:

```bash
xattr -cr "/Applications/ChatGPT Codex 管理工具.app"
xattr -cr "/Applications/ChatGPT Codex.app"
xattr -cr "/Applications/Codex++ 管理工具.app"
xattr -cr "/Applications/Codex++.app"
```

### macOS 首次启动提醒

macOS 包是未签名的社区构建，pkg 安装包也一样。如果 macOS 阻止首次启动，请执行：

```bash
xattr -cr "/Applications/ChatGPT Codex 管理工具.app"
xattr -cr "/Applications/ChatGPT Codex.app"
xattr -cr "/Applications/Codex++ 管理工具.app"
xattr -cr "/Applications/Codex++.app"
```
