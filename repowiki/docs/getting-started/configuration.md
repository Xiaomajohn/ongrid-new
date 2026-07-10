---
generated_by: repo-wiki-agent
baseline_commit: "47bad98d46a1d70231d237285784a8192d7756c7"
last_updated: "2026-07-06"
managed_sections:
  - "## Configuration Sources"
---

# 配置说明

<!-- BEGIN:REPO_WIKI_MANAGED -->
## Configuration Sources

配置来源优先级（高 → 低）：

1. 环境变量
2. `.env`（参考 `.env.example`）
3. 代码内默认值

主要配置项见：

- `.env.example` — 所有可配置项清单
- `deploy/.env.example` — 部署相关
- `deploy/install/.env.example` — 安装脚本相关

<!-- END:REPO_WIKI_MANAGED -->

详细字段含义参见源码 `internal/pkg/config/`。