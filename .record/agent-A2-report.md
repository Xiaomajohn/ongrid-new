# 子 agent A2 报告 — PluginHost / Manifest 解析

> 任务 T2:在 `internal/pluginhost/manifest/` 下新增 3 个文件(types / format_detect / loader),
> 提供 plugin 清单的统一数据结构、4 种 format 的自动识别、启动时目录扫描与解析。
> 编译状态:`go build ./internal/pluginhost/manifest/...` 通过。

---

## 1. 文件清单

| 文件 | 行数 | 职责 |
|---|---|---|
| `internal/pluginhost/manifest/types.go` | 104 | `Format` / `Kind` 常量;`PluginManifest`、`Capability`、`Permissions`、`LoadResult` 结构体 |
| `internal/pluginhost/manifest/format_detect.go` | 159 | `DetectFormat(root)` 按优先级识别 4 种 manifest format;`ManifestFileName(fmt)` 反查文件名 |
| `internal/pluginhost/manifest/loader.go` | 239 | `LoadDirs(ctx, cfg, val)` 递归扫目录并解析;`Validator` 接口(供 Phase 2 sandbox 实现);`parseManifestFile` |

均位于 `package manifest`,**只 import 标准库**(`context`、`encoding/json`、`errors`、`fmt`、`io/fs`、`os`、`path/filepath`),无任何 A 子包依赖。

---

## 2. 4 种 Format 识别决策表

| 优先级 | Format 常量 | 字符串值 | 触发的 manifest 文件 | `ManifestFileName` 返回 | 备注 |
|---|---|---|---|---|---|
| 1 (最优先) | `FormatPluginHost` | `"pluginhost"` | `<root>/plugin.json`(读其 `format` 字段;空默认 pluginhost;显式 4 种之一则尊重) | `plugin.json` | 主推 |
| 2 | `FormatClaude` | `"claude"` | `<root>/.claude-plugin/plugin.json` | `.claude-plugin/plugin.json` | 向后兼容 |
| 3 | `FormatOpenclaw` | `"openclaw"` | `<root>/openclaw.plugin.json` | `openclaw.plugin.json` | 第三方 |
| 4 | `FormatBareSkills` | `"bare_skills"` | `<root>/skills/<name>/SKILL.md`(任一存在) | `""` (空,无集中清单) | 无 manifest 解析,仅记路径 |
| 5 兜底 | — | — | 上述都不存在 | — | 返 `ErrUnknownManifest` |

**剪枝策略**:`DetectFormat` 内对前 3 种格式先做 `os.Stat` 命中即返回;`bare_skills` 走 `os.ReadDir(skills/)` 单层目录扫。无 stat 权限错误会被包装并向上抛(非 `ErrNotExist` 时)。

**`peekFormatField`** 只用做 plugin.json 存在时的 `format` 字段探查,JSON 格式错误时回退到 `FormatPluginHost`,错误细节留给后续 `parseManifestFile` 写进 `LoadResult.Warnings`。

---

## 3. PluginManifest 字段清单

> 标注:**必填** = 任意 format 下 loader 解析后会强制要求非空;**选填** = 可省。
> `class` 字段在所有 capability 中默认为 `"safe"`,运行时由 Phase 3 强制校验。

| 字段 | JSON tag | 类型 | 必填? | 含义 / Phase 消费方 |
|---|---|---|---|---|
| `ID` | `id` | `string` | **必填** | plugin 唯一 ID,Phase 2 DB 主键 |
| `Name` | `name` | `string` | 选填 | 展示名;Phase 3 前端 fallback |
| `Version` | `version` | `string` | 选填 | 语义版本,Phase 4 升级策略使用 |
| `Description` | `description,omitempty` | `string` | 选填 | 列表页简介 |
| `Format` | `format,omitempty` | `Format` | 选填(空 → 检测结果) | 解析时被 loader 用 `DetectFormat` 补齐 |
| `Entry` | `entry,omitempty` | `string` | 选填 | 子进程入口相对路径;存在时由 loader 调 `Validator.PathSafeUnderRoot` |
| `Transport` | `transport,omitempty` | `string` | 选填 | `subprocess` / `http` / `inproc`;Phase 3 runtime 选型 |
| `Capabilities` | `capabilities` | `[]Capability` | 选填(但建议非空) | 7 类能力清单,Phase 3 adapter 分发的输入 |
| `Permissions` | `permissions,omitempty` | `Permissions` | 选填 | 副作用声明,Phase 2 sandbox/permission 校验 |
| `Signature` | `signature,omitempty` | `string` | 选填 | 离线签名;Phase 4 接入 |
| `UIMetadata` | `ui_metadata,omitempty` | `map[string]any` | 选填 | 前端表单渲染依据 |
| `HostDependencies` | `host_dependencies,omitempty` | `[]string` | 选填 | 反向 hostcall 列表(例 `["prom","loki","skill","llm","edge.execute_cmd"]`);Phase 3 hostcall/scope 强校验 |
| `DataScopes` | `data_scopes,omitempty` | `map[string]json.RawMessage` | 选填 | 各 host call 的可见范围;Phase 3 hostcall/scope 落地到具体 client |

### Capability 字段清单

| 字段 | JSON tag | 必填? | 取值 / 默认 |
|---|---|---|---|
| `Kind` | `kind` | **必填** | 7 类 `Kind` 常量之一 |
| `Name` | `name` | **必填** | plugin 内唯一;被 Invoke 路由 |
| `Class` | `class,omitempty` | 选填 | `safe` / `mutating` / `dangerous`,默认 `safe` |
| `Schema` | `schema,omitempty` | 选填 | JSON Schema 串 |
| `Metadata` | `metadata,omitempty` | 选填 | adapter 扩展用 |

### Permissions 字段清单

| 字段 | JSON tag | 含义 |
|---|---|---|
| `NetworkEgress` | `network_egress,omitempty` | 允许出网地址(如 `gitlab.com:443`) |
| `FSWrite` | `fs_write,omitempty` | 允许写入路径(相对 plugin 根) |
| `EnvAccess` | `env_access,omitempty` | 允许读取的环境变量名 |

### LoadResult 字段清单

| 字段 | 类型 | 含义 |
|---|---|---|
| `Manifest` | `*PluginManifest` | 解析成功时填,失败或 bare_skills 时为 nil |
| `Path` | `string` | 该结果对应的 plugin 根目录 |
| `Warnings` | `[]string` | 解析/校验/权限期间的非致命错误,不中断整体加载 |

### Kind 7 类常量

| 常量 | 字符串值 | 对应 adapter(Phase 3) |
|---|---|---|
| `KindAITool` | `ai.tool` | `adapter/skill_adapter.go` |
| `KindNotifier` | `notifier` | `adapter/notify_adapter.go` |
| `KindWorkflowNode` | `workflow.node` | `adapter/flow_adapter.go` |
| `KindSkillRunner` | `skill.runner` | `adapter/skill_adapter.go` |
| `KindLLMProvider` | `llm.provider` | `adapter/llm_adapter.go` |
| `KindEmbedProvider` | `embedding.provider` | `adapter/embedding_adapter.go` |
| `KindEvaluator` | `alert.evaluator` | `adapter/evaluator_adapter.go` |

---

## 4. 已实现的 Loader 行为要点

1. **默认目录**:`cfg.Dirs` 为空时回落到 `["/etc/ongrid/plugins"]`。
2. **ctx 兜底**:`LoadDirs` 在外层 for 循环和 `walkCandidates` 中各做一次 `ctx.Err()` 检查,提前返回 `ctx.Err()`。
3. **非致命错误**:目录不存在 / 不是目录 / walk 权限失败 / json 解析失败 / 沙箱校验拒绝 — 一律追加到对应 `LoadResult.Warnings`,**不中断**整体流程。
4. **scan 策略**:`walkCandidates` 先检查根目录本身,再递归到子目录;遇到命中 manifest 的目录会用 `fs.SkipDir` 避免扫到嵌套 plugin(防止 a plugin 内部携带另一个 plugin)。
5. **format 补齐**:`parseManifestFile` 在 `m.Format == ""` 时写入检测到的 fmt,使下游不必再自行推断。
6. **entry 沙箱校验**:只在 `manifest.Entry != ""` 且 `val != nil` 时调用 `Validator.PathSafeUnderRoot`;校验失败写 warning,**不**把整个 manifest 丢掉,以便上层决策。
7. **`AllowTarball` / `EnableHTTP` / `HTTPAllow`**:目前只保字段、留 TODO(见下);非核心,不在本任务里展开。

---

## 5. 已知 TODO(留给后续 Phase)

### Phase 2 — sandbox 集成

- [ ] `internal/pluginhost/sandbox/path.go` 提供 `Validator.PathSafeUnderRoot(p, root string) error` 的具体实现
  (`EvalSymlinks` + `pathHasPrefix` 双向校验)。
- [ ] loader 接入 `Manifest sha256` 计算,落到 `plugin_instances.ManifestSHA256`。
- [ ] 接入 `sandbox/permission.ValidateNetwork / ValidateFSWrite / ValidateEnv`,
  在 `parseManifestFile` 之后批量校验;失败信息并入 `LoadResult.Warnings`。
- [ ] `LoadDirsConfig.AllowTarball`:启动时遍历 tarball 列表、解压到临时目录并沿用 `LoadDirs`;
  当前无 tar 处理逻辑,需新增 `internal/pluginhost/manifest/tarball.go`。
- [ ] `cfg.EnableHTTP == true` 时跳过 host_dependencies 中 `http` 相关的告警(目前不消费此字段)。

### Phase 3 — adapter 字段消费

- [ ] `Manifest.Format` 分流:
  - `FormatBareSkills` 时由 adapter 直接遍历 `skills/<name>/SKILL.md`,反推 capability 列表(目前
    `loadOne` 已对 bare_skills 返回 nil manifest,需在 biz 层补齐"自构造 manifest"逻辑)。
  - 其他 3 种 format 已具备完整 manifest 数据,可直接走 `LoadResult.Manifest.Capabilities`。
- [ ] `Capability.Class` 在 Phase 3 的 adapter wire-up 时拦截:`mutating` / `dangerous` 触发
  `internal/manager/data/approval` 审批流(尚未存在)。
- [ ] `HostDependencies` + `DataScopes` 在 hostcall/scope 上:
  - 列出未声明的 hostcall 时返 `host_call_undeclared`;
  - scope 强校验直接落到 `prom_allowlist`、`loki_label_selectors` 等字段。
- [ ] `UIMetadata` 作为 `plugin_capabilities.UIMetadataJSON` 入库(尚未建表)。
- [ ] `Transport` 在 registry 按值分发(`subprocess` / `http` / `inproc`),空时默认 `subprocess`。
- [ ] `LoadDirsConfig.EnableHTTP` / `HTTPAllow` 在 `Transport == "http"` 的 install 路径生效(当前不消费)。

### 其他

- [ ] `Signature` 字段:Phase 4 接入离线签名校验 + 信任链配置。
- [ ] `parseManifestFile` 当前不做 `JSON Schema` 自身校验,Phase 2 之后上 `validator.Validate(m)`。
- [ ] `DetectFormat.peekFormatField` 的 `format` 字段值若不在 4 种白名单内,静默回退到
  `FormatPluginHost`,无 warning。Phase 3 之前可视情况补一条 `LoadResult.Warnings`。

---

## 6. 红线自检

- [x] 仅新增 3 个文件,目录 `internal/pluginhost/manifest/`,未碰 A 老代码。
- [x] 全部 import 来自标准库(白名单:context / encoding/json / errors / fmt / io/fs / os / path/filepath)。
- [x] 没有 import 任何 internal/manager/* / internal/iam/* / internal/edgeagent/* / api/* / internal/skill/* / internal/pkg/*。
- [x] 没有写其它 pluginhost 子包文件(pluginhost.go / deps.go / sandbox / registry / runtime / invoke / adapter / biz / server / model / data / hostcall)。
- [x] 没有写测试文件。
- [x] `go build ./internal/pluginhost/manifest/...` 通过(零 warning,零 error)。
