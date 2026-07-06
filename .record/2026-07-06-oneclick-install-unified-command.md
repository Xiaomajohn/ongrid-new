# 2026-07-06 一键安装命令拼接单一真相源

## 问题

一键安装的后端 `SSHInstaller.runCurlPipe` 自己用 `fmt.Sprintf` 拼 `curl ...` 命令,
和前端 `InstallCommandRow` 里的字符串模板是两套独立代码。
前端拼命令用 `window.location.host`(浏览器地址栏),
后端拼命令用 `serverHTTPAddr`(环境变量 `PublicURL`),
两套独立真相,端口不对齐时走 nginx 443 端口的 `/install.sh` 报 404。

## 改动目的

命令 100% 由前端 `buildInstallCommand()` 拼,后端只收一条完整命令字符串,
SSH 上去执行,不再有任何 `fmt.Sprintf` 拼装逻辑。
零兜底:command 为空直接报错。

## 改动文件

### 1. 新建 `web/src/lib/installCommand.ts`
抽 `buildInstallCommand()` 纯函数,导出给前端各处调用。
接收 `{accessKey, secretKey, host, tunnelPort?}`,返回 `{cmd, display}`。

### 2. `web/src/pages/Edges.tsx`
`InstallCommandRow` 删除本地字符串模板,改为 `import { buildInstallCommand }`。
(Hosts.tsx 的 InstallCommandRow 本次不动,因为只展示手动命令不影响一键安装链路)

### 3. `web/src/api/devices.ts`
`InstallEdgeOptions` 接口加 `command?: string` 字段。

### 4. `web/src/components/InstallEdgeModal.tsx`
`submit` 函数改为:
1. 先调 `createEdge()` 拿凭据(access_key_id + secret_key)
2. 用同一套 `buildInstallCommand()` 生成 curl 命令
3. 调 `installEdge(created.id, {task_name, command})` 传给后端
注意:用了 `createEdge` 返回的新 edge id,凭据也是这组。

### 5. `internal/manager/server/installjob/http.go`
`installCreateReq` struct 加 `Command string` 字段。
`Usecase.Create` 接口签名加 `command string` 参数。
`createInstall` handler 调用点透传 `req.Command`。

### 6. `cmd/ongrid/main.go`
`installjobUsecaseAdapter.Create` 签名加 `command string`。
`encodeInstallJobOptions` 函数加 `command` 字段到 JSON payload。

### 7. `internal/manager/biz/installjob/worker.go`
`Installer` 接口 `Install` 方法签名加 `cmd string` 参数。
加 `parseCommandFromOptions()` 函数,同款 JSON-tag-局部反射模式。
`Execute` 里调用 `parseCommandFromOptions` 并透传给 `installer.Install`。

### 8. `internal/manager/biz/installjob/installer.go`
`Install` 方法加 `cmd string` 参数,空则报错。
`runCurlPipe` 签名改为只收 `cmd string`,删除 `fmt.Sprintf` 拼装代码,
改为 `runCmd := strings.TrimSpace(cmd)`。
删除了 `shellescape` 和 `taskNameArg` 辅助函数(已无调用处)。

## 不兼容变更

老的 `install_jobs` 行(没有 `command` 字段)重跑时会报
"command is empty — 前端必须通过 POST /install-edge 的 command 字段塞入...",
需要从 UI 重新触发一次新安装。

## 编译验证

```
go build ./internal/manager/biz/installjob/...  # OK
go build ./internal/manager/server/installjob/...  # OK
go build ./cmd/ongrid/...  # OK
npx tsc --noEmit (改动文件无相关错误)  # OK
```
