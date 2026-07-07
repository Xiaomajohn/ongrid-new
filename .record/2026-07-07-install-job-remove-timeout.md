# 2026-07-07 install job 日志面板去掉超时判断

## 背景

operator 反馈：「一键安装日志 · job #1」面板显示 `状态: timeout`，开始 09:41:48，结束 09:43:05（约 1 分 17 秒）。但实际从 log_output 看 install.sh 已经成功跑完：

- `[OK] installed: ongrid-edge v0.9.0`
- `[OK] connected: edge_id=1 via 192.168.25.30:40012`
- `[OK] state dir writable by ongrid-edge`
- `[OK] journald readable by ongrid-edge`
- `[OK] data-plane host 192.168.25.30:443 reachable (TCP)`
- `[OK] self-check passed`

worker 是在「5) wait until the freshly-installed edge comes online」阶段（60s `defaultWaitOnlineWindow`）拿不到 DB 里 `edges.status='online'`，把整个 job 标成 `StatusTimeout` 并 return。

按 user 给的指示「我不需要超时，如果安装不完就一直不退出」，去掉这个超时判断。

## 根因

`internal/manager/biz/installjob/worker.go:301-309` 旧逻辑：

```go
if err := w.waitEdgeOnline(execCtx, job.DeviceID, w.cfg.WaitOnline); err != nil {
    logCtx.Warn("installjob: edge did not come online in time", slog.Any("err", err))
    _ = w.repo.UpdateStatus(execCtx, jobID, StatusTimeout, nil)
    _ = w.repo.ClearCredentialSnap(execCtx, jobID)
    return
}
```

`waitEdgeOnline` 是「install.sh 跑完后等 agent 在 DB 里 status=online」的辅助步骤，**不是 install 本身**。把它失败就标 `StatusTimeout` 会让 install 已成功但 agent register 慢的场景被误判。

## 修复

### `internal/manager/biz/installjob/worker.go`

1. **`defaultWorkerTimeout` 10min → 24h**：用户原话「如果安装不完就一直不退出」。`defaultWorkerTimeout` 仍然保留作为 worker 槽的硬兜底（防 install.sh 永久卡死），但放长到 24h，符合「只要 install.sh 没退 worker 就不退」的语义。

2. **`defaultWaitOnlineWindow` 60s → 5s**：waitEdgeOnline 改为 best-effort，只短暂探一下 agent 有没有 register，失败不阻塞 worker 终态判定。

3. **waitEdgeOnline 失败处理改写**：移除 `UpdateStatus(StatusTimeout)` 与 `return`，失败时仅 warn log，让 worker fallthrough 到 success 路径。

```go
if err := w.waitEdgeOnline(execCtx, job.DeviceID, w.cfg.WaitOnline); err != nil {
    logCtx.Warn("installjob: edge did not come online within wait window (best-effort, not failing job)",
        slog.Any("err", err),
        slog.Duration("wait_window", w.cfg.WaitOnline))
}
```

4. **状态机注释更新**：`queued ──▶ running ──▶ (success | failed | timeout)` 改为 `(success | failed)`，明确 timeout 分支已废弃。

## 行为变化

| 场景 | 旧行为 | 新行为 |
| --- | --- | --- |
| install.sh 成功 + agent 60s 内 register | success | success（不变） |
| install.sh 成功 + agent 5s 内未 register | **timeout** | **success**（仅 warn log） |
| install.sh 失败 | failed | failed（不变） |
| install.sh 永久卡住 | 10min 后失败 | 24h 后兜底（防 worker 槽死） |

agent 是否真的 register 由 `edge.presence` 后续异步追踪，UI 侧通过 `edges.status` 自行观察；install job 状态机不再被 agent register 异步等待影响。

## 不在范围内（避免 scope creep）

- **`StatusTimeout` enum 保留**：data/model 里的 `StatusTimeout = "timeout"` 不删，向后兼容老的 timeout 记录（前端 `StatusBadge` 仍能渲染）。
- **前端 `InstallLogPanel.tsx` 不改**：`TERMINAL` 数组仍含 `'timeout'`，老的 timeout 数据依然被识别为终态、停止轮询；新 job 不再产生 timeout 状态，前端无需调整。
- **install.sh 内部 60s `/healthz` 等待**：那是 install.sh 自检逻辑，不在本次 scope。

## 安全与权限

无。本次改动只影响 worker 内部状态机判定，不动任何 HTTP/认证/权限路径。

## 验证

1. `go build ./...` 编译通过 ✓
2. 逻辑路径覆盖：
   - install.sh 成功 → worker waitEdgeOnline 5s → 无论结果都走 success
   - install.sh 失败 → 走 failed（路径未改）
3. 受限于沙箱，未跑端到端：在测试机手动 `POST /v1/devices/{id}/install-edge`，轮询 `/installJobs/{id}` 看终态应是 `success`（即使 agent register 慢）；老的 `status=timeout` 数据仍能正常显示。

## 回滚

`worker.go` 是单文件改动，回滚只需把 `defaultWorkerTimeout` / `defaultWaitOnlineWindow` 改回原值，把 waitEdgeOnline 失败处理恢复 `UpdateStatus(StatusTimeout) + return` 即可。