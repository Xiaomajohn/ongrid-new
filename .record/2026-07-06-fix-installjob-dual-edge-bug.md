# 2026-07-06 一键安装"双 edge 创建"bug：worker 改 BindEdgeFromAccessKey

## 背景

`.record/2026-07-06-replace-ongrid-binary-bind-mount.md` 解决了
`HandleRegister` 用 fingerprint upsert 覆盖 `edge.DeviceID` 的问题。
但替换新 binary 后 `install_jobs` 还是 timeout,根本原因又走深了一层
——**前端和 worker 同时创建了 edge**,产生第 2 个 edge(凭证浪费 +
关联错乱)。

## 现象

替换 binary 后,`POST /api/v1/devices/1/install-edge` 触发 5 次 install:

| install_job_id | status   | 备注                                |
| -------------- | -------- | ----------------------------------- |
| 8              | timeout  | 触发时间最久,worker 还在用老逻辑等 |
| 18, 19, 20, 21 | timeout  | 这 4 次是 usecase.go 修复后的样本  |

`install_jobs` 表里 `device_id=1` 一直绑不到 edge;agent 实际 register
的 edge(22, 23, 24)都跑到了 `device_id=2`(fingerprint upsert 的产物),
worker `waitEdgeOnline(1)` 永远等不到。

## 根因 1:前端先创 edge,worker 又创一个

`.record/2026-07-06-oneclick-install-end-to-end-fix.md` 里
`InstallEdgeModal.tsx:64-70`:

```tsx
const created: CreateEdgeResponse = await createEdge({ name: device.name || '' });
const { cmd } = buildInstallCommand({
  accessKey: created.access_key_id,
  secretKey: created.secret_key,
});
```

前端 `POST /v1/edges` 先创建一个 edge(比如 edge 22),把它的
access_key/secret_key 嵌进 cmd。

但 `installjob/worker.go` 老逻辑里 worker 拿到 cmd 之前先调
`issuer.CreateEdgeForDevice(ctx, deviceID, taskName)` —— 这会**再创一个
edge 23**,worker 拿这个新 edge 23 的 access/secret 自己拼一份 cmd(覆盖
前端传进来的 cmd?)。实际不会覆盖——cmd 已经是 options_json 里的,worker
只是把 issuer 创的 edge 23 绑到 device 1。

最终数据库状态:

| edge_id | access_key  | device_id | 来源     |
| ------- | ----------- | --------- | -------- |
| 22      | ak_xxx_A    | 2(被 fingerprint upsert 改) | 前端 createEdge |
| 23      | ak_xxx_B    | 1         | worker CreateEdgeForDevice |

cmd 里嵌入的是 `ak_xxx_A`(前端 edge 22 的凭证),agent register 时
edgeauth.AccessKey = `ak_xxx_A` → 命中 edge 22 → 落到 device 2。
worker `waitEdgeOnline(device_id=1)` 等的是 edge 23,等不到 → timeout。

## 根因 2:20s wait timeout 太短

即使绑定逻辑对了,从 SSH 拨号到 systemd start + frontier online 的总时
延在 aarch64 openEuler 24.03 + 30MB ongrid-edge 下载场景实测
**22-25s**(22s install 走完,26s 才 online);`defaultWaitOnlineWindow=20s`
会偶发 false-negative。改 60s。

## 修复方案

让 worker **不再创建新 edge**,只把前端创的那个 edge 绑到 device:

### `internal/manager/biz/installjob/worker.go`

- `EdgeIssuer` 接口从 `CreateEdgeForDevice(...)` 改为
  `BindEdgeFromAccessKey(ctx, deviceID, accessKey, taskName) error`
- `defaultWaitOnlineWindow` 20s → 60s(注释说明 22-25s 装 + 1-3s
  handshake)
- 新增 `parseAccessKeyFromCmd(cmd)` 函数:字符串扫描 `--access-key=`
  拿前端嵌入的 access_key(不做 regex,零依赖)
- `installer.Install` 传 `"" ""` 给 access/secret(cmd 已经嵌入了)
- 流程:`parseAccessKeyFromCmd` → `issuer.BindEdgeFromAccessKey` →
  installer → waitEdgeOnline

### `internal/manager/biz/edge/install_creds.go`

替换 `CreateEdgeForDevice` 为 `BindEdgeFromAccessKey`:

```go
func (i *InstallEdgeIssuer) BindEdgeFromAccessKey(ctx context.Context, deviceID uint64, accessKey, taskName string) error {
    if i == nil || i.uc == nil {
        return fmt.Errorf("edge: InstallEdgeIssuer not wired (nil uc)")
    }
    if deviceID == 0 {
        return fmt.Errorf("edge: BindEdgeFromAccessKey: deviceID is 0")
    }
    if accessKey == "" {
        return fmt.Errorf("edge: BindEdgeFromAccessKey: accessKey is empty")
    }

    // 1) 按 access_key 反查 edge.ID
    ed, err := i.uc.repo.GetByAccessKey(ctx, accessKey)
    if err != nil {
        return fmt.Errorf("edge: BindEdgeFromAccessKey: GetByAccessKey(%q): %w", accessKey, err)
    }
    if ed == nil {
        return fmt.Errorf("edge: BindEdgeFromAccessKey: edge not found for access_key=%q", accessKey)
    }

    // 2) SetDeviceID + edge_devices Link
    if err := i.uc.repo.SetDeviceID(ctx, ed.ID, deviceID); err != nil {
        i.log.Warn("install-edge: SetDeviceID failed", ...)
    }
    if i.links != nil {
        if err := i.links.Link(ctx, ed.ID, deviceID, devicemodel.EdgeDeviceRelationHost); err != nil {
            i.log.Warn("install-edge: edge_devices Link failed", ...)
        }
    }

    // 3) task_name 透传(空时跳过)
    if trimmed := strings.TrimSpace(taskName); trimmed != "" {
        if err := i.uc.repo.UpdateTaskName(ctx, ed.ID, trimmed); err != nil {
            i.log.Warn("install-edge: update task_name failed", ...)
        }
    }
    return nil
}
```

要点:

- **不创建新 edge**——前端 POST /v1/edges 已经创好,worker 创就重复
- **GetByAccessKey 查不到时报错**(不静默 fallback CreateEdge):手工填的
  cmd / 误调用场景要可见
- **SetDeviceID/Link 失败只 warn**:agent register 时 HandleRegister 会
  以 `edge.DeviceID` 优先 Get 一次(上一轮 usecase.go 修复),所以关联
  丢失不会让 install 失败,只是 waitEdgeOnline 可能等不到

### `internal/manager/biz/installjob/installer.go`

`accessKey/secretKey == ""` 不再 hard-fail,转 debug log(cmd 嵌入是新的
约定路径,worker 拿空串是正常状态)。

## 编译 + 替换

按 operator 提醒的「bind-mount 替换」流程:

```bash
# 1. 编译
go build -o /tmp/ongrid-v5 ./cmd/ongrid
# -rwxr-xr-x 1 root root 58M  /tmp/ongrid-v5

# 2. 停容器(down 比 stop 干净——kill + 删容器 + 清网络)
cd /opt/ongrid && docker compose down ongrid
# Container ongrid Stopped → Removed → Network ongrid_default Removing

# 3. 替换 host 文件
cp /tmp/ongrid-v5 /opt/ongrid/ongrid-app/ongrid
# md5 验证:5cf4df729432e6c5b36ff7d19a576cbc  /tmp/ongrid-v5
#           5cf4df729432e6c5b36ff7d19a576cbc  /opt/ongrid/ongrid-app/ongrid

# 4. 重启加载
docker compose up -d ongrid
# Container ongrid Started

# 5. 容器内 /ongrid md5 = host md5 → 确认加载的是新 binary
docker exec ongrid md5sum /ongrid
# 5cf4df729432e6c5b36ff7d19a576cbc  /ongrid

# 6. 服务自检
docker logs --tail 30 ongrid
# http server listening listener=api addr=:8080 ✓
# mysql opened ✓ migration 1/21 ~ 21/21 ✓
# install_skill bolted onto chat runtime bag tool_count=39 ✓
# ping reachability: round complete reachable=1 ✓
# 路径 /root/builder/ongrid-new 出现在日志 → 编译产物 = 当前 binary
```

## 验证(待 operator 端到端跑)

下次操作员在 SPA 触发 `POST /api/v1/devices/1/install-edge`,预期:

1. `POST /v1/edges` 创建 edge N
2. cmd 嵌入 edge N 的 access_key
3. `POST /devices/1/install-edge` 创建 install_job K, options_json.command
   带 edge N.access_key
4. worker 取 options_json → `parseAccessKeyFromCmd` 拿到 access_key →
   `BindEdgeFromAccessKey(1, ak_N, taskName)` → edge N.SetDeviceID(1)
5. SSH 目标机 install.sh 跑完(SELinux relabel、systemd start)
6. agent register → edgeauth.AccessKey=ak_N → 命中 edge N → 用
   `edge.DeviceID=1` 走 HandleRegister(优先 Get,不走 fingerprint
   upsert)→ 关联保持
7. frontier `edge online edge_id=N device_id=1`
8. `waitEdgeOnline(1, 60s)` 命中 → `install_jobs.status=success` ✓

## 教训

**单步原则**:任何"创建带状态的对象"的操作,要么前端创,要么后端创,
不能两端各创一遍然后指望"配对字段"对上。本次 bug 本质是
"worker 不知道前端已经创了 edge,自己又创了一个"——缺少协议层约束
(接口契约只描述"返回 cmd",没描述"edge 也由前端创")。

修复后**接口契约明确**:前端 `createEdge` 是 edge 生命周期的唯一起点,
worker 只做"反向关联"。cmd 里嵌的 access_key 是约定的"凭据指针",
worker 用它来定位 edge.ID。
