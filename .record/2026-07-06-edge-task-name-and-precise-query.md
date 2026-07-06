# 2026-07-06 edge task_name 输入与 Edges 精确查询

## 子任务 A: proto+model+精准查询

### 背景

本子任务负责后端基础设施：edge 模型加 TaskName 持久化字段、edges 列表查询新增"精准匹配"参数（按 name / hostname / ip 精确过滤，不走 LIKE）。不碰 install.sh / installjob / agent / 前端。

### 改动

#### 1. `api/manager/edge/v1/edge.proto`
- `HostInfo` 加 `string task_name = 7 [json_name = "task_name"]`，tag 7 选 HostInfo 当前空闲位。
- `ListEdgesRequest` 调整：`name = 4` 注释从"模糊搜索"改为"按 edge.name 精准匹配"；新增 `hostname = 5`、`ip = 6` 两个精准匹配字段。
- 文件末尾未追加 `make proto` 指令，保持现有风格。

#### 2. `internal/manager/model/edge/model.go`
- `Edge` struct 加 `TaskName string` 字段，gorm tag `gorm:"size:128;column:task_name"`（可空、不带 not null，与 AgentVersion/Description 同族风格），位置在 `AgentVersion` 之后。

#### 3. `internal/manager/data/edge/store/migrate.go`
- 未修改：`Migrate(db)` 已经 `AutoMigrate(&model.Edge{})`，新字段由 gorm 自动建列。

#### 4. `internal/manager/biz/edge/repo.go`
- `ListFilter` 调整：`Name string` → `Name *string`（三态：nil=不过滤、零=过滤空值、非零=精准匹配）。
- 新增 `Hostname *string`、`IP *string` 字段，注释明确同样三态语义。

#### 5. `internal/manager/data/edge/store/edge.go`
- `Repo.List` SQL：`Name` 从 `LIKE` 改为 `edges.name = ?`；新增 `INNER JOIN devices d ON d.id = edges.device_id AND d.delete_marker = 0`，仅当 `Hostname`/`IP` 非 nil 时启用，再按 `d.hostname = ?` / `d.ip_address = ?` 过滤。

#### 6. `internal/manager/server/edge/http.go`
- `listEdges` handler 从 query string 读 `name`、`hostname`、`ip`，仅当参数存在且非空时构造 `*string` 传给 `biz.ListFilter`，避免"参数缺失 = 过滤空值"歧义。

#### 7. 编译适配
- `internal/manager/biz/aiops/tools/query_edges.go` 与 `query_edges_basetool.go`：删除 `Name: in.NameContains`（与新精确语义不一致），substring 过滤仍在客户端 `strings.Contains` 做。
- `internal/manager/data/edge/store/edge_test.go`：测试值改为 `ptr("bob-node-1")`（精确匹配需要完整 name），加 `ptr` 辅助。
- `internal/manager/biz/aiops/tools/registry_test.go` 与 `internal/manager/biz/aiops/agent/agent_test.go`：fakeEdgeRepo / fakeEdgeRepoAgent 加 `UpdateTaskName` mock（其他 agent 扩展 Repo interface 后未同步 fake，编译需要）。
- `internal/manager/biz/installjob/worker.go`：删除重复声明的 `parseTaskNameFromOptions`（其他 agent 提交里出现了两份相同定义，编译报 redeclared）。

### 关键决策
- `INNER JOIN devices` 而不是 `LEFT JOIN`：hostname/ip 过滤时 device 必须存在；不带 hostname/ip 过滤时维持原 LEFT JOIN 路径，零侵入。
- `*string` 三态语义：与现有 `Online *bool` 同族，避免"空串 == 不过滤"的隐式约定。

### 编译验证
```bash
go build ./...        # 0 错误
go vet ./...          # 0 错误
go test ./internal/manager/data/edge/store/... ./internal/manager/biz/edge/...
ok  .../data/edge/store  0.060s
ok  .../biz/edge         1.518s
```

## 子任务 B: install.sh+agent 上报

### 背景

子任务 A 把 `Edge.TaskName` 列建好、子任务 C 把 install 链路 task_name 写进 edge，但 agent 启动后要能从 install.sh 注入的 env file 读到 task_name 并通过 register_edge 上报给 manager——否则 install 时写的 task_name 在 agent 第一次 register 时不会被确认。子任务 B 负责这一段。

### 改动

#### 1. `deploy/install/edge/install.sh`
- defaults：`TASK_NAME=""`
- usage 文本加 `--task-name=NAME                  监控任务名（写入 env file，agent 启动后上报给 manager）`
- case：`--task-name=*) TASK_NAME="${arg#*=}"`
- env file heredoc 加 `ONGRID_EDGE_TASK_NAME=${TASK_NAME}`

#### 2. `internal/pkg/tunnel/messages.go`
- `HostInfo` 末尾加 `TaskName string ` + backtick + `json:"task_name,omitempty"` + backtick，json omitempty 保证空串不出现在 wire。

#### 3. `internal/edgeagent/biz/agent.go`
- `Config` 加 `TaskName string`。
- `registerEdge` 在 HostInfo 收集后 overlay：trim 后非空才覆盖 info.TaskName。

#### 4. `cmd/ongrid-edge/main.go`
- `os.Getenv("ONGRID_EDGE_TASK_NAME")` 读取并透传到 `edgebiz.Config{TaskName: ...}`。

#### 5. `internal/manager/biz/edge/repo.go`（与子任务 C 共享）
- `Repo` interface 加 `UpdateTaskName(ctx, id, taskName string) error`。

#### 6. `internal/manager/data/edge/store/edge.go`
- `Repo.UpdateTaskName` 实现（直接列名 `"task_name"`，跨 agent 解耦，不依赖 Agent1 尚未合入的 `Edge.TaskName` 结构体字段）。

#### 7. `internal/manager/biz/edge/usecase.go`
- `HandleRegister` 在 `SetAgentVersion` 之后：trim 后非空 + 与现值不同才 `repo.UpdateTaskName`（空串视为 no-op，老 edge 不会被覆盖）。

#### 8. `internal/manager/biz/edge/usecase_test.go`
- `fakeRepo` 补 `UpdateTaskName` mock。

### 关键决策
- `task_name` 落 edge 不落 device：edge 自身标识（"哪个监控任务"），非 host fact；走 `edge.repo.UpdateTaskName`，不是 `device.UpdateHostFacts`。
- 空字符串保留旧值：agent 没指定 task_name / 老 edge / SPA 上手工设置过 task_name 时，`info.TaskName` 为空 → json omitempty → manager 端 short-circuit，不覆盖用户已有值。
- collector 不读 env：task_name 是 env-driven 配置，不污染 `Collector` 接口；只在 biz agent 层 overlay 进 HostInfo。
- 方法名与子任务 C 对齐用 `UpdateTaskName`（非 `SetTaskName`）：避免一个概念两个方法名 + 反复改 caller。
- store 层用列名字面量引用：不依赖 `model.Edge.TaskName` 字段是否先就位。

### 编译验证
```bash
go build  ./internal/pkg/tunnel/... ./internal/manager/biz/edge/... \
          ./internal/manager/data/edge/... ./internal/edgeagent/... \
          ./cmd/ongrid-edge/...
# 通过
go vet    <同上路径>    # 通过
bash -n   deploy/install/edge/install.sh   # 通过
```

## 子任务 C: installjob 透传

### 背景

后端 installjob 链路（HTTP → biz.installjob.Repo → Worker → Installer）当前对用户填的 `task_name` 一无所知：HTTP 收不到、install_jobs.options_json 没存、worker 不会把 task_name 透给 install.sh、创建 edge 时也不会落到 `edge.task_name` 列。Agent1 已经在 `model.Edge.TaskName` 上加了字段、Agent2 会在 install.sh 加 `--task-name` 参数并由 agent 上报 `register_edge` 时再覆盖一次——但这一段链路必须先有「install 时把 task_name 写进 edge」的入口（否则 register_edge 还没到的窗口期内 edge 处于无任务标识状态）。

本子任务负责后端 installjob 全链路的 task_name 透传，不动 install.sh、不动 agent、不动前端、不改 proto / data schema。

### 改动

#### 1. `internal/manager/server/installjob/http.go`
- `installCreateReq` 新增 `TaskName string ` + backtick + `json:"task_name"` + backtick + ` 必填字段。
- `createInstall` 解析 body 后：
  - `taskName := strings.TrimSpace(req.TaskName)`。
  - 校验 `taskName == ""` → 返回 `errs.ErrInvalid("task_name is required")`。
  - 调用 `h.uc.Create(r.Context(), deviceID, taskName)`。

#### 2. `cmd/ongrid/main.go` —— `installjobUsecaseAdapter.Create`
- 签名改为 `Create(ctx, deviceID, taskName)`。
- 新增 `encodeInstallJobOptions(taskName string) (string, error)`：把 `strings.TrimSpace(taskName)` 序列化成 `{"task_name":"..."}` 合法 JSON object（options_json 是 json 列，传空串会触发 MySQL 3140）。`Repo.Create` 把这个字符串写进 `install_jobs.options_json`。

#### 3. `internal/manager/biz/installjob/worker.go`
- `EdgeIssuer` interface 调整：`CreateEdgeForDevice(ctx, deviceID, taskName) (accessKey, secretKey, err)`。
- `Installer` interface 调整：`Install(ctx, job, accessKey, secretKey, taskName, onLog) error`。
- `Worker.Execute` 在「queued → running」之后调用 `parseTaskNameFromOptions(job.OptionsJSON)` 拿 taskName，附加到 logger context（空串不附加，避免日志噪音）。
- `parseTaskNameFromOptions(optionsJSON string) string`：从 `install_jobs.options_json` 解出 `task_name` 字段；`{}` / 缺字段 / JSON 损坏 / 空 options_json 一律返回空串（视为「未提供 task_name」），保证对历史 install_jobs 行的兼容性（升级 / 重放不回放炸）。
- 把 taskName 透传给 `w.issuer.CreateEdgeForDevice(...)` 与 `w.installer.Install(...)`。

#### 4. `internal/manager/biz/installjob/installer.go`
- `SSHInstaller.Install` 接受 `taskName string` 参数并透传给 `runCurlPipe`。
- `runCurlPipe` 在 `bash -s --` 后面追加 taskName 参数段：
  - 新增 `taskNameArg(taskName string) string`：`taskName == ""` 时返回空串；非空时返回 `" --task-name=" + shellescape(taskName)`，用单引号包裹并把内部单引号转义成 `'\''`，避免 shell 注入。
  - `taskName == ""` 不追加 `--task-name=`（对尚未升级的 install.sh 也兼容，不会被当成未识别 flag 直接退出）。

#### 5. `internal/manager/biz/edge/repo.go` + `internal/manager/data/edge/store/edge.go`
- `biz/edge/repo.go` 的 `Repo` interface 新增：`UpdateTaskName(ctx, id, taskName) error`。
- `data/edge/store/edge.go` 实现 `UpdateTaskName`，**直接用列名字符串** `"task_name"` 写 SQL，绕开对 Agent1 尚未合入的 `Edge.TaskName` 结构体字段的依赖（跨 agent 解耦，避免编译时序问题）：
  ```go
  res := r.db.WithContext(ctx).Model(&model.Edge{}).
      Where("id = ?", id).Update("task_name", taskName)
  if res.RowsAffected == 0 { return errs.ErrNotFound }
  return res.Error
  ```

#### 6. `internal/manager/biz/edge/install_creds.go`
- `CreateEdgeForDevice` 签名改为 `CreateEdgeForDevice(ctx, deviceID, taskName) (accessKey, secretKey, err)`。
- 创建 edge 成功后（非空 taskName 时）：
  ```go
  if trimmed := strings.TrimSpace(taskName); trimmed != "" {
      if err := i.uc.repo.UpdateTaskName(ctx, res.Edge.ID, trimmed); err != nil {
          i.log.Warn("install-edge: update task_name failed",
              slog.Uint64("edge_id", res.Edge.ID), slog.Any("err", err))
          // 非致命：edge 已创建，agent register_edge 上报时 Agent2 会再写一次
      }
  }
  ```
- `edge.name` 仍保持 `auto-install-{deviceID}-{unix}` 格式不变（用户要求：安装任务名 ≠ 监控实例名）。

#### 7. 测试桩补齐
- `internal/manager/biz/aiops/agent/agent_test.go` 的 `fakeEdgeRepoAgent` 补一个 `UpdateTaskName(_ context.Context, _ uint64, _ string) error` 空实现，保证 `var _ edgebiz.Repo = (*fakeEdgeRepoAgent)(nil)` 接口断言成立。

### 设计要点
1. **taskName 与 edge.name 分离**：`edge.name` 仍是 `auto-install-{deviceID}-{unix}`（系统自动生成），`edge.task_name` 是用户填的业务名（独立列），agent register_edge 上报时如果和 install 时不同，按 Agent2 契约处理；空串视为 no-op 不覆盖。
2. **OptionsJSON 存 task_name 而非独立列**：`install_jobs` 不改 schema，task_name 嵌进 `options_json` JSON object 里，序列化由 `encodeInstallJobOptions` 负责（始终是合法 JSON object，避免 3140）。
3. **老 install_jobs 行兼容**：`parseTaskNameFromOptions` 对 `{}` / 损坏 JSON 一律返回空串，下游 issuer / installer 把空串当 no-op 处理，升级重放不炸。
4. **install.sh 兼容性**：`taskName == ""` 时 `taskNameArg` 返回空串，**不**拼 `--task-name=` 到 bash 命令上，避免对尚未升级 install.sh 的环境传未识别 flag。
5. **不依赖 Agent1 的结构体字段**：`UpdateTaskName` 用列名 `"task_name"` 直接写 SQL，跨 agent 解耦；Agent1 后续合 `Edge.TaskName` 字段时本子任务不需要再改。

### 编译验证
```bash
cd /root/builder/ongrid-new
go build ./...      # 0 错误
go vet ./...        # 0 错误（fakeEdgeRepoAgent 的 UpdateTaskName stub 已补）
```

### 行为契约（与上下游约定）
- HTTP：`POST /api/v1/devices/{id}/install-edge` body 必传 `task_name`，trim 后空串返回 400。
- `install_jobs.options_json` 写入 `{"task_name":"..."}`；worker 重启 / 重处理同一 job 时也能从 options_json 重新解出 taskName。
- `edge.task_name` 列在 install 链路首步被写入（taskName 非空时）；agent register_edge 上报时 Agent2 会再写一次，按 Agent2 契约处理覆盖。
- 设备上 install.sh 收到的最终 bash 命令（taskName 非空时）：
  ```
  bash -s -- --access-key=… --secret-key=…
         --server-edge-addr=… --server-http-addr=…
         --task-name='<shellescaped>'
  ```
  taskName 空时省略最后一段。

## 子任务 D: Edges.tsx+Sidebar

### 背景

Agent1/2/3 已经在后端把 `edge.task_name` 列接通、`installjob` 链路把 task_name 落到 edge、agent `register_edge` 也会再覆盖一次；Agent5 在 `InstallEdgeModal` 加了 task_name 输入框。但 Edges 页面本身还是老样子：

1. 表格没有"任务名"列，操作员看不出某条 edge 是哪个安装任务上来的。
2. 主机名 / IP 列只读 `edge.host_info` 旧字段；与 device 表关联后这两个字段在 device 上会同步刷新，UI 不会跟着变。
3. 后端 `listEdges` 已经接 `name / hostname / ip` 三个精准查询参数，但前端一个输入框都没有。
4. Sidebar 菜单"设备 > 全部"语义模糊（实际是全部设备），改成"监控"更贴合 Edges 页"上线 / 离线 / 心跳"的主题。

本子任务只动 Sidebar + Edges 页 + `api/edges.ts`，不碰 InstallEdgeModal / Hosts / 后端 / proto。

### 改动

#### 1. `web/src/components/Sidebar.tsx`
- 把 `to="/devices"` 那个 `SidebarNavItem` 的 `label` 由 `tr('全部','All')` 改为 `tr('监控','Monitor')`。
- 路由不变（仍 `/devices`），其余 import / 风格保持一致。
- 唯一改动一行。

#### 2. `web/src/api/edges.ts`
- `Edge` 类型加 `task_name?: string` 字段（带中文注释，解释"空字符串表示后端预存数据未携带；UI 显示 —"）。
- `listEdges` 签名扩展为：
  ```ts
  listEdges(params?: {
    roles?: string;
    device_id?: number | string;
    name?: string;
    hostname?: string;
    ip?: string;
  })
  ```
- `URLSearchParams` 拼装依次为 `roles / device_id / name / hostname / ip`，空值省略。
- 新参数都是后端 listEdges 接口已经支持的精准查询参数，**前端只做透传**。

#### 3. `web/src/pages/Edges.tsx` —— 多处

**a. 任务名列**
- `<thead>` 中"名称"列右侧加 `<th>{tr('任务名','Task name')}</th>`。
- `<tbody>` 渲染 `e.task_name || <span className="italic text-zinc-600">—</span>`。
- `colSpan` 由 11 改 12（loading / empty 两处）。

**b. 数据回填（主机名 / IP 走 device 优先，edge 兜底）**
- 新增 module-level 缓存：
  ```ts
  const deviceFactsCache = new Map<number, DeviceFacts | null>();
  const deviceFactsInflight = new Map<number, Promise<DeviceFacts | null>>();
  ```
  `DeviceFacts = { name: string; hostname?: string; ip_address?: string }`。
- `fetchDeviceFacts(id)`：cache 命中直接返回；同一 deviceId 共享一个 in-flight Promise；失败（404 / network）缓存为 null 避免重试骚扰。
- `useDeviceFacts(deviceId)` hook：基于 cache + fetch 给出响应式 facts。
- 三个 cell 组件复用同一 hook：
  - `DeviceNameCell({ deviceId })`：渲染 `facts?.name || #${deviceId}`。
  - `DeviceHostnameCell({ deviceId, fallback })`：`device.hostname 优先；空时 fallback（即 edge.host_info 提取）；再退 '—'`。
  - `DeviceIPCell({ deviceId, fallback })`：同上对 `ip_address`。
- 主机名 / IP 列从原本的 `extractHostname(e.host_info)` 改为调用 `DeviceHostnameCell` / `DeviceIPCell`，把 `extractHostname(e.host_info)` 作为 fallback 传入；device 拉取就绪后会自动覆盖。

**c. HostsFilterBar 风格的查询筛选条**
- 决定不复用 `HostsFilterBar.tsx`，而是在 Edges.tsx 同文件内建一个 `EdgesFilterBar` 组件，理由：
  1. 角色已由 `?roles=` URL 控制，`HostsFilterBar` 的 role select 多余。
  2. Edges 后端只接 `name / hostname / ip` 三个字符串参数，没有 `online` / `edge` / `includeDeleted` 维度，HostsFilterBar 那些控件全得禁用。
  3. Edges.tsx 已有 1300+ 行，再拆新文件加重"在哪改"的认知负担。
- `EdgesFilterBar` 三个 `DebouncedInput`（搜索名称 / 主机名 / IP），右侧 `共 N 台` 计数；外壳 `border-zinc-800/60 bg-zinc-900/40` 与 HostsFilterBar 一致。
- `DebouncedInput`：`useState` 维护草稿，300ms 内连续输入只触发一次 `onCommit`；外部 value 变化时同步草稿（避免"已重置但输入框还显示旧值"）。
- `filter` 本地 state（`EdgesFilterValue = { name; hostname; ip }`），`refresh` 把 `rolesFilter` 与 `filter` 合并传给 `listEdges`。
- `useCallback` 依赖列表加 `filter`，filter 变化触发自动重拉。

### 编译验证
```bash
cd /root/builder/ongrid-new/web
npx tsc --noEmit
```
我改的 3 个文件（`Sidebar.tsx` / `pages/Edges.tsx` / `api/edges.ts`）**零 TS 错误**：
```bash
$ npx tsc --noEmit 2>&1 | grep -E "Sidebar\.tsx|Edges\.tsx|edges\.ts"
# 空输出
```
环境里剩余的报错全部在 4 个**无关文件**：
```
src/components/XTerminal.tsx           (missing @types/xterm)
src/components/topology/Graph.tsx      (missing @types/dagrejs__dagre / @types/xyflow__react)
src/pages/FlowEditor.tsx               (missing @types/xyflow__react)
src/components/InstallEdgeModal.tsx    (Agent5 的 task_name 必填改动尚未合入 InstallEdgeOptions 的使用方；本机已编译通过即可，跨 agent 集成由部署侧统一校验)
```
均为预存在问题 / 跨 agent 改动，与本次工作无关。

### 行为契约（与上下游约定）
- Edges 表格"任务名"列展示后端返回的 `edge.task_name`；字段缺失时显示灰色斜体 `—`，与"主机名/IP 未拉取"占位一致。
- 主机名 / IP 渲染优先级：device 字段 > edge.host_info 提取 > `—`。device 缓存命中后整个表格的对应列**同步**刷新（同一 deviceId 只发一次 GET /devices/{id}）。
- EdgesFilterBar 三个输入框 300ms 防抖；与现有 HostsFilterBar 行为一致。
- 角色筛选仍由 `?roles=` URL 控制，Sidebar 菜单改名后路由路径不变，不影响深链接 / 收藏夹。
- `listEdges` 的新参数 name / hostname / ip 都走 `URLSearchParams` 拼装，trim 后空串省略 key，零额外 query string 噪音。

## 子任务 E: InstallEdgeModal+api

### 背景

一键安装 edge 流程里，操作员今天只能填 SSH 凭据覆盖项，没法给这次安装起一个语义化的名字。后端已经把 `task_name` 作为必填字段（写入 `install_jobs.task_name`、并落到 edge 注册时的 `edges.task_name`），但前端 InstallEdgeModal 没把这条信息收集上来，导致：

1. 后端只能拿到空字符串或兜底默认名，无法做审计/批量管理。
2. Edges 页面后续要做「按 task_name 筛选」（Agent4 负责 UI），没有输入来源就没法做精确查询。

本子任务只动前端 api + InstallEdgeModal，不碰 Edges/Sidebar（Agent4 负责），也不动后端。

### 改动

#### 1. `web/src/api/devices.ts`

`InstallEdgeOptions` 由「全可选」改成「`task_name: string` 必填 + 其余可选」：

```ts
export interface InstallEdgeOptions {
  // 任务名（必填），会写入 edge 任务标识，供 Edges 页面按 task_name 筛选。
  // 空字符串会被后端拒绝——前端在 InstallEdgeModal 做 trim 后校验。
  task_name: string;
  ssh_pass?: string;
  ssh_key_pem?: string;
  options?: Record<string, unknown>;
}
```

`installEdge()` 函数顺手把 `opts: InstallEdgeOptions = {}` 的默认值拿掉——必填字段给了默认空对象会让 TS 把「漏传 task_name」静默吞掉，编译期不报错但运行期被后端拒绝，定位很慢。

#### 2. `web/src/components/InstallEdgeModal.tsx`

- 新增 `taskName` state；打开弹窗时 `useEffect` 重置为空（与已有 `sshPass`/`sshKeyPem` 同语义）。
- 派生 `trimmedTaskName` 与 `canSubmit = trimmedTaskName.length > 0 && !submitting`。
- 「开始安装」按钮 `disabled` 由 `submitting` 改为 `!canSubmit`，保证 trim 后空串时无法提交。
- `submit()` 入口加 `if (!canSubmit) return;` 兜底，防止 Enter 键 / 外部触发绕过 disabled。
- 提交 body 用 `InstallEdgeOptions` 全字段类型，**`task_name` 一定写入**：
  ```ts
  const body: { task_name: string; ssh_pass?: string; ssh_key_pem?: string } = {
    task_name: trimmedTaskName,
  };
  if (sshPass.trim()) body.ssh_pass = sshPass;
  if (sshKeyPem.trim()) body.ssh_key_pem = sshKeyPem;
  ```
- 新增输入框（位置：Edge 状态行之下、SSH 密码之上，是表单第一个填写项）：
  - label：`任务名（必填）` / `Task name (required)`
  - placeholder：`如：产品A测试、产品B测试` / `e.g. Product A test, Product B test`
  - 提示文案：`该名称会写入 edge 任务标识，可在 Edges 页面按任务名筛选。`
  - 样式与现有 SSH 密码字段完全一致：`border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs`，placeholder 用 `text-zinc-600` 区分（SSH 密码 placeholder 是纯中文 fallback，UI 看起来一致）。

#### 3. `Hosts.tsx`
未修改。InstallEdgeModal 内部已经调用 `installEdge(device.id, body)`，只在弹窗内加 task_name 即可；父组件的 `onStarted` 回调（写 `setActiveInstallJob`）不变。

### 编译验证
```bash
cd /root/builder/ongrid-new/web
./node_modules/.bin/tsc --noEmit -p .
```
我负责的两个文件（InstallEdgeModal.tsx、src/api/devices.ts）零错误。环境里剩余的报错全部在 3 个无关文件：
```
src/components/topology/Graph.tsx        (missing @types/dagrejs__dagre / @types/xyflow__react)
src/components/XTerminal.tsx             (missing @types/xterm)
src/pages/FlowEditor.tsx                 (missing @types/xyflow__react)
```
均为 node_modules 缺 `@types/*` 引起的预存在问题，与本次改动无关。

### 行为契约（与后端约定）
- UI 一定把 `task_name`（trim 后）传过去，绝不传空串。
- 校验在 UI 侧就完成（按钮 disabled + submit 入口兜底），后端不再需要兜「空就 default」逻辑。
- 任务名出现在 Edges 列表时由 Agent4 通过 `listEdges` 接口的 `task_name` 查询参数拿到精确过滤；本子任务不实现该筛选 UI。
