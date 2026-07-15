# Changelog

所有本 plugin 的变更记录。版本遵循 [Semantic Versioning](https://semver.org/)。

## [0.1.0] - 2026-07-14

### 新增
- 初始版本,demo plugin
- `plugin.json`:manifest,声明 1 个 `ai.tool` 类能力 `create_issue`
- `bin/.gitkeep`:subprocess binary 占位,真实可执行文件由 CI 构建产物填入
- `Dockerfile`:多阶段构建示意(builder 编译 / runtime 镜像瘦身),Phase 5+ 真实实现时把 demo 占位 shell script 替换为 Go 程序
- `README.md`:展示 pluginhost manifest 字段含义、stdio JSON-RPC 协议、host.call 反向调用 envelope、permissions 白名单语义、ui_metadata 渲染

### 安全
- 网络白名单仅 `gitlab.com:443`
- 不写本地任何文件(`fs_write: []`)
- 不读环境变量,凭据走 `vault.get_ref`(`env_access: []`)

### 已知限制
- binary 是占位 stub,在 Phase 5 接入真实 GitLab Issue API 调用
- 仅声明 `ai.tool: create_issue`;Phase 5+ 计划扩展 `notifier: gitlab_webhook` capability
