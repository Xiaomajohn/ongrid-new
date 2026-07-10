---
generated_by: repo-wiki-agent
baseline_commit: "47bad98d46a1d70231d237285784a8192d7756c7"
last_updated: "2026-07-06"
managed_sections:
  - "## Frontend → Backend"
  - "## Edge Agent → Manager"
---

# 数据流

<!-- BEGIN:REPO_WIKI_MANAGED -->
## Frontend → Backend

典型链路：

1. 用户在 React 页面点击操作（如安装 Edge）
2. 前端调用 `web/src/api/` 下的封装，发起 HTTP 请求
3. 请求经 Nginx（生产）或 Vite dev server（本地）转发到 `cmd/ongrid` 启动的 HTTP 服务
4. HTTP handler（`internal/manager/server/`）调用对应 biz 服务（`internal/manager/biz/`）
5. biz 通过 data 层（`internal/manager/data/`）持久化到 MySQL / Redis

## Edge Agent → Manager

1. `cmd/ongrid-edge/main.go` 启动后，向 Manager 注册并建立 tunnel 连接
2. 控制面通过 `api/tunnel/v1/tunnel.proto` 下发指令
3. 边缘 Agent 调用 `internal/edgeagent/service/` 与各插件（`internal/edgeagent/plugins/`）执行命令、采集指标、上报日志
4. 结果通过同一 tunnel 流式回传控制面

<!-- END:REPO_WIKI_MANAGED -->

更细的调用图参见源码导入关系。