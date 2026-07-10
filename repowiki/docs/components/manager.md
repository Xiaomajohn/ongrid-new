---
generated_by: repo-wiki-agent
baseline_commit: "47bad98d46a1d70231d237285784a8192d7756c7"
last_updated: "2026-07-06"
managed_sections:
  - "## Overview"
  - "## Layers"
  - "## gRPC Services (5)"
  - "## RPC Inventory (aiops 示例)"
  - "## RPC Inventory (edge)"
  - "## Message Types"
  - "## Subdomain Layout"
  - "## Cross-Subdomain Communication"
  - "## Architecture Constraints"
---

# Manager 服务

<!-- BEGIN:REPO_WIKI_MANAGED -->
## Overview

`internal/manager/` 是控制面核心域，按 5 个层 + 33 个子域组织。

## Layers

| 层 | 路径 | 角色 |
|---|---|---|
| `biz` | `internal/manager/biz/` | 业务用例 + 领域模型 |
| `data` | `internal/manager/data/` | 仓储实现（MySQL / Redis） |
| `model` | `internal/manager/model/` | DO / PO 实体 |
| `server` | `internal/manager/server/` | HTTP / gRPC handler |
| `service` | `internal/manager/service/` | 跨 biz 编排 |

## gRPC Services (5)

| 子域 | proto | service | RPC 数 |
|---|---|---|---|
| `aiops` | `./api/manager/aiops/v1/aiops.proto` | `AiopsService`[^11] | 5 |
    - `rpc CreateChatSession`[^14]
    - `rpc ListChatSessions`[^17]
    - `rpc PostMessage`[^21]
    - `rpc StreamMessage`[^25]
    - `rpc ListMessages`[^28]

| `alert` | `./api/manager/alert/v1/alert.proto` | `AlertService`[^9] | 4 |
    - `rpc ListIncidents`[^10]
    - `rpc GetIncident`[^11]
    - `rpc AcknowledgeIncident`[^12]
    - `rpc ResolveIncident`[^13]

| `edge` | `./api/manager/edge/v1/edge.proto` | `EdgeService`[^13] | 5 |
    - `rpc CreateEdge`[^16]
    - `rpc ListEdges`[^19]
    - `rpc GetEdge`[^22]
    - `rpc DeleteEdge`[^25]
    - `rpc RotateSecret`[^28]

| `metric` | `./api/manager/metric/v1/metric.proto` | `MetricService`[^12] | 1 |
    - `rpc QueryHostMetrics`[^16]

| `notification` | `./api/manager/notification/v1/notification.proto` | `NotificationService`[^10] | 6 |
    - `rpc ListChannels`[^11]
    - `rpc GetChannel`[^12]
    - `rpc CreateChannel`[^13]
    - `rpc UpdateChannel`[^14]
    - `rpc DeleteChannel`[^15]
    - `rpc TestChannel`[^16]


## RPC Inventory (aiops 示例)

- `rpc CreateChatSession`[^14]
- `rpc ListChatSessions`[^17]
- `rpc PostMessage`[^21]
- `rpc StreamMessage`[^25]
- `rpc ListMessages`[^28]

## RPC Inventory (edge)

- `rpc CreateEdge`[^16]
- `rpc ListEdges`[^19]
- `rpc GetEdge`[^22]
- `rpc DeleteEdge`[^25]
- `rpc RotateSecret`[^28]

## Message Types

### `aiops`

- `message ChatSession`[^43]
- `message ToolCall`[^55]
- `message ToolResult`[^62]
- `message ChatMessage`[^70]
- `message TokenUsage`[^85]
- `message CreateChatSessionRequest`[^93]

### `alert`

- `message AlertIncident`[^31]
- `message ListIncidentsRequest`[^48]
- `message ListIncidentsResponse`[^56]
- `message GetIncidentRequest`[^63]
- `message GetIncidentResponse`[^67]
- `message AcknowledgeIncidentRequest`[^71]

### `edge`

- `message HostInfo`[^41]
- `message Edge`[^54]
- `message CreateEdgeRequest`[^68]
- `message CreateEdgeResponse`[^72]
- `message ListEdgesRequest`[^80]
- `message ListEdgesResponse`[^93]


## Subdomain Layout

每个子域在 5 层中均有对应实现：

```
internal/manager/
  biz/<subdomain>/        ─ 业务
  data/<subdomain>/       ─ 仓储
  model/<subdomain>/      ─ 实体
  server/<subdomain>/     ─ handler
  service/<subdomain>/    ─ 编排
```

## Cross-Subdomain Communication

- 经 `api/manager/<sub>/v1/*.proto` 调用
- 经事件总线异步解耦
- 跨域共享通过 `internal/pkg/`

## Architecture Constraints

- 接口在消费方定义，禁止循环依赖
- 依赖通过构造函数注入，无全局变量
- 高基数字段（user_id/email/url）禁止作 Prometheus label
- 错误用 `%w` 包装；不重复记录

<!-- END:REPO_WIKI_MANAGED -->

## 引用
[^9]: api/manager/alert/v1/alert.proto L9–L39 — [api/manager/alert/v1/alert.proto#L9-L39](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/alert/v1/alert.proto#L9-L39)
[^10]: api/manager/notification/v1/notification.proto L10–L40 — [api/manager/notification/v1/notification.proto#L10-L40](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/notification/v1/notification.proto#L10-L40)
[^10]: api/manager/alert/v1/alert.proto L10–L20 — [api/manager/alert/v1/alert.proto#L10-L20](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/alert/v1/alert.proto#L10-L20)
[^11]: api/manager/aiops/v1/aiops.proto L11–L41 — [api/manager/aiops/v1/aiops.proto#L11-L41](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/aiops/v1/aiops.proto#L11-L41)
[^11]: api/manager/alert/v1/alert.proto L11–L21 — [api/manager/alert/v1/alert.proto#L11-L21](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/alert/v1/alert.proto#L11-L21)
[^11]: api/manager/notification/v1/notification.proto L11–L21 — [api/manager/notification/v1/notification.proto#L11-L21](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/notification/v1/notification.proto#L11-L21)
[^12]: api/manager/alert/v1/alert.proto L12–L22 — [api/manager/alert/v1/alert.proto#L12-L22](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/alert/v1/alert.proto#L12-L22)
[^12]: api/manager/notification/v1/notification.proto L12–L22 — [api/manager/notification/v1/notification.proto#L12-L22](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/notification/v1/notification.proto#L12-L22)
[^12]: api/manager/metric/v1/metric.proto L12–L42 — [api/manager/metric/v1/metric.proto#L12-L42](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/metric/v1/metric.proto#L12-L42)
[^13]: api/manager/alert/v1/alert.proto L13–L23 — [api/manager/alert/v1/alert.proto#L13-L23](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/alert/v1/alert.proto#L13-L23)
[^13]: api/manager/notification/v1/notification.proto L13–L23 — [api/manager/notification/v1/notification.proto#L13-L23](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/notification/v1/notification.proto#L13-L23)
[^13]: api/manager/edge/v1/edge.proto L13–L43 — [api/manager/edge/v1/edge.proto#L13-L43](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/edge/v1/edge.proto#L13-L43)
[^14]: api/manager/aiops/v1/aiops.proto L14–L24 — [api/manager/aiops/v1/aiops.proto#L14-L24](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/aiops/v1/aiops.proto#L14-L24)
[^14]: api/manager/notification/v1/notification.proto L14–L24 — [api/manager/notification/v1/notification.proto#L14-L24](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/notification/v1/notification.proto#L14-L24)
[^15]: api/manager/notification/v1/notification.proto L15–L25 — [api/manager/notification/v1/notification.proto#L15-L25](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/notification/v1/notification.proto#L15-L25)
[^16]: api/manager/notification/v1/notification.proto L16–L26 — [api/manager/notification/v1/notification.proto#L16-L26](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/notification/v1/notification.proto#L16-L26)
[^16]: api/manager/edge/v1/edge.proto L16–L26 — [api/manager/edge/v1/edge.proto#L16-L26](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/edge/v1/edge.proto#L16-L26)
[^16]: api/manager/metric/v1/metric.proto L16–L26 — [api/manager/metric/v1/metric.proto#L16-L26](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/metric/v1/metric.proto#L16-L26)
[^17]: api/manager/aiops/v1/aiops.proto L17–L27 — [api/manager/aiops/v1/aiops.proto#L17-L27](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/aiops/v1/aiops.proto#L17-L27)
[^19]: api/manager/edge/v1/edge.proto L19–L29 — [api/manager/edge/v1/edge.proto#L19-L29](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/edge/v1/edge.proto#L19-L29)
[^21]: api/manager/aiops/v1/aiops.proto L21–L31 — [api/manager/aiops/v1/aiops.proto#L21-L31](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/aiops/v1/aiops.proto#L21-L31)
[^22]: api/manager/edge/v1/edge.proto L22–L32 — [api/manager/edge/v1/edge.proto#L22-L32](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/edge/v1/edge.proto#L22-L32)
[^25]: api/manager/aiops/v1/aiops.proto L25–L35 — [api/manager/aiops/v1/aiops.proto#L25-L35](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/aiops/v1/aiops.proto#L25-L35)
[^25]: api/manager/edge/v1/edge.proto L25–L35 — [api/manager/edge/v1/edge.proto#L25-L35](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/edge/v1/edge.proto#L25-L35)
[^28]: api/manager/edge/v1/edge.proto L28–L38 — [api/manager/edge/v1/edge.proto#L28-L38](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/edge/v1/edge.proto#L28-L38)
[^28]: api/manager/aiops/v1/aiops.proto L28–L38 — [api/manager/aiops/v1/aiops.proto#L28-L38](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/aiops/v1/aiops.proto#L28-L38)
[^31]: api/manager/alert/v1/alert.proto L31–L51 — [api/manager/alert/v1/alert.proto#L31-L51](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/alert/v1/alert.proto#L31-L51)
[^41]: api/manager/edge/v1/edge.proto L41–L61 — [api/manager/edge/v1/edge.proto#L41-L61](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/edge/v1/edge.proto#L41-L61)
[^43]: api/manager/aiops/v1/aiops.proto L43–L63 — [api/manager/aiops/v1/aiops.proto#L43-L63](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/aiops/v1/aiops.proto#L43-L63)
[^48]: api/manager/alert/v1/alert.proto L48–L68 — [api/manager/alert/v1/alert.proto#L48-L68](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/alert/v1/alert.proto#L48-L68)
[^54]: api/manager/edge/v1/edge.proto L54–L74 — [api/manager/edge/v1/edge.proto#L54-L74](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/edge/v1/edge.proto#L54-L74)
[^55]: api/manager/aiops/v1/aiops.proto L55–L75 — [api/manager/aiops/v1/aiops.proto#L55-L75](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/aiops/v1/aiops.proto#L55-L75)
[^56]: api/manager/alert/v1/alert.proto L56–L76 — [api/manager/alert/v1/alert.proto#L56-L76](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/alert/v1/alert.proto#L56-L76)
[^62]: api/manager/aiops/v1/aiops.proto L62–L82 — [api/manager/aiops/v1/aiops.proto#L62-L82](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/aiops/v1/aiops.proto#L62-L82)
[^63]: api/manager/alert/v1/alert.proto L63–L83 — [api/manager/alert/v1/alert.proto#L63-L83](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/alert/v1/alert.proto#L63-L83)
[^67]: api/manager/alert/v1/alert.proto L67–L87 — [api/manager/alert/v1/alert.proto#L67-L87](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/alert/v1/alert.proto#L67-L87)
[^68]: api/manager/edge/v1/edge.proto L68–L88 — [api/manager/edge/v1/edge.proto#L68-L88](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/edge/v1/edge.proto#L68-L88)
[^70]: api/manager/aiops/v1/aiops.proto L70–L90 — [api/manager/aiops/v1/aiops.proto#L70-L90](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/aiops/v1/aiops.proto#L70-L90)
[^71]: api/manager/alert/v1/alert.proto L71–L91 — [api/manager/alert/v1/alert.proto#L71-L91](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/alert/v1/alert.proto#L71-L91)
[^72]: api/manager/edge/v1/edge.proto L72–L92 — [api/manager/edge/v1/edge.proto#L72-L92](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/edge/v1/edge.proto#L72-L92)
[^80]: api/manager/edge/v1/edge.proto L80–L100 — [api/manager/edge/v1/edge.proto#L80-L100](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/edge/v1/edge.proto#L80-L100)
[^85]: api/manager/aiops/v1/aiops.proto L85–L105 — [api/manager/aiops/v1/aiops.proto#L85-L105](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/aiops/v1/aiops.proto#L85-L105)
[^93]: api/manager/edge/v1/edge.proto L93–L113 — [api/manager/edge/v1/edge.proto#L93-L113](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/edge/v1/edge.proto#L93-L113)
[^93]: api/manager/aiops/v1/aiops.proto L93–L113 — [api/manager/aiops/v1/aiops.proto#L93-L113](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/api/manager/aiops/v1/aiops.proto#L93-L113)