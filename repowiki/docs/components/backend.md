---
generated_by: repo-wiki-agent
baseline_commit: "47bad98d46a1d70231d237285784a8192d7756c7"
last_updated: "2026-07-06"
managed_sections:
  - "## Overview"
  - "## Entrypoints"
  - "## Entry Symbol"
  - "## Layering"
  - "## Cross-Domain Boundary"
  - "## Entrypoint Symbols by BC"
---

# 后端服务 (Go)

<!-- BEGIN:REPO_WIKI_MANAGED -->
## Overview

控制面后端由两个入口二进制构成：

- `ongrid` — Manager + IAM 控制面入口
- `ongrid-edge` — 边缘 Agent 入口

两个入口由各 BC（`internal/manager`、`internal/iam`、`internal/edgeagent`、`internal/pkg`）组装。

## Entrypoints

| 入口 | 路径 | 角色 |
|---|---|---|
| `./cmd/ongrid-edge/main.go` | 命令入口 | 'main' 函数 |
| `./cmd/ongrid/main.go` | 命令入口 | 'main' 函数 |

## Entry Symbol

`cmd/ongrid/main.go` 顶层定义：

- `main`[^195]
- `llmResolverFunc`[^2631]
- `newLLMResolver`[^2635]
- `pluginEndpointResolver`[^2656]
- `edgeReachableLokiURL`[^2697]
- `edgeReachableTempoURL`[^2704]

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

## Cross-Domain Boundary

按 AGENTS.md 约束，`internal/<domain>` 之间禁止直接 import，必须经：

- API（`api/<domain>/v1/*.proto`）
- 事件总线
- `internal/pkg/` 内的通用基础库

接口在消费方定义，避免循环依赖；构造依赖通过构造函数注入，不使用全局变量。

## Entrypoint Symbols by BC

### cmd/ongrid

- `main`[^195]
- `llmResolverFunc`[^2631]
- `newLLMResolver`[^2635]
- `pluginEndpointResolver`[^2656]
- `edgeReachableLokiURL`[^2697]
- `edgeReachableTempoURL`[^2704]
- `isEdgeReachableURL`[^2715]
- `edgeAuthAdapter`[^2739]

### cmd/ongrid-edge

- `main`[^56]
- `buildCollector`[^332]
- `envOr`[^379]
- `collectorAdapter`[^391]

<!-- END:REPO_WIKI_MANAGED -->


## 引用
[^195]: cmd/ongrid/main.go L195–L200 — [cmd/ongrid/main.go#L195-L200](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/cmd/ongrid/main.go#L195-L200)
[^2631]: cmd/ongrid/main.go L2631–L2636 — [cmd/ongrid/main.go#L2631-L2636](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/cmd/ongrid/main.go#L2631-L2636)
[^2635]: cmd/ongrid/main.go L2635–L2640 — [cmd/ongrid/main.go#L2635-L2640](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/cmd/ongrid/main.go#L2635-L2640)
[^2656]: cmd/ongrid/main.go L2656–L2661 — [cmd/ongrid/main.go#L2656-L2661](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/cmd/ongrid/main.go#L2656-L2661)
[^2697]: cmd/ongrid/main.go L2697–L2702 — [cmd/ongrid/main.go#L2697-L2702](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/cmd/ongrid/main.go#L2697-L2702)
[^2704]: cmd/ongrid/main.go L2704–L2709 — [cmd/ongrid/main.go#L2704-L2709](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/cmd/ongrid/main.go#L2704-L2709)
[^2715]: cmd/ongrid/main.go L2715–L2720 — [cmd/ongrid/main.go#L2715-L2720](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/cmd/ongrid/main.go#L2715-L2720)
[^2739]: cmd/ongrid/main.go L2739–L2744 — [cmd/ongrid/main.go#L2739-L2744](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/cmd/ongrid/main.go#L2739-L2744)
[^332]: cmd/ongrid-edge/main.go L332–L337 — [cmd/ongrid-edge/main.go#L332-L337](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/cmd/ongrid-edge/main.go#L332-L337)
[^379]: cmd/ongrid-edge/main.go L379–L384 — [cmd/ongrid-edge/main.go#L379-L384](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/cmd/ongrid-edge/main.go#L379-L384)
[^391]: cmd/ongrid-edge/main.go L391–L396 — [cmd/ongrid-edge/main.go#L391-L396](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/cmd/ongrid-edge/main.go#L391-L396)
[^56]: cmd/ongrid-edge/main.go L56–L61 — [cmd/ongrid-edge/main.go#L56-L61](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/cmd/ongrid-edge/main.go#L56-L61)