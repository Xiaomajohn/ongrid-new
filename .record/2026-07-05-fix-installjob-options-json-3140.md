# 2026-07-05 installjob options_json 3140 修复：gorm 零值 "" 撞 MySQL JSON 列

## 现象

修完 1146（见 .record/2026-07-05-fix-installjob-data-layer.md）后，调用
`POST /api/v1/devices/1/install-edge` 仍然 500：

```
Error 3140 (22032): Invalid JSON text: "The document is empty." at position 0
in value for column 'install_jobs.options_json'.
```

worker 每分钟轮询触发的也是同一报错（仍是 `ListByDevice` 走 SQL
SELECT）。

## 全链路核对（按规则第 3 条）

| 层 | 路径 | 结论 |
|---|---|---|
| 1. 前端 | `web/src/components/InstallEdgeModal.tsx` 调 `installEdge(deviceId, body)`；`body = { ssh_pass?, ssh_key_pem? }`，无 options 字段 | OK，没传 options |
| 2. 前端 API | `web/src/api/devices.ts:225 installEdge(...)` POST `/devices/{id}/install-edge`，body 是 opts | OK |
| 3. 后端路由 | `internal/manager/server/installjob/http.go:115` `r.Post("/v1/devices/{id}/install-install-edge", h.createInstall)` → `h.uc.Create(ctx, deviceID)` | OK |
| 4. 后端业务 | `cmd/ongrid/main.go:3928 installjobUsecaseAdapter.Create` 构造 `InstallJob{...}` 字段 | **BUG**：没有给 `OptionsJSON` 赋值 |
| 5. 数据交互层 | `internal/manager/data/installjob/model.go:61` 字段定义 `OptionsJSON string gorm:"type:json;..."` | 类型 string，零值 `""` |

## 根因

`OptionsJSON` 是 `string` 类型，列是 MySQL JSON。`installjobUsecaseAdapter.Create`
构造 `InstallJob` 字面量时**漏给 `OptionsJSON` 赋值**，Go 零值 `""` 由
gorm 透传到 MySQL：

- MySQL 8 对 JSON 列校验严格：`""` 不是合法 JSON，报 3140
- INSERT 直接失败 ⇒ HTTP 500；worker 后续轮询 SELECT 也连带失败

整个字段在代码库内**只定义不读写**（搜 `OptionsJSON` 只有 model.go 一处），
是预留接入点——v1 没有 options 业务，但字段保留为未来扩展位。问题在于
没人写它，于是零值踩了类型陷阱。

## 修复

最小化修复，给 `Create` 兜底赋 `{}`：

### `cmd/ongrid/main.go`

`installjobUsecaseAdapter.Create` 里 struct 字面量加一行：

```go
// options_json 列是 MySQL JSON 类型，不能接受空字符串（会报
// 3140 Invalid JSON text）。当前没有真正接入的 options 字段，
// 显式赋 "{}" 作为合法 JSON 占位；后续若需要承载真实 options，
// 改成 *string / sql.NullString 以支持 NULL 语义更稳妥。
OptionsJSON:  "{}",
```

### `internal/manager/data/installjob/model.go`

字段上方加注释，约束"必须是合法 JSON 字面量"+"Go string 零值是 ""，
写库前必须显式赋值"，让后续接入者知道坑在哪儿、推荐升级到
`*string` / `sql.NullString`。

## 验证

```bash
$ go vet ./cmd/ongrid/... ./internal/manager/biz/installjob/... \
         ./internal/manager/data/installjob/...
(no output)

$ VERSION=$(cat VERSION) \
  go build -trimpath -ldflags "-X main.version=$VERSION" \
    -o bin/ongrid ./cmd/ongrid
(no output)

$ ls -la bin/ongrid
-rwxr-xr-x 1 root root 60257672  7月  5 23:45 bin/ongrid
# ELF 64-bit ARM aarch64, 与 ongrid 容器镜像一致
```

部署（线上机器 / ARM64）：

```bash
cd /opt/ongrid
docker compose stop ongrid                # bind-mount 文件被持锁 → "文本文件忙"
cp -v bin/ongrid /opt/ongrid/ongrid-app/ongrid
docker compose start ongrid
```

部署后验证：

```bash
$ curl -X POST https://192.168.25.30/api/v1/devices/1/install-edge \
       -H "Authorization: Bearer ${TOKEN}"
{"install_job_id": <新 id>, "status": "queued"}

$ docker exec ongrid mysql ... -e \
    "SELECT id, status, options_json FROM install_jobs ORDER BY id DESC LIMIT 3;"
# options_json 应为 {}（JSON_OBJECT()），不再是空字符串或 NULL
```

最近 60 秒日志中 `3140` / `options_json` 关键字出现次数：**0**。

## 不再做的事（避免回归）

- 不要在 `installjobUsecaseAdapter.Create` 里把 `OptionsJSON` 那一行删了
  —— 一旦回到 gorm 零值，3140 立刻复现。
- 不要在 biz / data 层之外另起一个 places 写 `OptionsJSON`：集中在一个
  兜底点更好审计。
- 未来真要接 options 业务，把字段类型从 `string` 升级到 `*string` 或
  `sql.NullString`，配合 MySQL JSON NULL 语义，并把这个修复处的
  `OptionsJSON: "{}"` 改成 `OptionsJSON: nil` / `OptionsJSON: sql.NullString{}`。
- 不要让其他预留 JSON 字段（比如未来某 BC 的 `extra_json` /
  `meta_json`）走 installjob 这种"字段定义了、没人写、零值踩雷"的路径：
  要么 type 选 `*string`，要么构造函数显式兜底，二选一必须命中。