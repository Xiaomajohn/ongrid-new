---
kind: frontend_style
name: 基于 Tailwind CSS + CSS 变量的双主题系统
category: frontend_style
scope:
    - '**'
source_files:
    - web/tailwind.config.ts
    - web/src/styles/index.css
    - web/src/store/theme.ts
    - web/postcss.config.js
    - web/package.json
---

## 体系概览
Ongrid Web 前端采用 Vite + React + TypeScript 构建，样式体系以 **Tailwind CSS v3** 为核心，通过 **CSS 自定义属性（CSS Variables）** 实现暗色/亮色双主题与可切换强调色。组件层几乎全部使用 Tailwind 原子类组合，未引入第三方 UI 组件库。

## 核心文件与包
- `web/tailwind.config.ts` — Tailwind 配置：扩展字体（Inter / JetBrains Mono）、语义化颜色 token、`pulse-dot` 动画与 keyframes；启用 `darkMode: 'class'`。
- `web/src/styles/index.css` — 全局样式入口：声明 `@tailwind base/components/utilities`，定义 `:root.dark` 与 `:root.light` 两套设计 token（`--bg`、`--card`、`--accent`、`--info`、`--warn`、`--ok`、`--danger` 等），并通过高特异性选择器将大量硬编码的 `zinc-*` 工具类在亮模式下重映射到语义 token，从而在不重构组件的前提下实现亮色主题。
- `web/src/store/theme.ts` — Zustand store 持久化用户选择的强调色预设（品牌紫/玫粉/海蓝/青色/蓝绿/翡翠），通过写入 `document.documentElement.style.setProperty('--accent', ...)` 即时生效，无组件重渲染。
- `web/postcss.config.js` — PostCSS 插件链仅包含 `tailwindcss` 与 `autoprefixer`。
- `web/package.json` — 依赖锁定：`react 18`、`zustand 5`、`@xyflow/react`（拓扑图）、`recharts`（图表）、`xterm`（终端）、`lucide-react`（图标）。

## 架构与约定
- **主题开关**：通过在 `<html>` 根节点切换 `class="light"` / `class="dark"`（由 `darkMode: 'class'` 驱动），而非媒体查询自动检测。页面级 sticky header `.app-header` 通过 `backdrop-filter` + 语义 token 实现毛玻璃效果，并针对亮模式提高不透明度。
- **设计 Token 策略**：所有背景、文字、边框、强调色均通过 `rgb(var(--token) / <alpha-value>)` 形式消费，新增主题只需修改 `:root.<mode>` 下的 RGB 三元组，无需改动组件类名。
- **渐进迁移策略**：由于历史代码中大量直接使用 `bg-zinc-*` / `text-zinc-*` 等暗色偏好类，index.css 提供一套“亮模式 zinc 重映射”覆盖规则，按 `bg-zinc-900`、`divide-zinc-*`、`hover:`、`focus:`、半透明 `/20..80` 变体逐一映射到亮色 palette，避免一次性大规模重构。
- **强调色可定制**：`theme.ts` 预置 6 个从 Logo 渐变中提取的 accent 值，用户可在设置页切换，Zustand persist 到 `localStorage.ongrid.theme`，并在应用启动时同步注入 `--accent`，保证首屏无闪烁。
- **第三方库适配**：为 react-flow 的 Controls/MiniMap 提供暗/亮两套覆盖规则，使其与 zinc 画布保持一致；Markdown 渲染区 `.md-body` 内置代码块、表格、链接样式，并提供亮模式覆盖。
- **打印输出**：`@media print` 将报告区域强制转为白底黑字，保留强调色但加深对比度，同时防止卡片跨页断裂。

## 开发者应遵循的规则
1. **优先使用语义 token 类**：背景用 `bg-card`/`bg-bg`，文字用 `text-text`/`text-muted`/`text-faint`，强调用 `bg-accent`/`text-accent`/`border-accent`，而非直接写 `bg-zinc-900` 或 `text-zinc-100`。
2. **新增主题色只改 CSS 变量**：在 `:root.dark` 或 `:root.light` 下添加新的 `--xxx` 三元组，并在 `tailwind.config.ts` 的 `colors.extend` 中暴露对应 token，不要在组件里硬编码十六进制。
3. **亮模式兼容**：若必须使用 `zinc-*` 或某颜色的半透明变体，请在 index.css 的“亮模式重映射”区块补充对应选择器，确保 `html.light` 下可读性达到 AA 对比度。
4. **强调色预设**：如需新增强调色选项，在 `store/theme.ts` 的 `ACCENT_DEFS` 追加条目（含 id、zh/en label、RGB、hex），保持来自品牌渐变的约束。
5. **动画与交互**：使用 tailwind.config 中已定义的 `animate-pulse-dot` 以及 `anim-rise`/`anim-fade`/`anim-scale` 微动效，尊重 `prefers-reduced-motion`。
6. **聚焦态**：表单控件通过自身 border 变化表达 focus，全局 `*:focus-visible` 对输入框禁用 outline ring，按钮/链接保留软环提示。