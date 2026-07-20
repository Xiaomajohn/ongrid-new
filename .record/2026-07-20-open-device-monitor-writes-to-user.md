# 放开 devices / monitor panels 的全部写接口给 user 角色

## 背景

紧接 [2026-07-20-open-create-device-to-user.md](2026-07-20-open-create-device-to-user.md)：
那次只把 `POST /v1/devices`（添加主机）放开给 user，其它设备写接口（修改、
删除、恢复、角色、SSH 凭据）以及监控面板的写接口仍是 admin-only。

用户进一步要求：主机（devices）和监控（monitors）的**增删改查和所有操作**
都对 user 放开，不再保留 admin 闸门。设备登记、调整、面板配置在内部
运维场景下都是日常工作流的一部分，不再由 admin 单独把控。

## 改动

### 1. `internal/manager/server/device/http.go`

`Handler.Register` 里的 6 处 `r.With(h.requireAdmin).<Method>(...)` 全部
改为直接的 `<Method>(...)`，去掉 admin 闸门：

| 路由 | 改前 | 改后 |
| --- | --- | --- |
| `PATCH  /v1/devices/{id}` | `r.With(h.requireAdmin).Patch(...)` | `r.Patch(...)` |
| `PATCH  /v1/devices/{id}/roles` | `r.With(h.requireAdmin).Patch(...)` | `r.Patch(...)` |
| `DELETE /v1/devices/{id}` | `r.With(h.requireAdmin).Delete(...)` | `r.Delete(...)` |
| `POST   /v1/devices/{id}/restore` | `r.With(h.requireAdmin).Post(...)` | `r.Post(...)` |
| `PUT    /v1/devices/{id}/ssh-credentials` | `r.With(h.requireAdmin).Put(...)` | `r.Put(...)` |
| `DELETE /v1/devices/{id}/ssh-credentials` | `r.With(h.requireAdmin).Delete(...)` | `r.Delete(...)` |

`requireAdmin` 中间件保留作为兜底工具（暂未被引用），便于后续要重新
收紧某条策略时直接接回。

注释同步更新：

- Register 顶部的路由说明由 `(admin)` 改为 `(any authed)`，并把
  Register 函数头部那段"放开新增主机"的说明改成"设备管理类写接口全部
  any-authed"。

### 2. `internal/manager/server/monitor/http.go`

两条闸门路径同时清理：

- 路由级中间件：`r.With(h.requireAdminMW).Post("/v1/monitor/panels/{id}/restore", h.restore)`
  → `r.Post("/v1/monitor/panels/{id}/restore", h.restore)`，去掉
  `requireAdminMW` 包装。
- handler 内联检查：create / update / delete / restore 四个 handler 里
  的 `if !h.requireAdmin(w, r) { return }` 全部改为 `if !h.requireUser(w, r) { return }`。

注释同步更新，Register 顶部的 `(admin)` 改为 `(any authed)`，并在
`restore` handler 上把"Admin only — …"的历史说明改为"任何已认证角色
均可触发"的变更说明。

`requireAdmin` / `requireAdminMW` 函数体保留作兜底。

## 范围限定

### 本次放开（全部 any-authed）

**devices（设备管理）**：
- `POST   /v1/devices`
- `GET    /v1/devices` / `GET /v1/devices/{id}`
- `PATCH  /v1/devices/{id}`（name / description / hostname / SSH）
- `PATCH  /v1/devices/{id}/roles`
- `DELETE /v1/devices/{id}?hard=true`
- `POST   /v1/devices/{id}/restore`
- `GET    /v1/devices/{id}/edges`
- `PUT    /v1/devices/{id}/ssh-credentials`
- `GET    /v1/devices/{id}/ssh-info`
- `DELETE /v1/devices/{id}/ssh-credentials`

**monitor panels（监控面板）**：
- `GET    /v1/monitor/panels`
- `POST   /v1/monitor/panels`
- `PATCH  /v1/monitor/panels/{id}`
- `DELETE /v1/monitor/panels/{id}?hard=true`
- `POST   /v1/monitor/panels/{id}/restore`

### 不在本次范围（保持原有权限）

- `POST /v1/edges` 与 edge 子树下的写接口仍为 admin-only /
  `writeMW("edge:*")`，参见 `internal/manager/server/edge/http.go`。
  edge 的 access_key_id / secret_key_hash 是探针认证凭据，权限面比
  主机 / 监控面板大，本次不动。
- `POST /v1/devices/{id}/install-edge`（一键装机）保持 any-authed
  （installjob handler 本身不限 role）。
- `internal/manager/server/devicessh/`（WebShell、SFTP、FS）走独立的
  authz 模型，不在设备 admin 闸门管辖范围内。
- 用户 / IAM / 集成 / 系统设置等管理面接口不在本次讨论范围。

## 验证

- 逻辑：去掉中间件 / 改为 `requireUser` 后，handler 内部仍然走
  `tenantctx.From(r.Context())`，校验链跟原 admin 路径一致；任意
  带有效 JWT 的请求都能进入业务逻辑。
- 编译：
  - `go build ./internal/manager/server/device/...` ✅
  - `go build ./internal/manager/server/monitor/...` ✅
  - `go vet ./internal/manager/server/device/...` ✅
  - `go vet ./internal/manager/server/monitor/...` ✅
- 端到端：未在 dev 跑（按项目规则禁止 Windows 调 Linux 上的脚本 / 打包）。

## 与前一次 record 的关系

[2026-07-20-open-create-device-to-user.md](2026-07-20-open-create-device-to-user.md)
描述了**第一次**放开（只放了 `POST /v1/devices`）。那条 record 的
"范围限定"表格里把其它设备写接口标成"admin"，**那是当时的快照**，
本次之后已过时；真实情况以本文档为准。如果以后要做权限回滚或审计，
应把这两篇 record 当成一条完整的变更轨迹看。

## 不在本次范围内（接口未变）

- `POST /v1/edges`、edge 子树写接口、rotate-secret、upgrade。
- `POST /v1/users`、`POST /v1/orgs` 等 IAM / 组织管理接口。