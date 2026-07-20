# 放开 POST /v1/devices 给 user 角色

## 背景

`POST /api/v1/devices`（添加主机）此前用 `requireAdmin` 中间件拦截，普通
user 调用会被拒。前端 Hosts / 监控页面在 user 角色下尝试新建主机时拿到
403（被误读为 404），导致"添加主机"功能对非 admin 不可用。

设备管理本身在内部运维场景下属于日常操作，不应只开放给 admin。本次按用户
要求把"添加主机"接口对所有已认证角色放开。

## 改动

- `internal/manager/server/device/http.go`
  - `Handler.Register`：`r.With(h.requireAdmin).Post("/v1/devices", h.create)`
    改为 `r.Post("/v1/devices", h.create)`，去掉 admin 闸门。
  - 同步注释：`POST /v1/devices (admin)` → `POST /v1/devices (any authed)`，
    并补充"其余写入类接口继续 admin-only"的说明。

## 范围限定

仅放开"注册新主机"这一步。其他跟设备相关的写接口保持 admin-only，避免
权限越界：

| 路由 | 权限 | 说明 |
| --- | --- | --- |
| `POST /v1/devices` | any authed | **本次放开**，user 也可新建主机 |
| `GET /v1/devices`、`GET /v1/devices/{id}`、`GET /v1/devices/{id}/edges`、`GET /v1/devices/{id}/ssh-info` | any authed | 读接口本来就是开放 |
| `PATCH /v1/devices/{id}`、`PATCH /v1/devices/{id}/roles`、`DELETE /v1/devices/{id}`、`POST /v1/devices/{id}/restore` | admin | 修改 / 删除 / 恢复 / 角色调整继续限 admin |
| `PUT /v1/devices/{id}/ssh-credentials`、`DELETE /v1/devices/{id}/ssh-credentials` | admin | SSH 凭据属于敏感操作，继续限 admin |

理由：新建主机时已经要把 SSH 凭据一起带过来，所以这一步不可避免地要
对 user 开放；但事后想"改密码 / 改私钥 / 把别人的主机删了"这种破坏面
大得多，限定 admin 更稳妥。如果后续 user 反馈 SSH 凭据修改也需要
放开，再单独评估。

## 验证

- 逻辑：`create` handler 内部只校验 `tenantctx`，本身不依赖 role；去
  掉中间件后任何带有效 JWT 的请求都会进 handler，校验链路跟原 admin
  请求一致。
- 编译：`go build ./internal/manager/server/device/...` 通过。
- 端到端：未在 dev 跑（按项目规则禁止 Windows 调 Linux 上的脚本 / 打包）。

## 不在本次范围内

- `POST /v1/edges` 仍为 admin-only（保留 edge 的 admin 闸门，参见
  `internal/manager/server/edge/http.go` 的 `writeMW("edge:*")`）。
- 一键装机 `POST /v1/devices/{id}/install-edge` 仍然 any-authed
  （installjob handler 内部不限 role）。