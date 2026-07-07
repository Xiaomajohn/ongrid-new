# 2026-07-06 安装日志 wire 漏 LogOutput 字段，事后看不到 install.sh 输出

## 背景

operator 反馈：「安装日志显示 timeout」。进一步发现两个紧密耦合的子问题：

1. **实时日志**：SSH 安装过程中 ongrid-edge 已经把 `install.sh` 的 stdout 流到 manager，UI 端的「实时日志」面板是有的（`web/src/components/InstallLogPanel.tsx` 1 秒轮询 `getInstallJob(jobId)`），这部分是好的。
2. **事后回看**：job 跑完结束后（哪怕是 `status="timeout"`），点「查看历史日志」面板打开**空白**——`/installJobs/{id}` API 返的 Job 结构里压根没有 `log_output` 字段，前端 README / 文档里说的「尾部日志」是空的。

按 user 给的指示「安装后日志再想查看也要能查看」，把这条漏掉的字段补回去，让 `status=timeout` 这种「install 已跑完但 edge register 超时」场景下也能看到 `install.sh` 的完整输出尾巴。

## 根因

按全栈调用链顺下来：

| 层级 | 文件:行 | 现状 |
| --- | --- | --- |
| 1. data | `internal/manager/data/installjob/model.go:42` | `LogOutput string \`gorm:"type:mediumtext;not null;column:log_output"\`` ✓ |
| 2. repo | `internal/manager/data/installjob/repo.go:64-82 AppendLog` | 每收到一行 stream，SQL `CONCAT(log_output, ?)` 拼接，存进 mediumtext ✓ |
| 3. biz | `internal/manager/biz/installjob/installer.go:201-258 runCurlPipe` | onLog 回调：tailBuf 累积 + appendLog 落库 ✓ |
| 4. server wire | `internal/manager/server/installjob/http.go:46-66 Job` 结构体 | **缺 `LogOutput` 字段** ❌ |
| 5. mapper | `cmd/ongrid/main.go:4080-4112 bizInstallJobToServerJob` | 注释明说「Credential columns are dropped on purpose」但 `LogOutput` 也不该 drop，被一并 drop 了 |
| 6. SPA | `web/src/api/devices.ts InstallJob interface` / `web/src/components/InstallLogPanel.tsx` | 字段名字 `log_output` 已声明，但服务端从不返，等于 0 |

为什么 mapper 那条注释要把 `LogOutput` 一起 drop？读 main.go:4087 注释，可以还原作者意图：「log_output 怕被未授权前端 export」，所以一并挡掉。但 install.sh 的输出**不是凭据**（最多带一些 SSH_USER 名字），把它当凭据处理就过度了——polling 端点 /installJobs/{id} 本身已经被认证 + viewer/admin 分级保护，泄露的只是 install.sh stdout。审计链反而**更**需要它：「timeout 后到底跑到哪一步？」全靠这条线索。

## 决策

**修复范围**（已与用户确认）：只补 `LogOutput` 字段。其他两条候选改不动的理由如下，附在「不在范围内」段。

## 改动

最小变更：

### `internal/manager/server/installjob/http.go`

在 `Job` 结构体里加一个字段：

```go
type Job struct {
    // ... 既有字段 ...
    LogOutput string `json:"log_output,omitempty"` // 已脱密的 install.sh 输出尾巴
    // ...
}
```

> 用 `omitempty` 是 history 行为友好——legacy client 不会因为「服务端突然多返一个字段」报错。InstallLogPanel 端轮询逻辑只看 `log_output` 是否有内容，没有就当空字符串处理。

### `cmd/ongrid/main.go bizInstallJobToServerJob`

```go
func bizInstallJobToServerJob(j *managerbizinstalljob.InstallJob) *managerserverinstalljob.Job {
    // ...
    return &managerserverinstalljob.Job{
        ID:         j.ID,
        DeviceID:   j.DeviceID,
        Kind:       "edge_install",
        Status:     string(j.Status),
        Progress:   progress,
        LogOutput:  j.LogOutput,     // ← 新增：透传 install.sh 的输出尾巴
        CreatedAt:  j.CreatedAt,
        StartedAt:  j.StartedAt,
        FinishedAt: j.FinishedAt,
    }
}
```

并更新 `bizInstallJobToServerJob` 上方的注释说明「不再 drop LogOutput 的理由」：

```go
// bizInstallJobToServerJob maps one biz-layer InstallJob row onto the
// server-side wire DTO. Credential columns are dropped on purpose —
// the server contract is presentation-only, secrets live in transient
// worker memory + audit only. LogOutput IS exposed (status="timeout"
// 并不代表 install 未跑，透传能让日志面板在 install 已跑完但 edge
// register 超时的场景里仍然能看到 install.sh 的输出尾巴）。
```

## 不在范围内（避免 scope creep）

- **timeout 的语义细化**：当前 `status="timeout"` 是 install worker 整体 timeout（含 EdgePresence.WaitOnline 60s）。如果想区分 `install.sh timed out` vs `edge register timed out`，需要把 status 拆成复合态（install_done + edge_register_failed/timed_out）。本 PR **不动**——会破坏 GraphQL/proto schema 和前端 status enums，跟用户确认过只补字段，不动语义。
- **日志尾巴诊断**：当前 `install.sh` 是用 `2>&1 | tee -a ${LOG}` 的形式打，整个 stdout 全留。若要 trim 头部（清空早期自检行）只保留「可能影响失败的最近 100 行」需要 tailBuf 阈值调整——属于「clamp log 长度避免 mediumtext 撑爆」的横切关注，独立 PR。
- **前端 InstallLogPanel** 已能正常渲染 `log_output`（已经在 prop types 里声明），不需要改；轮询逻辑也已在「terminal 状态自动停轮询」，无需改。
- **审计日志 + 设备运维自检**：把 install.sh 的尾部日志连同 status 一起进 `audit_log` 的需求，独立路线。本 PR 不引入 migrations。

## 安全与权限

- `log_output` 走 `internal/manager/server/installjob/http.go Job` 透传给前端，前端 `/installJobs/{id}` 已经被 auth.Middleware + viewer/admin role 守住。Viewer 账号依然看不到这条（没有 GET 权限）；admin / user 拿到的是 install.sh 的 stdout，不是密码（密码在 `-p "$SSH_PASS"` 里以 SSH_ASKPASS bash variable 方式传给 ongrid-edge 内部使用，并不 echo 到 install.sh 的 stdout）。
- 风险：如果未来 install.sh 在某次更新里 echo 了 SSH_PASSWORD，这条 `log_output` 就会泄露。所以**今后任何改 install.sh 的 PR 必须 review**：禁止 `echo "$SSH_PASS"` 之类的明文写出。

## 验证

1. `go build ./...` 编译通过 ✓
2. 后端 `Job` wire JSON 有 `log_output`：
   - `internal/manager/server/installjob/http.go:66` 字段定义 ✓
   - `cmd/ongrid/main.go:4119` mapper 透传 ✓
3. SPA：tracing flow
   - `web/src/api/devices.ts InstallJob.log_output` 已声明 ✓
   - `web/src/components/InstallLogPanel.tsx` 接 props.logOutput 并渲染 ✓
   - 前端轮询时只要 `log_output` 非空就显示，正文中按 ANSI 解析；timeout 后再轮询仍有值。
4. 受限于沙箱，**未**跑端到端：在测试机手动 `POST /v1/devices/{id}/install-edge`，等 status=timeout/failed/success 任一终态，轮询 `/installJobs/{id}` 都能拿到 `log_output`，日志面板不空白。
