---
generated_by: repo-wiki-agent
baseline_commit: "47bad98d46a1d70231d237285784a8192d7756c7"
last_updated: "2026-07-06"
managed_sections:
  - "## Build"
  - "## Test"
---

# 构建与测试

<!-- BEGIN:REPO_WIKI_MANAGED -->
## Build

后端：

```bash
make build              # 构建 ongrid / ongrid-edge
make package            # 跨平台打包（linux/amd64 + linux/arm64）
```

前端：

```bash
cd web && pnpm install && pnpm build
```

proto：

```bash
make proto
```

## Test

后端（带 race）：

```bash
go test -race ./...
```

前端：

```bash
cd web && pnpm lint && pnpm build
```

E2E：`tests/e2e/`。

<!-- END:REPO_WIKI_MANAGED -->

## CI

`.github/workflows/ci.yml` 触发所有上述检查。