---
kind: logging_system
name: 结构化日志系统（slog JSONL + stderr）
category: logging_system
scope:
    - '**'
source_files:
    - internal/pkg/logger/logger.go
    - cmd/ongrid/main.go
    - cmd/ongrid-edge/main.go
---

## 1. 使用的框架与输出形式
- 标准库 `log/slog`：整个仓库统一使用 Go 1.21+ 内置的 slog，未引入 zap、logrus、zerolog 等第三方日志库。
- JSONL 行式输出：通过 `slog.NewJSONHandler(os.Stderr, ...)` 将每条日志序列化为 JSON 单行，直接写入进程标准错误流，便于 systemd/journald 或容器编排层采集。
- 无运行时 sink 切换：当前实现固定输出到 stderr，没有按环境/组件动态切换文件、网络 sink 的代码路径。

## 2. 核心文件与包
- `internal/pkg/logger/logger.go`：唯一封装点。提供 `New(level)` 创建带最小级别的 JSON handler，以及 `WithService(l, name)` 注入 `service` 属性，供两个二进制入口在启动时调用。
- `cmd/ongrid/main.go`：云端 ongrid-manager 入口，以 `logger.WithService(logger.New(slog.LevelInfo), "ongrid")` 初始化根 logger。
- `cmd/ongrid-edge/main.go`：边缘 ongrid-edge 入口，以相同模式初始化，并进一步用 `log.With(slog.String("comp", ...))` 为子组件附加上下文属性。

## 3. 架构与设计约定
- **结构化字段优先**：所有业务日志均通过 `slog.Info/Warn/Error(...)` 附带键值对（如 `slog.String("http_addr", ...)`, `slog.Uint64("user_id", u.ID)`, `slog.Any("err", err)`），禁止拼接字符串作为消息主体。
- **服务级标识**：每个进程通过 `WithService(name)` 注入 `service=ongrid|ongrid-edge`，用于区分不同二进制实例。
- **组件级标识**：在子模块中通过 `log.With(slog.String("comp", "authz"|"webshell"|"plugins"|...))` 追加 `comp` 字段，形成 `service → comp` 两级维度。
- **默认级别 INFO**：两个主程序均以 `slog.LevelInfo` 启动；测试用例中可见 `LevelError` 仅用于断言场景，生产不暴露 DEBUG。
- **安全约束**：logger 包注释明确“只允许结构化日志、JSON handler、严禁记录原始用户内容（聊天消息、请求体、密钥）”，trace_id / org_id 由调用方通过 slog attributes 注入。

## 4. 开发者应遵循的规则
- 新增日志一律使用 `slog` 并通过 `internal/pkg/logger` 提供的 `New` / `WithService` 获取 logger 实例，不要自行 `slog.NewJSONHandler`。
- 日志消息保持简洁语义化，关键上下文全部以 `slog.Xxx(key, value)` 形式传递，避免把敏感信息拼进 message。
- 需要区分来源时，先 `log.With(slog.String("comp", "xxx"))` 再在该作用域内复用该 logger。
- 如需调整全局级别，应在对应 `cmd/*/main.go` 的 `logger.New(...)` 处修改 level，而非在业务代码里覆盖 handler。
- 测试中若需捕获日志，可直接构造 `slog.NewJSONHandler` 指向 buffer，但生产路径必须走 stderr JSONL。