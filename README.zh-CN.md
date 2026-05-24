# CodexTools

[![README in English](https://img.shields.io/badge/README-English-2563eb)](./README.md)

CodexTools 是从 Codex++ 重构工作中拆出来的独立 Go + React 管理器仓库。
它把管理界面、Relay 和 Provider 配置、脚本管理、诊断、路径修复等能力放到一个可以单独构建和发布的项目里。

## 包含内容

- Go 后端：负责本地命令分发、静态资源服务和桌面式启动体验。
- React 管理器界面：面向非技术用户，入口清晰、操作集中。
- Relay 配置、历史对话修复、脚本中心、运行日志、诊断报告、修复工具。
- 独立仓库结构：不再依赖原始单体仓库路径。

## 目录结构

- `main.go`：Go 后端入口。
- `web/`：React + Vite 前端。
- `docs/`：项目介绍页和公开展示资源。

## 本地运行

```bash
npm --prefix web install
npm --prefix web run vite:build
go run .
```

## 构建

```bash
npm --prefix web run check
npm --prefix web run vite:build
go build -o codextools .
```

## 功能说明

1. 首页启动区
   把启动、连接服务、状态检查和修复入口集中到首页，减少非技术用户的判断成本。
2. 中转与 API 管理
   支持官方登录、兼容 API、协议切换、中转测试和注入辅助。
3. 界面增强控制
   管理增强功能、启动模式以及相关辅助能力。
4. 脚本中心
   支持脚本市场安装、本地启用禁用、更新和卸载。
5. 修复与诊断
   集成日志、诊断报告、路径修复和快捷方式修复。
6. 历史对话修复
   提供旧会话提供商归属修复能力，减少“记录看不见”的问题。

## 电报群

地址：`https://t.me/wanai8`

## 项目介绍页

仓库内附带英文默认、可切换中文按钮的介绍页：

- [打开项目介绍页](./docs/index.html)

仓库推送到 GitHub 后，可以直接用内置工作流把 `docs/` 发布为 GitHub Pages 页面。

## 说明

- 当前 Go 后端为了兼容已有工作流，仍保留部分面向 Codex/Codex++ 的命令命名。
- Watcher 安装与移除在当前 Go 重构版里仍未实现，README 已如实标注。
