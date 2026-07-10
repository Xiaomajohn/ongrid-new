---
generated_by: repo-wiki-agent
baseline_commit: "47bad98d46a1d70231d237285784a8192d7756c7"
last_updated: "2026-07-06"
managed_sections:
  - "## Stack"
  - "## Metrics"
---

# 监控告警

<!-- BEGIN:REPO_WIKI_MANAGED -->
## Stack

可观测性由以下组件构成：

- **Prometheus** — 指标抓取与存储
- **Grafana** — 仪表盘（`deploy/grafana/provisioning/`）
- **Loki** — 日志聚合（`deploy/install/loki-config.yaml`）
- **Tempo** — 链路追踪（OTel，`deploy/install/tempo-config.yaml`）
- **node_exporter / mysqld_exporter / redis_exporter / postgres_exporter / mongodb_exporter** — 各组件 exporter

Exporter 二进制位于 `bin/linux-amd64/` 与 `bin/linux-arm64/`。

## Metrics

- 控制面：`/metrics` 暴露 Prometheus 指标
- 边缘 Agent：通过 collector 上报到 Manager

告警规则：`deploy/install/prometheus-rules.yml`。

<!-- END:REPO_WIKI_MANAGED -->

## Runbook

高风险告警须配 Runbook 链接；变更须有回滚方案（见 AGENTS.md 运维约束）。