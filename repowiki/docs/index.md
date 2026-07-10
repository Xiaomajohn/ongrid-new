---
generated_by: repo-wiki-agent
baseline_commit: "47bad98d46a1d70231d237285784a8192d7756c7"
last_updated: "2026-07-06"
managed_sections:
  - "## Overview"
  - "## Quick Links"
  - "## Components"
---

# ongrid-new Documentation

欢迎来到 `ongrid-new` 仓库 Wiki。

<!-- BEGIN:REPO_WIKI_MANAGED -->
## Overview

本文档由 repo-wiki agent 自动生成与维护，所有技术陈述都附带可追溯到源码的引用（文件路径 + 行号）。

- **仓库名**：ongrid-new
- **基线 commit**：`47bad98d46a1d70231d237285784a8192d7756c7`
- **最近更新**：2026-07-06
- **远程仓库**：<https://github.com/Xiaomajohn/ongrid-new.git>

## Quick Links

- [本地开发](getting-started/local-dev.md) — 搭建开发环境
- [构建与运行](getting-started/build-and-run.md) — Makefile 与常用命令
- [配置说明](getting-started/configuration.md) — 环境变量与配置文件
- [整体架构](architecture/overview.md) — 系统设计
- [数据流](architecture/data-flow.md) — 前后端调用链
- [后端服务](components/backend.md) — Go 后端组件
- [Edge Agent](components/edge-agent.md) — 边缘节点 Agent
- [前端](components/frontend.md) — React + TS 前端
- [运维](operations/build-and-test.md) — 构建、测试、部署
- [API 参考](api/grpc-services.md) — gRPC 服务清单

## Components

本仓库采用 monorepo 布局，核心域：

| 域 | 路径 | 说明 |
|---|---|---|
| Manager | `internal/manager/` | 控制面服务 |
| IAM | `internal/iam/` | 身份认证与权限 |
| Edge Agent | `internal/edgeagent/` | 边缘节点 Agent |
| 前端 | `web/` | React + TypeScript |
| API 定义 | `api/` | protobuf 定义 |
| 共享包 | `internal/pkg/` | 通用基础库 |
| 部署 | `deploy/` | Docker Compose 与脚本 |

<!-- END:REPO_WIKI_MANAGED -->

## About This Documentation

本文档由 repo-wiki agent skill 维护。技术陈述附带源码引用，可点击跳转验证。Managed blocks 由 agent 维护，块外的人工编辑会被保留。