---
generated_by: repo-wiki-agent
baseline_commit: "47bad98d46a1d70231d237285784a8192d7756c7"
last_updated: "2026-07-06"
managed_sections:
  - "## Services"
---

# gRPC 服务清单

<!-- BEGIN:REPO_WIKI_MANAGED -->
## Services

`api/` 下的 protobuf 定义：

- `api/iam/v1/iam.proto` — 身份认证
- `api/manager/aiops/v1/` — AIOps
- `api/manager/alert/v1/` — 告警
- `api/manager/edge/v1/` — 边缘节点
- `api/manager/metric/v1/` — 指标
- `api/manager/notification/v1/` — 通知
- `api/tunnel/v1/tunnel.proto` — 控制面↔Edge 隧道

构建：

```bash
make proto
```

buf 配置：`api/buf.yaml` + `api/buf.gen.yaml`。

<!-- END:REPO_WIKI_MANAGED -->

新增 RPC 时遵守：先改 proto → buf 生成 → handler 加 Swagger 注释。