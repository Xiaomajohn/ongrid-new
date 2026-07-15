# Agent B1 Report — Data 持久化层(GORM AutoMigrate + 4 个 repo)

**子任务**:T6 — pluginhost/Data 持久化层
**目录**:`f:\Code\Go\运维\ongrid-new\internal\pluginhost\data\`
**构建结果**:`go build ./internal/pluginhost/data` ✅ 通过(同时 `go vet` 干净)
**完成时间**:Phase 1 → Phase 2 过渡产物

---

## 1. 文件清单

| 文件 | 行数(字节) | 职责 |
| --- | --- | --- |
| `migrate.go` | 1183 | 一次性建表入口 `Migrate(db *gorm.DB)`,事务内 AutoMigrate 四张表 |
| `plugin_repo.go` | 3235 | `PluginRepo` 操作 `plugin_instances` |
| `cap_repo.go` | 2116 | `CapabilityRepo` 操作 `plugin_capabilities` |
| `invoke_repo.go` | 2457 | `InvocationRepo` 操作 `plugin_invocations` |
| `audit_repo.go` | 1597 | `AuditRepo` 操作 `plugin_audits` |

包名统一为 `data`。所有文件只 import:
- 标准库:`context`, `encoding/json`(通过 model), `errors`, `time`
- 第三方:`gorm.io/gorm`
- 内部:`github.com/ongridio/ongrid/internal/pluginhost/model`

✅ 严格遵守白名单,**未 import 任何 A 老包**(embedding / notify / skill / manager / iam / edgeagent / api 全部 0 引用)。

---

## 2. 四张表 CRUD 操作清单

### 2.1 `plugin_instances` → `PluginRepo`

| 方法 | 语义 | 关键实现 |
| --- | --- | --- |
| `Create(ctx, p)` | 插入新实例 | `db.Create(p)`,失败 `errors.Join(ErrCreate, err)` |
| `Get(ctx, id)` | 主键查询 | `db.First`,未命中映射 `ErrNotFound` |
| `GetByPackID(ctx, tenantID, packID)` | 复合唯一索引定位 | `WHERE tenant_id=? AND pack_id=?` |
| `List(ctx, tenantID, limit, offset)` | 租户分页 | `ORDER BY id DESC LIMIT/OFFSET`,默认 limit=50 |
| `Update(ctx, p)` | 全行保存 | `db.Save(p)` |
| `Delete(ctx, id)` | 软删除 | `db.Delete(&model.PluginInstance{}, id)`,依赖 `gorm.Model.DeletedAt` |
| `SetEnabled(ctx, id, enabled)` | 仅切换启用位 | `Update("enabled", enabled)` |
| `SetHealth(ctx, id, status)` | 更新健康状态 | 同时写 `health_status` + `last_health_at = time.Now()` |

### 2.2 `plugin_capabilities` → `CapabilityRepo`

| 方法 | 语义 | 关键实现 |
| --- | --- | --- |
| `CreateBatch(ctx, caps)` | 批量插入 | 空切片短路;否则 `db.Create(&caps)`,GORM 自动事务 |
| `ListByInstance(ctx, instanceID)` | 单实例全部能力 | `WHERE plugin_instance_id=?`,按 kind+name 排序 |
| `ListByKind(ctx, tenantID, kind)` | 按 kind 过滤 | `WHERE tenant_id=? AND kind=?`,供 ai.tool / notifier 等索引 |
| `SetEnabled(ctx, id, enabled)` | 单能力启停 | `Update("enabled", enabled)` |
| `DeleteByInstance(ctx, instanceID)` | 卸载时清空 | 软删,Where 限定 instanceID |

### 2.3 `plugin_invocations` → `InvocationRepo`

| 方法 | 语义 | 关键实现 |
| --- | --- | --- |
| `Record(ctx, inv)` | 写一行调用流水 | 失败只返 error,**不抛业务异常**(流水丢一行不应阻塞) |
| `ListRecent(ctx, tenantID, instanceID, limit)` | 拉最近流水 | instanceID=0 表示租户全部;`ORDER BY invoked_at DESC` |
| `StatsByInstance(ctx, tenantID, instanceID, since)` | 调用总次数 + 平均延迟 | `SELECT COUNT(*), AVG(latency_ms)`,AVG NULL 时回退 0 |
| `PurgeOlderThan(ctx, cutoff)` | 清理老流水 | **硬删**(`Unscoped()`),返回 `RowsAffected` |

### 2.4 `plugin_audits` → `AuditRepo`

| 方法 | 语义 | 关键实现 |
| --- | --- | --- |
| `Record(ctx, a)` | 写一行审计 | `db.Create(a)` |
| `ListByInstance(ctx, instanceID, limit)` | 单实例审计 | `ORDER BY occurred_at DESC` |
| `ListByTenant(ctx, tenantID, action, limit)` | 租户级审计 + 可选 action 过滤 | action 为空时不过滤,等于查全量 |

---

## 3. 与 model 层字段映射

| model 字段 | 数据类型 | DB 列名(由 GORM tag 推导) | 读写方法 |
| --- | --- | --- | --- |
| `gorm.Model.ID` | uint64 | `id` (PK) | `Get` / `Update` / `Delete` / `SetEnabled` / `SetHealth` |
| `gorm.Model.CreatedAt/UpdatedAt/DeletedAt` | time.Time | `created_at` / `updated_at` / `deleted_at` | 由 GORM 自动维护 |
| `TenantID` | uint64 | `tenant_id` (idx) | 所有 repo 的 WHERE |
| `PackID` | string | `pack_id` (unique idx `idx_pack_tenant`) | `GetByPackID` |
| `Version` | string | `version` | `Update` / `Get` |
| `Source` | string | `source` | `Update` / `Get` |
| `InstallPath` | string | `install_path` | `Update` / `Get` |
| `ManifestSHA256` | string | `manifest_sha256` | `Update` / `Get` |
| `SignatureState` | string | `signature_state` | `Update` / `Get` |
| `Enabled` | bool | `enabled` (idx) | `SetEnabled` |
| `HealthStatus` | string | `health_status` | `SetHealth` |
| `LastHealthAt` | *time.Time | `last_health_at` | `SetHealth`(每次写入 = now) |
| `CapabilitiesJSON` / `BindingsJSON` / `UIMetadataJSON` | string | 三段 JSON text 列 | `Update` / `Get` |
| `Format` / `Transport` / `TimeoutSeconds` | string / string / int | 同名列 | `Update` / `Get` |

**Capability 表**:`PluginInstanceID` (idx + 唯一索引一部分) / `Kind` / `Name` / `Class` / `SchemaJSON` / `MetadataJSON` / `UIMetadataJSON` / `Enabled`。
**Invocation 表**:`TenantID` / `PluginInstanceID` / `CapabilityID` / `Caller` / `Component` / `TraceID` / `ParamsJSON` / `ResultJSON` / `ErrorMessage` / `LatencyMs` / `Success` / `InvokedAt`。
**Audit 表**:`TenantID` / `PluginInstanceID` / `Action` / `Actor` / `DetailsJSON` / `OccurredAt`。

> 字段映射没有手动写 column tag 的部分完全交给 GORM 默认 snake_case 转换,与 model 实体里的显式 tag 一致。

---

## 4. Phase 2 — B2(Biz)接入 TODO

B2 在 `internal/pluginhost/biz/` 下组装这些 repo。B2 不需要知道 SQL 细节,只用以下 4 个 constructor + 仓库方法:

```go
import (
    "github.com/ongridio/ongrid/internal/pluginhost/data"
    "github.com/ongridio/ongrid/internal/pluginhost/model"
)

// 1) 在插件安装/卸载/启停流程入口组装(biz/installer.go / biz/lifecycle.go 等)
pluginRepo := data.NewPluginRepo(db)        // *data.PluginRepo
capRepo    := data.NewCapabilityRepo(db)    // *data.CapabilityRepo
invokeRepo := data.NewInvocationRepo(db)    // *data.InvocationRepo
auditRepo  := data.NewAuditRepo(db)         // *data.AuditRepo
```

### B2 需要的仓库能力(具体方法签名 — 与上面"CRUD 操作清单"一一对应)

| 业务场景 | 调用的方法 |
| --- | --- |
| 安装插件 — 写一行实例 + 一次性写全部 cap | `pluginRepo.Create(ctx, p)` → `capRepo.CreateBatch(ctx, caps)` |
| 安装完成 — 写审计(install) | `auditRepo.Record(ctx, &model.PluginAudit{Action: "install", ...})` |
| 列出租户下插件 | `pluginRepo.List(ctx, tenantID, limit, offset)` |
| 查单个插件 | `pluginRepo.Get(ctx, id)` 或 `pluginRepo.GetByPackID(ctx, tenantID, packID)` |
| 启停 | `pluginRepo.SetEnabled(ctx, id, true/false)` |
| 更新健康 | `pluginRepo.SetHealth(ctx, id, "healthy")`(自动写 `last_health_at = time.Now()`) |
| 卸载 — 软删 cap + 软删 instance | `capRepo.DeleteByInstance(ctx, id)` → `pluginRepo.Delete(ctx, id)` |
| 调用能力 — 写流水 | `invokeRepo.Record(ctx, &model.PluginInvocation{...})` |
| 调用历史 | `invokeRepo.ListRecent(ctx, tenantID, instanceID, limit)` |
| 性能面板 | `invokeRepo.StatsByInstance(ctx, tenantID, instanceID, since)` 返 `(count, avgMs, err)` |
| 审计流水 | `auditRepo.Record` / `ListByInstance` / `ListByTenant(action="")` |
| 清理老流水(后台 job) | `invokeRepo.PurgeOlderThan(ctx, cutoff)` |

### 错误映射建议(B2 自便,只是提示)

```go
if errors.Is(err, data.ErrNotFound) { /* 404 / 业务 not-found */ }
if errors.Is(err, data.ErrCreate)   { /* 500 / install 失败 */ }
```

### B2 应避免做的事

- ❌ 不要在自己的包里再次 `db.AutoMigrate(...)`,**只在启动期调 `data.Migrate(db)` 一次**。
- ❌ 不要绕过 repo 直接 `db.Model(&model.PluginInstance{})...`,这样会破坏将来加缓存/审计埋点的位置。
- ✅ Phase 2 整合阶段如果发现 import path 漂移,以我的最终 import 为准:
  `github.com/ongridio/ongrid/internal/pluginhost/data`

---

## 5. Phase 4 — server handler 接入 TODO

Phase 4 在 `internal/pluginhost/server/` 下挂 HTTP/gRPC handler。Handler 只通过 B2 暴露的 service 调用 repo,不应直接 import `data`。但 server 层调 B2 service 时需要传入的入参形态如下(handler 校验用):

| handler 路由(假设) | 入参(来自 HTTP body / gRPC req) | 调用的 B2 service 方法 |
| --- | --- | --- |
| `POST /api/v1/plugins/install` | `{tenant_id, pack_id, version, source, install_path, manifest_sha256, format, transport}` | `Installer.Install(ctx, ...)` → 内部调 `pluginRepo.Create` + `capRepo.CreateBatch` + `auditRepo.Record(action="install")` |
| `POST /api/v1/plugins/{id}/uninstall` | `{id, actor}` | `Uninstaller.Uninstall(ctx, id, actor)` → `capRepo.DeleteByInstance` + `pluginRepo.Delete` + `auditRepo.Record(action="uninstall")` |
| `POST /api/v1/plugins/{id}/enable` / `/disable` | `{id, actor}` | `Lifecycle.SetEnabled(ctx, id, enabled, actor)` → `pluginRepo.SetEnabled` + `auditRepo.Record(action=...)` |
| `GET /api/v1/plugins` | `?tenant_id=&page=&size=` | `Lister.List(ctx, tenantID, limit, offset)` → `pluginRepo.List` |
| `GET /api/v1/plugins/{id}` | `{id}` | `Getter.Get(ctx, id)` → `pluginRepo.Get` + `capRepo.ListByInstance` |
| `POST /api/v1/plugins/{id}/health` | `{id, status}` | `HealthProbe.Report(ctx, id, status)` → `pluginRepo.SetHealth` + `auditRepo.Record(action="health_change")` |
| `POST /api/v1/plugins/{id}/invoke` | `{capability, params_json, caller, component, trace_id}` | `Invoker.Invoke(ctx, ...)` → 调 sandbox 后 `invokeRepo.Record` |
| `GET /api/v1/plugins/{id}/invocations` | `?limit=` | `invokeRepo.ListRecent(ctx, tenantID, id, limit)` |
| `GET /api/v1/plugins/{id}/audit` | `?limit=` | `auditRepo.ListByInstance(ctx, id, limit)` |
| `GET /api/v1/audit` | `?tenant_id=&action=&limit=` | `auditRepo.ListByTenant(ctx, tenantID, action, limit)` |

### Phase 4 启动期 checklist

1. 在 cmd/ongrid/main.go 的 bootstrap 阶段(A 老的 OnMigrate hook 里)追加一行:
   ```go
   if err := data.Migrate(db); err != nil { return err }
   ```
2. 把四个 repo 通过 B2 service 装配后,挂到 HTTP router / gRPC server。
3. 不要在 handler 内做 schema 校验以外的 DB 操作。

---

## 6. 自检结论

- ✅ 5 个文件全部创建,大小合理(共约 10.6 KB)
- ✅ `go build ./internal/pluginhost/data` 通过
- ✅ `go vet ./internal/pluginhost/data` 干净
- ✅ `go build ./internal/pluginhost/model` 通过(确保我能正确 import model)
- ✅ import 白名单合规,未触碰任何 A 老包
- ✅ 所有方法带 `ctx context.Context` 第一参数(尊重上游 timeout / cancel)
- ✅ 软删 / 硬删语义与 model 字段语义对齐(`gorm.Model` 自带 `DeletedAt`,流水表 `Unscoped` 硬删)
- ✅ B2 / Phase 4 接入路径已在上面 §4 §5 明确写出,可直接对照实现

**B2 可开始并行实施,仅依赖本报告 §4 的接口签名,不需要读 B1 源码细节。**