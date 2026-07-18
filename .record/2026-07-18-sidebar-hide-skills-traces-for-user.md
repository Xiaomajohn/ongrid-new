# 2026-07-18 — 侧边栏对 user 角色隐藏「技能 / 链路」入口

## 需求

把侧边栏里「技能（Skills）」和「链路（Traces）」两个菜单，对 `user` 角色的用户隐藏。
`admin` / `viewer` / 其它角色保持可见。

## 设计

权限判断完全复用项目已有的 `usePermissions()`（`web/src/store/me.ts`），不要再造一套角色判定：

- `isUser` 已经定义为 `role === 'user'`
- 用户登录时 role 走 JWT 同步进 `useAuth`，首屏同步可用，不会闪一下又藏掉
- `/v1/me` 异步返回后 role 也会回写到这里做二次确认（fallback 链已就位）

## 改动

只动一个文件：`web/src/components/Sidebar.tsx`

### 1) 解构出 `isUser`

```diff
- const { isAdmin } = usePermissions();
+ const { isAdmin, isUser } = usePermissions();
```

### 2) collapsed 视图（折叠侧边栏）下的 Skills 入口加守卫

折叠侧边栏只放图标，如果 user 角色下不藏掉，user 角色在折叠态下会突然看到一个 Wrench 图标，
破坏了"展开 / 折叠视觉一致"的预期。所以这里也要藏：

```diff
+ {!isUser && (
+   <Link
+     to="/skills"
+     aria-label={tr('技能', 'Skills')}
+     className="rounded-lg p-2 text-zinc-300 hover:bg-zinc-800 hover:text-zinc-100"
+   >
+     <Wrench size={16} />
+   </Link>
+ )}
```

### 3) expanded 视图 - Agent 区的 Skills

```diff
  <SidebarNavItem to="/workflows" icon={Route} label={tr('工作流', 'Workflows')} />
- <SidebarNavItem to="/skills" icon={Wrench} label={tr('技能', 'Skills')} />
+ {!isUser && <SidebarNavItem to="/skills" icon={Wrench} label={tr('技能', 'Skills')} />}
```

### 4) expanded 视图 - 监控告警区的 Traces

```diff
  <SidebarNavItem to="/logs" icon={FileText} label={tr('日志', 'Logs')} />
- <SidebarNavItem to="/traces" icon={Waypoints} label={tr('链路', 'Traces')} />
+ {!isUser && <SidebarNavItem to="/traces" icon={Waypoints} label={tr('链路', 'Traces')} />}
```

## 为什么只藏 UI、不动路由

- 路由层 `/skills`、`/traces` 仍然对所有登录用户开放（RouteGuard 只做 token 校验）
- 后端 `requireAdmin` / `requireRole` 等权限闸口不在这次范围里 — 如果后续 user 直接输
  URL 强行访问后端也已有兜底（EmptyState 或 403），这与"管理员入口"的处理一致
- 本次只解决"菜单可见性"问题，UI 层先隔离入口即可；如后续 user 角色访问被后端拒绝，
  再统一加 RequireRole 包装

## 角色可见性矩阵

| 角色 | 技能 | 链路 |
|------|------|------|
| admin | ✓ | ✓ |
| viewer | ✓ | ✓ |
| user | ✗ | ✗ |
| 其它 / 未识别 | ✓ | ✓（走 `!isUser` 默认值） |

注：`!isUser` 的语义是"除 user 外都可见"，所以将来新增 role 不会被误伤。

## 验证

按项目开发规则：
- 本地不跑 .sh / make
- 本地不写单元测试（按规则第 3 条）
- 只做静态代码走查 + 角色矩阵核对

修改落在同一个文件，三处守卫都遵循 `{!isUser && (...)}` 模式，逻辑一致。

## 关联文件

- `web/src/components/Sidebar.tsx` — 三处菜单 + 一处解构
- `web/src/store/me.ts` — `isUser` 定义来源（未改动）
- `web/src/store/auth.ts` — `role` 字段来源（未改动）