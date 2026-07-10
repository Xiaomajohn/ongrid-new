---
generated_by: repo-wiki-agent
baseline_commit: "47bad98d46a1d70231d237285784a8192d7756c7"
last_updated: "2026-07-06"
managed_sections:
  - "## Go Module Layout"
  - "## Layering"
---

# 模块依赖图

<!-- BEGIN:REPO_WIKI_MANAGED -->
## Go Module Layout

```
cmd/
  ongrid/         # 控制面入口
  ongrid-edge/    # 边缘 Agent 入口
internal/
  manager/        # 控制面（biz / data / server / service / model）
  iam/            # 身份与权限
  edgeagent/      # 边缘能力（biz / plugins / service / collector）
  pkg/            # 通用基础库（auth / config / dbx / llm / tunnel …）
  skill/          # 内置 skill 注册
api/              # protobuf 定义（iam / manager / tunnel）
web/              # 前端（React + TS + Vite）
deploy/           # Docker Compose / 安装脚本 / systemd unit
```

## Layering

BC 内部分层（Kratos 风格）：

```
server  ── HTTP / gRPC handler
  ↓
service ── 跨 biz 编排
  ↓
biz     ── 业务用例 + 领域模型
  ↓
data    ── 仓储实现（MySQL / Redis）
  ↓
model   ── DO / PO 实体
```

- `internal/<domain>` 之间禁止直接 import，必须经 API / 事件 / `internal/pkg/`
- 接口在消费方定义（避免循环依赖）

<!-- END:REPO_WIKI_MANAGED -->

## 校验

`.go-arch-lint.yml` 约束了跨层调用规则，CI 会执行校验。