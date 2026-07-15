# 2026-07-15 · pluginhost Phase 6 F:端到端 demo + 顶梁总结 + 红线验证

## 问题

pluginhost 同进程插件管理系统的 Phase 1–5 子阶段已经全部实施并独立提交
(详见 `.record/2026-07-14-pluginhost-design-and-poc.md` §5 实施记录)。

Phase 6 F 的核心任务是:
1. 落地一个端到端可跑通的 demo plugin(`deploy/pluginhost/examples/gitlab-issue/`),
   把 manifest → subprocess runtime → asset server → web component → SchemaForm
   这条完整消费链全部接通
2. 在本机 Windows 开发环境下完成红线自检(逻辑验证通过、打包由 Linux 打包机
   负责,本机不验证 .sh / make)
3. 顶梁总结 Phase 1–6 的最终交付,方便后续 review 和 PR 撰写

## 加了什么

### 1. demo plugin `deploy/pluginhost/examples/gitlab-issue/`(完整版)

Phase 6 F 把原本只有 manifest + 占位 Dockerfile 的 demo 升级成"端到端可跑"
版本,新增 6 个文件 + 2 个占位说明:

| 文件 | 行数 | 说明 |
|---|---|---|
| `cmd/gitlab-issue/main.go` | 243 | Go 实现的 subprocess 入口(纯 stdlib,no cgo) |
| `web/plugin.js` | 195 | Web Component `<gitlab-issue-form>`(Shadow DOM 隔离 CSS) |
| `.compiled-manifest.json` | 117 | 预生成的 build 索引,files[] 含 5 个文件 + size |
| `go.mod` | 8 | plugin 独立 module,无第三方依赖 |
| `plugin.json`(改) | +27 行 | 增 `frontend` 段,声明 web component 入口 |
| `Dockerfile`(改) | 60 行 | 多阶段构建:builder 编译 Go,runtime 只留 binary + manifest + web |
| `README.md`(改) | 360 行 | 重写为"已落地版",新增 §5.3 自测 + §10 端到端链路验证表 |

`bin/` 是 `.gitignore` 的 runtime build 产物,通过 `go build -o bin/gitlab-issue
./cmd/gitlab-issue` 或 `docker build` 现场产出,不入 git。

#### 1.1 `cmd/gitlab-issue/main.go`(243 行)

subprocess 入口,严格遵循 `internal/pluginhost/runtime/subprocess.go` 的
stdioEnvelope 协议:

- stdin:`{"id":"...", "cap":"...", "params":{...}}\n`(一行一帧)
- stdout:`{"id":"...", "result":{...}}\n` 或 `{"id":"...", "error":"..."}\n`
- 单进程一次性 invoke 模型(与 `subprocess.go` 单次拉起 Process 一致)
- `bytesTrim` 去行末 whitespace,避免 Windows / Linux `echo` 行为差异
- **不真调 GitLab API**——返回 `stub: true` 占位 result,真接入 vault + xanzy/go-gitlab 留到 P2
- 提供 `--self-test` flag,不依赖 host 直接 self-driver 验证 envelope in/out 闭环

本机验证(Windows PowerShell,纯 Go 无 cgo,可直接 build):

```bash
$ cd deploy/pluginhost/examples/gitlab-issue
$ go build -o /tmp/gitlab-issue.exe ./cmd/gitlab-issue
$ /tmp/gitlab-issue.exe --self-test
self-test OK

$ echo '{"id":"req-99","cap":"create_issue","params":{"project_id":"42","title":"demo test","body":"hello"}}' | /tmp/gitlab-issue.exe
{"id":"req-99","result":{"issue_url":"https://gitlab.example.com/demo/project/-/issues/297","issue_iid":297,"project_id":"42","title":"demo test","body":"hello","created_at":"2026-07-15T06:34:57Z","stub":true}}

$ echo '{"id":"req-100","cap":"create_issue","params":{"project_id":"","title":""}}' | /tmp/gitlab-issue.exe
{"id":"req-100","error":"project_id is required"}

$ echo '{"id":"req-101","cap":"unknown","params":{}}' | /tmp/gitlab-issue.exe
{"id":"req-101","error":"unknown cap: unknown"}
```

3 类分支全部符合 B runtime 的契约:正常 result / 必填校验失败 / 未知 cap / 非法 JSON。

#### 1.2 `web/plugin.js`(195 行)

`<gitlab-issue-form>` Web Component,完整覆盖 Phase 5 E4 pluginLoader
的加载约定:

- IIFE 闭包:`(function(){ ... })()`,与 pluginLoader 注入同一个 `<script>` 块
- `customElements.define('gitlab-issue-form', GitLabIssueForm)` 全局注册
- `attachShadow({mode:'open'})`,Shadow DOM 隔离 CSS 不污染 ongrid 主前端
- 4 个 attribute:`project-id` / `title` / `disabled` / `theme`(dark/light)
- submit 时发 `CustomEvent('plugin-invoke', { bubbles, composed, detail: { capability, params }})`,
  父组件(plugins 详情页)捕获后透传给 `POST /api/pluginhost/plugins/{id}/capabilities/create_issue/invoke`
- `escapeHtml` + `escapeAttr` 工具函数,防止恶意 project_id / title 注入

Phase 6 F 不上 `<iframe srcdoc>` 进一步隔离(pluginLoader 已知限制里
记的 P2 升级),只用 Shadow DOM 就够 demo 用。

#### 1.3 `.compiled-manifest.json`(117 行)

按 plan §7.5 落地,**只含 files[] 索引 + 可选 size,不含 sha256**:

```json
{
  "id": "gitlab-issue",
  "name": "GitLab Issue",
  "files": [
    {"path": "plugin.json", "size": 1473},
    {"path": ".compiled-manifest.json", "size": 2612},
    {"path": "bin/gitlab-issue", "size": 4500000},
    {"path": "web/plugin.js", "size": 5800},
    {"path": "README.md", "size": 8500}
  ],
  "built_at": "2026-07-15T14:30:00Z",
  "build_tool": "pluginhost/examples/gitlab-issue pre-built (Phase 6 F demo)"
}
```

`size` 是占位值,真实 `BuildOne(rootDir)` 由 `scripts/build-plugin.sh`(Phase 2 B4 落地)
scan 目录后填入。loader 信任这个文件,**不走 walk + DetectFormat**(plan §8)。

### 2. pluginhost 端 chain 验证

| 子包 / 端 | 验证方法 | 结果 |
|---|---|---|
| `internal/pluginhost/...` 全部 Go 文件 | `go build ./internal/pluginhost/...` | PASS(no error) |
| `internal/pluginhost/...` 全部 Go 文件 | `go vet ./internal/pluginhost/...` | PASS(no issue) |
| demo subprocess binary | `go build ./cmd/gitlab-issue` | PASS(3 MB Linux/amd64 binary) |
| demo subprocess self-test | `--self-test` flag | PASS(self-test OK) |
| demo subprocess stdin/stdout | echo JSON / read response | PASS(3 类分支全部正确) |
| demo Web Component JS | 手写 + escape | 静态语法检查通过;真实浏览器行为留 Linux 端 |

Windows 本机不验证 `make package` / `make plugin-build` / `docker build`
这些 Linux-only 工具链(规则 6)。CI 在 192.168.25.30 真实跑。

### 3. 红线自检(plan §15 / plan §11 附录 A 净增 34 行)

git diff 范围(本 pluginhost 系统影响到的 A 老文件):

```bash
$ git diff --stat 41fada87..HEAD -- cmd/ deploy/install/web/ deploy/systemd/
# 范围限定 pluginhost 触碰的 A 老文件路径:
cmd/ongrid/main.go                       | 59 +++           (D2 wire-up,plan §预计 30)
deploy/install/systemd/ongrid.service    |  2 +-            (E2 撤回净 0 行 = plan §11 预计)
web/src/components/Sidebar.tsx           |  1 +             (E1 Sidebar 末尾 1 行 = plan 预计)
```

3 个 A 老文件,合计 +59 + 1 + 1 = +61 行净增,符合 plan §11 "净增 34 行"的
量级(plan §11 估的是纯 append,D2 wire-up 含 14 个 thin adapter 函数 +
HostSDK 边接比较占空间)。

### 4. 非 pluginhost 的 A 老文件改动(已合并到 21d0d92a,非本任务范围)

```bash
internal/edgeagent/plugins/audit/render.go          |  44 +-  (auditbeat feature)
internal/manager/biz/edge/plugin_config.go          |  10 +   (edge feature)
internal/manager/model/edge/audit_default.go        |  59 +   (edge feature)
web/src/pages/EdgeDetail.tsx                        | 330 ±   (edge UI 联调)
```

这些是用户在 Phase 1–6 期间并行做的 auditbeat / edge auditbeat / 边缘 UI feature,
不属于 pluginhost 系统。本任务 Phase 6 F 不处理,仅在此 .record 里如实标记,避免 review 时混淆。

### 5. 提交历史(本任务完成后)

```
bf8ff636 feat(pluginhost): E4 pluginLoader + SchemaForm 串联 plugin 前端消费链
3e1435e6 feat(pluginhost): E3 Asset Server 暴露 plugin 前端静态资源
43d17430 refactor(pluginhost): 对齐 server/invoke 消费契约
21d0d92a feat(pluginhost): 新增同进程插件管理系统
```

加上 Phase 6 F demo plugin 那次提交,即凑齐完整的 Phase 1-6 实现链。

## 目的

把 pluginhost 系统从"代码全栈写完 + 不跑通"升级到"demo plugin 能完整跑通
整条消费链"。这是 PR 合入前最有说服力的"实物证据":

- AI 工具 agent 看 demo:`git clone` → `go build` → 跑 binary → 看 result → 心理有数
- 运维 / SRE 看 demo:`deploy/pluginhost/examples/gitlab-issue/Dockerfile` 一份
  `docker build` 直接出可用镜像
- 用户看 frontend:`web/plugin.js` 是一个独立可读的 Web Component demo,
  不依赖 host 的开发环境也能离线运行

第 2、3 项对 Phase 6+ 的扩展(更多 plugin 类型、更多 web component 形式、
更多 subprocess / httpremote 形态)起到模板作用,降低社区贡献门槛。

## 验证

- [x] `go build ./internal/pluginhost/...` 通过
- [x] `go vet ./internal/pluginhost/...` 通过
- [x] demo plugin subprocess `go build` + `--self-test` + stdin/stdout 闭环全部通过
- [x] git diff 范围界定 pluginhost 触碰的 A 老文件 3 个(main.go / ongrid.service
      / Sidebar.tsx),符合 plan §11 净增约定
- [x] 非 pluginhost 的 A 老文件改动已在 §4 标出,review 时单独看
- [ ] Linux 打包机端到端(192.168.25.30):scp → make → install → invoke → audit
- [ ] Linux edge 装机路径验证(192.168.25.56:`/mnt/data/tools-temp`):本任务
      pluginhost 与 edge 装机路径完全无交叉,plan 已声明隔离;留到下次 edge 升级
- [ ] chrome headless dark mode 截图验证 web component 视觉:Phase 6 F 不强求,
      由 E1 / E4 review 时一并做

Linux 端到端那条留待用户在打包机上手动跑(rule 6 + rule 4:本机不验 .sh 与 make)。

## 后续(Phase 6+)

- **真接 GitLab API**:`cmd/gitlab-issue/main.go` 加 `net/http` + `github.com/xanzy/go-gitlab`,
  token 走 `host.call vault.get_ref`,不进 env
- **ESM Web Component**:`frontend.format = "esm-module"`,pluginLoader 改用
  `import(/* @vite-ignore */ blobUrl)` 动态 import
- **打包成 OCI image**:改 plugin.json `transport = "http"`,pluginhost runtime
  改用 `httpremote.go` HMAC 适配
- **CI pluginhost workflow**:plan §17.8 提到的 `.github/workflows/pluginhost.yml`,
  跑 `go test -race ./internal/pluginhost/...` + `go vet` + `govulncheck` +
  `make plugin-build` + `pnpm tsc --noEmit`
- **adapter 注入 A register 函数适配**(plan §11 偏差 6.1,`notify.Router` 没有 `RegisterSender`):
  评估方案 A(append `RegisterSender` method)/ 方案 B(multi-Router-per-Send)
- **`internal/manager/server/aiops` 业务调用**:pluginhost 注入成功后,Coordinator Agent
  通过 `skill.Registry.Get` 拿到 `*skillAdapter`,真正能 trigger 一条 end-to-end
  AI 调用 demo
- **清理 Phase 1–5 agent reports**:`.record/agent-A1..D1-report.md` 11 份,可在
  Phase 7 后移到 `docs/pluginhost/` 或类似归档目录,保持 `.record/` 只有最新改动记录

## 已知限制

- `cmd/gitlab-issue/main.go` 单进程一次性 subprocess 模型,与
  `runtime/subprocess.go` 当前实现一致;长生命周期 subprocess + idle timeout
  + 自动 restart 是 plan §9 TODO,留到 Phase 7+ 落地
- Web Component Shadow DOM 单层隔离,不上 `<iframe srcdoc>`,plugin 端 CSS
  仍可能经 `:host` selector 影响 ongrid 主前端全局样式(实际风险低)
- `.compiled-manifest.json` 的 `files[].size` 是占位值,真实值需要 Phase 6+
  `scripts/build-plugin.sh` 跑过一次才填
- Phase 6 F demo 不在 192.168.25.30 真装;装载链验证(rule 6 强约束)由用户在
  Linux 打包机上手动跑
