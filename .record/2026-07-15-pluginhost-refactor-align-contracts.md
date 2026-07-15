# 2026-07-15 · pluginhost 重构:对齐 server / invoke 消费方契约

## 问题
commit 21d0d92a 提交后,`internal/pluginhost/` 内多个子包(registry / runtime /
biz / model / pluginhost.go)与 `server/handler.go` + `invoke/router.go` 的消费
契约存在不一致:

- `server/handler.go` 调 `Reg.SetEnabledByID(id, true)` / `Reg.LookupByID(id)` /
  `Reg.LookupCapabilityByInstanceID(id, name)`,但 commit 21d0d92a 的 registry
  只暴露 `SetEnabled(string, bool)` / `Lookup(string)`(key 是 PackID)
- `biz/lifecycle.go` 的 `strpackid(uint64) string` 把 uint64 主键转成字符串
  才能调 `Reg.SetEnabled` / `Reg.Unregister`,与 server 用 uint64 主键的路径
  产生二次封装
- `runtime.Runtime` 接口旧版自带 `PluginInstance` / `Capability` 类型,与
  `registry.PluginInstance` / `registry.Capability` 双轨并存,导致 invoke
  router 要做类型映射
- `model.PluginInstance` 含 `Format` / `Transport` / `TimeoutSeconds` 字段,
  这三个字段实际由 `manifest.PluginManifest` / runtime 内部持有,放在
  gorm.Model 上会引起 "DB 字段和真实数据源不同步" 的潜在风险
- `pluginhost.go` 的 `Run` 流程用 fmt.Println 占位,Phase 1.5 已被 server
  端的 wire-up 接管 Run 入口;Phase 1.5 后 Run 内部的"step1 migrate /
  step2 load" 等 print 占位需要清理掉,避免日志噪声

## 加了什么

### 1. registry 新增 uint64 主键索引能力

新增 3 个方法,基于 uint64 主键(ID = `plugin_instances.id`)反查:

- `LookupByID(id uint64) (*PluginInstance, bool)` — 按 DB 主键查单个 instance
- `SetEnabledByID(id uint64, enabled bool) error` — 按 DB 主键切启用位
- `LookupCapabilityByInstanceID(id uint64, capName string) (*Capability, bool)` —
  复合查询,(DB 主键, cap 名) 一次拿到 capability,避免调用方先 `LookupByID`
  再 `LookupCapability` 的两次遍历

辅助函数:
- `cloneInstance(*PluginInstance) *PluginInstance` — 深拷贝,处理 `LastHealthAt`
  指针与 `Capabilities` slice;`SetEnabledByID` 写入时拷一份避免外部引用脏读
- `cloneCapability(*Capability) *Capability` — 深拷贝 `Schema`(`json.RawMessage`)
  + `Metadata` map

### 2. runtime 去掉自有类型,改用 registry 类型

`runtime.PluginInstance` / `runtime.Capability` 已删除,`Runtime.Invoke` 直接
接 `*registry.PluginInstance` + `*registry.Capability`。理由:

- registry 是 pluginhost 唯一的"实例/能力"权威源(进程级 + 持久化都用它)
- 多份类型会让 invoke router 频繁做字段映射,易漂移
- runtime 只关心 transport,不关心"实例长啥样",类型借用 registry 即可

调整后 `Pool.Add` / `Pool.Get` 签名从 `pluginID string` 改为 `packID string`
(语义一致,key 名变更以表达"以 PackID 作为 pool key"的事实)。

`Pool.Add` 由 `error` 返值改为无返值,重复 Add 走"覆盖 + Close 旧"语义;
`Pool.Remove` 移除(同上);`Pool.CloseAll` 由 `error` 返值改为无返值
(错误吞掉,Close 失败不应阻断其它 transport 回收)。

### 3. biz/lifecycle 改用 uint64 主键

- `Enable` / `Disable` 改调 `Reg.SetEnabledByID(pluginID, enabled)`,删除
  `strpackid(uint64) string` 的中间转换
- `Uninstall` 流程:先 `Reg.LookupByID(pluginID)` 拿 packID,再
  `Reg.Unregister(packID)`,把"DB 主键 → PackID"的桥接放到 lifecycle 内
- 删除 `lookupInstance(key string)` 辅助函数(被 LookupByID 替代)
- `strpackid(uint64) string` 保留,标注为 `// nolint:unused`,留给历史/调试

### 4. model.PluginInstance 收敛字段

移除与 manifest 重复的字段(由 manifest 持有更合适):

- 删除 `Format` / `Transport` / `TimeoutSeconds` 三个字段
- 保留 `CapabilitiesJSON` / `BindingsJSON` / `UIMetadataJSON` 三块 JSON 冗余
  字段(便于审计与运维查询,不在 hot path 上解析)
- `PluginInstance` 重新标注 PackID 字段语义:PackID = plugin.json.name
  (PascalCase,如 "AlarmPlugin"),磁盘目录 = `/var/lib/ongrid/plugins/<PackID>/`,
  与 PackID 完全相等;任何代码路径都不允许重写 pack_id(避免目录与 DB 漂移)

### 5. pluginhost.go 简化

- `Run` 内的 `fmt.Println("step1 migrate")` 等占位 print 移除
- `ErrDepsIncomplete` 错误保留(与 New 的必填校验配合)
- `ErrNotImplemented` 保留,供 Install / Uninstall / Enable / Disable / Invoke
  在 Phase 1.5 阶段尚未接入 biz 时的占位
- 顶层 import 顺序调整:`fmt` 移到普通 import 块内(非 group 形式)

## 目的

让 `internal/pluginhost/` 11 个子包之间的类型契约在 server / invoke 消费侧
"零适配":

- server handler 调 `Reg.SetEnabledByID` / `Reg.LookupByID` → 编译通过,无需
  helper 二次封装
- invoke router 调 `Reg.LookupCapabilityByInstanceID` → 一次遍历拿到 cap,
  后续 `pool.Get(packID)` 取 runtime,链路最短
- biz 层 lifecycle 用 uint64 主键,不再 `strpackid` 转字符串
- model 层不持有"可能被 runtime / manifest 改"的字段,降低漂移风险

## 不影响

- `cmd/ongrid/main.go` 的 wire-up 代码(commit 21d0d92a 已 append 59 行,
  全部兼容新契约):`phdata.NewPluginRepo(db)` / `phregistry.New()` /
  `phpool.NewPool()` / `phinvoke.NewRouter(reg, pool)` 签名未变
- `internal/pluginhost/server/handler.go`(commit 21d0d92a 已落地):9 个 HTTP
  端点全部走新契约(本任务前已对齐)
- `internal/pluginhost/invoke/router.go`(commit 21d0d92a 已落地):router 的
  `LookupCapabilityByInstanceID` + `LookupByID` 调用路径与本任务新方法一致
- `cmd/ongrid/main.go` 老 wire-up(register 顺序 / errgroup / signal handling)
  完全不动
- A 老业务文件(manager / iam / edgeagent / skill / pkg / api / web 老页面)
  0 改动

## 验证

```
$ go build ./internal/pluginhost/...
BUILD_OK

$ go vet ./internal/pluginhost/...
(无输出)
```

cmd/ongrid 的 build 在 Windows 上仍因 `onnxruntime_go` cgo 失败(plan §11
已知约束,与本任务无关);pluginhost 子包独立 build 通过即视为 OK。

## 后续

- Phase 5 E3 Asset Server:把 plugin 前端资源(JS/CSS/HTML)通过 pluginhost
  server 的 `/api/pluginhost/{id}/assets/{path...}` 暴露给 web 端,使用
  sandbox/path.go 校验路径不越狱到 plugin 安装目录之外
- Phase 5 E4 pluginLoader + SchemaForm:web 端的 `web/src/components/plugin/`
  下新增 pluginLoader(动态加载 plugin frontend bundle)+ SchemaForm(根据
  capability.schema 渲染输入表单)
- Phase 6 F:`.record/` 收口 + demo plugin 真实跑通 + 全链路 `go build` 在
  Linux 打包机(192.168.25.30)复测
