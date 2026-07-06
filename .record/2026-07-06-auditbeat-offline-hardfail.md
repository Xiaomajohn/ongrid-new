# auditbeat 离线资源缺失时硬失败而非 warn 跳过

## 背景

edge 集成了 audit 插件（内部包 `internal/edgeagent/plugins/audit`，commit 450d92a），
其二进制 `auditbeat` 是 Elastic 闭源产物，构建时**不联网下载**，需要运维预先把对应
arch 的二进制放到 `resource/auditbeat/<arch>/auditbeat`，然后 `make stage-auditbeat`
镜像到 `bin/<arch>/auditbeat`，最终 `make package` 打进制包。

之前的实现在 auditbeat 二进制缺失时只打印 warning 继续打包，运维误以为打包成功，
结果部署后 edge 上的 audit 插件始终无法启动，定位问题费时。

## 改了什么

把 auditbeat 缺失的语义从「warn 继续」改为「直接退出报错」。三处链路统一处理：

### 1. `Makefile` `stage-auditbeat` 目标

之前：缺失时打印提示 + `continue`，所有 arch 都跑完后正常结束。
现在：用 `fail` 标志累计每个 arch 的缺失结果，循环结束后 `if [ $$fail -ne 0 ]; then exit 1; fi`。
注释同步更新，明确说明 "build-time no-network policy" + "hard-fail" 语义。

### 2. `dist/package.sh` auditbeat 复制段

之前：缺失时 `warn "auditbeat binary ${src} missing; ..."` 继续打包。
现在：`die "auditbeat binary ${src} missing — auditbeat is offline-only and is NOT auto-fetched. ..."` 直接 exit 1。
注释追加说明：shipping 一个 audit 插件永远不会启动的 tarball 比一个响亮的构建失败更糟糕。

> 注意：promtail / otelcol-contrib 等二进制缺失仍保持 warn 行为，因为它们有
> `make fetch-promtail` / `make fetch-otelcol` 在线补救路径；auditbeat 没有。

### 3. `dist/build-edge-bundle.sh` auditbeat 条目

之前：所有缺失的条目一律 `echo missing ...; continue`。
现在：在缺失分支前判断 `src_in_bundle == "auditbeat"`，是则 `exit 1` 并附明确指引；
否则保持原有 warn+continue。其他条目（promtail/otelcol/exporters）维持原行为。

## 改动目的

让 auditbeat 这类**离线、不可在线补救**的依赖在打包阶段就把"二进制缺失"问题暴露
出来，避免下游部署后才发现 audit 插件不可用。运维拿到错误信息即可按指引补齐
`resource/auditbeat/<arch>/auditbeat` 后重新打包。

## 验证

- `bash -n dist/package.sh dist/build-edge-bundle.sh` 通过（exit=0）
- `make -n stage-auditbeat` 干跑显示分支正确：缺失时打印 3 行 error 到 stderr 并 `exit 1`
- 未实际执行 `make stage-auditbeat`，因为本地 `resource/auditbeat/<arch>/auditbeat` 不存在，
  跑起来会按设计直接失败；按 project-rule 本地不强制验证 .sh/.make 错误。

## 后续待办

- 运维侧：在能联网的环境下载 `auditbeat-9.4.2-linux-{amd64,arm64}.tar.gz`，解压
  得到 `auditbeat` 二进制，按 `resource/auditbeat/README.md` 放到对应目录。
- 不属于本任务范围：前端、后端、业务逻辑、测试均未触碰。