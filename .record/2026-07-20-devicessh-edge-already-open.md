# devicessh / edge 子树对 user 的开放状态核实

## 背景

紧接 [2026-07-20-open-device-monitor-writes-to-user.md](2026-07-20-open-device-monitor-writes-to-user.md)：
那条 record 在"不在本次范围"一节里把 `devicessh`（WebShell / SFTP / FS）
和 edge 子树标成"保持原有权限"。用户据此进一步要求把这两块也对
user 角色一并放开。

实际核实下来，**这两个包在当前代码里已经对所有已认证角色开放**，
不需要再改代码。下文把核实过程记下来，便于以后审计。

## devicessh（WebShell / SFTP / FS）

**当前状态：完全 any-authed，没有任何 admin 闸门 / role 检查。**

代码证据：

- `internal/manager/server/devicessh/http.go`
  - `ShellHandler.Register` 注册的两条路由
    (`/v1/devices/{id}/shell`、`/v1/devices/{id}/shell-direct`)
    没有用任何 `With(...).RequireRole(...)` 中间件。
  - `handle(...)` 内部只校验 `tenantctx.From(r.Context())`，仅判断
    "有没有登录"，不读 `t.Role`。
- `internal/manager/server/devicessh/fs.go`
  - `FSHandler.Register` 的 11 条 `/v1/devices/{id}/fs/*` 路由都没挂
    中间件；handler 内部只 `h.svcOrUnavailable(w)` 判断 svc 是否 wired，
    没有任何 role / admin gate。
- `cmd/ongrid/main.go`
  - `devicesshShellHandler` 和 `devicesshFSHandler` **没有调用**
    `SetAuthz(authzMW)`（同位置的 `edgeHandler.SetAuthz(authzMW)` /
    `webshellHandler.SetAuthz(authzMW)` 是有的，devicessh 这两个没接）。

也就是说：任何带有效 JWT 的请求都能进 devicessh 的所有端点，
admin / user / 未来的 viewer 都一样能调。fs.go 的注释
("casbin policy at the authz layer scopes") 描述的是**计划**，
实际并未落地——这条注释是个误导，下次路过需要顺手修正。

## edge 子树

**当前状态：走 casbin authz，member 角色已经覆盖 `* : * : write`。**

代码证据：

- `internal/manager/server/edge/http.go`
  - `Handler.Register` 的所有写接口
    (`POST /v1/edges`、`DELETE /v1/edges/{id}`、
    `POST /v1/edges/{id}/rotate-secret`、
    `POST /v1/edges/{id}/upgrade`、
    `POST /v1/edges/{id}/upgrade-package`、
    `PUT /v1/edges/{id}/plugins/{name}`)
    都用 `r.With(h.writeMW("edge:*"))` 或 `h.writeMW("edge:plugin")` 包
    装。`writeMW` 内部走 `h.authz.Require(obj, act)` 而**不是**
    `h.requireAdmin`（生产环境 `cmd/ongrid/main.go` 里调了
    `edgeHandler.SetAuthz(authzMW)`，所以 `h.authz != nil`）。
- `internal/pkg/authzmw/middleware.go` 的 `Require` 最终调到
  `authzEnf.AllowAnyOrg(ctx, userID, obj, act)`，由 casbin 拍板。
- casbin policy（`internal/iam/biz/authz/authz.go` 的 `rolePolicies`）：
  ```
  member, *, *, read
  member, *, *, write
  member, *, device:shell, exec
  ```
  matcher (`internal/iam/biz/authz/model.conf`)：
  ```
  m = g(r.sub, p.sub, r.dom)
    && (p.dom == "*" || r.dom == p.dom)
    && (p.obj == "*" || keyMatch(r.obj, p.obj))
    && (p.act == "*" || r.act == p.act)
  ```
  对 member user 的 `edge:*` / `write` 请求：
  - `g(user_id, member, org_id)` ✓（依赖 `HydrateMemberships` 把
    `OrgMembership` 同步到 casbin g rules，启动期就跑过）
  - `p.dom == "*"` ✓
  - `p.obj == "*"` ✓
  - `p.act == "write"` && `r.act == "write"` ✓

  所以 member 已经能调所有 edge 写接口。

唯一的闸门是 `viewer` 角色没有 `write` 权限（policy 中只有 `read`），
但 viewer 跟 user 在本系统里是两个不同的概念：user 在 OrgMembership
里通常是 `member` 角色，理论上**不会**被归为 `viewer`。

## 结论

- devicessh：代码层面已经是 any-authed，user / admin 都能调所有
  WebShell、SFTP、FS 端点。**无需代码改动。**
- edge 子树：走 casbin，member role 已经有 `* : * : write`，
  user 实际上能调所有 edge 写接口。**无需代码改动。**

## 跟用户预期的差异

之前那条
[2026-07-20-open-device-monitor-writes-to-user.md](2026-07-20-open-device-monitor-writes-to-user.md)
在"不在本次范围"里把这两块标成"保持原有权限"，**那是基于源码
表面的 `requireAdmin` 中间件做的保守判断**。本次核对后发现：

- devicessh 根本没接 `requireAdmin` / authz，是纯 any-authed。
- edge 接了 casbin，但 casbin 的 `member: *: *: write` 已经覆盖。

如果用户那边仍然看到 devicessh / edge 在 user 角色下 403，可能是
别的原因（前端 token 失效、casbin 启动期 g rule 没灌进去、
或用户实际属于 viewer 角色），请把现场日志 / 状态码贴出来再排查，
而不是再次去改 handler。

## 改进项（建议在另一个 PR 做）

1. 修 `devicessh/fs.go:73` 那条误导注释
   ("casbin policy at the authz layer scopes …")，改成实际状态
   （"this handler does not gate by role; an authz middleware should be
   added when viewer/admin splits are introduced"），避免下一个看代码
   的人被坑。
2. 评估是否给 `devicessh` 接 `SetAuthz(authzMW)`，按
   `device:shell: read / exec` 跟现有 casbin policy 对齐——这样
   viewer 就打不开 WebShell / SFTP，符合 policy 设计意图。
3. 在 dev 环境做一次端到端验证：user 角色（member）实测
   `POST /v1/edges`、`POST /v1/devices/{id}/shell`、
   `GET /v1/devices/{id}/fs/list`，确认全部 200 / 101 而不是 403。

## 验证

- 静态：read-through 完 `devicessh/{http,fs}.go`、
  `edge/http.go`、`authzmw/middleware.go`、`authz/authz.go`、
  `model.conf`、`cmd/ongrid/main.go` 的相关片段（grep 定位 + read
  复核），未发现任何 `requireAdmin` / `Role != "admin"` 检查会作用到
  devicessh 或 edge 的 user 调用路径上。
- 编译：本次未改代码，无新增编译项，沿用之前
  `go build / go vet` 全部通过的状态。
- 端到端：未跑（按项目规则禁止 Windows 调 Linux 上的脚本 / 打包，
  且本次没有改动需要验证）。