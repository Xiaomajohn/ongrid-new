# 后端与数据交互 — 鸟瞰

> 本文档描述 ongrid 的后端架构：**Manager 与 EdgeAgent 内部如何分层、一次 HTTP 请求走过了哪些层、反向隧道如何打通 Manager→Edge、进程内的后台 worker 各自负责什么**。聚焦"调用链与角色"，不深入 SQL 实现或具体算法。代码位置：`cmd/`（入口）、`internal/{iam,manager,edgeagent,skill,pkg}/`。

---

## 1. 总览

### 1.1 二进制与运行时

ongrid 由 **两个进程**组成，云边分离：

| 二进制 | 路径 | 部署位置 | 入口 | 核心职责 |
| --- | --- | --- | --- | --- |
| `ongrid`（Manager） | `cmd/ongrid/main.go` | 云端 / 中心节点 | `main()` | 反向隧道服务端 + HTTP/HTTPS API + 后台 worker + IM 网关 + AI Agent 编排 |
| `ongrid-edge`（Edge 探针） | `cmd/ongrid-edge/main.go` | 每台被纳管主机 | `main()` | 注册到 Manager、本地主机指标采集、命令白名单执行（bash / restart / host_files）、自升级 |

两个进程通过 **frontier 反向隧道** 通信：Edge 主动外连到 Manager 的 frontier 服务端，Manager 通过 tunnel 服务端反向调用 Edge，**Edge 不需要入站端口**。

### 1.2 BC 与分层（Manager 视角）

Manager 内部严格遵循 `cmd → web → controlplane → repo → model` 单向依赖：

```
┌──────────────────────────────────────────────────────────────────────────┐
│                          Manager 进程 (ongrid)                            │
│                                                                          │
│   cmd/ongrid/main.go                            进程入口（仅组装）         │
│       │                                                                 │
│       ▼                                                                 │
│   web/  （chi 路由 + middleware）         ← 对外 HTTP 入口                │
│       │                                                                 │
│       ▼                                                                 │
│   controlplane/  ← service/  业务编排层（DTO 转换、参数校验）             │
│       │              ↑                                                 │
│       │              └──── 一些横切：frontierbound（反向调用 Edge）        │
│       ▼                                                                 │
│   repo/  ← internal/manager/biz/   业务用例 + Repository 接口             │
│       │                                                                 │
│       ▼                                                                 │
│   model/  ← internal/manager/data/  GORM 持久化实现                        │
│       │                                                                 │
│       ▼                                                                 │
│   internal/manager/model/    实体与领域类型                                │
│                                                                          │
│   后台：独立 ticker / goroutine，调用 repo 层（metric、alert、imbridge…）  │
│   跨进程：frontier 服务端（云端） + 隧道客户端连入                          │
└──────────────────────────────────────────────────────────────────────────┘
```

⚠️ **图示对齐仓库**：
- `cmd → web → controlplane → repo → model` 这个名词来自 `AGENTS.md` 约定的架构原则；仓库里实际对应的路径是 `cmd/ongrid/` → `internal/manager/server/`（web/ch router）→ `internal/manager/service/`（编排层）→ `internal/manager/biz/`（业务用例 + 接口）→ `internal/manager/model/` 与 `internal/manager/data/`（实体 + GORM 实现）。下文凡是提到"分层"都用 `cmd/server/service/biz/data/model` 这组真实路径。

---

## 2. Manager BC 内部分层

Manager 顶层目录仅 5 个：`biz / data / model / server / service`，各自对应持续化与对外暴露的一个环节。

### 2.1 `internal/manager/model/` — 持久化实体层

**职责**：定义系统持久化的实体（行类型）、领域值对象、字段上的约束标签。**无业务逻辑、无 IO、无外部依赖**（除 gorm/io/errs 之类的横切库）。

每个域一个子目录，共 **18 个**，全部与 `data/` 平行同名：

| 子目录 | 含义 | 行类型（GORM） |
| --- | --- | --- |
| `edge/` | 边缘探针身份 | `Edge` |
| `device/` | 被纳管主机 | `Device`、`EdgeDevice`（M:N 联结） |
| `metric/` | 主机指标时序 | `HostMetric`、`HostMetric5m`、`HostMetric1h`、`DeadLetter` |
| `alert/` | 告警 / 调查 | `Incident`、`Event`、`Silence`、`Rule`、`Channel`、`Delivery`、`InvestigationReport` |
| `aiops/` | AI 会话 | `ChatSession`、`ChatMessage`、`ChatToolCall`、`UserAgent` |
| `flow/` | Workflow DAG | `Flow`、`FlowRun`、`FlowRunNode` |
| `report/` | 周期报告 | `ReportSchedule`、`Report` |
| `monitor/` | 自定义面板 | `MonitorPanel` |
| `topology/` | 业务拓扑 | `Node`、`Relation`、`RelationType`、`NodeType` |
| `knowledge/` | 知识库 | `KnowledgeRepo`、`SshIdentity`（文档本体在 Qdrant） |
| `secret/` | 凭证保险库 | `Secret` |
| `marketplace/` | 已安装 skill | `InstalledSkill` |
| `mcp/` | 外部 MCP server | `McpServer` |
| `setting/` | 系统设置 KV | `SystemSetting` |
| `audit/` | 审计 | `AuditLog` |
| `approval/` | 危险操作审批 | `Approval` |
| `imbridge/` | IM 集成 | `ImApp`、`ImThread` |
| `webshell/` | WebSSH | `WebShellSession` |

> **域值类型与行类型分离**：`metric/model.go` 同时定义 `Point` / `Bucket5m` / `Bucket1h`（无 gorm tag 的纯值对象）与 `HostMetric*`（行类型）。`biz` 层在内部用前者，对外 / 落库才转后者。这是「**JSON 列存序列化字符串，biz 用领域类型**」的体现。

### 2.2 `internal/manager/data/` — GORM 持久化实现层

**职责**：把 `biz` 层声明的 Repository 接口落地为 GORM 调用；`AutoMigrate` / `Migrate` 提供；事务、批量写入、索引的工程实现都在这一层。

共 **18 个**子目录（与 `model/` 一一对应）：

| 子目录 | 关键文件 | 作用 |
| --- | --- | --- |
| `<domain>/store/<repository>.go` | GORM 实现的 Repo | biz `Repository` 接口的具体实现 |
| `<domain>/store/migrate.go` | `Migrate(db)` | 该域的 schema 升级入口 |
| `<domain>/store/conv.go`（部分域） | 行类型 ↔ 域类型 / DTO 互转 | 减少 `biz` 层的样板 |

> **关键约束**：所有 `TEXT` 列 `NOT NULL`（MySQL 8 Error 1101）；软删除统一用 `gorm.io/plugin/soft_delete` 的毫秒级 `DeleteMarker` + `DeletedAt` 审计列；`CreateInBatches(size=500)` 用于时序高吞吐写入。

### 2.3 `internal/manager/biz/` — 业务用例 + Repository 接口

**职责**：定义业务用例（Usecase）、把多步操作（如"12 步原子 upsert"注册探针）组装成事务；声明 `Repository` 接口（在消费方定义，反转依赖）。

共 **21 个**子目录。**注意**：biz 比 model 多 3 个，因为有些域是纯业务编排、不需要持久化：

| 域类型 | 子目录 | 与 model/data 的关系 |
| --- | --- | --- |
| 实体域（18 个） | `aiops/alert/approval/audit/device/edge/flow/imbridge/knowledge/marketplace/mcp/metric/monitor/report/secret/setting/topology/webshell` | 与 model/data 一一对应 |
| **非实体编排（3 个）** | `grafana/、promwrite/、skill/` | 不对应 model，封装对第三方（Grafana HTTP/Prometheus remote_write/Skill runner）的封装 |

每个 biz 子目录典型结构：

| 文件 | 作用 |
| --- | --- |
| `interface.go`（或 `repo.go`） | `Repository` 接口 + `Usecase` 接口 + DTO |
| `usecase.go` | 业务用例实现（编排 + 事务 + 跨表操作） |
| `types.go`（可选） | 域内部值对象 / 枚举 |
| `conv.go`（可选） | `model` ↔ biz 内 DTO 的转换 |

### 2.4 `internal/manager/service/` — 业务编排（DTO 转换）层

**职责**：把 `server/` 的 HTTP 入口参数转换为 biz 层用例调用；做参数校验、调用链复用、复合编排；**封装对外 RPC**（frontierbound 反向调用 Edge）。

共 **9 个**子目录，远少于 server，因为 service 是"按需编排"，不是和路由一一对应：

| 子目录 | 编排对象 |
| --- | --- |
| `edge/` | `service/edge` 是 Manager→Edge 隧道 RPC 的 shim：调用 `EdgeCaller.Call(ctx, edgeID, method, body)` 分发到 edge 探针 |
| `alert/` | alert 复合用例（评估 + 路由 + 投递） |
| `aiops/` | AI 会话编排：拉 persona、构造 LLM 请求、流式回复 |
| `aiopsconfig/` | AI 配置（provider/model/tool 清单）的合并计算 |
| `metric/` | 时序读路径（raw → 5m → 1h 透写） |
| `prometheus/` | Prometheus 远端写（remote_write）封装 |
| `systemhealth/` | `/healthz` / `/readyz` / `/metrics` 装配 |
| `systemupgrade/` | 自升级包管理（拉 frontier、上传 binary、应用） |
| `frontierbound/` | **反向隧道 RPC 客户端**：Manager → Edge 的所有方法都从这里派发 |

### 2.5 `internal/manager/server/` — HTTP 路由层（chi）

**职责**：对外 HTTP/HTTPS 入口；URL 路由注册；中间件装配；调 service / biz。

**27 个**子目录，可分三类：

| 类别 | 子目录 | 作用 |
| --- | --- | --- |
| **业务路由**（18+） | `aiops/alert/approval/audit/device/edge/flow/imbridge/knowledge/logs/marketplace/mcp/metric/monitor/prometheus/report/secret/setting/skill/topology/traces/webshell` | 每个域一个 `http.go`，路由前缀 `/api/v1/<domain>/...` |
| **横切** | `middleware/` | 全局中间件（auth → tenantctx → casbin → 响应 wrapper） |
| **基础设施路由** | `integration/` `edgeauth/` `systemhealth/` `systemupgrade/` | `/api/v1/integration/*`（注册 token 一次性换取 long-lived key）、边缘探针注册入口、`/healthz` `/readyz` `/metrics`、自升级 endpoint |

每个业务路由典型文件：

| 文件 | 作用 |
| --- | --- |
| `<domain>/http.go` | `Register(r chi.Router)` 注册本域路由；handler 内调 `service/` 或 biz |
| `<domain>/dto.go`（可选） | HTTP 入参 / 出参 DTO，含 swagger 注释 |

---

## 3. HTTP 请求生命周期（一次 API 调用走过的链路）

以 `POST /api/v1/alert-rules`（创建告警规则）为例，从浏览器到落库的完整链路：

```
┌────────────┐    ┌────────────────────────────────────────────────────────┐
│  SPA       │    │                   Manager 进程                          │
│  web/src   │    │  ┌──────────────────────────────────────────────────┐   │
│            │    │  │  chi router (internal/manager/server/...)         │   │
│  fetch()   │    │  │  ┌──────────────────────────────────────────┐   │   │
│   ─POST──► │ ─► │  │  │ middleware chain                          │   │   │
│            │    │  │  │  ① logging/recovery (请求级 trace_id)     │   │   │
│  Bearer   │    │  │  │  ② auth (pkg/auth): 验 JWT / refresh token │   │   │
│  locale   │    │  │  │  ③ tenantctx: 解析 tenant_id 放进 ctx     │   │   │
│            │    │  │  │  ④ authz (pkg/authzmw + casbin): RBAC     │   │   │
│            │    │  │  └──────────────────────────────────────────┘   │   │
│            │    │  │  ┌──────────────────────────────────────────┐   │   │
│            │    │  │  │ ⑤ chi.URLParam, render.JSON, etc.         │   │   │
│            │    │  │  │ ⑥ alert/http.go:RuleCreate handler         │   │   │
│            │    │  │  │ ⑦ service/alert (DTO 转换 + 校验)         │   │   │
│            │    │  │  │ ⑧ biz/alert.Usecase.Create                 │   │   │
│            │    │  │  │ ⑨ data/alert/store.RuleRepository.Create  │   │   │
│            │    │  │  │ ⑩ GORM AutoMigrate, INSERT INTO alert_rules│   │   │
│            │    │  │  └──────────────────────────────────────────┘   │   │
│            │    │  └──────────────────────────────────────────────────┘   │
│  ◄─────── │ ◄──│  响应: {code, message, data:{...}}                       │
└────────────┘    └────────────────────────────────────────────────────────┘
```

### 3.1 中间件堆栈（按调用顺序）

| 顺序 | 中间件 | 文件 | 职责 |
| --- | --- | --- | --- |
| ① | logging / recovery | `server/middleware/` | 请求级 trace_id（slog）、panic 兜底 |
| ② | auth | `internal/pkg/auth` | Bearer JWT 验证；refresh token 单独处理；解析 `user_id / tenant_id / role` 塞入 ctx |
| ③ | tenantctx | `internal/pkg/tenantctx` | 把 tenant_id 提取成显式 ctx value，后续 repo 层强制注入 WHERE |
| ④ | authz | `internal/pkg/authzmw` + casbin | 路由级 RBAC：`Require("alert_rule", "create")` 这种 obj/act 二元组 |
| ⑤ | 业务 handler | `server/<domain>/http.go` | URL param 提取、参数解析、调 service / biz |

> **响应统一形状**：所有 handler 用 `render.JSON(w, r, Response{Code:0, Message:"ok", Data:...})` 包装；业务错误通过 `errs` 包转成 `code` + `message`，前端 `client.ts` 据此识别。

### 3.2 Server → Service → Biz → Data 的角色分工

| 层 | 输入 | 输出 | 不知道的事情 |
| --- | --- | --- | --- |
| `server/<domain>/http.go` | chi `http.Request`（URL param / body / query） | `render.JSON` | 不做业务校验、不碰 DB |
| `service/<domain>/` | HTTP DTO | biz 层 DTO | 不写 SQL、不懂 schema |
| `biz/<domain>/usecase.go` | biz DTO | 业务结果 | 不懂 HTTP、不知道路由 |
| `data/<domain>/store/` | biz 传值 | 持久化结果（行数 / ID） | 不做业务校验、不懂 DTO |
| `model/<domain>/` | — | 实体 / 域值类型 | 无 IO、无依赖 |

**接口反转**：Repository 接口定义在 **biz 层**，`data` 层去实现它。这样切换存储（未来如换 ClickHouse）只动 `data/`，业务逻辑零改动。

### 3.3 跨 BC 与跨进程的偏差

- **跨 BC**：Manager 调 IAM（用户/角色）走 `internal/iam/` 同进程内 import（IAM 是 Manager 启动即存在的 BC）；Manager 调 Edge 不直走 biz，而是 **service/frontierbound 通过反向隧道 RPC**。
- **跨进程**：Manager ↔ Edge 通过 frontier 隧道（见第 5 节）。
- **跨组件**：Manager ↔ Qdrant / Loki / Tempo / Prometheus / Grafana 通过 `internal/pkg/{qdrantx,logquery,tracequery,promquery,grafana,...}` 各自封装。

---

## 4. EdgeAgent BC 内部分层

`internal/edgeagent/` 是 **每台被纳管主机上的进程**，架构与 Manager 不同：它不是云端那种厚重分层，而是 **agent 主循环 + 插件** 的扁平结构。

| 子目录 | 职责 | 入口 / 关键文件 |
| --- | --- | --- |
| `bash/` | cloud_bash / 本地命令执行 | binary parser、runner |
| `biz/` | Edge 主循环与跨子系统编排 | `agent.go`、`upgrade.go`、`json_codec.go` 等 |
| `collector/` | **指标采集**（CPU / Mem / Disk / Net / load1/5/15…） | 10s 心跳原始样本 |
| `cmdpolicy/` | **命令白名单** + 审计 | 自升级 / bash / host_files 的 deny 列表，bash 命中规则即拒绝 |
| `host_files/` | 主机文件读取 / 写入 | 文件路径白名单、内容上限 |
| `model/` | Edge 内部状态结构（不持久化） | 连接状态、上报缓冲等 |
| `plugins/` | **插件注册中心** | 按 capability 注册 Skill / MCP client / IM gateway 实现 |
| `restart_service/` | 服务重启（systemd / docker / k8s） | 白名单 + 审计 |
| `service/` | tunnel server 端（与 frontier 通信） | 接 Manager 反向调用 |
| `skill/` | Edge 端 skill 加载与执行 | `loader.go` + `builtin/`（bash / host-files / restart-service） |
| `webshell/` | WebSSH 协议实现 | 终端复用、审计 |

### 4.1 Edge 主循环（agent.go）

```
┌─────────────────────────────────────────────────────────┐
│                      EdgeAgent 主循环                    │
│                                                         │
│   startup                                                │
│     ├─► 加载 skills (skill/loader.go)                   │
│     ├─► 注册 plugins (plugins/registry)                 │
│     ├─► 启动 collector（指标采集 ticker）                 │
│     ├─► 通过 tunnel service 端注册到 Manager            │
│     │       POST /api/v1/integration/register           │
│     │       ← 拿到 access_key / secret_key / edge_id    │
│     └─► 进入 loop:                                      │
│           ├─ 10s: collector 采集 → push_host_metrics    │
│           ├─ 心跳: last_seen_at 翻 online               │
│           ├─ 接 Manager 反向 RPC:                       │
│           │    bash.run / host_files.read / restart …   │
│           └─ 退出 / 自升级信号                          │
└─────────────────────────────────────────────────────────┘
```

### 4.2 Edge 端的 cmdpolicy（命令白名单 + 审计）

- **白名单**：bash / restart / host_files 操作先过 `cmdpolicy/` 的正则 / 路径规则。
- **审计**：所有危险操作（即使被拒）写入本地审计日志 + 回报 Manager，Manager 在 audit_logs 端入库。
- 这层**不在主循环，是 wrapper**：每个危险插件入口第一行做 policy check。

---

## 5. 反向隧道：Manager → Edge 调用链

Manager → Edge 的所有 RPC 都不走 Manager 的入站 HTTP，统一走 frontier 隧道。

### 5.1 一次反向 RPC 的完整路径

以 Manager 在 `service/edge.RunCommand(edgeID, ...)` 为例：

```
Manager 进程                       frontier 服务端                EdgeAgent 进程
─────────────                      ──────────────                ──────────────
service/edge.RunCommand
   │
   ▼
frontierbound.EdgeCaller.Call()
   │  (1) 包装 method + body 成 frontier RPC frame
   ▼
frontier.Client (long-lived)
   │  (2) 通过已建立的"主动外连"反向通道发包
   ▼
═══════════════════ 隧道网络（mTLS / token）═══════════════════►
                                                          │
                                              service/<rpc> 入口
                                                          │
                                              tunnel server on Edge
                                                          │
                                              plugins/<plugin>.Invoke()
                                                          │
                                                  bash.run / host_files.read / ...
                                                          │
                                              ◄─────────── 响应逆向回传
```

### 5.2 关键抽象

| 抽象 | 路径 | 角色 |
| --- | --- | --- |
| `frontierbound.Client` | `internal/manager/service/frontierbound/` | Manager 侧的远端调用客户端；从 tunnel client 派生 |
| `frontier.Client` | `internal/pkg/tunnel/` | frontier SDK 提供的双向流客户端 |
| frontier 服务端 | Manager 启动时在本进程起的 frontier `service-end`（`cmd/ongrid/main.go`） | 长连接接收端 + 协议解析 |
| tunnel server | `internal/edgeagent/service/` | Edge 侧 tunnel server，把 reverse-RPC 派发到对应 plugin |

### 5.3 边界与约束

- **Edge 不需要入站端口**：所有连接都是 Edge 主动外连 Manager，**NAT 穿透友好**。
- **EdgeCaller.Call 的 per-method timeout**：每个 RPC（如 `bash.run`）有独立的 timeout；超过即释放底层连接，由 agent 主循环下一次心跳发现后再 retry。
- **不存续调用**：刷新前端不会杀死后台 LLM 调用——agent 编排把 turn 与请求 ctx 解耦；停止靠显式 `POST /chat/sessions/{id}/stop`（前端 Esc 触发）。

---

## 6. 进程内后台 worker

Manager 进程内除了 HTTP 服务外，还跑多个独立 ticker / goroutine 负责维护态、订阅事件、投递通知。所有 worker 都通过 repo 层（不走 HTTP），所以它们与请求处理共享同一套 biz / data / model 抽象。

| Worker | 触发频率 | 入口 | 职责 |
| --- | --- | --- | --- |
| **指标下采样**（5m / 1h） | 每 5 分钟 / 每小时 | `biz/metric/downsample.go` | 从 `host_metrics_raw` 聚合到 `host_metrics_5m` / `_1h`；写 dead_letter 当摄入失败 |
| **指标保留期清理** | 每 N 小时 | `biz/metric/retention.go` | 过期分桶 TTL 清理 |
| **告警评估** | 持续 | `biz/alert/evaluator/` | 拉活跃 `alert_rules`，对 Prometheus / Qdrant / Loki 做查询，超阈值即触发 `Incident` |
| **告警路由 / 通知投递** | 持续 | `biz/alert/router/` + `biz/alert/notify_delivery.go` | 把 `Incident` 路由到匹配 `Channel`（webhook / Slack / 飞书 / 钉钉 / 企业微信 / Telegram），写入 `notification_deliveries`；失败 retry 用指数退避 |
| **AI 调查** | 事件触发 | `biz/aiops/investigator/` | `Incident` 触发调查管线 → 拼上下文 → LLM 工具调用 → 写 `investigation_reports` |
| **重试 worker** | 持续 | `biz/audit/+delivery retry/` | 失败投递 / 异步任务重试，指数退避 + 最大重试上限 |
| **IM supervisor** | 持续 | `internal/imbridge/` | 心跳 IM 平台（飞书 / 钉钉 / Slack / Telegram），重连、消息推送、chat ↔ thread 映射维护（`im_threads`） |
| **市场校验** | 持续 | `biz/marketplace/verifier/` | 已安装 skill 的 manifest / 签名 / 兼容性校验 |
| **拓扑镜像** | 持续（hook） | `internal/topology/` | 设备 register 时自动创建 `Node`，注销时标记孤立 |
| **系统健康 / 自升级** | ticker + signal | `service/systemhealth/` + `service/systemupgrade/` | `/healthz` 汇总；监听中心 frontier 的升级包并应用 |
| **Qdrant 索引同步** | 事件触发 | `biz/knowledge/ingest/` | 知识库文档写 MySQL 元数据的同时投递向量到 Qdrant |
| **审计写入** | 持续 | `biz/audit/` | 全员可写，仅后台按 retention 清空 |
| **WebSSH 会话清理** | ticker | `biz/webshell/` | 超时会话归档 |

> **边界**：worker 不开新端口、不与外部 HTTP 通信（IM supervisor 除外）；它们都跑在同一个 Manager 进程里，横向扩展时按 Manager 实例自动 fan-out（共识靠 DB 行锁）。

---

## 7. 跨 BC 服务端 SDK 装配（cmd/ongrid/main.go）

`cmd/ongrid/main.go` 是 Manager 的**唯一编排入口**。它的角色是"按依赖图把所有 BC 装配成可运行进程"，本身不应有业务逻辑（`.golangci.yml` / `.go-arch-lint.yml` 强制）。

主要步骤（高层、按顺序）：

1. **load config**：从 `internal/pkg/config` 读取 env / config 文件；所有 `ONGRID_*` 变量以 `.env.example` 为准。
2. **init logger**：slog + trace_id，所有组件共享。
3. **init DB**：通过 `internal/pkg/dbx` 拿 GORM `*gorm.DB`；按域顺序执行 `data/<domain>/store/migrate.go`。
4. **init IAM**：先于 Manager 启动用户 / org / RBAC 表。
5. **构造 repo**：每个域的 `data` 层实例化（注入 DB + 可选 logger）。
6. **构造 biz**：每个域的 `usecase` 实例化（注入 repo）。
7. **构造 service**：把 usecase 注入到 service 层；`frontierbound` 注入 tunnel client。
8. **启动 frontier 服务端**：在本进程起 frontier `service-end`，作为反向隧道接收入口。
9. **启动 chi router**：装载所有 `server/<domain>/http.go.Register()`；挂 middleware。
10. **启动后台 worker**：按 6 节列表，按需启 ticker / goroutine；context cancel 时统一退出。
11. **httpSrv.ListenAndServe**：监听 `ONGRID_HTTP_ADDR`；同时 readiness probe 观察 frontier + DB。
12. **sig term**：cancel root ctx，graceful shutdown（关 HTTP → 停 worker → 断 frontier）。

> **测试**：上述每一步在 `cmd/ongrid/main_kernel_test.go`、`main_test.go` 等单测里都有 mock 验证，确保"按图索骥"启动逻辑可观测。

---

## 8. IAM BC（身份域）的特殊角色

`internal/iam/` 与 manager 平级，但 **IAM 是 Manager 启动的**（不独立部署）。它的分层与 manager 完全一致：

| 路径 | 角色 |
| --- | --- |
| `internal/iam/model/` | `User`、`Org`、`Membership`、casbin policy 行 |
| `internal/iam/data/` | GORM 实现 + casbin adapter |
| `internal/iam/biz/` | usecase（登录、刷新、组织切换、权限分配） |
| `internal/iam/server/` | HTTP 路由：`/api/v1/auth/login`、`/api/v1/auth/refresh`、`/api/v1/users`、`/api/v1/orgs`、`/api/v1/authz/*` |
| `internal/iam/service/` | token 签发、refresh token 校验、casbin enforcer 装配 |

> **多租户设计**：Org 是 tenant 边界；所有 manager 域的 SQL 都强制带 `tenant_id = ?` 过滤（在 biz 层或 `pkg/tenantctx` 包装）；前端 store 持有的 token 含 `tenant_id` 声明。
> **当前阶段**：项目处于"单租户 first-then-多租户"路径——`installed_skills` 等少量表已带 `tenant_id`；其余表尚不带，意味着"切租户需迁移"。

---

## 9. Skill 框架（跨 BC）

`internal/skill/` 不是某个 BC 内部的子包，而是跨 BC 共享的"skill 装载与执行"框架：

| 文件 | 作用 |
| --- | --- |
| `loader.go` | 把 `SKILL.md` 装载为可调用单元；解析 manifest、能力声明、参数 schema |
| `subprocess.go` | 子进程执行（`subprocess_process_unix.go` / `_windows.go` 平台分支） |
| `registry.go` | 全局 skill 注册表 |
| `schema.go` / `types.go` | 参数 schema + 类型定义 |
| `builtin/` | Manager 自带 skill（如 web_search） |
| `loader_test.go` / `subprocess_test.go` | 装载与子进程执行单测 |

Edge 端也有同名 mini skill 框架（`internal/edgeagent/skill/`，builtin 是 `bash / host-files / restart-service`）。

---

## 10. 跨文档引用

- 实体表名 / 字段语义：[data-model.md](./data-model.md)
- HTTP 路由前缀 → 后端 BC → SPA 客户端文件映射：[frontend-backend-contract.md](./frontend-backend-contract.md)
- 安装 / 启动 frontier 服务端的脚本：`deploy/install/install.sh` + `deploy/Dockerfile.frontier`
- 后台 worker 的多实例一致性策略：DB 行锁 + idempotent task key（详见 `internal/manager/biz/<domain>/` 中各 worker 注释）

---

## 11. 自查清单

- ✅ 描述了 Manager、EdgeAgent 两个进程的入口与角色
- ✅ 列出 `model/data/biz/service/server` 五层与每个层"不知道什么"
- ✅ 画出一次 HTTP 请求的中中间件链（5 步）
- ✅ 描述 frontier 反向隧道 Manager→Edge 调用链
- ✅ 列出后台 worker 清单（≥10 个）与触发频率
- ✅ 说明 `cmd/ongrid/main.go` 的组装步骤（≥10 步）
- ✅ 与 `data-model.md` 用了同一组实体名；与 `frontend-backend-contract.md` 用了同一组路由前缀
