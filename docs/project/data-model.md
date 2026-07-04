# 数据模型 — 鸟瞰

> 本文档描述 ongrid 的持久化数据模型：**实体（表）的组成、字段语义、实体间关系、设计原则与真值源**。聚焦"系统持久化了什么、为什么这么划分、跨表一致性如何保证"，不深入 SQL 实现细节。代码位置 `internal/{manager,iam}/model/<domain>/model.go`。

---

## 1. 总体鸟瞰

### 1.1 物理存储

- **主库**：MySQL 8（生产），通过 `internal/pkg/dbx` 抽象；开发环境兼容 SQLite，**同一份 GORM schema** 同时支持两个方言。
- **迁移**：`internal/manager/data/<domain>/store/migrate.go` 提供 `Migrate(db)` 函数，Manager 启动时按域顺序执行；不会写破坏性变更。
- **典型错误防护**：
  - 所有 `TEXT` 列都是 `NOT NULL`（MySQL 8 禁止 TEXT DEFAULT，Error 1101），Go 零值 `""` 满足；
  - 软删除统一使用 `gorm.io/plugin/soft_delete` 的毫秒级 `DeleteMarker` + `DeletedAt` 审计列双重机制，**保证唯一索引能区分历史行与活动行**。

### 1.2 实体全景图（按业务域分组）

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           ongrid 持久化实体全景                              │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  [身份域]  users · orgs · memberships · casbin policies                      │
│      │                                                                    │
│      ▼                                                                    │
│  [设备 / 边缘域]  edges ──M:N── edge_devices ──1:1── devices               │
│                  edges.access_key_id + secret_key_hash                     │
│                  devices.fingerprint / node_id / roles / online            │
│                                                                             │
│  [指标域]  host_metrics_raw · host_metrics_5m · host_metrics_1h            │
│          host_metrics_dead_letter · monitor_panels                         │
│                                                                             │
│  [告警 / 通知域]  alert_rules ──┐                                           │
│                    alert_incidents ◄── alert_events (审计流水)             │
│                    alert_silences ──┘                                       │
│                    notification_channels ── notification_deliveries        │
│                    investigation_reports                                    │
│                                                                             │
│  [AI / 智能体域]  chat_sessions ── chat_messages ── chat_tool_calls         │
│                  chat_mutating_proposals (旧)                              │
│                  user_agents · investigation_reports (跨域)               │
│                                                                             │
│  [业务拓扑域]  nodes ── relations ── relation_types                        │
│                  node_types · devices.node_id → nodes.id                   │
│                                                                             │
│  [Workflow / 报告域]  flows ── flow_runs ── flow_run_nodes                 │
│                      report_schedules ── reports                           │
│                                                                             │
│  [知识 / 安全域]  knowledge_repos ── (Qdrant: qdrant docs)                │
│                  ssh_identities · secrets (AES-GCM 加密)                   │
│                  system_settings (key/value)                               │
│                                                                             │
│  [集成域]  installed_skills · mcp_servers                                  │
│                                                                             │
│  [审批 / IM / WebShell]  approvals · im_apps ── im_threads                │
│                          webshell_sessions                                  │
│                                                                             │
│  [审计]  audit_logs (全员写、仅后台清)                                      │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. 实体清单（按 `internal/manager/model/` 子目录）

| 子目录 | 业务域 | 主要实体（表名） | 真值源角色 |
| --- | --- | --- | --- |
| `edge/` | 边缘探针身份 | `edges` | **agent 身份凭证**（access_key_id / secret_key_hash / online / last_seen_at / agent_version） |
| `device/` | 被纳管主机 | `devices`、`edge_devices` | **主机事实**（hostname / OS / CPU / Mem / Disk / 实时使用率 / roles / node_id） + **edge↔device M:N 关系** |
| `metric/` | 主机指标 | `host_metrics_raw`、`host_metrics_5m`、`host_metrics_1h`、`host_metrics_dead_letter` | **三层时序**：原始 10s 采样、5 分钟聚合、1 小时聚合 + 死信队列 |
| `alert/` | 告警 / 调查 | `alert_rules`、`alert_incidents`、`alert_events`、`alert_silences`、`notification_channels`、`notification_deliveries`、`investigation_reports` | **告警规则、故障事件、事件流水、静默、通知渠道与投递记录、AI 调查诊断报告** |
| `aiops/` | AI Agent 会话 | `chat_sessions`、`chat_messages`、`chat_tool_calls`、`user_agents` | **多轮对话、消息、工具调用、用户级 agent persona** |
| `flow/` | 可编程 Workflow | `flows`、`flow_runs`、`flow_run_nodes` | **可视化 DAG 工作流定义与执行历史** |
| `report/` | 周期报告 | `report_schedules`、`reports` | **报告计划与生成的报告产物** |
| `monitor/` | 监控面板 | `monitor_panels` | **用户自建 PromQL 面板 → 异步镜像到 Grafana** |
| `topology/` | 业务拓扑 | `nodes`、`relations`、`relation_types`、`node_types` | **业务实体顶点 + 有向关系图** |
| `knowledge/` | 知识库 | `knowledge_repos`、`ssh_identities`；文档本体在 **Qdrant** 向量库 | **Git 仓库注册表 + SSH 凭证**；向量文档以 Qdrant 为真值源 |
| `secret/` | 凭证保险库 | `secrets` | **命名凭证实例**（多字段 bag，Data 列 AES-GCM 加密） |
| `marketplace/` | 技能市场 | `installed_skills` | **已安装的 skill / plugin 包**（含 manifest_sha256、capability、credential binding） |
| `mcp/` | 外部 MCP 服务 | `mcp_servers` | **外部 MCP server 注册**（endpoint / credential 引用 / tools 缓存） |
| `setting/` | 系统设置 | `system_settings` | **Key/Value DB 驱动配置**（LLM 密钥 / Grafana / Prom / Loki / Tempo / WebSearch 等） |
| `audit/` | 审计 | `audit_logs` | **全员写、retention-only 清空的操作审计流水** |
| `approval/` | 危险操作审批 | `approvals` | **通用审批原语**：cloud_bash、restart_service、Flow 审批节点等都在这里排队等待人类决策 |
| `imbridge/` | IM 集成 | `im_apps`、`im_threads` | **IM 机器人注册 + 会话到 IM chat 的映射** |
| `webshell/` | WebSSH | `webshell_sessions` | **WebSSH 会话审计**（不存密码） |

---

## 3. 核心实体详解

### 3.1 设备 / 边缘域（**核心**）

> 这是平台所有能力的物理承载点。**May 2026 实体拆分**后，agent 身份（Edge）和主机事实（Device）已完全分离。

#### `edges`（`internal/manager/model/edge/model.go`）

| 关键字段 | 类型 | 语义 |
| --- | --- | --- |
| `id` | uint64 PK | 自增主键 |
| `name` | string(128) | 算子友好的展示名；新建时允许为空，**首次握手时由 HostInfo.hostname 自动回填** |
| `access_key_id` | string(32) unique | base64url(18 bytes) **24 字符** 随机 ID，探针握手凭证之一 |
| `secret_key_hash` | string(512) | **argon2id 哈希**；明文 SecretKey 仅在 `Create` 一次性返回，**永不入库** |
| `status` | enum('online','offline') | 探针在线状态；`HandleRegister` / `HandleHeartbeat` 翻 online，`HandleOffline` 翻 offline |
| `device_id` | *uint64 | **便捷指针**：指向 host device；真实"哪个 device 是这个 edge 的 host"以 `edge_devices` 联结表为准 |
| `agent_version` | string(32) | 探针二进制自我报告的 semver，**用于 SPA 审计探针版本漂移** |
| `delete_marker` / `deleted_at` | soft_delete | 软删除；保留 audit |

#### `devices`（`internal/manager/model/device/model.go`）

| 关键字段 | 类型 | 语义 |
| --- | --- | --- |
| `id` | uint64 PK | 自增主键 |
| `fingerprint` | string(128) unique | **稳定 per-host id**：`fp_` + sha256(hostid 或 hw_fingerprint)；克隆 VM 时优先用硬件指纹 |
| `user_id` | *uint64 | 首个 edge 注册者（审计用） |
| `name` | string(255) | 展示名；首次 seed 取 Hostname，运维可改 |
| `hostname` / `os` / `os_version` / `arch` / `kernel_version` | string | 主机事实 |
| `ip_address` | string(45) | 主要 IPv4 |
| `cpu_count` / `mem_total_bytes` / `disk_total_bytes` | int/uint64 | **容量事实** |
| `cpu_usage_pct` / `mem_usage_pct` / `disk_usage_pct` | float32 | **实时使用率反规范化**（由 host metric 摄入路径回填，避免列表渲染 JOIN） |
| `roles` | uint8 | **角色位掩码**（server=1 / storage=2 / network=4 / database=8），多角色并存 |
| `online` / `last_seen_at` | bool/time | 探针可达性的反规范化（任意 link 的 edge online → online） |
| `node_id` | *uint64 unique | **指向 `topology.nodes`**，NodeMirror hook 在 register 时写入；migration 回填兜底 |

#### `edge_devices`（联结表）

| 关键字段 | 类型 | 语义 |
| --- | --- | --- |
| `(edge_id, device_id, type)` | unique | M:N 联结；`type` 是 `EdgeDeviceRelationHost=1` / `Discovered=2` |

> **设计要点**：`edges.device_id` 是只读便捷指针（向后兼容旧调用），**真值源**是联结表。未来同一 host 上多 agent / edge 扫描 LAN 发现的设备都通过 type 区分。

---

### 3.2 指标域

#### `host_metrics_raw / host_metrics_5m / host_metrics_1h`

三层时序聚合，下采样时序固定：

| 层级 | 表 | 主键 | 字段 | 来源 |
| --- | --- | --- | --- | --- |
| Raw | `host_metrics_raw` | auto id | 完整字段（cpu/mem/load1/5/15/net_rx/tx/disk_pct） | 探针 10s 心跳 → tunnel `push_host_metrics` → `metric.Writer.WriteRaw`（`CreateInBatches` size=500） |
| 5m | `host_metrics_5m` | (edge_id, ts) | avg/max 对 gauge，sum 对 counter（net_rx/tx） | 后台 downsample 任务每 5 分钟跑一次 |
| 1h | `host_metrics_1h` | (edge_id, ts) | 同上，1 小时桶 | 后台 downsample 任务每小时 |

`dead_letter` 行保留 7 天用于**摄入路径重试耗尽后的样本**（每个失败样本一行，不聚合）。

> **域值类型 vs 行类型**：`metric.Point` / `Bucket5m` / `Bucket1h` 是 biz 层的无存储标记值对象；`HostMetric*` 是带 gorm tag 的行类型。`biz.Writer` / `biz.Reader` 不泄漏存储细节。

---

### 3.3 告警 / 通知域

#### `alert_rules` — 规则定义

| 关键字段 | 语义 |
| --- | --- |
| `rule_key` unique | 稳定 lower_snake id（dedupe key + 关联字段），内置 / 自建各占命名空间 |
| `kind` | 评估器种类：`metric_threshold` (UI 入口，编译为 metric_raw) / `metric_raw` / `metric_anomaly` / `metric_forecast` / `metric_burn_rate` / `log_match` / `log_volume` / `trace_latency` / `trace_error_rate` |
| `source_type` / `scope_type` | 信号源 + 作用范围（host / global / monitoring_pipeline） |
| `join_mode` | 'all' / 'any' |
| `severity` | info / warning / critical |
| `conditions_json` | JSON 编码的条件数组 |
| `labels_json` / `annotations_json` | 告警附带的标签和说明 |
| `notify_channel_ids_json` | **钉选渠道**（空 = 走全局 severity/scope 过滤） |
| `notify_window_seconds` + `notify_min_fires` | **发送策略 dampening**：窗口内 fires < 阈值则不通知（业务层拒绝"一个零一个非零"的非法混合） |

**RuleKind 编译**：UI 上 `metric_threshold` + `conditions[]` 在 `biz/alert.buildRuleRow` 落库前被改写为 `kind=metric_raw` + 单条 PromQL 表达式，**存储层只看到一种形态**。

#### `alert_incidents` — 故障事件

| 关键字段 | 语义 |
| --- | --- |
| `dedupe_key` unique | 同一"逻辑告警"在窗口内复用此 key |
| `device_id` | **May 2026 由 `edge_id` 重命名**，底层整数复用——为了延续 dedupe key 不变 |
| `status` | open / acknowledged / silenced / resolved |
| `event_count` / `first_fired_at` / `last_fired_at` | 重复触发的计数与时间窗 |
| `silenced_until` / `acknowledged_at` / `resolved_at` | 状态机相关时间戳 |
| `value` / `threshold` | 触发时的指标值与阈值 |
| `labels_json` / `annotations_json` | 标签与说明 |

#### `alert_events` — 事件流水（不可变）

每个 incident 状态变迁都产生一行（firing / acknowledged / silenced / resolved / reopened / note / notification_sent / notification_failed / inhibited / `ai_initial_diagnosis` 等），`actor_type` 区分系统 / 用户。

#### `notification_channels` + `notification_deliveries`

- **Channel**：webhook / slack / feishu / dingtalk / wecom / telegram；`match_severity_min` + `match_scope_types` 是路由器筛子。
- **Delivery**：每次发送尝试一行（pending → success / failed），含 `provider_message_id` / `request_json` / `response_json` / `error_message`；**PR-D 风格 retry worker** 据此重试。

#### `alert_silences` — 静默

`(scope, scope_type, device_id?, rule?, status)` 任意维度组合的静默，`starts_at` / `ends_at` 窗口期，`status` 在 active / expired / cancelled 之间流转。

#### `investigation_reports`（alert 子包下，但跨 aiops 域）

AI 调查诊断报告：一行一份（与 incident 1:1），含 `status` (pending/running/ready/failed/skipped) + `findings_md` + `evidence_json` + `suggested_actions_json` + `confidence` + `audit_session_id`（指回 aiops chat 会话）。

---

### 3.4 AI / 智能体域

#### `chat_sessions` — 多轮对话

| 关键字段 | 语义 |
| --- | --- |
| `id` | **UUID（char 36）**，路由不可枚举；客户端可以先用本地 id，服务端落库前不绑定 |
| `user_id` | 会话所有者 |
| `title` | 摘要，初始取首条用户消息 |
| `scope_json` | agent 派发 tool call 时允许的 edge 名 allowlist（null = 不限） |
| `agent_id` | 调用的 persona（如 "incident-investigator"）；null = 用户自由会话 |
| `parent_session_id` | 子 agent 场景：coordinator spawn 的 worker 指回 coordinator |
| `background` | fire-and-forget 子 agent |
| `related_incident_id` | 与告警的关联（IncidentDetail "深入诊断" 创建的会话） |
| `kind` | 'user' / 'investigation' — `/chat` 列表过滤 `kind='user'`，自动 spawn 的不淹没用户列表 |

#### `chat_messages` — 单条消息

UUID 主键；`role` 限定 user/assistant/tool/system；`tool_calls` 字段通过 `gorm:"-"` 标记为瞬态，不写入 messages 表（join 自 `chat_tool_calls`）。

#### `chat_tool_calls` — 工具调用

UUID；`llm_call_id`（OpenAI/Anthropic 等 LLM 协议分配的 id）严格配对 role=tool 消息，避免 DeepSeek v4+ 这种"严格 provider"因 orphan tool 拒绝请求。

---

### 3.5 业务拓扑域

#### `nodes` / `relations` / `relation_types` / `node_types`

**typed property graph**：

- `Node`：`{type, name, props_json}` —— 业务实体顶点
- `Relation`：`{src_id, dst_id, type, props_json}` —— 有向边；`(src,dst,type)` 唯一（一对顶点可有多条 type 不同的边）
- `RelationType` 元数据：`{name, direction, propagates, semantics_tag}` —— 决定边语义，**AIOps 通过 `SemanticsTag` 而非 name 路由**
- `NodeType` 元数据：`{name, display_name, display_name_en, builtin, tier, description}` —— 5 个内置类型（app/service/cluster/app/rack），运维可注册自定义类型

`devices.node_id` 指向 `nodes.id`（见 3.1），建立"物理主机 → 业务拓扑"的桥。

> `SemanticsTag` 闭合集合：`hard_dep` / `runtime_dep` / `aggregation` / `redundancy` / `observation` / `traffic` / `annotation` —— 新增 tag 应走 ADR 流程。

---

### 3.6 Workflow / 报告域

#### `flows` / `flow_runs` / `flow_run_nodes`

| 表 | 主键 | 关键字段 |
| --- | --- | --- |
| `flows` | uint64 | `graph_json`（nodes + edges + position 的 canvas 文档）、`version`（每次保存 +1，让历史 runs 保留执行时的快照） |
| `flow_runs` | **char(36) UUID** | `flow_id`、`flow_version`（快照）、`status`（pending/running/succeeded/failed/canceled）、`trigger_type`、`trigger_json` |
| `flow_run_nodes` | uint64 | 每个执行节点一行，`input_json` / `output_json`（表达式解析后真正传给执行器的值）/ `fired_port` |

Manager 重启时把 stale running 行的 `status` 扫到 failed（执行器 in-process，runs 不跨崩溃存活）。

#### `report_schedules` / `reports`

- `ReportSchedule` — cron 配置：kind (`daily` / `weekly` / `monthly` / `custom`) + cronspec + timezone + scope_json + channel_ids_json + agent_persona + next_fire_at
- `Report` — 产物：`schedule_id` + period (`period_start`, `period_end`) UNIQUE 防止重复生成；含 `content_json`（结构化卡片）+ `content_md`（导出 / IM / 搜索回退）+ `summary_text` + share_token（30 天 TTL）

---

### 3.7 知识库 / 安全 / 设置域

#### `knowledge_repos` — Git 仓库注册表

Git URL + branch + last_synced_at + file_count；**文档本体存 Qdrant**，DB 只存注册信息（清表删子）。

#### `ssh_identities` — SSH 凭证

私有 key + passphrase（AES 加密）；`hosts` JSON 数组支持 glob；按 host 匹配 `git clone` 时物化为 0600 tempfile 给 `ssh -i`。

#### `secrets` — 凭证保险库（n8n 风格）

| 字段 | 语义 |
| --- | --- |
| `name` unique | 人类标签（vault 引用名），不再是 env var 名 |
| `type` | 凭证类型（tencentcloud / aws / custom ...） |
| `data` | **AES-256-GCM 加密的 JSON 字段 map**（`pkg/secretbox`）；**永不通过 list API 明文返回** |
| `description` | 备注 |

> 消费侧：skill / MCP server 在 manifest 声明注入映射（哪字段 → 哪个 env 或文件），binding 决定哪个 vault 实例填哪个槽位。**manifest 拥有注入映射，binding 拥有选择权**，ongrid 不硬编码语义。

#### `system_settings` — 系统设置 KV

`(category, key)` unique 索引。`category` 命名空间：`llm` / `prom` / `grafana` / `loki` / `tempo` / `websearch` / `agent`。**Key 后缀 `_api_key / _secret / _token / _password` 自动被 list 端点脱敏**（无需 allowlist）。

`llm` 类别下每个 provider（openai/anthropic/zhipu/gemini/deepseek/kimi/custom）4 个键：`<provider>_api_key` / `<provider>_base_url` / `<provider>_models`（JSON 数组） / `<provider>_default_model`（单 slug）。

---

### 3.8 集成 / 审批 / IM / WebShell / 审计

#### `installed_skills`（marketplace）

`(tenant_id, pack_id)` unique —— 安装幂等；`manifest_sha256` 是磁盘内容锁（重复检测 + 启动校验）；`capabilities_json` 是用户审批的能力声明；`bindings_json` 是 vault slot → credential name 的选择。

#### `mcp_servers`

外部 MCP server 注册：name (unique 工具名前缀)、transport (`http` / `stdio`)、endpoint、credential name（**只存名字，不存密钥**）、`header_template_json`（带 `{{field}}` 占位符）、trusted / enabled、`tools_cache_json`（上次成功探测的工具快照）。

#### `approvals` — 通用审批原语

任意来源（agent cloud-bash / restart_service / Flow 审批节点）的危险操作都在这里排队。`kind` 路由执行器；`payload_json` 是执行器需要的动作 spec；`status` 走 `pending → approved → executed` 或 `rejected / failed`。**严格加法性**：不动 `chat_mutating_proposals` 表。

#### `im_apps` / `im_threads`

- `im_apps` — IM 机器人注册：`provider` (feishu/dingtalk/telegram/slack) × `app_id` unique；`mode` (stream/webhook)；`app_secret` 加密；Telegram 走 `allow_from`（user_id allowlist，**空 = 全部拒绝**，避免公开 bot 风险）；Slack 双 token JSON 存在 app_secret
- `im_threads` — `(im_app_id, im_chat_id, im_thread_id)` unique → `ongrid_session_id` 的映射；**所有发送者共享一个 session**，不按用户切分（控制 row 增长）

#### `webshell_sessions`

INSERT on open + UPDATE on close；含 `bytes_stdin` / `bytes_stdout` / `terminated_by` 枚举（user / idle / disconnect / admin_kill / ssh_auth_fail / ssh_exit / device_offline）。**永不含密码字段**。

#### `audit_logs`

`(actor, action, resource, outcome)` 一行；`action` 是稳定的 CRUD / 状态机动词集合（`auth_login_failed` / `user_create` / `rule_update` / `incident_ack` 等），**状态机子动作（enable vs disable）由 payload 表达，action 保持扁平**。`request_id` 串到 slog / trace。

---

## 4. 设计原则

### 4.1 多租户 / 单租户的当前取舍

`docs/project/README.md` 已说明 monorepo 多 BC 禁止跨包 import；数据模型侧目前所有表**没有 `org_id` 列**（私有 MVP 单租户）。模型层注释明文标注 "post-pivot there is no org_id"，未来扩展时：

- `installed_skills.tenant_id` 已预留（默认 0）
- `flows` / `reports` / `reports_schedules` 模型注释："no org_id column — private-MVP single tenant"
- `audit_logs` 也走"无 org_id"，按 `user_id` 索引

### 4.2 ID 类型策略

| 类型 | 使用场景 | 例 |
| --- | --- | --- |
| `uint64 autoIncrement` | 经典关系表 / 频繁 JOIN / 跨表外键 | edges、devices、alert_incidents、reports（schedule_id）、flows |
| `char(36) UUID` | **路由不可枚举 + 客户端可预生成** 的会话 / 产物类 | chat_sessions、chat_messages、chat_tool_calls、reports（id）、approvals |
| `*string` | 模型层无存储内容（来源 = Qdrant）的占位 | knowledge.Doc.ID 是 qdrant point id |

### 4.3 软删除（`DeleteMarker` + `DeletedAt`）双轨

`gorm.io/plugin/soft_delete` 提供毫秒级 `DeleteMarker` 整数 + 审计时间 `DeletedAt`：
- 业务默认查询被 `DeleteMarker = 0` 过滤（隐藏历史行）
- 唯一索引把 `DeleteMarker` 列也包进去，**允许同名 / 同 ID 的历史行存在而不冲突**
- 跨表 JOIN 也必须显式 `delete_marker = 0`（见 `reconcileOfflineOrphansSQL` 中的显式条件）

### 4.4 TEXT 列 NOT NULL

MySQL 8 禁止 TEXT DEFAULT；GORM `default:''` 在 TEXT 上无效 → 所有 TEXT 列**Go 零值 `""` 满足，业务层显式写入空字符串**。

### 4.5 JSON 列的双重角色

- **展示层 JSON 字符串**（`labels_json` / `conditions_json` / `config_json` / `capabilities_json` 等）：**真值源在数据库的字符串**，业务层用 `json.Unmarshal` 反序列化（带 `(*Rule) Conditions()` / `(*Channel) Config()` / `(*Silence) Matchers()` 等便捷方法）
- **领域值类型**（`metric.Point` / `Bucket5m` / `chat.Message.ToolCalls`）：**无 gorm tag**，**存储层用专门的行类型（`HostMetric*`）承载**

这种"存储字符串 + 内存结构体"的两层设计让 schema 演进（增字段）不需要迁移。

### 4.6 加密与脱敏

| 类型 | 机制 |
| --- | --- |
| 凭证（API Key / Token / SSH Private Key / 密码） | `secrets.data` 和 `ssh_identities.private_key` 经 `pkg/secretbox`（AES-256-GCM，`ONGRID_SECRET_KEY`）密封后入库 |
| 设置里的敏感 key | `system_settings.sensitive=true` 字段 + 后缀匹配（`*_api_key / *_secret / *_token / *_password`）由 list API 自动脱敏 |
| 审计日志里的敏感 payload | `audit_logs.PayloadJSON` 在写入前由业务层 redact（LLM keys / SSH keys / passwords / tokens 只记 **shape**，不记值） |

### 4.7 真值源（Source of Truth）矩阵

| 概念 | 真值源 | 次级 / 缓存 / 反规范化 |
| --- | --- | --- |
| Edge agent 身份 | `edges` 表 | 无 |
| Edge↔Device 关系 | `edge_devices` 联结表 | `edges.device_id` 便捷指针 |
| Host 事实 | `devices` 表 | `edges.host_info` 字段（向后兼容） |
| Host 拓扑归属 | `topology.nodes` | `devices.node_id` |
| 主机使用率 | `host_metrics_*` 时序表 | `devices.cpu_usage_pct` 等（反规范化，列表渲染零 JOIN） |
| Edge 在线状态 | `edges.status` | `devices.online`（反规范化） |
| 告警去重 | `alert_incidents.dedupe_key` unique | 无 |
| 文档内容 | Qdrant 向量库 | `knowledge_repos`（只存仓库元信息） |
| 凭证 | `secrets` 表（加密） | 无明文副本 |
| 配置 | `system_settings` 表 | 启动时载入内存 cache；env seed → DB |
| 审计 | `audit_logs` 表 | 无 |

---

## 5. 跨表一致性策略

### 5.1 拆分（M:N）与真值源

- `edges.device_id` 出现是**为了向后兼容**——拆出来自 `edge_devices` 后，老调用 `edge.DeviceID` 仍能编译。**真值源是联结表**，`device_id` 字段在 `HandleRegister` 中由业务层负责同步（见 `internal/manager/biz/edge/usecase.go`）。
- `devices.online` / `last_seen_at` 是 `edges` 在线状态的**反规范化**——任意一个链接的 edge online，device 就 online；reconciler 定期跑 `ReconcileOfflineOrphans` 把"无任何 online edge 但 device 还 online"的孤儿修回。

### 5.2 探针注册时的多表写入

`edge.Usecase.HandleRegister` 在一个事务里完成 7+ 步：

```
Get edge → 计算 fingerprint → RebindFingerprint (legacy→v3)
→ FindOrCreateByFingerprint (device upsert)
→ UpdateHostFacts (device 容量字段)
→ MarkOnline (device 在线状态)
→ EnsureNodeForDevice (topology node 反规范化)
→ SetNodeID (device 写入 node_id)
→ Link (edge_devices M:N)
→ SetDeviceID (edges.device_id 同步)
→ UpdateName (空 name 回填 hostname)
→ UpdateStatus (edge online + last_seen)
→ SetAgentVersion (探针版本)
```

任何中间步骤失败都会让 edge 处于不一致状态 → 调用方（tunnel lifecycle 回调）必须包外层重试或人工介入。

### 5.3 软删除与审计保留

软删除 ≠ 物理删除：
- `audit_logs` **永不软删**，由 retention 任务按策略清空
- `installed_skills` 软删后 `(tenant_id, pack_id)` 唯一索引释放，可重新安装同 id
- `alert_rules` 软删后 `notify_channel_ids_json` 引用的 channel 不会被 CountRulesReferencingChannel 算到 → 业务层会"看得见"该 channel 引用减少，正确允许删除

### 5.4 三时序聚合的不可变性

`host_metrics_raw` 一旦写入不会被修改（删除走 retention）；`5m` / `1h` 桶由后台 downsample 任务以 `Save` 覆盖（基于 `(edge_id, ts)` 复合主键保证幂等），重跑任务不会污染数据。

---

## 6. 跨文档引用

- 后端代码如何使用这些模型 → [backend-data-flow.md](./backend-data-flow.md)
- 前端 SPA 看到这些模型的什么子集 → [frontend-backend-contract.md](./frontend-backend-contract.md)
- 安装时数据库迁移如何落库 → `docs/install/` 与 `internal/manager/data/<domain>/store/migrate.go`
- E2E 测试如何使用这些实体 → `docs/test/e2e-catalog.md`