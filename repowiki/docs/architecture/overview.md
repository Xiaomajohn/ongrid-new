---
generated_by: repo-wiki-agent
baseline_commit: "47bad98d46a1d70231d237285784a8192d7756c7"
last_updated: "2026-07-06"
managed_sections:
  - "## System Overview"
  - "## Technology Stack"
  - "## Key Components"
  - "## Entrypoints"
---

# 整体架构

<!-- BEGIN:REPO_WIKI_MANAGED -->
## System Overview

`ongrid-new` 是一个面向边缘节点纳管与运维的全栈平台，包含：

- **控制面**：`internal/manager` + `internal/iam` — 提供 Web API、gRPC 服务、告警/通知/指标/AIOps
- **边缘 Agent**：`internal/edgeagent` — 部署在被纳管主机上，执行指标采集、命令执行、文件管理、服务重启等
- **前端**：`web/` — React + TypeScript SPA
- **通信**：`api/tunnel/v1/tunnel.proto` — 控制面与边缘节点之间的隧道
- **部署**：`deploy/docker-compose.yml` + `deploy/install/install.sh` 一键安装脚本

## Technology Stack

- **后端**：Go（见 `.tool-versions`）+ Kratos / Hertz / gRPC
- **前端**：React 18 + TypeScript + Vite + Tailwind CSS
- **API**：protobuf + gRPC（`api/`）+ HTTP/REST
- **数据**：MySQL（主存储）+ Redis（缓存/锁）
- **可观测性**：Prometheus + Grafana + Loki + Tempo（OTel）
- **部署**：Docker Compose + systemd

## Key Components

| 组件 | 路径 | 角色 |
|---|---|---|
| ongrid | `cmd/ongrid/` | 控制面入口 |
| ongrid-edge | `cmd/ongrid-edge/` | 边缘 Agent 入口 |
| Manager | `internal/manager/` | 控制面业务（多模块） |
| IAM | `internal/iam/` | 身份与权限 |
| Edge Agent | `internal/edgeagent/` | 边缘能力实现 |
| 共享包 | `internal/pkg/` | 通用基础库 |

## Entrypoints

- 后端：`cmd/ongrid/main.go`
- 边缘：`cmd/ongrid-edge/main.go`
- 前端构建入口：`web/src/main.tsx`

<!-- END:REPO_WIKI_MANAGED -->

## Design Decisions

详见 [ADR 索引](../adr/index.md)。