# 设备管理API

<cite>
**本文引用的文件**
- [internal/manager/server/device/http.go](file://internal/manager/server/device/http.go)
- [internal/manager/server/device/credentials.go](file://internal/manager/server/device/credentials.go)
- [internal/manager/server/device/monitors_http.go](file://internal/manager/server/device/monitors_http.go)
- [internal/manager/biz/device/usecase.go](file://internal/manager/biz/device/usecase.go)
- [internal/manager/model/device/model.go](file://internal/manager/model/device/model.go)
- [internal/manager/server/topology/http.go](file://internal/manager/server/topology/http.go)
- [internal/manager/biz/topology/usecase.go](file://internal/manager/biz/topology/usecase.go)
- [web/src/api/devices.ts](file://web/src/api/devices.ts)
- [cmd/ongrid/main.go](file://cmd/ongrid/main.go)
</cite>

## 目录
1. [简介](#简介)
2. [项目结构](#项目结构)
3. [核心组件](#核心组件)
4. [架构总览](#架构总览)
5. [详细接口说明](#详细接口说明)
6. [依赖关系分析](#依赖关系分析)
7. [性能与可用性](#性能与可用性)
8. [故障排查指南](#故障排查指南)
9. [结论](#结论)
10. [附录：curl示例与最佳实践](#附录curl示例与最佳实践)

## 简介
本文件为“设备管理”子域（Device）的REST API文档，覆盖设备CRUD、状态查询、拓扑关联、SSH凭证管理、监控面板列表等能力。所有端点统一位于 /api/v1 前缀下，认证采用JWT Bearer Token；写操作需管理员角色。

## 项目结构
设备管理HTTP路由由Handler注册到受保护路由组，业务逻辑通过Usecase封装，数据模型定义在model层，前端调用封装在web/src/api/devices.ts中。

```mermaid
graph TB
subgraph "HTTP层"
D["device.Handler<br/>Register()"]
T["topology.Handler<br/>Register()"]
end
subgraph "业务层"
DU["device.Usecase"]
TU["topology.Usecase"]
end
subgraph "模型层"
DM["device.Model"]
TM["topology.Model"]
end
subgraph "前端"
FE["web/src/api/devices.ts"]
end
subgraph "入口"
M["cmd/ongrid/main.go<br/>挂载路由"]
end
M --> D
M --> T
D --> DU
T --> TU
DU --> DM
TU --> TM
FE --> D
FE --> T
```

图表来源
- [internal/manager/server/device/http.go:46-75](file://internal/manager/server/device/http.go#L46-L75)
- [internal/manager/server/topology/http.go:48-76](file://internal/manager/server/topology/http.go#L48-L76)
- [internal/manager/biz/device/usecase.go:126-146](file://internal/manager/biz/device/usecase.go#L126-L146)
- [internal/manager/biz/topology/usecase.go:14-30](file://internal/manager/biz/topology/usecase.go#L14-L30)
- [internal/manager/model/device/model.go:34-128](file://internal/manager/model/device/model.go#L34-L128)
- [internal/manager/server/topology/http.go:48-76](file://internal/manager/server/topology/http.go#L48-L76)
- [web/src/api/devices.ts:62-116](file://web/src/api/devices.ts#L62-L116)
- [cmd/ongrid/main.go:2444-2448](file://cmd/ongrid/main.go#L2444-L2448)

章节来源
- [internal/manager/server/device/http.go:46-75](file://internal/manager/server/device/http.go#L46-L75)
- [internal/manager/server/topology/http.go:48-76](file://internal/manager/server/topology/http.go#L48-L76)
- [cmd/ongrid/main.go:2444-2448](file://cmd/ongrid/main.go#L2444-L2448)

## 核心组件
- HTTP处理器
  - device.Handler：提供 /v1/devices/* 路由，含设备CRUD、角色更新、软删除/恢复、边端关联、SSH凭据管理等。
  - topology.Handler：提供 /v1/topology/* 路由，用于节点、关系、类型、节点类型的CRUD。
- 业务用例
  - device.Usecase：封装设备创建、更新、删除、角色设置、可达性探测、SSH凭据读写、与拓扑节点的镜像同步等。
  - topology.Usecase：封装节点/关系/类型的校验与持久化，以及设备→拓扑节点的镜像逻辑。
- 数据模型
  - device.Model：设备实体字段、角色位图、与Edge的多对多关系类型等。
  - topology.Model：节点、关系、关系类型、节点类型等。
- 前端API封装
  - web/src/api/devices.ts：设备相关的前端函数与类型定义，映射后端JSON结构。

章节来源
- [internal/manager/server/device/http.go:30-75](file://internal/manager/server/device/http.go#L30-L75)
- [internal/manager/biz/device/usecase.go:126-146](file://internal/manager/biz/device/usecase.go#L126-L146)
- [internal/manager/model/device/model.go:34-128](file://internal/manager/model/device/model.go#L34-L128)
- [internal/manager/server/topology/http.go:48-76](file://internal/manager/server/topology/http.go#L48-L76)
- [internal/manager/biz/topology/usecase.go:14-30](file://internal/manager/biz/topology/usecase.go#L14-L30)
- [web/src/api/devices.ts:5-116](file://web/src/api/devices.ts#L5-L116)

## 架构总览
设备管理API遵循“HTTP → Usecase → Repo/Model”的分层设计。写操作经管理员中间件校验，读操作仅需已认证租户成员。错误统一以JSON返回，包含error与code字段。

```mermaid
sequenceDiagram
participant C as "客户端"
participant H as "device.Handler"
participant U as "device.Usecase"
participant DB as "数据库/模型"
C->>H : "POST /api/v1/devices"
H->>H : "requireAdmin(鉴权)"
H->>U : "Create(input)"
U->>DB : "持久化设备+SSH凭据"
DB-->>U : "设备实体"
U-->>H : "设备DTO"
H-->>C : "201 + 响应体"
```

图表来源
- [internal/manager/server/device/http.go:61-75](file://internal/manager/server/device/http.go#L61-L75)
- [internal/manager/biz/device/usecase.go:54-124](file://internal/manager/biz/device/usecase.go#L54-L124)

## 详细接口说明

### 通用约定
- 基础路径：/api/v1
- 认证：请求头携带 Authorization: Bearer <token>
- 权限：标注“管理员”的接口需 admin 角色，否则返回 403；未认证返回 401
- 分页：多数列表支持 limit、offset 参数
- 过滤：各列表接口支持各自查询参数
- 错误格式：{ "error": "...", "code": "..." }，常见code包括 unauthorized、forbidden、invalid、not-found、conflict、internal 等

章节来源
- [internal/manager/server/device/http.go:77-91](file://internal/manager/server/device/http.go#L77-L91)
- [internal/manager/server/device/http.go:624-654](file://internal/manager/server/device/http.go#L624-L654)
- [internal/manager/server/topology/http.go:78-92](file://internal/manager/server/topology/http.go#L78-L92)
- [internal/manager/server/topology/http.go:646-675](file://internal/manager/server/topology/http.go#L646-L675)

### 设备清单与详情
- GET /api/v1/devices
  - 描述：列出设备，支持按主机名、名称、角色、在线状态、是否包含已删除进行过滤，支持分页
  - 查询参数
    - hostname: string
    - name: string
    - roles: string（逗号分隔的角色名，如 server,storage；或 unknown）
    - online: true|false|1|0
    - include_deleted: true|1|yes
    - limit: int
    - offset: int
  - 响应体
    - items: Device[]
    - total: number
  - 状态码：200
- GET /api/v1/devices/{id}
  - 描述：获取单个设备详情
  - 响应体：Device
  - 状态码：200

章节来源
- [internal/manager/server/device/http.go:215-284](file://internal/manager/server/device/http.go#L215-L284)
- [internal/manager/server/device/http.go:340-356](file://internal/manager/server/device/http.go#L340-L356)
- [web/src/api/devices.ts:62-82](file://web/src/api/devices.ts#L62-L82)
- [web/src/api/devices.ts:118-120](file://web/src/api/devices.ts#L118-L120)

### 设备创建
- POST /api/v1/devices（管理员）
  - 描述：注册新设备并写入SSH连接信息
  - 请求体
    - name: string（必填）
    - description: string
    - hostname: string
    - ssh_host: string（必填）
    - ssh_port: number（默认22）
    - ssh_user: string（必填）
    - ssh_auth_kind: "password" | "key"（必填）
    - ssh_password: string（当auth_kind=password时必填）
    - ssh_key: string（当auth_kind=key时必填）
  - 响应体：CreateDeviceResponse（包含id、name、hostname、description、created_at及ssh_*上下文与明文凭据）
  - 状态码：201

章节来源
- [internal/manager/server/device/http.go:286-338](file://internal/manager/server/device/http.go#L286-L338)
- [internal/manager/biz/device/usecase.go:54-124](file://internal/manager/biz/device/usecase.go#L54-L124)
- [web/src/api/devices.ts:84-116](file://web/src/api/devices.ts#L84-L116)

### 设备更新
- PATCH /api/v1/devices/{id}（管理员）
  - 描述：局部更新展示字段与SSH连接块；未提供的字段保持不变，空字符串表示清空（仅对部分字段有效）
  - 请求体
    - name?: string
    - description?: string
    - hostname?: string
    - ssh_host?: string
    - ssh_port?: number
    - ssh_user?: string
    - ssh_auth_kind?: "password" | "key"
    - ssh_password?: string
    - ssh_key?: string
  - 响应体：Device（最新完整DTO）
  - 状态码：200

章节来源
- [internal/manager/server/device/http.go:358-456](file://internal/manager/server/device/http.go#L358-L456)
- [web/src/api/devices.ts:122-145](file://web/src/api/devices.ts#L122-L145)

### 设备角色
- PATCH /api/v1/devices/{id}/roles（管理员）
  - 描述：更新设备的角色集合（server/storage/network/database），或传入 ["unknown"] 清空
  - 请求体：{ "roles": string[] }
  - 状态码：204

章节来源
- [internal/manager/server/device/http.go:458-474](file://internal/manager/server/device/http.go#L458-L474)
- [internal/manager/biz/device/usecase.go:182-211](file://internal/manager/biz/device/usecase.go#L182-L211)
- [internal/manager/model/device/model.go:137-216](file://internal/manager/model/device/model.go#L137-L216)

### 设备删除与恢复
- DELETE /api/v1/devices/{id}?hard=true|false（管理员）
  - 描述：soft delete（默认）或 hard delete（不可恢复）
  - 状态码：204
- POST /api/v1/devices/{id}/restore（管理员）
  - 描述：恢复软删除的设备行
  - 响应体：Device（恢复后的最新DTO）
  - 状态码：200

章节来源
- [internal/manager/server/device/http.go:476-513](file://internal/manager/server/device/http.go#L476-L513)
- [internal/manager/biz/device/usecase.go:436-453](file://internal/manager/biz/device/usecase.go#L436-L453)
- [web/src/api/devices.ts:155-179](file://web/src/api/devices.ts#L155-L179)

### 设备与边端关联
- GET /api/v1/devices/{id}/edges
  - 描述：列出该设备关联的边端（host/discovered）
  - 响应体：{ "items": [{ edge_id, device_id, type, created_at }] }
  - 状态码：200

章节来源
- [internal/manager/server/device/http.go:515-545](file://internal/manager/server/device/http.go#L515-L545)
- [web/src/api/devices.ts:181-185](file://web/src/api/devices.ts#L181-L185)

### SSH凭证管理
- PUT /api/v1/devices/{id}/ssh-credentials（管理员）
  - 描述：替换整块SSH凭据（kind/value二选一，user必填）
  - 请求体
    - kind: "password" | "key"
    - value: string（密码或私钥）
    - host?: string
    - port?: number（默认22）
    - user: string（必填）
    - host_key?: string
  - 状态码：204
- GET /api/v1/devices/{id}/ssh-info
  - 描述：读取SSH信息（含明文凭据与edge_online）
  - 响应体
    - host, port, user, auth_kind, has_password, has_key, password?, key?, host_key?, last_seen_at?, last_error?, edge_online: boolean
  - 状态码：200
- DELETE /api/v1/devices/{id}/ssh-credentials?kind=password|key（管理员）
  - 描述：按kind清除对应凭据字段
  - 状态码：204

章节来源
- [internal/manager/server/device/credentials.go:65-112](file://internal/manager/server/device/credentials.go#L65-L112)
- [internal/manager/server/device/credentials.go:114-147](file://internal/manager/server/device/credentials.go#L114-L147)
- [internal/manager/server/device/credentials.go:169-188](file://internal/manager/server/device/credentials.go#L169-L188)
- [internal/manager/biz/device/usecase.go:294-395](file://internal/manager/biz/device/usecase.go#L294-L395)
- [web/src/api/devices.ts:187-232](file://web/src/api/devices.ts#L187-L232)

### 设备监控面板（按设备维度）
- GET /api/v1/devices/{id}/monitors
  - 描述：拉取与该设备绑定的监控面板（含全局面板）
  - 查询参数
    - include_deleted: true|1|yes
  - 响应体：{ "panels": Panel[] }
  - 状态码：200

章节来源
- [internal/manager/server/device/monitors_http.go:30-70](file://internal/manager/server/device/monitors_http.go#L30-L70)

### 拓扑管理（与设备关联）
- 节点
  - GET /api/v1/topology/nodes
  - POST /api/v1/topology/nodes（管理员）
  - GET /api/v1/topology/nodes/{id}
  - PATCH /api/v1/topology/nodes/{id}（管理员）
  - DELETE /api/v1/topology/nodes/{id}（管理员）
- 关系
  - GET /api/v1/topology/relations
  - POST /api/v1/topology/relations（管理员）
  - GET /api/v1/topology/relations/{id}
  - PATCH /api/v1/topology/relations/{id}（管理员）
  - DELETE /api/v1/topology/relations/{id}（管理员）
- 关系类型
  - GET /api/v1/topology/relation-types
  - POST /api/v1/topology/relation-types（管理员）
  - GET /api/v1/topology/relation-types/{name}
  - DELETE /api/v1/topology/relation-types/{name}（管理员）
- 节点类型
  - GET /api/v1/topology/node-types
  - GET /api/v1/topology/node-types/{name}
  - POST /api/v1/topology/node-types（管理员）
  - DELETE /api/v1/topology/node-types/{name}（管理员）

章节来源
- [internal/manager/server/topology/http.go:48-76](file://internal/manager/server/topology/http.go#L48-L76)
- [internal/manager/biz/topology/usecase.go:32-112](file://internal/manager/biz/topology/usecase.go#L32-L112)
- [internal/manager/biz/topology/usecase.go:114-201](file://internal/manager/biz/topology/usecase.go#L114-L201)
- [internal/manager/biz/topology/usecase.go:252-334](file://internal/manager/biz/topology/usecase.go#L252-L334)
- [internal/manager/biz/topology/usecase.go:336-419](file://internal/manager/biz/topology/usecase.go#L336-L419)

### 设备状态与可达性
- 在线状态
  - Device.Online：来自边端心跳的聚合结果，表示“该主机上有边端在线”
- 网络可达
  - Device.Reachable / LastReachableAt：由后台定时任务对ssh_host执行ping后回填，代表“网络层可达”
- 最近SSH交互
  - SSHLastSeenAt / SSHLastError：记录最近一次SSH成功/失败时间戳与错误摘要

章节来源
- [internal/manager/model/device/model.go:82-114](file://internal/manager/model/device/model.go#L82-L114)
- [internal/manager/biz/device/usecase.go:228-292](file://internal/manager/biz/device/usecase.go#L228-L292)

## 依赖关系分析
- 设备模块
  - HTTP层依赖device.Usecase；Usecase依赖Repo与EdgeDeviceRepo；设备模型定义在device.Model
- 拓扑模块
  - HTTP层依赖topology.Usecase；Usecase依赖Node/Relation/Type/NodeType四个Repo
- 路由挂载
  - main.go将device和topology Handler注册到受保护路由组

```mermaid
classDiagram
class DeviceHandler {
+Register(r)
+create()
+list()
+get()
+update()
+updateRoles()
+delete()
+restore()
+listEdges()
+putSSHCredentials()
+getSSHInfo()
+deleteSSHCredentials()
}
class DeviceUsecase {
+Create()
+List()
+Get()
+UpdateNameDescription()
+UpdateRoles()
+Delete()
+Restore()
+SetSSHCredentials()
+GetSSHCredentials()
+ClearSSHCredentialsField()
+PingReachable()
}
class TopologyHandler {
+Register(r)
+listNodes()
+createNode()
+updateNode()
+deleteNode()
+listRelations()
+createRelation()
+updateRelation()
+deleteRelation()
+listRelationTypes()
+createRelationType()
+deleteRelationType()
+listNodeTypes()
+createNodeType()
+deleteNodeType()
}
class TopologyUsecase {
+CreateNode()
+UpdateNode()
+DeleteNode()
+CreateRelation()
+UpdateRelation()
+DeleteRelation()
+RegisterRelationType()
+DeleteRelationType()
+RegisterNodeType()
+DeleteNodeType()
}
DeviceHandler --> DeviceUsecase : "调用"
TopologyHandler --> TopologyUsecase : "调用"
```

图表来源
- [internal/manager/server/device/http.go:46-75](file://internal/manager/server/device/http.go#L46-L75)
- [internal/manager/biz/device/usecase.go:126-146](file://internal/manager/biz/device/usecase.go#L126-L146)
- [internal/manager/server/topology/http.go:48-76](file://internal/manager/server/topology/http.go#L48-L76)
- [internal/manager/biz/topology/usecase.go:14-30](file://internal/manager/biz/topology/usecase.go#L14-L30)

章节来源
- [internal/manager/server/device/http.go:46-75](file://internal/manager/server/device/http.go#L46-L75)
- [internal/manager/biz/device/usecase.go:126-146](file://internal/manager/biz/device/usecase.go#L126-L146)
- [internal/manager/server/topology/http.go:48-76](file://internal/manager/server/topology/http.go#L48-L76)
- [internal/manager/biz/topology/usecase.go:14-30](file://internal/manager/biz/topology/usecase.go#L14-L30)

## 性能与可用性
- 列表分页：使用limit/offset控制返回量，避免大表全量扫描
- 角色过滤：后端将位图匹配转换为IN列表，命中索引idx_devices_roles
- 可达性探测：后台定时任务批量ping并回填reachable，减少实时IO
- 错误快速失败：参数校验集中在usecase层，尽早返回invalid错误

[本节为通用建议，不直接分析具体文件]

## 故障排查指南
- 401 未认证：检查Authorization头是否正确携带Bearer Token
- 403 无权限：确认当前用户角色是否为admin（写操作需要）
- 400 invalid：检查必填字段、端口范围、auth_kind与凭据匹配、roles枚举值
- 404 not-found：确认设备ID存在且未被硬删除
- 500 internal：查看服务端日志定位底层错误

章节来源
- [internal/manager/server/device/http.go:77-91](file://internal/manager/server/device/http.go#L77-L91)
- [internal/manager/server/device/http.go:624-654](file://internal/manager/server/device/http.go#L624-L654)
- [internal/manager/server/topology/http.go:78-92](file://internal/manager/server/topology/http.go#L78-L92)
- [internal/manager/server/topology/http.go:646-675](file://internal/manager/server/topology/http.go#L646-L675)

## 结论
设备管理API围绕“设备实体+SSH凭据+拓扑关联”构建，提供完整的CRUD、状态与可达性观测、凭证轮换与监控面板访问能力。通过统一的错误格式与严格的权限控制，便于前后端集成与安全运维。

[本节为总结，不直接分析具体文件]

## 附录：curl示例与最佳实践

- 登录获取Token（参考IAM登录流程，此处略）
- 列出设备（分页+过滤）
  - curl -sS -H "Authorization: Bearer $TOKEN" "http://localhost:8080/api/v1/devices?limit=20&offset=0&online=true&include_deleted=false"
- 创建设备（管理员）
  - curl -sS -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d '{
      "name":"db-node-01",
      "description":"生产数据库节点",
      "hostname":"db-node-01",
      "ssh_host":"10.0.0.10",
      "ssh_port":22,
      "ssh_user":"root",
      "ssh_auth_kind":"password",
      "ssh_password":"your-password"
    }' "http://localhost:8080/api/v1/devices"
- 更新设备（局部更新）
  - curl -sS -X PATCH -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d '{
      "description":"更新描述",
      "ssh_port":2222
    }' "http://localhost:8080/api/v1/devices/1"
- 更新角色（管理员）
  - curl -sS -X PATCH -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d '{"roles":["server","database"]}' "http://localhost:8080/api/v1/devices/1/roles"
- 删除设备（软删）
  - curl -sS -X DELETE -H "Authorization: Bearer $TOKEN" "http://localhost:8080/api/v1/devices/1"
- 恢复设备（管理员）
  - curl -sS -X POST -H "Authorization: Bearer $TOKEN" "http://localhost:8080/api/v1/devices/1/restore"
- 查看SSH信息
  - curl -sS -H "Authorization: Bearer $TOKEN" "http://localhost:8080/api/v1/devices/1/ssh-info"
- 设置SSH凭据（管理员）
  - curl -sS -X PUT -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d '{
      "kind":"key",
      "value":"-----BEGIN PRIVATE KEY-----...",
      "user":"root",
      "port":22
    }' "http://localhost:8080/api/v1/devices/1/ssh-credentials"
- 清除某类凭据（管理员）
  - curl -sS -X DELETE -H "Authorization: Bearer $TOKEN" "http://localhost:8080/api/v1/devices/1/ssh-credentials?kind=password"
- 列出设备关联边端
  - curl -sS -H "Authorization: Bearer $TOKEN" "http://localhost:8080/api/v1/devices/1/edges"
- 列出设备监控面板
  - curl -sS -H "Authorization: Bearer $TOKEN" "http://localhost:8080/api/v1/devices/1/monitors?include_deleted=false"

最佳实践
- 始终使用HTTPS与有效的TLS证书
- 最小权限原则：仅管理员可写，普通用户只读
- 凭据轮换：优先使用DELETE ?kind= 单独轮换，避免全量PUT
- 分页与过滤：列表接口务必带上limit/offset与必要过滤条件
- 错误处理：根据响应中的code分支处理unauthorized/forbidden/invalid/not-found/internal

[本节为通用指导，不直接分析具体文件]