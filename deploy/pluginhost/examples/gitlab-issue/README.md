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
