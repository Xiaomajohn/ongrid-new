---
generated_by: repo-wiki-agent
baseline_commit: "47bad98d46a1d70231d237285784a8192d7756c7"
last_updated: "2026-07-06"
managed_sections:
  - "## Topology"
---

# 部署拓扑

<!-- BEGIN:REPO_WIKI_MANAGED -->
## Topology

典型部署由 Docker Compose 编排：

- **frontier**：反向代理 / 隧道入口
- **ongrid**：控制面后端
- **ongrid-edge**：边缘 Agent（每台被纳管主机一份）
- **mysql / redis**：存储
- **prometheus / grafana / loki / tempo**：可观测性
- **nginx**：前端静态资源 + HTTP 反代
- **searxng**：可选搜索后端

部署脚本入口：`deploy/install/install.sh`（一键安装 + 启动 + 升级）

参见 `deploy/docker-compose.yml` 与 `deploy/install/README.md`。

<!-- END:REPO_WIKI_MANAGED -->

边缘节点独立安装参见 [部署指南](../operations/deploy.md) 与 `docs/install/edge.md`。