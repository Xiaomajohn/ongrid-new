---
generated_by: repo-wiki-agent
baseline_commit: "47bad98d46a1d70231d237285784a8192d7756c7"
last_updated: "2026-07-06"
managed_sections:
  - "## Overview"
  - "## Plugins (9)"
  - "## Entrypoint Symbols"
  - "## Plugin Symbols (示例)"
  - "## Core Sub-Directories"
  - "## Plugin Sample"
---

# Edge Agent

<!-- BEGIN:REPO_WIKI_MANAGED -->
## Overview

Edge Agent 入口位于 `cmd/ongrid-edge/main.go`，实现位于 `internal/edgeagent/`，按能力划分为多个插件。

## Plugins (9)

| 插件 | 路径 | 用途 |
|---|---|---|
| `audit` | `internal/edgeagent/plugins/audit/` | 审计日志 |
| `custommetrics` | `internal/edgeagent/plugins/custommetrics/` | 自定义指标采集 |
| `databasemetrics` | `internal/edgeagent/plugins/databasemetrics/` | 数据库指标（MySQL/PG/Mongo/Redis） |
| `hostmetrics` | `internal/edgeagent/plugins/hostmetrics/` | 主机指标（CPU/内存/磁盘/网络） |
| `logs` | `internal/edgeagent/plugins/logs/` | 日志采集 |
| `metrics` | `internal/edgeagent/plugins/metrics/` | 指标入口 |
| `metricscommon` | `internal/edgeagent/plugins/metricscommon/` | 指标公共工具 |
| `procmetrics` | `internal/edgeagent/plugins/procmetrics/` | 进程指标 |
| `traces` | `internal/edgeagent/plugins/traces/` | 链路追踪 |

## Entrypoint Symbols

- `cmd/ongrid-edge/main.go` L56: `func main`[^56]
- `cmd/ongrid-edge/main.go` L332: `func buildCollector`[^332]
- `cmd/ongrid-edge/main.go` L379: `func envOr`[^379]
- `cmd/ongrid-edge/main.go` L391: `type collectorAdapter`[^391]

## Plugin Symbols (示例)

### `audit/`

- `./internal/edgeagent/plugins/audit/plugin.go` L46: `func New`[^46]
- `./internal/edgeagent/plugins/audit/plugin.go` L81: `func OutputPath`[^81]
- `./internal/edgeagent/plugins/audit/render.go` L130: `func render`[^130]
- `./internal/edgeagent/plugins/audit/render.go` L160: `type templateData`[^160]
- `./internal/edgeagent/plugins/audit/render.go` L180: `func buildTemplateData`[^180]
- `./internal/edgeagent/plugins/audit/render.go` L248: `func auditWorkDirExclude`[^248]

### `custommetrics/`

- `./internal/edgeagent/plugins/custommetrics/plugin.go` L25: `type Pusher`[^25]
- `./internal/edgeagent/plugins/custommetrics/plugin.go` L29: `type EdgeIDProvider`[^29]
- `./internal/edgeagent/plugins/custommetrics/plugin.go` L31: `type Plugin`[^31]
- `./internal/edgeagent/plugins/custommetrics/plugin.go` L45: `func New`[^45]
- `./internal/edgeagent/plugins/custommetrics/plugin_test.go` L16: `type fakePusher`[^16]
- `./internal/edgeagent/plugins/custommetrics/plugin_test.go` L49: `func TestCustomMetricsPushesSyntheticUpSamples`[^49]
- `./internal/edgeagent/plugins/custommetrics/plugin_test.go` L93: `func TestCustomMetricsRunClosesStartScopedStoppedChannel`[^93]
- `./internal/edgeagent/plugins/custommetrics/plugin_test.go` L113: `func TestCustomMetricsTargetsAreIsolated`[^113]

### `databasemetrics/`

- `./internal/edgeagent/plugins/databasemetrics/plugin.go` L31: `type Pusher`[^31]
- `./internal/edgeagent/plugins/databasemetrics/plugin.go` L35: `type EdgeIDProvider`[^35]
- `./internal/edgeagent/plugins/databasemetrics/plugin.go` L37: `type Plugin`[^37]
- `./internal/edgeagent/plugins/databasemetrics/plugin.go` L53: `func New`[^53]
- `./internal/edgeagent/plugins/databasemetrics/plugin_test.go` L17: `type fakeDatabasePusher`[^17]
- `./internal/edgeagent/plugins/databasemetrics/plugin_test.go` L50: `func TestDatabaseMetricsPushesSyntheticUpSamples`[^50]
- `./internal/edgeagent/plugins/databasemetrics/plugin_test.go` L97: `func TestDatabaseMetricsPushesSyntheticUpWhenExporterCannotStart`[^97]
- `./internal/edgeagent/plugins/databasemetrics/plugin_test.go` L137: `func TestDatabaseMetricsRunClosesStartScopedStoppedChannel`[^137]

### `hostmetrics/`

- `./internal/edgeagent/plugins/hostmetrics/plugin.go` L62: `func New`[^62]
- `./internal/edgeagent/plugins/hostmetrics/plugin.go` L97: `type plugin`[^97]
- `./internal/edgeagent/plugins/hostmetrics/plugin.go` L233: `func buildArgs`[^233]
- `./internal/edgeagent/plugins/hostmetrics/plugin.go` L249: `func stringSpec`[^249]

### `logs/`

- `./internal/edgeagent/plugins/logs/plugin.go` L27: `func New`[^27]
- `./internal/edgeagent/plugins/logs/render.go` L109: `func render`[^109]
- `./internal/edgeagent/plugins/logs/render.go` L193: `func stringSlice`[^193]
- `./internal/edgeagent/plugins/logs/render.go` L213: `func stringMap`[^213]
- `./internal/edgeagent/plugins/logs/render.go` L235: `func joinRegex`[^235]


## Core Sub-Directories

`internal/edgeagent/` 下子目录：

- `internal/edgeagent/biz/` — 8 文件含公开符号
- `internal/edgeagent/cmdpolicy/` — 7 文件含公开符号
- `internal/edgeagent/collector/` — 10 文件含公开符号
- `internal/edgeagent/host_files/` — 6 文件含公开符号
- `internal/edgeagent/model/` — 1 文件含公开符号
- `internal/edgeagent/plugins/` — 32 文件含公开符号
- `internal/edgeagent/restart_service/` — 2 文件含公开符号
- `internal/edgeagent/service/` — 1 文件含公开符号
- `internal/edgeagent/skill/` — 1 文件含公开符号
- `internal/edgeagent/webshell/` — 1 文件含公开符号

## Plugin Sample

以 `bash/` 插件为例（受 `deploy/edge/bash-policy.example.yaml` 约束）：

- 入口：`internal/edgeagent/bash/`
- 策略：`deploy/edge/bash-policy.example.yaml`

<!-- END:REPO_WIKI_MANAGED -->

## 引用
[^16]: internal/edgeagent/plugins/custommetrics/plugin_test.go L16–L21 — [internal/edgeagent/plugins/custommetrics/plugin_test.go#L16-L21](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/custommetrics/plugin_test.go#L16-L21)
[^17]: internal/edgeagent/plugins/databasemetrics/plugin_test.go L17–L22 — [internal/edgeagent/plugins/databasemetrics/plugin_test.go#L17-L22](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/databasemetrics/plugin_test.go#L17-L22)
[^25]: internal/edgeagent/plugins/custommetrics/plugin.go L25–L30 — [internal/edgeagent/plugins/custommetrics/plugin.go#L25-L30](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/custommetrics/plugin.go#L25-L30)
[^27]: internal/edgeagent/plugins/logs/plugin.go L27–L32 — [internal/edgeagent/plugins/logs/plugin.go#L27-L32](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/logs/plugin.go#L27-L32)
[^29]: internal/edgeagent/plugins/custommetrics/plugin.go L29–L34 — [internal/edgeagent/plugins/custommetrics/plugin.go#L29-L34](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/custommetrics/plugin.go#L29-L34)
[^31]: internal/edgeagent/plugins/custommetrics/plugin.go L31–L36 — [internal/edgeagent/plugins/custommetrics/plugin.go#L31-L36](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/custommetrics/plugin.go#L31-L36)
[^31]: internal/edgeagent/plugins/databasemetrics/plugin.go L31–L36 — [internal/edgeagent/plugins/databasemetrics/plugin.go#L31-L36](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/databasemetrics/plugin.go#L31-L36)
[^35]: internal/edgeagent/plugins/databasemetrics/plugin.go L35–L40 — [internal/edgeagent/plugins/databasemetrics/plugin.go#L35-L40](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/databasemetrics/plugin.go#L35-L40)
[^37]: internal/edgeagent/plugins/databasemetrics/plugin.go L37–L42 — [internal/edgeagent/plugins/databasemetrics/plugin.go#L37-L42](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/databasemetrics/plugin.go#L37-L42)
[^45]: internal/edgeagent/plugins/custommetrics/plugin.go L45–L50 — [internal/edgeagent/plugins/custommetrics/plugin.go#L45-L50](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/custommetrics/plugin.go#L45-L50)
[^46]: internal/edgeagent/plugins/audit/plugin.go L46–L51 — [internal/edgeagent/plugins/audit/plugin.go#L46-L51](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/audit/plugin.go#L46-L51)
[^49]: internal/edgeagent/plugins/custommetrics/plugin_test.go L49–L54 — [internal/edgeagent/plugins/custommetrics/plugin_test.go#L49-L54](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/custommetrics/plugin_test.go#L49-L54)
[^50]: internal/edgeagent/plugins/databasemetrics/plugin_test.go L50–L55 — [internal/edgeagent/plugins/databasemetrics/plugin_test.go#L50-L55](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/databasemetrics/plugin_test.go#L50-L55)
[^53]: internal/edgeagent/plugins/databasemetrics/plugin.go L53–L58 — [internal/edgeagent/plugins/databasemetrics/plugin.go#L53-L58](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/databasemetrics/plugin.go#L53-L58)
[^56]: cmd/ongrid-edge/main.go L56–L61 — [cmd/ongrid-edge/main.go#L56-L61](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/cmd/ongrid-edge/main.go#L56-L61)
[^62]: internal/edgeagent/plugins/hostmetrics/plugin.go L62–L67 — [internal/edgeagent/plugins/hostmetrics/plugin.go#L62-L67](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/hostmetrics/plugin.go#L62-L67)
[^81]: internal/edgeagent/plugins/audit/plugin.go L81–L86 — [internal/edgeagent/plugins/audit/plugin.go#L81-L86](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/audit/plugin.go#L81-L86)
[^93]: internal/edgeagent/plugins/custommetrics/plugin_test.go L93–L98 — [internal/edgeagent/plugins/custommetrics/plugin_test.go#L93-L98](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/custommetrics/plugin_test.go#L93-L98)
[^97]: internal/edgeagent/plugins/hostmetrics/plugin.go L97–L102 — [internal/edgeagent/plugins/hostmetrics/plugin.go#L97-L102](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/hostmetrics/plugin.go#L97-L102)
[^97]: internal/edgeagent/plugins/databasemetrics/plugin_test.go L97–L102 — [internal/edgeagent/plugins/databasemetrics/plugin_test.go#L97-L102](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/databasemetrics/plugin_test.go#L97-L102)
[^109]: internal/edgeagent/plugins/logs/render.go L109–L114 — [internal/edgeagent/plugins/logs/render.go#L109-L114](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/logs/render.go#L109-L114)
[^113]: internal/edgeagent/plugins/custommetrics/plugin_test.go L113–L118 — [internal/edgeagent/plugins/custommetrics/plugin_test.go#L113-L118](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/custommetrics/plugin_test.go#L113-L118)
[^130]: internal/edgeagent/plugins/audit/render.go L130–L135 — [internal/edgeagent/plugins/audit/render.go#L130-L135](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/audit/render.go#L130-L135)
[^137]: internal/edgeagent/plugins/databasemetrics/plugin_test.go L137–L142 — [internal/edgeagent/plugins/databasemetrics/plugin_test.go#L137-L142](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/databasemetrics/plugin_test.go#L137-L142)
[^160]: internal/edgeagent/plugins/audit/render.go L160–L165 — [internal/edgeagent/plugins/audit/render.go#L160-L165](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/audit/render.go#L160-L165)
[^180]: internal/edgeagent/plugins/audit/render.go L180–L185 — [internal/edgeagent/plugins/audit/render.go#L180-L185](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/audit/render.go#L180-L185)
[^193]: internal/edgeagent/plugins/logs/render.go L193–L198 — [internal/edgeagent/plugins/logs/render.go#L193-L198](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/logs/render.go#L193-L198)
[^213]: internal/edgeagent/plugins/logs/render.go L213–L218 — [internal/edgeagent/plugins/logs/render.go#L213-L218](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/logs/render.go#L213-L218)
[^233]: internal/edgeagent/plugins/hostmetrics/plugin.go L233–L238 — [internal/edgeagent/plugins/hostmetrics/plugin.go#L233-L238](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/hostmetrics/plugin.go#L233-L238)
[^235]: internal/edgeagent/plugins/logs/render.go L235–L240 — [internal/edgeagent/plugins/logs/render.go#L235-L240](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/logs/render.go#L235-L240)
[^248]: internal/edgeagent/plugins/audit/render.go L248–L253 — [internal/edgeagent/plugins/audit/render.go#L248-L253](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/audit/render.go#L248-L253)
[^249]: internal/edgeagent/plugins/hostmetrics/plugin.go L249–L254 — [internal/edgeagent/plugins/hostmetrics/plugin.go#L249-L254](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/internal/edgeagent/plugins/hostmetrics/plugin.go#L249-L254)
[^332]: cmd/ongrid-edge/main.go L332–L337 — [cmd/ongrid-edge/main.go#L332-L337](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/cmd/ongrid-edge/main.go#L332-L337)
[^379]: cmd/ongrid-edge/main.go L379–L384 — [cmd/ongrid-edge/main.go#L379-L384](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/cmd/ongrid-edge/main.go#L379-L384)
[^391]: cmd/ongrid-edge/main.go L391–L396 — [cmd/ongrid-edge/main.go#L391-L396](https://github.com/Xiaomajohn/ongrid-new/blob/47bad98d46a1d70231d237285784a8192d7756c7/cmd/ongrid-edge/main.go#L391-L396)