# promtail 默认收集 audit 插件的 JSONL 日志（移除 Glob 探测，启动期竞态修复）

> 日期：2026-07-23
> 触发：操作员反馈「promtail 没有默认收集 auditd 插件的日志，也没有其他的日志，应该默认就初始化收集的」。

## 1. 现象 & 根因

`internal/edgeagent/plugins/logs/render.go` 中 audit 输出路径的「自动 tail」用的是 **on-disk Glob 探测**：

```go
// 旧实现
if matches, _ := filepath.Glob(audit.OutputPath(workDir)); len(matches) > 0 {
    filePaths = append(filePaths, audit.OutputPath(workDir))
}
```

它依赖一个隐含假设：**render() 被调用时，audit 输出文件已经存在**。但 supervisor 在 reconcile 时按 plugin 名遍历，所有 plugin 的 Configure 几乎同步发生：

1. supervisor `reconcile()` → 依次调用 `Configure(logs)` → `render()` 跑，此时 auditbeat 子进程可能还没启动，`<workDir>/audit/audit.jsonl-YYYYMMDD.ndjson` 不存在
2. `filepath.Glob(...)` 返回 `nil`
3. `len(matches) > 0` 为假 → audit 路径被**默默丢弃**，永远不会出现在 promtail 配置里
4. supervisor 只在 spec 变化时重渲染配置；audit 文件从无到有不是 spec 变化 → 已丢弃的路径**永远不会被加回来**
5. 结果：默认启用的 audit 插件产生的所有事件，**永远到不了 Loki**；Logs 页面 / `ongrid_source="auditbeat"` 流永远为空

## 2. 修复

把 audit 路径从「探测后追加」改成「无条件追加」：

```go
// 新实现
if workDir != "" {
    filePaths = append(filePaths, audit.OutputPath(workDir))
}
```

### 2.1 为什么无条件追加是安全的

promtail 的 `__path__` 字段原生支持 glob，且对「glob 无匹配」的处理是 **零条目（no-op）**——不会推送空流，也不会报错。所以：

- audit 插件默认启用（`pluginDefaultEnabled[audit]=true`，见 `internal/manager/biz/edge/plugin_config.go`）→ auditbeat 写出文件 → promtail 的 glob 立刻匹配上 → 数据进入 Loki
- audit 插件被操作员显式禁用 → glob 不匹配任何文件 → scrape job 空转 → **不发任何 Loki entry**，不浪费带宽
- auditbeat 二进制缺失 / darwin edge → 同上，glob 空转
- audit 插件后续被启用（操作员从 UI 打开）→ supervisor reconcile，但 logs spec 没变（audit glob 一直在）→ 不需要 logs 重渲染，auditbeat 一旦写文件 promtail 立即 tail

### 2.2 不动 supervisor

supervisor 只在 cfg 变化时重启 plugin。无条件包含 audit glob 后，**logs 配置从一开始就正确**——既不需要 supervisor 在 audit 文件出现时触发 logs 重渲染，也不需要新增 `auto_tail_audit` 字段让 logs 感知 audit 的开关。

### 2.3 文件改动

| 文件 | 改动 |
|---|---|
| [internal/edgeagent/plugins/logs/render.go](../internal/edgeagent/plugins/logs/render.go) | 删除 `filepath.Glob` 探测；`filePaths = append(filePaths, audit.OutputPath(workDir))` 无条件追加；移除 `"path/filepath"` import；用详细注释（启动竞态的 4 步）替换旧的"best-effort probe"注释 |
| [internal/edgeagent/plugins/logs/render_test.go](../internal/edgeagent/plugins/logs/render_test.go) | 更新 `testWorkDir` 注释（之前是「probe 看不到」，现在是「无条件包含」）；新增 `TestRenderAlwaysIncludesAuditGlob`：空 spec 下必须出现 `__path__: '<workDir>/audit/audit.jsonl-*.ndjson'` |
| [internal/edgeagent/plugins/logs/plugin.go](../internal/edgeagent/plugins/logs/plugin.go) | `ConfigRender` 注释更新：「Promtail's renderer probes ...」→「Promtail always includes the audit plugin's JSONL output path glob in file_paths」 |
| [internal/edgeagent/plugins/audit/plugin.go](../internal/edgeagent/plugins/audit/plugin.go) | 包级 doc：「logs plugin auto-discovers ...」→「logs plugin tails that path ... as an unconditional __path__ glob (no on-disk probe)」；`OutputPath` doc 同步更新 |
| [cmd/ongrid-edge/main.go](../cmd/ongrid-edge/main.go) | audit 插件注册处的注释从「auto-discovers」改成「unconditionally tails」 |

## 3. 验证

```
$ go test ./internal/edgeagent/plugins/logs/... ./internal/edgeagent/plugins/audit/... -count=1
ok  	github.com/ongridio/ongrid/internal/edgeagent/plugins/logs    0.843s
ok  	github.com/ongridio/ongrid/internal/edgeagent/plugins/audit   0.832s
```

`TestRenderAlwaysIncludesAuditGlob` 是新增的契约测试，**钉死**「空 spec 下 audit glob 必须出现在 file_paths」的行为——避免后续重构把 Glob 探测重新引入。

部署到 edge 后，审计流验证步骤：

```bash
# 在 edge 主机
cat /var/lib/ongrid-edge/plugins/logs/promtail.yaml | grep -A1 audit.jsonl
# 应当看到：
#   __path__:      /var/lib/ongrid-edge/plugins/audit/audit.jsonl-*.ndjson

# 在 manager Loki 端查
curl -sG http://<manager>:3100/loki/api/v1/query \
  --data-urlencode 'query={ongrid_source=~"file:.*audit.jsonl.*",device_id="<edge_id>"}' \
  | jq '.data.result | length'
# 应当 > 0
```

## 4. 取舍

- **为什么不在 supervisor 里加 audit→logs 的依赖感知？** 当前修复让 logs 配置一开始就正确，supervisor 视角下 audit 启停是 plugin 自身的 reconcile，**logs 不需要被联动重渲染**。引入依赖感知会让 supervisor 状态机变复杂（logs 配置 + audit 状态两轴），得不偿失。
- **为什么不加 `auto_tail_audit: bool` spec 开关给操作员？** 默认值就该是「包含」——glob 空匹配是 promtail 的天然 no-op，不需要开关。引入开关等于允许操作员制造「audit 开着但 Loki 没数据」的隐式状态，违反「默认安全」原则。
- **为什么不强制 operator 在 spec 里声明 `file_paths` 才算合规？** logs 插件当前 spec 字段语义是「追加」而不是「替换」——审计 glob 必须默认就在；让操作员显式声明等于把 audit 推送到 Loki 的责任从框架挪到人，回归本次 bug 的本质。

## 5. 适用范围

- 所有 ongrid-edge 部署（fresh install + 已存在 edge 通过 `systemctl restart ongrid-edge` 触发 reconcile）
- 与 auditbeat 9.x 的 `audit.jsonl-YYYYMMDD.ndjson` 命名约定一致；auditbeat 7.x 旧部署若文件名格式不同，需要同步更新 `audit.OutputPath()` 的 glob 模式