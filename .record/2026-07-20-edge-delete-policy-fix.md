# edge 删除 403 修复 — casbin 缺 member delete policy

## 背景

监控设备详情页 `/hosts/:hostId/monitor` → 探针 Tab → 删除 edge 探针
（非 admin 用户），提示 403。沿用 [2026-07-20-devicessh-edge-already-open.md](2026-07-20-devicessh-edge-already-open.md)
的核对结论，原本预期 "user 角色也能删 edge"——本次确认那条 record
**结论误判**，本次修复。

## 根因

完整调用链：

1. **UI**：监控设备详情页 `/hosts/:hostId/monitor` → 探针 Tab
   `MonitorDeviceProbes` → 删除按钮显示条件 `canMutate`（user / admin 都 true）
2. **API**：`web/src/api/edges.ts:183` 调 `DELETE /v1/edges/{id}`
3. **路由注册**：`internal/manager/server/edge/http.go:145`
   ```go
   r.With(h.deleteMW("edge:*")).Delete("/v1/edges/{id}", h.deleteEdge)
   ```
   `deleteMW("edge:*")` → `h.authz.Require("edge:*", "delete")`
   **act = `"delete"`**（这是与 POST 路由的关键区别）。
4. **authzmw**：`internal/pkg/authzmw/middleware.go:86`
   `m.z.AllowAnyOrg(r.Context(), t.UserID, "edge:*", "delete")`。
5. **casbin policy**：`internal/iam/biz/authz/authz.go` 的 `rolePolicies`：

   ```go
   {admin,   "*", "*", "*"},            // admin 全通
   {member,  "*", "*", "read"},         // user 读
   {member,  "*", "*", "write"},        // user 写
   {member,  "*", "device:shell", "exec"},
   {viewer,  "*", "*", "read"},
   ```

   **没有 `member : * : * : delete` 这一行**。

   - admin 走 `* : * : *` → 通过
   - user（OrgMembership = member）→ casbin 查 `delete` act，无 policy 命中 → 403
   - viewer → 前端 `canMutate` 已隐藏删除按钮，不会触发

## 为什么之前那条 record 误判

[2026-07-20-devicessh-edge-already-open.md](2026-07-20-devicessh-edge-already-open.md)
核查的路由只列了：

- `POST /v1/edges`
- `DELETE /v1/edges/{id}`
- `POST /v1/edges/{id}/rotate-secret`
- `POST /v1/edges/{id}/upgrade`
- `POST /v1/edges/{id}/upgrade-package`
- `PUT /v1/edges/{id}/plugins/{name}`

并基于 `r.With(h.writeMW(...))` / `h.deleteMW(...)` 这两条 MW 的命名
推断 "write 类已经被 `member : * : * : write` 覆盖"——但实际上
`deleteMW` 传的 act 是字面量 `"delete"`，**不归 write 管**。

也就是说，原 record 只看了 `write` 这一侧，没核 `delete` 这条 act，
导致漏了 DELETE `/v1/edges/{id}` 这一条线。

## 修复

在 `rolePolicies` 里 `member` 的 `write` policy 之后加一行 `delete`：

```go
// internal/iam/biz/authz/authz.go
{iammodel.MembershipRoleMember, "*", "*", "read"},
{iammodel.MembershipRoleMember, "*", "*", "write"},
{iammodel.MembershipRoleMember, "*", "*", "delete"},   // ← 新增
{iammodel.MembershipRoleMember, "*", "device:shell", "exec"},
```

效果（一次性下发到 casbin_rule 表，启动期 `SeedRolePolicies` 会
幂等 insert）：

- admin → 已有 `* : * : *`，删除照旧
- user（member）→ 新增的 `delete` 命中，403 变 204
- viewer → 仍只有 read，前端不显示删除按钮，行为不变

## 为什么不是改前端

前端 `canMutate = admin || user`（web/src/store/me.ts:111），删除按钮
对 user 显示。如果后端不让 user 删，前端要加 `isAdmin` 判断才能
隐藏按钮——但这与以下两条已有原则冲突：

- `internal/manager/server/device/http.go:67-69` 注释：设备管理类写接口
  全部 any-authed，user 可修改 / 删除主机 / 改 SSH 凭据
- `internal/manager/server/monitor/http.go:49-51` 注释：监控面板的增
  删改查对所有已认证角色开放（沿袭 2026-07-20 决定）

edge 是比 device / panel 更敏感的资源（删除会立即断开 tunnel / metrics /
loki 推送，access_key 失效需要重新签发），但按"探针属于该主机，user
有权管理自己主机的探针"的产品意图，保持与 device / panel 一致更合理。
本次采用"加 delete policy"路径，若后续收紧（user 不准删 edge），
反向调整也很简单——把这条 policy 删掉 + 前端 `MonitorDeviceProbes` 把
删除按钮条件从 `canMutate` 改成 `isAdmin` 即可。

## 验证

- `go build ./internal/iam/biz/authz/... ./internal/manager/server/edge/...` exit=0
- 静态核对：
  - `edge/http.go:122-127` 的 `deleteMW` 仍传 `"delete"` act，casbin
    现在能命中新增的 policy
  - `cmd/ongrid/main.go:803` 的 `edgeHandler.SetAuthz(authzMW)` 不变，
    `h.authz != nil` 仍成立（write / delete 都会走 casbin）
  - `SeedRolePolicies` 是 idempotent 的 AddPolicy（authz.go:118），
    已部署实例升级后会自动灌入新 policy，不需要手工 SQL

## 端到端

按项目规则（AGENTS.md 时钟管理硬规则 + Windows 禁调 Linux 脚本），
未在本地跑端到端。需要在 192.168.25.30 上 make build-amd64 后用
user 账号实测：

1. 进 `/hosts/<host-id>/monitor` → 探针 Tab
2. 点任意一个 edge 的删除按钮 → 应得 204 而非 403
3. admin 账号同样操作 → 仍 204（回归覆盖）

如发现 user 账号仍是 403，按 record 2026-07-20 末尾的"可能的原因"
清单排查（casbin 启动期 HydrateMemberships 失败、JWT 里 t.Role 异常
等）。
