---
generated_by: repo-wiki-agent
baseline_commit: "47bad98d46a1d70231d237285784a8192d7756c7"
last_updated: "2026-07-06"
managed_sections:
  - "## Overview"
  - "## Modules (7)"
  - "## Style System"
  - "## API Layer Exports"
  - "## UI Components"
  - "## Pages"
  - "## Build Commands"
---

# 前端 (React + TS)

<!-- BEGIN:REPO_WIKI_MANAGED -->
## Overview

前端位于 `web/`，使用：

- React 18 + TypeScript
- Vite（开发与构建）
- Tailwind CSS（UI 配色与排版）
- pnpm（包管理）

入口：`web/src/main.tsx`，构建产物 `web/dist/`。

## Modules (7)

| 模块 | 路径 | 角色 |
|---|---|---|
| `api` | `web/src/api/` | 后端接口封装（按域划分） |
| `components` | `web/src/components/` | 通用 UI 组件 |
| `i18n` | `web/src/i18n/` | 多语言文案 |
| `lib` | `web/src/lib/` | 通用工具 |
| `pages` | `web/src/pages/` | 业务页面 |
| `store` | `web/src/store/` | 状态管理 |
| `test` | `web/src/test/` | 测试 |

## Style System

- 全局样式：`web/src/styles/index.css`
- 主题色：`zinc` 中性骨架 + `indigo` 主操作
- 语义色：emerald(成功) / amber(降级) / red(异常) / sky(信息)
- `html.light` 覆盖：浅色模式

## API Layer Exports


## UI Components

- `./web/src/components/ActionChip.tsx` L13: `ActionChip`[^13]
- `./web/src/components/AgentBadge.tsx` L18: `AgentBadgeSize`[^18]
- `./web/src/components/AgentBadge.tsx` L41: `AgentBadge`[^41]
- `./web/src/components/AgentSidePanel.tsx` L27: `AgentSidePanel`[^27]
- `./web/src/components/Avatar.tsx` L19: `Avatar`[^19]
- `./web/src/components/ChatInput.tsx` L40: `SubmitPayload`[^40]
- `./web/src/components/ChatInput.tsx` L48: `ModelSelection`[^48]
- `./web/src/components/ChatInput.tsx` L76: `ChatInput`[^76]

## Pages

- `./web/src/pages/AdminLayout.tsx` L48: `AdminLayout`[^48]
- `./web/src/pages/Agents.tsx` L51: `AgentsPage`[^51]
- `./web/src/pages/AlertRules.tsx` L187: `AlertRulesPage`[^187]
- `./web/src/pages/Alerts.tsx` L40: `AlertsPage`[^40]
- `./web/src/pages/Approvals.tsx` L14: `ApprovalsPage`[^14]

## Build Commands

```bash
cd web
pnpm install
pnpm lint
pnpm build       # tsc --noEmit + vite build
```

<!-- END:REPO_WIKI_MANAGED -->

## 引用
[^13]: web/src/components/ActionChip.tsx L13–L16 — [web/src/components/ActionChip.tsx#L13-L16](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/web/src/components/ActionChip.tsx#L13-L16)
[^14]: web/src/pages/Approvals.tsx L14–L17 — [web/src/pages/Approvals.tsx#L14-L17](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/web/src/pages/Approvals.tsx#L14-L17)
[^18]: web/src/components/AgentBadge.tsx L18–L21 — [web/src/components/AgentBadge.tsx#L18-L21](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/web/src/components/AgentBadge.tsx#L18-L21)
[^19]: web/src/components/Avatar.tsx L19–L22 — [web/src/components/Avatar.tsx#L19-L22](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/web/src/components/Avatar.tsx#L19-L22)
[^27]: web/src/components/AgentSidePanel.tsx L27–L30 — [web/src/components/AgentSidePanel.tsx#L27-L30](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/web/src/components/AgentSidePanel.tsx#L27-L30)
[^40]: web/src/pages/Alerts.tsx L40–L43 — [web/src/pages/Alerts.tsx#L40-L43](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/web/src/pages/Alerts.tsx#L40-L43)
[^40]: web/src/components/ChatInput.tsx L40–L43 — [web/src/components/ChatInput.tsx#L40-L43](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/web/src/components/ChatInput.tsx#L40-L43)
[^41]: web/src/components/AgentBadge.tsx L41–L44 — [web/src/components/AgentBadge.tsx#L41-L44](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/web/src/components/AgentBadge.tsx#L41-L44)
[^48]: web/src/pages/AdminLayout.tsx L48–L51 — [web/src/pages/AdminLayout.tsx#L48-L51](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/web/src/pages/AdminLayout.tsx#L48-L51)
[^48]: web/src/components/ChatInput.tsx L48–L51 — [web/src/components/ChatInput.tsx#L48-L51](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/web/src/components/ChatInput.tsx#L48-L51)
[^51]: web/src/pages/Agents.tsx L51–L54 — [web/src/pages/Agents.tsx#L51-L54](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/web/src/pages/Agents.tsx#L51-L54)
[^76]: web/src/components/ChatInput.tsx L76–L79 — [web/src/components/ChatInput.tsx#L76-L79](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/web/src/components/ChatInput.tsx#L76-L79)
[^187]: web/src/pages/AlertRules.tsx L187–L190 — [web/src/pages/AlertRules.tsx#L187-L190](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/web/src/pages/AlertRules.tsx#L187-L190)