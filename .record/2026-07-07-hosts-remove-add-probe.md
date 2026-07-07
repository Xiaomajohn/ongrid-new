# 2026-07-07 — Hosts 页去掉"添加探针"按钮，统一探针签发入口

## 背景

`/hosts` 主机列表页（`web/src/pages/Hosts.tsx`）头部有"添加探针"按钮，与
`/devices` 监控页面（`web/src/pages/Edges.tsx`）的"新建"按钮功能重复 —
两个入口都打开 `CreateEdgeModal` 调后端 `POST /v1/edges` 签发一组 access /
secret 凭据，operator 容易困惑。统一保留两条互补路径即可：

1. **一键安装自动签发** — `/hosts` 列表每行的 `InstallButton`，自动调
   `InstallEdgeModal` 完成签发 + 远程安装，零手工复制
2. **监控页面手动签发** — `/devices`（Edges.tsx）"新建"按钮 +
   `HostDetail.tsx` ProbesTab 的"添加探针"链接（跳到 /devices），签发
   后人工复制命令到目标主机

## 目标

- Hosts 页头部去掉独立的"添加探针"按钮（与 /devices "新建"重复）
- 保留 Hosts 页行的"一键安装"入口（自动添加 + 自动安装）
- 保留 HostDetail.tsx ProbesTab 内的"添加探针"链接（跳到 /devices）
- 清理 Hosts.tsx 中已无用的探针签发相关 state / handler / 私有组件

## 改动一览

### 前端

| 文件 | 改动 |
|------|------|
| `web/src/pages/Hosts.tsx` | 删除顶部"添加探针"按钮；删除 `createProbeOpen` / `secretReveal` state；删除 `onCreateProbe` handler；删除 `<CreateEdgeModal>` / `<SecretRevealModal>` 渲染；删除私有组件 `CreateEdgeModal` / `SecretRevealModal` / `InstallCommandRow`（3 个函数共 188 行）；清理 imports：`Copy` / `Check` from lucide-react，`createEdge` / `CreateEdgeResponse` from `@/api/edges`，`Download` from lucide-react（原本就未使用）。文件从 1086 行减到 864 行。 |
| `web/src/pages/Hosts.tsx` | 顶部注释新增"探针签发入口说明"块，说明 /hosts 不再有添加探针入口，签发走两条路径：一键安装 + 监控页面。 |

## 不动的部分

- `web/src/pages/Edges.tsx` — `/devices` "新建"按钮保留，作为监控页
  面的签发入口
- `web/src/pages/HostDetail.tsx` — ProbesTab 的"添加探针"链接保留，
  跳到 `/devices` 签发
- `web/src/api/edges.ts` — `createEdge` API 不变
- 后端 `POST /v1/edges` 路由不变

## 关键设计

### 1. 不引入新"添加探针"路由

保留的"监控页面点击添加"指的就是 `/devices` 页面的"新建"按钮（与
`/hosts` 头部之前那个按钮复用同一个 `CreateEdgeModal` UI 和同一个
后端端点）。删除的是 Hosts 顶部的入口，不是"添加探针"这个能力本身。

### 2. 删的是 UI 入口，不删探针签发 API

`createEdge` API、`CreateEdgeResponse` 类型、`/v1/edges` 路由都没动。
`/hosts` 行的"一键安装"按钮内部走的是 `InstallEdgeModal`（在
`web/src/components/InstallEdgeModal.tsx`），它内部也调 `createEdge` —
所以"一键安装自动添加"是同一组 API 的不同前端入口。

## 验证

- `tsc --noEmit` 通过（Hosts.tsx 无新增错误；其余错误均为本项目
  预存在的 `@types/xterm` / `@types/xyflow__react` 等包缺失，
  与本次改动无关）
- Hosts 页头部只剩"添加设备" + "WebSSH 会话"两个按钮
- 行的"一键安装"按钮行为不变（自动签发 + 远程安装）
- `/devices` 页面"新建"按钮和 HostDetail.tsx ProbesTab 的"添加探针"
  链接行为不变

## 影响面

- 用户体感：Hosts 页头部少一个按钮；其余入口都不变
- 探针签发 API 行为不变
- 后端无改动
- 文件行数：Hosts.tsx 减 222 行
