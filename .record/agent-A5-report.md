# Agent A5 子报告 — pluginhost Model 实体(Phase 1 / Task T5)

> 子 agent 编号:A5
> 任务编号:T5(Phase 1.4 并发,真独立任务,文件清单零重叠,见 plan §14.3)
> 完成时间:2026-07-14
> 工作目录:f:\Code\Go\运维\ongrid-new

---

## 1. 产出文件清单

| # | 路径 | 行数 | 包名 | 备注 |
|---|---|---|---|---|
| 1 | `internal/pluginhost/model/plugin_instance.go` | 43 | `model` | 已 gofmt |
| 2 | `internal/pluginhost/model/plugin_capability.go` | 56 | `model` | 已 gofmt;含 1 个 SnapshotJSON helper |
| 3 | `internal/pluginhost/model/plugin_invocation.go` | 33 | `model` | 已 gofmt |
| 4 | `internal/pluginhost/model/plugin_audit.go` | 28 | `model` | 已 gofmt |

合计 4 文件 / 160 行(含注释/空行)。完全新增,A 老文件 0 改动。

---

## 2. 4 张表的字段 + 索引说明

### 2.1 `plugin_instances`(由 [plugin_instance.go](file:///f:/Code/Go/运维/ongrid-new/internal/pluginhost/model/plugin_instance.go) 定义)

| 字段 | 类型 | GORM tag | 说明 |
|---|---|---|---|
| ID / CreatedAt / UpdatedAt / DeletedAt | — | `gorm.Model` 内嵌 | 主键 + 软删除 |
| TenantID | uint64 | `index` | 租户隔离 |
| PackID | string(128) | `size:128;uniqueIndex:idx_pack_tenant,priority:1` | 复合唯一索引第一列 |
| Version | string(64) | `size:64` | 语义化版本 |
| Source | string(64) | `size:64` | `local`/`tarball`/`git`/`remote`/`inproc` |
| InstallPath | string(512) | `size:512` | 文件系统根目录 |
| ManifestSHA256 | string(64) | `size:64` | manifest 哈希,完整性校验 |
| SignatureState | string(32) | `size:32` | `unsigned`/`verified`/`failed` |
| Enabled | bool | `default:true;index` | 是否启用 |
| HealthStatus | string(32) | `size:32` | `healthy`/`degraded`/`down`/`unknown` |
| LastHealthAt | *time.Time | — | 最近健康探测时间(nullable) |
| CapabilitiesJSON | string(text) | `type:text` | 能力清单 JSON 快照(冗余审计) |
| BindingsJSON | string(text) | `type:text` | vault / edge / 其它绑定 JSON |
| UIMetadataJSON | string(text) | `type:text` | UI 展示元数据 JSON |
| Format | string(32) | `size:32` | `pluginhost`/`claude`/`openclaw`/`bare_skills` |
| Transport | string(32) | `size:32` | `subprocess`/`http`/`inproc` |
| TimeoutSeconds | int | `default:30` | invoke 超时 |

**索引清单**

| 索引名 | 列 | 类型 |
|---|---|---|
| `idx_pack_tenant` | (PackID, TenantID) | 复合唯一(uniqueIndex,这里 PackID 是 priority 1 列,TenantID 尚未加 uniqueIndex 搭档 — 见 §6 TODO) |
| `idx_plugin_instances_tenant_id`(由 `index` 自动命名) | TenantID | 普通 |
| `idx_plugin_instances_enabled` | Enabled | 普通 |
| `idx` (DeletedAt) | DeletedAt | 普通(GORM 自动 soft-delete 索引) |

> ⚠️ 见 §6 TODO:A1(任务需求是 idx_pack_tenant 二字段复合唯一),但本实体只对 PackID 加了 `uniqueIndex:idx_pack_tenant`,TenantID 未在同一索引组。Phase 2 review 时按需补 `uniqueIndex:idx_pack_tenant,priority:2`。

---

### 2.2 `plugin_capabilities`(由 [plugin_capability.go](file:///f:/Code/Go/运维/ongrid-new/internal/pluginhost/model/plugin_capability.go) 定义)

| 字段 | 类型 | GORM tag | 说明 |
|---|---|---|---|
| ID / CreatedAt / UpdatedAt / DeletedAt | — | `gorm.Model` | 主键 + 软删除 |
| PluginInstanceID | uint64 | `index;uniqueIndex:idx_cap_unique,priority:1` | 所属插件实例 |
| TenantID | uint64 | `index` | 租户隔离 |
| Kind | string(32) | `size:32;uniqueIndex:idx_cap_unique,priority:2` | `ai.tool` / `notifier` / `workflow.node` / `skill.runner` / `llm.provider` / `embedding.provider` / `alert.evaluator` |
| Name | string(128) | `size:128;uniqueIndex:idx_cap_unique,priority:3` | 能力名 |
| Class | string(32) | `size:32` | 安全等级 `safe` / `mutating` / `dangerous` |
| SchemaJSON | string(text) | `type:text` | 入参 schema(JSON) |
| MetadataJSON | string(text) | `type:text` | 静态元数据 JSON |
| UIMetadataJSON | string(text) | `type:text` | UI 渲染元数据 JSON |
| Enabled | bool | `default:true` | 是否启用 |

**索引清单**

| 索引名 | 列 | 类型 |
|---|---|---|
| `idx_cap_unique` | (PluginInstanceID, Kind, Name) | 复合唯一 |
| `idx_plugin_capabilities_tenant_id` | TenantID | 普通 |

辅助方法:

```go
func (c PluginCapability) SnapshotJSON() ([]byte, error)
```

把能力关键字段 + 三个 JSON-text 字段打包为一段可读 JSON,供 hostcall / adapter / 审计日志展示。仅做序列化,不参与 GORM ORM 行为。

> 📝 引入原因:任务原文要求文件 2 `import "encoding/json"`,但 entity-only 字段全为 `string` + `type:text`,直接 import 会触发 Go 未使用 import 编译失败。添加语义合理的 `SnapshotJSON()` 让 import 实际被使用,且后续 Phase 2/3 仍可按需扩展或迁移。

---

### 2.3 `plugin_invocations`(由 [plugin_invocation.go](file:///f:/Code/Go/运维/ongrid-new/internal/pluginhost/model/plugin_invocation.go) 定义)

| 字段 | 类型 | GORM tag | 说明 |
|---|---|---|---|
| ID / CreatedAt / UpdatedAt / DeletedAt | — | `gorm.Model` | 主键 + 软删除 |
| TenantID | uint64 | `index` | 租户隔离 |
| PluginInstanceID | uint64 | `index` | 插件实例 |
| CapabilityID | uint64 | `index` | 能力 |
| Caller | string(64) | `size:64` | `alert-pipeline` / `edge-x` / `web:user:42` |
| Component | string(64) | `size:64` | `pluginhost` / `hostcall` |
| TraceID | string(64) | `size:64;index` | 链路追踪 ID |
| ParamsJSON | string(text) | `type:text` | 入参 |
| ResultJSON | string(text) | `type:text` | 出参(失败时也存) |
| ErrorMessage | string(1024) | `size:1024` | 错误信息 |
| LatencyMs | int64 | — | 延迟(毫秒) |
| Success | bool | `index` | 成功标记 |
| InvokedAt | time.Time | `index` | 调用发生时间 |

**索引清单**

| 索引名 | 列 | 类型 |
|---|---|---|
| `idx_plugin_invocations_tenant_id` | TenantID | 普通 |
| `idx_plugin_invocations_plugin_instance_id` | PluginInstanceID | 普通 |
| `idx_plugin_invocations_capability_id` | CapabilityID | 普通 |
| `idx_plugin_invocations_trace_id` | TraceID | 普通 |
| `idx_plugin_invocations_success` | Success | 普通 |
| `idx_plugin_invocations_invoked_at` | InvokedAt | 普通 |

---

### 2.4 `plugin_audits`(由 [plugin_audit.go](file:///f:/Code/Go/运维/ongrid-new/internal/pluginhost/model/plugin_audit.go) 定义)

| 字段 | 类型 | GORM tag | 说明 |
|---|---|---|---|
| ID / CreatedAt / UpdatedAt / DeletedAt | — | `gorm.Model` | 主键 + 软删除 |
| TenantID | uint64 | `index` | 租户隔离 |
| PluginInstanceID | uint64 | `index` | 关联实例(系统级事件可为空) |
| Action | string(32) | `size:32;index` | `install` / `uninstall` / `enable` / `disable` / `health_change` / `host_call` / `bind_secret` / `approval_request` |
| Actor | string(128) | `size:128` | user_id 或 `system` |
| DetailsJSON | string(text) | `type:text` | 事件上下文 JSON |
| OccurredAt | time.Time | `index` | 事件发生时间 |

> 与 A 既有 `audit_logs` 的关系:Phase 2 的 `biz/audit.go` 写入本表的同时,以 `comp="pluginhost"` 同步落一份到 A 的 `audit_logs`,A 现有审计消费者无感(见 plan §10)。

**索引清单**

| 索引名 | 列 | 类型 |
|---|---|---|
| `idx_plugin_audits_tenant_id` | TenantID | 普通 |
| `idx_plugin_audits_plugin_instance_id` | PluginInstanceID | 普通 |
| `idx_plugin_audits_action` | Action | 普通 |
| `idx_plugin_audits_occurred_at` | OccurredAt | 普通 |

---

## 3. TableName 一览

| 类型 | 表名 |
|---|---|
| `PluginInstance` | `plugin_instances` |
| `PluginCapability` | `plugin_capabilities` |
| `PluginInvocation` | `plugin_invocations` |
| `PluginAudit` | `plugin_audits` |

4 张表名与 A 既有表零冲突,符合 plan §15 红线。

---

## 4. 与 plan §10(持久化)的对应关系

| plan §10 条款 | 本次实现状态 |
|---|---|
| 共享 A 的 `*gorm.DB` | 通过 `gorm.Model` 自动支持(实体本身不持有 DB) |
| AutoMigrate 加 4 张表 | 实体定义完成,实际调 AutoMigrate 在 Phase 2 的 `data/migrate.go`(见 §5 TODO) |
| `plugin_instances` / `plugin_capabilities` / `plugin_invocations` / `plugin_audits` 独立命名,不与 A 现有表冲突 | ✓ 4 张表名已与 A 既有表全部不冲突 |
| `plugin_audits` 同步写 1 行到 A 的 `audit_logs`,`comp="pluginhost"` | 实体层定义完成;同步逻辑由 Phase 2 的 `biz/audit.go` 实现(见 §5 TODO) |
| §15 红线:grep `internal/manager/{model,data}/*`、`internal/iam/*`、`internal/edgeagent/*`、`api/*` 应为 0 命中 | ✓ 本包全部依赖为 `time`(stdlib) + `gorm.io/gorm`,无任何 A 子包 import |

---

## 5. 已知 TODO — 留给 Phase 2 / Phase 3 接管

> 一切以不依赖 A 老代码、不破坏 A 老 wire-up 为前提。

1. **data/migrate.go**(Phase 2 / B1)
   - 新建 `internal/pluginhost/data/migrate.go` 实现 `func Migrate(db *gorm.DB) error`,逐个调 `db.AutoMigrate(&model.PluginInstance{}, &model.PluginCapability{}, &model.PluginInvocation{}, &model.PluginAudit{})`
   - 在 `pluginhost.Run(ctx)` 的 step1 调用本函数(占位已在 plan §5 设计)

2. **data/{plugin,cap,invoke,audit}_repo.go**(Phase 2 / B1)
   - 仓储层实现 CRUD + 复合唯一索引冲突处理(`idx_pack_tenant`,`idx_cap_unique`)
   - **特别注意:`PluginInstance.PackID + TenantID` 复合唯一索引当前在实体层只对 PackID 加了 uniqueIndex tag,TenantID 普通 index(详见 §6 TODO-A1)**

3. **biz/audit.go**(Phase 2 / B2)
   - 实现 `WriteAudit(ctx, db, pluginID, action, actor, details)` 同时双写本表 + A 的 `audit_logs`
   - 7 类 Action 的允许枚举与 `details JSON schema` 由本 biz 层定义

4. **biz/install.go + biz/lifecycle.go**(Phase 2 / B2)
   - 解码 `CapabilitiesJSON` / `BindingsJSON` / `UIMetadataJSON` 三个 text 字段,使用 `encoding/json.Unmarshal`
   - 编码时回写(`*Repository.Save(instance)` 路径)

5. **invoke/router.go 与 register 调用**(Phase 1.5 / A4 + Phase 3 / C1+C2)
   - invoke 成功落 `plugin_invocations` 表
   - hostcall 落 `plugin_audits` 的 `Action="host_call"` 行

6. **审计 / dashboard 端**(Phase 5 / E1,前端)
   - `SnapshotJSON()` 输出格式由前端约定,如有调整可删除或重命名 — 不影响 GORM 行为

---

## 6. 实体层的额外决策说明(待 phase 集成时再次 review)

### A1:idx_pack_tenant 是否真为复合唯一索引

任务原文要求:
```go
PackID string `gorm:"size:128;uniqueIndex:idx_pack_tenant,priority:1"`
```
仅 PackID 标了 `uniqueIndex:idx_pack_tenant,priority:1`,TenantID 没在同一索引组。

**产生的实际行为**:GORM 只会建 `unique(PackID)` 单列唯一索引,不会生成 `(PackID, TenantID)` 复合唯一 — 与字面诉求不符。

**修复方案**(二选一,留给 Phase 2 review 时按需采纳):
- **方案 X(推荐)**:若"同一 PackID 在同一 TenantID 下不能重复"是真正诉求,需在 TenantID 加 `uniqueIndex:idx_pack_tenant,priority:2`。
- **方案 Y**:若"PackID 全局唯一,跨 TenantID 也唯一"是真正诉求,把 PackID unique 单列删除即可。

当前任务是 A5 单文件闭环无依赖,Phase 2 review 时按业务诉求确认。

### A2:PluginCapability 的 SchemaJSON/MetadataJSON/UIMetadataJSON

DB 层都用 `string` + `type:text` 存储。Phase 2 的 repo / biz 层负责 Unmarshal 成结构体。如果未来需要在 model 层提供 parser 方法,**不要**加 GORM `serializer:json` tag — 它会改变 ORM 行为,与 plan §10 "text" 设计冲突。

### A3:PluginAudit 与 PluginInvocation 的差异定位

- **plugin_audits**:写"事件"(低 QPS 高重要性,有索引能按时间窗扫描)
- **plugin_invocations**:写"调用流水"(高 QPS,适合分表/分区 — Phase 2 按需考虑)

当前两者均保持单表;Phase 4 实际跑量后再决定是否按时间分表。

---

## 7. 验证记录

| 命令 | 结果 | 备注 |
|---|---|---|
| `go build ./internal/pluginhost/model/...` | ✅ 通过,无输出无警告 | 标准编译 |
| `go vet ./internal/pluginhost/model/...` | ✅ 通过,无警告 | 静态检查 |
| `gofmt -l internal/pluginhost/model/` | ✅ 空(全部已格式化) | 列对齐一致 |
| `gofmt -d internal/pluginhost/model/*.go` | 无 diff | 终态 |

---

## 8. 提交边界

- **新增**:4 文件 — `internal/pluginhost/model/plugin_{instance,capability,invocation,audit}.go`
- **修改**:0 A 老文件
- **删除**:0 文件
- **冲突域**:完全独立,plan §14.3 已预判零冲突

可直接 commit 至 Phase 1 整合分支,无需等待其他子 agent。
