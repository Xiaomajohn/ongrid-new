---
generated_by: repo-wiki-agent
baseline_commit: "47bad98d46a1d70231d237285784a8192d7756c7"
last_updated: "2026-07-06"
managed_sections:
  - "## Overview"
  - "## Packages (29)"
  - "## Conventions"
  - "## Sample Public Symbols"
---

# 共享包 (pkg)

<!-- BEGIN:REPO_WIKI_MANAGED -->
## Overview

`internal/pkg/` 存放各 BC 复用的基础库，遵循 AGENTS.md 约束：

- 不依赖任何业务包
- 通过构造函数注入，无全局可变变量
- 所有 IO 函数第一个参数为 `context.Context`

## Packages (29)

| 包 | 用途 |
|---|---|
| `auth` | 认证核心 |
| `authzmw` | 认证中间件 |
| `config` | 配置加载 |
| `credinject` | 凭证注入 |
| `dbx` | 数据库封装 |
| `docextract` | 文档抽取 |
| `embedding` | 向量 embedding |
| `errs` | 错误码 |
| `grafana` | Grafana 集成 |
| `httpserver` | HTTP server 工具 |
| `llm` | LLM 客户端 |
| `logger` | slog 封装 |
| `logquery` | 日志查询 |
| `mcpclient` | MCP 客户端 |
| `notify` | 通知 |
| `passwd` | 密码 |
| `prom` | Prometheus 指标 |
| `promauth` | Prometheus 认证 |
| `promquery` | Prometheus 查询 |
| `promwrite` | Prometheus 写入 |
| `qdrantx` | Qdrant 向量库 |
| `runner` | 任务执行 |
| `secretbox` | 密钥加密 |
| `tenantctx` | 多租户上下文 |
| `tracequery` | Trace 查询 |
| `tracing` | 链路追踪 |
| `tunnel` | 隧道 |
| `workspace` | 工作区 |
| `zhipuauth` | 智谱认证 |

## Conventions

- 错误用 `%w` 包装；不重复记录
- 共享状态必须加锁，测试带 `-race`
- 敏感字段禁止明文入日志
- 密码必须 bcrypt / argon2id

## Sample Public Symbols

### `auth/`

- `./internal/pkg/auth/jwt.go` L24: `type Claims`[^24]
- `./internal/pkg/auth/jwt.go` L33: `type Signer`[^33]
- `./internal/pkg/auth/jwt.go` L41: `func NewSigner`[^41]
- `./internal/pkg/auth/middleware.go` L21: `func Middleware`[^21]
- `./internal/pkg/auth/middleware.go` L58: `func extractBearer`[^58]

### `authzmw/`

- `./internal/pkg/authzmw/middleware.go` L38: `type Authorizer`[^38]
- `./internal/pkg/authzmw/middleware.go` L44: `type Middleware`[^44]
- `./internal/pkg/authzmw/middleware.go` L50: `func New`[^50]

### `config/`

- `./internal/pkg/config/config.go` L20: `type Config`[^20]
- `./internal/pkg/config/config.go` L57: `type SkillsConfig`[^57]
- `./internal/pkg/config/config.go` L70: `type LogsConfig`[^70]
- `./internal/pkg/config/config.go` L82: `type TracesConfig`[^82]
- `./internal/pkg/config/config.go` L94: `type GrafanaConfig`[^94]
- `./internal/pkg/config/config_test.go` L8: `func TestLoadDefaults`[^8]
- `./internal/pkg/config/config_test.go` L140: `func TestLoadPromOverrides`[^140]
- `./internal/pkg/config/config_test.go` L166: `func TestLoadEdgeCollectorOverrides`[^166]
- `./internal/pkg/config/config_test.go` L186: `func TestLoadNotificationOverrides`[^186]
- `./internal/pkg/config/config_test.go` L242: `func TestLoadAlertOverrides`[^242]

### `credinject/`

- `./internal/pkg/credinject/credinject.go` L22: `type FileSpec`[^22]
- `./internal/pkg/credinject/credinject.go` L29: `type FilePlan`[^29]
- `./internal/pkg/credinject/credinject.go` L36: `type Plan`[^36]
- `./internal/pkg/credinject/credinject.go` L47: `func Resolve`[^47]
- `./internal/pkg/credinject/credinject.go` L93: `func sortStrings`[^93]
- `./internal/pkg/credinject/credinject_test.go` L5: `func TestResolveEnvAndFiles`[^5]
- `./internal/pkg/credinject/credinject_test.go` L33: `func TestResolveBadMode`[^33]

### `dbx/`

- `./internal/pkg/dbx/dbx.go` L40: `func Open`[^40]
- `./internal/pkg/dbx/dbx.go` L53: `func openMySQL`[^53]
- `./internal/pkg/dbx/dbx.go` L85: `func openSQLite`[^85]
- `./internal/pkg/dbx/dbx.go` L115: `func buildSQLiteDSN`[^115]
- `./internal/pkg/dbx/dbx.go` L139: `func redactDSN`[^139]
- `./internal/pkg/dbx/dbx_test.go` L13: `func TestOpen_SQLiteInMemory`[^13]
- `./internal/pkg/dbx/dbx_test.go` L32: `func TestOpen_DefaultsToMySQL`[^32]
- `./internal/pkg/dbx/dbx_test.go` L51: `func TestOpen_UnsupportedDialect`[^51]
- `./internal/pkg/dbx/dbx_test.go` L64: `type fakeModel`[^64]
- `./internal/pkg/dbx/dbx_test.go` L71: `func fakeMigrator`[^71]


<!-- END:REPO_WIKI_MANAGED -->

## 引用
[^115]: internal/pkg/dbx/dbx.go L115–L120 — [internal/pkg/dbx/dbx.go#L115-L120](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/dbx/dbx.go#L115-L120)
[^139]: internal/pkg/dbx/dbx.go L139–L144 — [internal/pkg/dbx/dbx.go#L139-L144](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/dbx/dbx.go#L139-L144)
[^13]: internal/pkg/dbx/dbx_test.go L13–L18 — [internal/pkg/dbx/dbx_test.go#L13-L18](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/dbx/dbx_test.go#L13-L18)
[^140]: internal/pkg/config/config_test.go L140–L145 — [internal/pkg/config/config_test.go#L140-L145](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/config/config_test.go#L140-L145)
[^166]: internal/pkg/config/config_test.go L166–L171 — [internal/pkg/config/config_test.go#L166-L171](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/config/config_test.go#L166-L171)
[^186]: internal/pkg/config/config_test.go L186–L191 — [internal/pkg/config/config_test.go#L186-L191](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/config/config_test.go#L186-L191)
[^20]: internal/pkg/config/config.go L20–L25 — [internal/pkg/config/config.go#L20-L25](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/config/config.go#L20-L25)
[^21]: internal/pkg/auth/middleware.go L21–L26 — [internal/pkg/auth/middleware.go#L21-L26](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/auth/middleware.go#L21-L26)
[^22]: internal/pkg/credinject/credinject.go L22–L27 — [internal/pkg/credinject/credinject.go#L22-L27](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/credinject/credinject.go#L22-L27)
[^242]: internal/pkg/config/config_test.go L242–L247 — [internal/pkg/config/config_test.go#L242-L247](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/config/config_test.go#L242-L247)
[^24]: internal/pkg/auth/jwt.go L24–L29 — [internal/pkg/auth/jwt.go#L24-L29](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/auth/jwt.go#L24-L29)
[^29]: internal/pkg/credinject/credinject.go L29–L34 — [internal/pkg/credinject/credinject.go#L29-L34](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/credinject/credinject.go#L29-L34)
[^32]: internal/pkg/dbx/dbx_test.go L32–L37 — [internal/pkg/dbx/dbx_test.go#L32-L37](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/dbx/dbx_test.go#L32-L37)
[^33]: internal/pkg/auth/jwt.go L33–L38 — [internal/pkg/auth/jwt.go#L33-L38](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/auth/jwt.go#L33-L38)
[^33]: internal/pkg/credinject/credinject_test.go L33–L38 — [internal/pkg/credinject/credinject_test.go#L33-L38](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/credinject/credinject_test.go#L33-L38)
[^36]: internal/pkg/credinject/credinject.go L36–L41 — [internal/pkg/credinject/credinject.go#L36-L41](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/credinject/credinject.go#L36-L41)
[^38]: internal/pkg/authzmw/middleware.go L38–L43 — [internal/pkg/authzmw/middleware.go#L38-L43](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/authzmw/middleware.go#L38-L43)
[^40]: internal/pkg/dbx/dbx.go L40–L45 — [internal/pkg/dbx/dbx.go#L40-L45](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/dbx/dbx.go#L40-L45)
[^41]: internal/pkg/auth/jwt.go L41–L46 — [internal/pkg/auth/jwt.go#L41-L46](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/auth/jwt.go#L41-L46)
[^44]: internal/pkg/authzmw/middleware.go L44–L49 — [internal/pkg/authzmw/middleware.go#L44-L49](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/authzmw/middleware.go#L44-L49)
[^47]: internal/pkg/credinject/credinject.go L47–L52 — [internal/pkg/credinject/credinject.go#L47-L52](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/credinject/credinject.go#L47-L52)
[^50]: internal/pkg/authzmw/middleware.go L50–L55 — [internal/pkg/authzmw/middleware.go#L50-L55](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/authzmw/middleware.go#L50-L55)
[^51]: internal/pkg/dbx/dbx_test.go L51–L56 — [internal/pkg/dbx/dbx_test.go#L51-L56](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/dbx/dbx_test.go#L51-L56)
[^53]: internal/pkg/dbx/dbx.go L53–L58 — [internal/pkg/dbx/dbx.go#L53-L58](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/dbx/dbx.go#L53-L58)
[^57]: internal/pkg/config/config.go L57–L62 — [internal/pkg/config/config.go#L57-L62](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/config/config.go#L57-L62)
[^58]: internal/pkg/auth/middleware.go L58–L63 — [internal/pkg/auth/middleware.go#L58-L63](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/auth/middleware.go#L58-L63)
[^5]: internal/pkg/credinject/credinject_test.go L5–L10 — [internal/pkg/credinject/credinject_test.go#L5-L10](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/credinject/credinject_test.go#L5-L10)
[^64]: internal/pkg/dbx/dbx_test.go L64–L69 — [internal/pkg/dbx/dbx_test.go#L64-L69](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/dbx/dbx_test.go#L64-L69)
[^70]: internal/pkg/config/config.go L70–L75 — [internal/pkg/config/config.go#L70-L75](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/config/config.go#L70-L75)
[^71]: internal/pkg/dbx/dbx_test.go L71–L76 — [internal/pkg/dbx/dbx_test.go#L71-L76](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/dbx/dbx_test.go#L71-L76)
[^82]: internal/pkg/config/config.go L82–L87 — [internal/pkg/config/config.go#L82-L87](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/config/config.go#L82-L87)
[^85]: internal/pkg/dbx/dbx.go L85–L90 — [internal/pkg/dbx/dbx.go#L85-L90](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/dbx/dbx.go#L85-L90)
[^8]: internal/pkg/config/config_test.go L8–L13 — [internal/pkg/config/config_test.go#L8-L13](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/config/config_test.go#L8-L13)
[^93]: internal/pkg/credinject/credinject.go L93–L98 — [internal/pkg/credinject/credinject.go#L93-L98](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/credinject/credinject.go#L93-L98)
[^94]: internal/pkg/config/config.go L94–L99 — [internal/pkg/config/config.go#L94-L99](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/pkg/config/config.go#L94-L99)