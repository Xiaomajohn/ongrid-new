# auditbeat 输出与日志轮转调整

## 背景

edge 端 audit 插件的 auditbeat 子进程两类日志都缺合适的轮转策略:

- 数据输出 `audit.jsonl`: 默认 10MB 滚动、保留 7 个 (~70MB), 运维需要更长的本地回溯窗口
- auditbeat 自身运行日志 `audit.log`: 完全走 auditbeat 9.x 默认 (10MB×7), 同样过小

运维反馈 audit.jsonl 滚动过快, audit.log 缺少显式分卷控制。

## 改动文件

- `internal/edgeagent/plugins/audit/render.go`
  - 模板 `output.file` 块: `rotate_every_kb: 10000 → 51200`, `number_of_files: 7 → 30`
  - 模板新增 `logging.files` 块: `rotateeverybytes: 52428800 (50MB)`, `keepfiles: 5`, `permissions: 0644`, `rotateonstartup: true`
  - 头部注释 "Fields auto-injected" 列表新增 `logging.files` 一条说明

## 不改动

- `internal/manager/model/edge/audit_default.go`: rotate / logging 不在 spec 暴露, 双源无需新增字段
- `web/src/pages/EdgeDetail.tsx` (`AuditSpecForm`): 旋钮硬编码, UI 不暴露
- `internal/edgeagent/plugins/audit/render_test.go`: 现有断言不依赖 rotate_every_kb / number_of_files 的具体值, 新加的 logging 块不影响断言

## 默认值

- 数据输出 audit.jsonl: 50MB × 30 ≈ 3GB 滚动窗口
- 运行日志 audit.log (路径 ${pluginDir}/logs/auditbeat): 50MB × 5 ≈ 250MB

## 目的

让 auditbeat 子进程的 IO 抖动更可控, 给运维一份"线上 audit.jsonl 之外"的本地历史兜底(不进 Loki 的瞬时降级场景也能撑一段时间); 运行日志不长期囤积, ~250MB 排障窗口足够。

## 部署注意

- `number_of_files: 30` 在磁盘紧张机器上可能撑到 ~3GB; pluginDir 默认在 `/var/lib/ongrid-edge/plugins/audit`, 需确认生产 `/var` 分区 ≥ 5GB 余量
- 磁盘特别小的机器可走 `raw_config` 模式覆盖回小窗口
- `logging.files.rotateonstartup: true` 沿用 auditbeat 默认行为, 不做额外变更

## 验证

- 本地 `go build ./...` 编译通过
- 打包机 `make build-arm64` 产出 arm64 边缘包
- edge 端部署后: `audit.jsonl` 单文件增长到 ~50MB 才滚动; `audit.log` 启动时分卷一次, 之后 50MB 滚动, 保留 5 个