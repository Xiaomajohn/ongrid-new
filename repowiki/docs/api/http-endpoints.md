---
generated_by: repo-wiki-agent
baseline_commit: "47bad98d46a1d70231d237285784a8192d7756c7"
last_updated: "2026-07-06"
managed_sections:
  - "## Endpoints"
---

# HTTP 端点

<!-- BEGIN:REPO_WIKI_MANAGED -->
## Endpoints

HTTP API 入口位于各 BC 的 `server/` 子包下，由 Kratos / Hertz 注册到总入口 `cmd/ongrid/main.go`。

主要端点分类：

- `/api/v1/auth/*` — 认证（IAM）
- `/api/v1/devices/*` — 边缘设备
- `/api/v1/alerts/*` — 告警
- `/api/v1/metrics/*` — 指标查询
- `/api/v1/aiops/*` — AIOps
- `/api/v1/notifications/*` — 通知
- `/api/v1/edges/install*` — 边缘安装
- `/healthz` `/readyz` `/metrics` — 可观测性（每服务暴露）

<!-- END:REPO_WIKI_MANAGED -->

## 响应格式

统一为：

```json
{ "code": 0, "message": "ok", "data": { ... } }
```

handler 须带 Swagger 注释（`@Summary`、`@Router`、`@Success` 缺一不可）。