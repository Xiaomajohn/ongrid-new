---
generated_by: repo-wiki-agent
baseline_commit: "47bad98d46a1d70231d237285784a8192d7756c7"
last_updated: "2026-07-06"
managed_sections:
  - "## Makefile Targets"
---

# 构建与运行

<!-- BEGIN:REPO_WIKI_MANAGED -->
## Makefile Targets

常用 Makefile 目标：

| 目标 | 用途 |
|---|---|
| `make build` | 构建 ongrid / ongrid-edge 二进制 |
| `make test` | 跑单元测试（含 race） |
| `make lint` | golangci-lint |
| `make proto` | 由 `.proto` 生成 Go 代码 |
| `make web` | 构建前端静态资源 |
| `make package` | 跨平台打包（linux/amd64 + arm64） |

详细命令清单参见 `Makefile` 与 [构建与测试](../operations/build-and-test.md)。

<!-- END:REPO_WIKI_MANAGED -->

具体二进制产物路径参见 `bin/` 目录。