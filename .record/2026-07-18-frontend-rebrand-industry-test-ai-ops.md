# 2026-07-18 前台品牌字替换为「行业测试AI运维平台」

## 背景
全平台所有用户可见的「Ongrid」品牌字、title、aria-label、文案全部替换为新平台名「行业测试AI运维平台」，涉及 Web 前台（登录页、侧边栏、设置、关于、ChatInput、主题色说明、告警 scope hint、规则预设等）。

## 改动清单（前台显示相关）
| 文件 | 行 | 改前 | 改后 |
| --- | --- | --- | --- |
| web/index.html | 14 | `<title>Ongrid</title>` | `<title>行业测试AI运维平台</title>` |
| web/src/components/OngridLogo.tsx | 15,19 | `title for screen readers (default "ongrid")` / `title = 'Ongrid'` | `title for screen readers (default "行业测试AI运维平台")` / `title = '行业测试AI运维平台'` |
| web/src/components/Sidebar.tsx | 62 | `tr('Ongrid 用户', 'Ongrid user')` | `tr('AI 运维用户', 'AI Ops user')` |
| web/src/components/Sidebar.tsx | 246 | `tr('Ongrid · 点击展开', 'Ongrid · click to expand')` | `tr('行业测试AI运维平台 · 点击展开', '行业测试AI运维平台 · click to expand')` |
| web/src/components/Sidebar.tsx | 345 | `tr('Ongrid 首页', 'Ongrid home')` | `tr('行业测试AI运维平台 首页', '行业测试AI运维平台 home')` |
| web/src/components/Sidebar.tsx | 349 | `<span ...>Ongrid</span>` (text-[16px]) | `<span ...>行业测试AI运维平台</span>` (text-[15px] + min-w-0 truncate) |
| web/src/components/ChatInput.tsx | 439 | `tr('为 Ongrid 添加技能', 'Add skills to Ongrid')` | `tr('为 行业测试AI运维平台 添加技能', 'Add skills to 行业测试AI运维平台')` |
| web/src/pages/Login.tsx | 65 | `tr('登录到 Ongrid', 'Sign in to Ongrid')` | `tr('登录到 行业测试AI运维平台', 'Sign in to 行业测试AI运维平台')` |
| web/src/pages/settings/About.tsx | 38 | `<h2 ...>Ongrid</h2>` | `<h2 ...>行业测试AI运维平台</h2>` |
| web/src/pages/settings/Preferences.tsx | 105-106 | `... 来源自 Ongrid logo 的两束渐变。` | `... 来源自行业测试AI运维平台 logo 的两束渐变。` |
| web/src/pages/EdgeDetail.tsx | 2340 | 描述文案「Ongrid 在 edge 上托管 exporter」 | 「AI 运维平台 在 edge 上托管 exporter」 |
| web/src/api/alerts.ts | 273,278 | `monitoring_pipeline` SCOPE_HINT 中英 Ongrid | 改为「AI 运维平台」/「AI Ops Platform」 |
| web/src/lib/rule_presets.ts | 79-80 | 告警规则 hint 中 Ongrid manager | 改为「AI 运维平台 manager」/「AI Ops Platform manager」 |
| web/src/pages/settings/Integrations.tsx | 516 | 「Ongrid 不代登录」 | 「AI 运维平台 不代登录」 |

## 设计取舍
- 顶部品牌字：原 Ongrid (6 字符) → 行业测试AI运维平台 (10 字)。字号 16px→15px，加 `min-w-0 truncate`，在 sidebar 64 宽下不溢出截断，亮色 / 暗色主题均保留。
- 用户兜底名：「Ongrid 用户」→「AI 运维用户」/「AI Ops user」，避免直接拼「行业测试AI运维平台 用户」读起来啰嗦，同时彻底消除用户面前的 ongrid 字样。
- 描述性文案：长句中的 Ongrid 改为简称「AI 运维平台」/「AI Ops Platform」，避免 10 字中文品牌名挤进句子造成阅读断裂。

## 不动的位置（说明）
- `OngridLogo` 组件名 / 文件名 / import 路径：React 技术命名，改名牵涉所有页面 import 与打包，保持不变。
- SVG `<linearGradient id="ongridLeftPillar/RightPillar">`：SVG defs 的 gradient id，纯技术 ID，不渲染文字。
- `web/public/ongrid-logo.svg` 文件名：静态资源路径，HTML 仅引用 `favicon.svg`，不动。
- `web/src/api/webshell.ts` `SUBPROTOCOL = 'ongrid.shell.v1'`、`ongrid_user_id` 字段：后端协议 / API 字段名，改了会断通讯。
- `web/src/pages/Logs.tsx` `FALLBACK_QUERY = '{ongrid_source=~".+"}'`：Loki label 是后端写入的 stream label，前端是消费者，不能改。
- `web/src/pages/IncidentDetail.tsx` Grafana datasource UID `ongrid-loki / ongrid-tempo`：Grafana 数据源注册名，后端配置。
- `web/src/pages/Plugins/install.tsx` placeholder 文件路径：文件系统实际路径。
- `web/src/pages/settings/Notifications.tsx` `X-Ongrid-Signature` HTTP header：HTTP 头名（webhook 签名），是协议字段。
- `web/src/pages/settings/Integrations.tsx` `<code>ongrid-prometheus</code>` / `<code>ongrid</code>`：Grafana 数据源 / 文件夹名，是后端资源。
- `web/src/lib/rule_presets.ts` `exprPreview: 'up{job="ongrid-manager"}'` 与 `draftBase.spec.expr`：PromQL label 是后端采集 job 标签。
- `web/src/pages/EdgeDetail.tsx` 等 `/etc/ongrid-edge/...` `/opt/ongrid` placeholder：edge 安装目录、Manager 安装目录，是服务器上的真实路径。
- `web/src/styles/index.css:12` 注释「this reads as 'Ongrid'」：颜色设计意图注释，无关前台显示。

## 验证
- 全量检索 `web/src` `web/index.html` 已无用户可见的 Ongrid 字样（剩余为组件名 / HTTP header / 文件路径 / 注释 / SVG id）。
- 本地执行 `cd web && npm run build` 验证编译通过。