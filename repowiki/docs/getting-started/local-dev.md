---
generated_by: repo-wiki-agent
baseline_commit: "47bad98d46a1d70231d237285784a8192d7756c7"
last_updated: "2026-07-06"
managed_sections:
  - "## Prerequisites"
  - "## First Run"
---

# 本地开发

<!-- BEGIN:REPO_WIKI_MANAGED -->
## Prerequisites

搭建本地开发环境所需工具：

- Go（见 `.tool-versions`）
- Node.js + pnpm（前端依赖）
- Docker + Docker Compose（启动依赖的 MySQL / Redis / Prometheus 等）
- `uv`（Python 包管理器，repo-wiki 工具链使用）
- `make`、`git`

## First Run

1. 克隆仓库：`git clone https://github.com/Xiaomajohn/ongrid-new.git`
2. 启动依赖服务：`cd deploy && docker compose up -d`
3. 后端构建：`make build`
4. 前端安装与构建：`cd web && pnpm install && pnpm dev`
5. 阅读 [构建与运行](build-and-run.md) 了解 Makefile 目标

<!-- END:REPO_WIKI_MANAGED -->

## 注意事项

具体端口、账号、配置请参见 [配置说明](configuration.md)。