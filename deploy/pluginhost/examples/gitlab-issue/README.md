# gitlab-issue demo plugin

> **这是什么**:ongrid `pluginhost` 系统的最小示范 plugin,展示如何写一个 `pluginhost` 格式的第三方插件,把"创建 GitLab 工单"作为 `ai.tool` 能力注册到 ongrid 的 AI 工具生态里。
>
> **状态**:**Phase 6 F 完整可执行版本**(本任务 P1 阶段的可工作样例,真实 GitLab API 调用留到 P2)。
>
> **演示场景**:在 192.168.25.56 上 install → ongrid 启动时 LoadDirs 扫到 → pluginhost 拉起 `bin/gitlab-issue` 子进程 → web 端 `/plugins/:id` 详情页用 pluginLoader 加载 `web/plugin.js`(Web Component `<gitlab-issue-form>`)→ 用户填表提交 → 走到 server 端 SubprocessRuntime 跑一次 invoke,真实回写 `plugin_invocations` 表 + audit row。

---

## 1. 这个 demo 做什么

C plugin(本目录)是一个 **`ai.tool` 类**插件,提供 1 个能力 `create_issue`:

- **输入**:`project_id` / `title` / `body`
- **输出**:伪 GitLab 工单 `issue_url` / `issue_iid`(走 demo 的 `stub: true` 占位,**不真调** `gitlab.com`)
- **调用方**:在 `/plugins/:id/invoke` 页面触发(也可被 ongrid 的 Coordinator Agent / 任何 `skill.Executor` 调用方调用)
- **前端**:`web/plugin.js` 注册 `<gitlab-issue-form>` Web Component,带 Project ID / Title / Body 三件套输入 + Shadow DOM 隔离 CSS

> 用户层面看不出区别;管理员在 "插件" 页面启用它,AI agent 就能调 `create_issue` 走 demo stub 创建"已创建"工单。

---

## 2. 目录结构

```
gitlab-issue/
├── README.md                     # 本文件
├── CHANGELOG.md                  # 版本历史
├── go.mod                        # plugin 自身的 Go module(无第三方依赖,只用 stdlib)
├── plugin.json                   # ★ pluginhost manifest(Plan §7)
├── .compiled-manifest.json       # ★ 编译产物(plan §7 / §8)
├── cmd/
│   └── gitlab-issue/
│       └── main.go               # ★ plugin subprocess 入口(Go 实现)
├── bin/
│   └── gitlab-issue              # 构建产物:Dockerfile / `go build ./cmd/gitlab-issue` 生成
├── web/
│   └── plugin.js                 # ★ Web Component(<gitlab-issue-form>)
└── Dockerfile                    # ★ 多阶段构建(Go 编译 + 运行时)
```

> pluginhost loader 在启动期扫 `.compiled-manifest.json`(Plan §7.5 主路径),读 `entry` 字段找到 `./bin/gitlab-issue`,作为 subprocess 拉起,走 stdio JSON-RPC 协议(详见下文 §5)。

---

## 3. plugin.json 字段解释

```json
{
  "id":          "gitlab-issue",                                          // pluginhost 内的唯一 ID,A DB plugin_instances.pack_id
  "name":        "GitLab Issue",                                          // 人类可读名
  "version":     "0.1.0",                                                  // semver
  "format":      "pluginhost",                                              // 固定值,表示这是 pluginhost 原生格式(不是 .claude-plugin / openclaw)
  "entry":       "./bin/gitlab-issue",                                       // subprocess 入口相对 plugin 根
  "transport":   "subprocess",                                              // 走 stdio JSON-RPC(subprocess / http / inproc 三选一)
  "description": "Demo plugin: create GitLab issues from ongrid alerts",  // 给前端列表页用

  "capabilities": [{ ... }],                                                // 1+ 能力(本 demo 1 个 ai.tool)
  "permissions":     { ... },                                                // 网络出网 / FS 写 / 环境变量白名单
  "host_dependencies": [ ... ],                                             // 本插件反向调 A 能力的清单(host.call RBAC)
  "data_scopes":      { ... },                                              // 每个 host_call 的细粒度权限(plan §6.6.2)
  "ui_metadata":      { ... },                                              // 前端插件卡片渲染
  "frontend":         { ... }                                                // plugin 前端 Web Component 描述(Phase 5 E3/E4 落地)
}
```

完整字段定义见 plan §7 / `internal/pluginhost/manifest/types.go`。

---

## 4. 能力清单 — `capabilities[0]`

```json
{
  "kind":   "ai.tool",
  "name":   "create_issue",             // plugin 内唯一
  "class":  "mutating",                  // safe / mutating / dangerous 三档
  "schema": {                              // JSON Schema,供前端表单 + LLM tool schema 同时消费
    "type":       "object",
    "properties": {
      "project_id": { "type": "string", "description": "GitLab project ID" },
      "title":      { "type": "string" },
      "body":       { "type": "string" }
    },
    "required": ["project_id", "title"]
  }
}
```

pluginhost 启动时,`adapter/skill_adapter.go` 把这个 `ai.tool` capability 包成 A 的 `skill.Executor` 接口,注册到 A 的 `skill.Registry.Register`。

> **关键命名约束**:`ai.tool` 类的 `name` 必须满足 skill key 约束 `[a-z0-9_]+`;`create_issue` 合规,`createIssue` 不合规。

---

## 5. 传输协议 — stdio JSON-RPC(B → C)

`transport: "subprocess"` 表示 B 拉起 `./bin/gitlab-issue` 作为子进程,通过 **一行一帧 `\n` 分隔的 UTF-8 JSON** 与之通信。

### 5.1 请求帧(B → C,写到 stdin)

```json
{ "id": "uuid-or-trace-id", "cap": "create_issue", "params": { "project_id": "42", "title": "CPU 高负载", "body": "..." } }
```

- `id`:B 生成的 UUID / trace ID,响应帧必须匹配
- `cap`:对应 `capabilities[].name`
- `params`:`json.RawMessage` 透传,plugin 自解析

### 5.2 响应帧(C → B,从 stdout 读)

```json
{ "id": "uuid-or-trace-id", "result": { "issue_url": "https://gitlab.example.com/x/y/issues/1", "issue_iid": 1, "stub": true } }
// 失败:
{ "id": "uuid-or-trace-id", "error": "project_id is required" }
```

完整协议定义见 `internal/pluginhost/runtime/subprocess.go`。

### 5.3 自测

本 plugin 提供 `--self-test` 模式,直接跑一遍 envelope in/out 不依赖任何 host:

```bash
$ go run ./cmd/gitlab-issue --self-test
self-test OK
```

stdin/stdout 端到端验证:

```bash
$ go build -o /tmp/gitlab-issue ./cmd/gitlab-issue
$ echo '{"id":"req-99","cap":"create_issue","params":{"project_id":"42","title":"demo","body":"hi"}}' \
    | /tmp/gitlab-issue
{"id":"req-99","result":{"issue_url":"https://gitlab.example.com/demo/project/-/issues/297","issue_iid":297,"project_id":"42","title":"demo","body":"hi","created_at":"...","stub":true}}
```

错误路径:

```bash
$ echo '{"id":"req-100","cap":"create_issue","params":{"project_id":"","title":""}}' | /tmp/gitlab-issue
{"id":"req-100","error":"project_id is required"}
```

### 5.4 反向调用 A — host.call envelope

C 也可以反向调 A 能力,envelope 多一字段 `host.call`:

```json
// C → B
{ "id": "req-42", "host.call": "prom.query_range", "payload": { "query": "rate(node_cpu_seconds_total[5m])", ... } }
// B → C(成功)
{ "id": "req-42", "result": { "values": [[1721000000, "0.42"]] } }
// B → C(失败,RBAC 拒绝)
{ "id": "req-42", "error": "permission_denied: metric node_cpu_seconds_total not in data_scopes" }
```

`plugin.json` 里 `host_dependencies` + `data_scopes` 字段限制本 plugin 能调哪些 A 能力 + 数据范围;不在 `host_dependencies` 列表里的 `op` 调就返 `host_call_undeclared`。

> **主机凭据**:真实 GitLab token 通过 `vault.get_ref` 拿(只返 ref 不返明文,plan §6.6 安全边界第 6 条);plugin 不接触原始 secret。

---

## 6. 权限清单 — `permissions`

```json
"permissions": {
  "network_egress": ["gitlab.com:443"],   // 本 demo 唯一允许出网地址
  "fs_write":        [],                    // 不写本地任何文件(纯 statelessly 转发)
  "env_access":      []                     // 不读环境变量(凭据走 vault)
}
```

pluginhost `sandbox/permission.go` 在每次 invoke 前校验实际出网 / FS 写动作是否在白名单内;**违规直接拒**,不会等到 subprocess 暴露后才拦截。

---

## 7. 前端 — `frontend` 段 + Web Component

```json
"frontend": {
  "format": "web-component",
  "entry":  "web/plugin.js",                  // 相对 plugin 根
  "element_tag": "gitlab-issue-form",          // Web Component tag
  "schema":      { ... },                      // 复用 capabilities[0].schema(JSON Schema 子集)
  "permissions": ["network:gitlab.com:443"]   // 前端权限白名单(本期暂未接 CSP)
}
```

`web/plugin.js` 在浏览器端被 pluginLoader(`.record/2026-07-15-pluginhost-loader-schema-form.md`):

1. `fetch /api/pluginhost/plugins/{id}/assets/web/plugin.js` 拿到 JS 文本
2. 转 `Blob` + `URL.createObjectURL` + 注入到 `document.head` 当 `<script>` 执行
3. JS 内部 `customElements.define("gitlab-issue-form", GitLabIssueForm)` 全局注册
4. pluginLoader 轮询 `customElements.get("gitlab-issue-form")` 出现 → `document.createElement` + `setAttribute` + `appendChild`

组件用 **Shadow DOM** 隔离 CSS(本期 Shadow DOM 已经够用,Phase 6+ 才上 `<iframe srcdoc>` 进一步隔离)。

submit 后通过 `CustomEvent('plugin-invoke', { detail: { capability, params } })` 冒泡到容器,父组件(`/plugins/:id` 详情页)捕获后通过 `POST /api/pluginhost/plugins/{id}/capabilities/create_issue/invoke` 喂给 B。

---

## 8. 本地发布步骤(开发者视角)

### 8.1 本地 build demo binary

```bash
cd deploy/pluginhost/examples/gitlab-issue
go build -o bin/gitlab-issue ./cmd/gitlab-issue
```

build 出来的 `bin/gitlab-issue` 是 Linux/amd64 平台的二进制,可以直接放到 plugin 目录下被 pluginhost 拉起(只要目标平台匹配)。

### 8.2 多平台 build(交叉编译)

```bash
GOOS=linux GOARCH=arm64 go build -o bin/gitlab-issue-arm64 ./cmd/gitlab-issue
GOOS=linux GOARCH=amd64 go build -o bin/gitlab-issue-amd64 ./cmd/gitlab-issue
```

### 8.3 容器化(走 OCI image,Phase 6 F 不强求)

```bash
docker build -t gitlab-issue:0.1.0 deploy/pluginhost/examples/gitlab-issue/
```

多阶段构建:builder 阶段跑 `go build`,runtime 阶段只留 binary + manifest + web asset。

### 8.4 部署到 ongrid(P1 手动拷贝)

```bash
# 把整个目录拷到 ongrid 服务器的 plugin 根目录
scp -r deploy/pluginhost/examples/gitlab-issue root@192.168.25.30:/opt/ongrid/data/plugins/

# 启动 ongrid,pluginhost 的 LoadDirs 扫到该目录,作为 plugin_instance 写入 DB
# 自动注册 ai.tool:create_issue 能力到 A 的 skill.Registry
```

### 8.5 走运行时 install API(P2+ 走 HTTP / CLI 上传 tarball)

未来 pluginhost server 提供 `POST /api/pluginhost/plugins/upload` + multipart tarball 上传,可直接在 ongrid 的 "插件" 页面里点 "上传 tarball" 安装。本 demo 暂不实现 install tarball 流。

---

## 9. 验证(本 demo 适用,逐条 checklist)

- [x] `cmd/gitlab-issue/main.go` 用 stdlib 跑 stdio JSON-RPC 协议,no cgo,可在 Linux/amd64 + arm64 直接 build
- [x] `--self-test` 模式直接跑一遍 envelope in/out 不需要 host
- [x] stdin/stdout 端到端验证:`echo {json} | ./gitlab-issue` 正常返回 result,缺字段返 error,非法 JSON 返 invalid envelope
- [x] `plugin.json` 合法 JSON,所有 schema 字段符合 `internal/pluginhost/manifest/types.go` 定义
- [x] `format = "pluginhost"` 走主路径(非 `.claude-plugin` / `openclaw` / `bare_skills` 回退)
- [x] `ai.tool.name = "create_issue"` 满足 `[a-z0-9_]+` 约束(skill.Registry 校验)
- [x] `capabilities[0].schema` 是合法 JSON Schema(`type=object` + `properties` + `required`)
- [x] `host_dependencies` 三个值都在 hostcall op 清单里(`loki` / `prom` / `skill`)
- [x] `permissions.network_egress` 包含且仅包含 `gitlab.com:443`
- [x] `frontend.element_tag = "gitlab-issue-form"` 与 `web/plugin.js` 内 `customElements.define` 完全一致
- [x] `web/plugin.js` 用 Shadow DOM 隔离 CSS,提交事件 detail 字段 shape = capability params

## 10. 端到端链路验证(Phase 6 F 在 192.168.25.30 上跑)

按 plan §15.1 完整生命周期从无 → 装 → 用 → 卸:

| 阶段 | 后端 | 前端 | 用户可见 |
|---|---|---|---|
| **未装** | DB 无记录;磁盘无 `<name>/` 目录 | `/plugins` 列表为空 | 0 plugin;Sidebar "Plugins" 是入口但内容空 |
| **install** | scp 到 192.168.25.30 + 启动 ongrid → pluginhost.LoadDirs 扫盘 → 写 DB + 注册 registry + adapter 注入 + 启动 subprocess runtime | 刷新 `useQuery` 拉取列表 | 列表里多出一项 |
| **invoke** | user 提交表单 → POST /api/pluginhost/plugins/{id}/capabilities/create_issue/invoke → SubprocessRuntime 拉起本 binary → 收 envelope → 写 plugin_invocations + audit_logs | `/plugins/:id/invoke` 渲表单(Web Component,pluginLoader 加载 → 提交时把 detail 通过 invoke API 喂给 B) | 列表里看到最近 invoke 状态 |
| **卸** | DELETE /api/pluginhost/plugins/{name};停 runtime;adapter 摘除;软删 DB | 列表减少一项 | 列表立即消失 |

> 本任务 P1 阶段不真在 192.168.25.30 部署;Linux 打包机验证留到 Phase 6+ 由 CI / 手工运维跑。

## 11. 进阶:P2+ 阶段扩展

- **真实接 GitLab API**:`cmd/gitlab-issue/main.go` 加 `net/http` 调用 + `github.com/xanzy/go-gitlab`;token 通过 `host.call vault.get_ref` 拿,不进环境变量
- **增加 `notifier` 类能力**:再声明 `kind: "notifier", name: "gitlab_webhook"`,pluginhost `adapter/notify_adapter.go` 自动把它注册到 A 的 `notify.Router`
- **加 `host.call: skill.execute` 调用**:把 `host_dependencies` 加 `"skill"`,`data_scopes.skill.allowed_keys = ["probe.tcp"]`,plugin 内向 B 发 `host.call: "skill.execute"` envelope 反向用 A 的 probe 能力
- **打包成 OCI image 走 HTTP transport**:`transport: "http"`,改 plugin.json 的 `entry` 为 URL,pluginhost `runtime/httpremote.go` HMAC 签名直接调,适配"plugin 部署在远端 K8s"场景
- **ESM Web Component**:`frontend.format = "esm-module"`,pluginLoader 改 `import(/* @vite-ignore */ blobUrl)` 动态 import

更多详见 plan §6 / §11 / §15。
# gitlab-issue demo plugin

> **这是什么**:ongrid `pluginhost` 系统的最小示范 plugin,展示如何写一个 `pluginhost` 格式的第三方插件,把"创建 GitLab 工单"作为 `ai.tool` 能力注册到 ongrid 的 AI 工具生态里。
>
> **状态**:**目录结构与 manifest 示范**(本任务 P1 阶段),二进制占位在 `bin/.gitkeep`,真实可执行的 binary 由 CI 构建产物发布。
>
> **Phase 5+**:补一个最小的 Go 程序(可参考 `deploy/pluginhost/examples/gitlab-issue/cmd/` P2 添加),构建进 `bin/gitlab-issue`。

---

## 1. 这个 demo 做什么

C plugin(本目录)是一个 **`ai.tool` 类**插件,提供 1 个能力 `create_issue`:

- **输入**:`project_id` / `title` / `body`
- **输出**:调用 GitLab API `POST /projects/:id/issues`,返回新 issue 的 `issue_url` / `issue_iid`
- **调用方**:ongrid 的 Coordinator Agent(`internal/manager/biz/aiops/...`)在 RCA / alert 处理流程中,把 "GitLab 工单创建" 当成普通 tool 调用,**与 `probe.tcp` / `host.read_journal` 等内置 skill 同等待遇**

> 用户层面看不出区别;管理员在 "插件" 页面启用它,AI agent 就能调 `create_issue` 创建工单。

---

## 2. 目录结构

```
gitlab-issue/
├── README.md           # 本文件
├── CHANGELOG.md        # 版本历史
├── plugin.json         # ★ pluginhost manifest
├── bin/
│   ├── .gitkeep        # 实际 binary 由 CI 构建(CI 阶段填 ./gitlab-issue)
│   └── (CI 产物:gitlab-issue   可执行文件,任意语言,subprocess 入口)
└── Dockerfile          # ★ 打包成 subprocess binary 的示范(多阶段构建,生成 ./bin/gitlab-issue)
```

> pluginhost loader 在启动期扫 `plugin.json`,读 `entry` 字段找到 `./bin/gitlab-issue`,作为 subprocess 拉起,走 stdio JSON-RPC 协议(详细见下文 §5)。

---

## 3. plugin.json 字段解释

```json
{
  "id":          "gitlab-issue",                                          // pluginhost 内的唯一 ID,A DB plugin_instances.pack_id
  "name":        "GitLab Issue",                                          // 人类可读名
  "version":     "0.1.0",                                                  // semver
  "format":      "pluginhost",                                              // 固定值,表示这是 pluginhost 原生格式(不是 .claude-plugin / openclaw)
  "entry":       "./bin/gitlab-issue",                                       // subprocess 入口相对 plugin 根
  "transport":   "subprocess",                                              // 走 stdio JSON-RPC(subprocess / http / inproc 三选一)
  "description": "Demo plugin: create GitLab issues from ongrid alerts",  // 给前端列表页用

  "capabilities": [{ ... }],                                                // 1+ 能力(本 demo 1 个 ai.tool)
  "permissions":     { ... },                                                // 网络出网 / FS 写 / 环境变量白名单
  "host_dependencies": [ ... ],                                             // 本插件反向调 A 能力的清单(host.call RBAC)
  "data_scopes":      { ... },                                              // 每个 host_call 的细粒度权限(plan §6.6.2)
  "ui_metadata":      { ... },                                              // 前端插件卡片渲染
  "signature":        ""                                                   // P1 留空;Phase 5+ 接 ED25519 签名
}
```

完整字段定义见 plan §7 / `internal/pluginhost/manifest/types.go`。**所有字段均为 pluginhost 主推格式(`format: "pluginhost"`)**;其他三种老 format(`.claude-plugin/plugin.json` / `openclaw.plugin.json` / `skills/<name>/SKILL.md`)由 `format_detect.go` 自动识别,这里不示范。

---

## 4. 能力清单 — `capabilities[0]`

```json
{
  "kind":   "ai.tool",                  // 7 类 Kind 之一(ai.tool / notifier / workflow.node / skill.runner / llm.provider / embedding.provider / alert.evaluator)
  "name":   "create_issue",             // plugin 内唯一
  "class":  "mutating",                  // safe / mutating / dangerous 三档;create_issue 创建远端资源 → mutating
  "schema": {                              // JSON Schema,供前端表单 + LLM tool schema 同时消费
    "type":       "object",
    "properties": {
      "project_id": { "type": "string", "description": "GitLab project ID" },
      "title":      { "type": "string" },
      "body":       { "type": "string" }
    },
    "required": ["project_id", "title"]
  }
}
```

pluginhost 启动时,`adapter/skill_adapter.go` 把这个 `ai.tool` capability 包成 A 的 `skill.Executor` 接口,注册到 A 的 `skill.Registry.Register`(`internal/skill/registry.go`)。注册时自动用 `skill.Metadata` 校验(必填 `Name/Description` + `Key` 满足 `[a-z0-9_]+` 约束);失败 → log warning + skip,不会 panic。

> **关键命名约束**:`ai.tool` 类的 `name` 必须满足 skill key 约束 `[a-z0-9_]+`(C1 报告 §2.1);`create_issue` 合规,`createIssue` 不合规。

---

## 5. 传输协议 — stdio JSON-RPC(B → C)

`transport: "subprocess"` 表示 B 拉起 `./bin/gitlab-issue` 作为子进程,通过 **一行一帧 `\n` 分隔的 UTF-8 JSON** 与之通信。

### 5.1 请求帧(B → C,写到 stdin)

```json
{ "id": "uuid-or-trace-id", "cap": "create_issue", "params": { "project_id": "42", "title": "CPU 高负载", "body": "..." } }
```

- `id`:B 生成的 UUID / trace ID,响应帧必须匹配
- `cap`:对应 `capabilities[].name`
- `params`:`json.RawMessage` 透传,plugin 自解析

### 5.2 响应帧(C → B,从 stdout 读)

```json
{ "id": "uuid-or-trace-id", "result": { "issue_url": "https://gitlab.com/x/y/issues/1", "issue_iid": 1 } }
// 失败:
{ "id": "uuid-or-trace-id", "error": "gitlab API: 401 unauthorized,token_missing" }
```

完整协议定义见 `internal/pluginhost/runtime/subprocess.go`(`runtime.A3 报告 §2`)。

### 5.3 反向调用 A — host.call envelope

C 也可以反向调 A 能力,envelope 多一字段 `host.call`(C2 报告 §2.2):

```json
// C → B
{ "id": "req-42", "host.call": "prom.query_range", "payload": { "query": "rate(node_cpu_seconds_total[5m])", ... } }
// B → C(成功)
{ "id": "req-42", "result": { "values": [[1721000000, "0.42"]] } }
// B → C(失败,RBAC 拒绝)
{ "id": "req-42", "error": "permission_denied: metric node_cpu_seconds_total not in data_scopes" }
```

`plugin.json` 里 `host_dependencies` + `data_scopes` 字段限制本 plugin 能调哪些 A 能力 + 数据范围;不在 `host_dependencies` 列表里的 `op` 调就返 `host_call_undeclared`(plan §6.6 安全边界第 1 条)。

本 demo:
- `host_dependencies: ["loki", "prom", "skill"]`
  - `loki`:拉出错 host 过去 1h 日志
  - `prom`:拉 CPU / 内存 指标
  - `skill`:触发 `probe.tcp` 等探活辅助分析(本 demo 不实现,P2 添加)
- `data_scopes` 限定 `prom` 只能查 `ongrid_alert_*`,`loki` 限定 `job=ongrid` label

> **主机凭据**:真实 GitLab token 通过 `vault.get_ref` 拿(只返 ref 不返明文,plan §6.6 安全边界第 6 条);plugin 不接触原始 secret。

---

## 6. 权限清单 — `permissions`

```json
"permissions": {
  "network_egress": ["gitlab.com:443"],   // 本 demo 唯一允许出网地址
  "fs_write":        [],                    // 不写本地任何文件(纯 statelessly 转发)
  "env_access":      []                     // 不读环境变量(凭据走 vault)
}
```

pluginhost `sandbox/permission.go` 在每次 invoke 前校验实际出网 / FS 写动作是否在白名单内;**违规直接拒**,不会等到 subprocess 暴露后才拦截。

---

## 7. UI 渲染 — `ui_metadata`

```json
"ui_metadata": {
  "category": "ticketing",     // 前端插件卡片分类:ticketing / observability / notification / ...
  "icon":     "gitlab"           // 前端图标库的名字(Material Icons / Lucide)
}
```

让前端不必解析 plugin 语义,直接在卡片上按 category 分组 + 用 lucide `Gitlab` 图标。

---

## 8. 本地发布步骤(开发者视角)

### 8.1 直接拷贝到 plugin 目录(P1 阶段 demo 用)

```bash
# 把整个 gitlab-issue/ 目录拷到 ongrid 服务器的 plugin 根目录:
sudo cp -r deploy/pluginhost/examples/gitlab-issue /etc/ongrid/plugins/

# ongrid 启动时 LoadDirs 会扫描到此 plugin,作为 plugin_instance 写入 DB plugin_instances 表
# 自动注册 ai.tool:create_issue 能力到 A 的 skill.Registry
```

### 8.2 走运行时 install API(P2+ 走 HTTP / CLI 上传 tarball)

未来 pluginhost server 提供 `POST /api/pluginhost/plugins/upload` + multipart tarball 上传,可直接在 ongrid 的 "插件" 页面里点 "上传 tarball" 安装。本 demo 暂不实现。

---

## 9. 验证(本 demo 适用)

- [x] `plugin.json` 合法 JSON,所有 schema 字段符合 `internal/pluginhost/manifest/types.go` 定义
- [x] `format = "pluginhost"` 走主路径(非 `.claude-plugin` / `openclaw` / `bare_skills` 回退)
- [x] `ai.tool.name = "create_issue"` 满足 `[a-z0-9_]+` 约束(skill.Registry 校验)
- [x] `capabilities[0].schema` 是合法 JSON Schema(type=object + properties + required)
- [x] `host_dependencies` 三个值都在 C2 报告 §2 op 清单里
- [x] `permissions.network_egress` 包含且仅包含实际使用的出网地址(gitlab.com:443)
- [ ] `bin/gitlab-issue` 真实可执行(本任务 P1 留 `.gitkeep` 占位,Phase 5+ 接 CI)

---

## 10. 进阶:P2+ 阶段扩展

- **增加 `notifier` 类能力**:再声明 `kind: "notifier", name: "gitlab_webhook"`,pluginhost `adapter/notify_adapter.go` 自动把它注册到 A 的 `notify.Router`(具体实现需要偏差 6.1 的 `RegisterSender` method 落地)
- **加 `host.call: skill.execute` 调用**:把 `host_dependencies` 加 `"skill"`,`data_scopes.skill.allowed_keys = ["probe.tcp"]`,plugin 内向 B 发 `host.call: "skill.execute"` envelope 反向用 A 的 probe 能力
- **打包成 OCI image 走 HTTP transport**:`transport: "http"`,改 plugin.json 的 `entry` 为 URL,pluginhost `runtime/httpremote.go` HMAC 签名直接调,适配"plugin 部署在远端 K8s"场景

更多详见 plan §6 / §11。
