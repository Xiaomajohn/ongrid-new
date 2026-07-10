---
kind: dependency_management
name: Go/Node 双栈依赖管理（单模块 + 子包）
category: dependency_management
scope:
    - '**'
source_files:
    - go.mod
    - go.sum
    - web/package.json
    - web/package-lock.json
    - api/buf.yaml
    - api/buf.gen.yaml
    - resource/auditbeat-9.4.2-linux-arm64.tar.gz
    - resource/auditbeat-9.4.2-linux-x86_64.tar.gz
    - deploy/install/systemd/install-deps.sh
    - deploy/install/edge/build-edge-bundle.sh
---

## 1. 使用的系统与方法
- Go：采用单一 `go.mod` 的单体仓库模式，通过 `require` 声明直接依赖、`// indirect` 标注传递依赖，配合根级 `go.sum` 锁定版本。
- Node.js：前端位于 `web/` 子目录，使用独立的 `package.json` + `package-lock.json` 管理 React/Vite 生态依赖。
- Proto 契约：通过 `api/buf.yaml` + `buf.gen.yaml` 驱动 `buf generate` 生成 Go gRPC stub，作为云边 RPC 契约的唯一事实来源。
- 二进制嵌入：审计采集器 `auditbeat` 等第三方二进制以 tarball 形式随源码分发（`resource/*.tar.gz`），构建时解压到 `bin/linux-{amd64,arm64}/`，不通过 go mod vendor 或外部包管理器拉取。

## 2. 关键文件与位置
- Go 依赖清单：`go.mod`、`go.sum`
- 前端依赖清单：`web/package.json`、`web/package-lock.json`、`web/node_modules/`
- Proto 构建配置：`api/buf.yaml`、`api/buf.gen.yaml`
- 预编译二进制与打包脚本：`resource/*.tar.gz`、`deploy/install/edge/build-edge-bundle.sh`、`deploy/install/systemd/install-deps.sh`
- CI 构建入口：`.github/workflows/ci.yml`、`.github/workflows/release.yml`、`Makefile`

## 3. 架构与约定
- 单一 Go module：所有后端代码（云端 ongrid、边缘 ongrid-edge、IAM、internal/pkg、internal/skill）共享同一个 module path `github.com/ongridio/ongrid`，避免多模块拆分带来的版本对齐成本。
- 依赖分层：核心业务库集中在 `internal/`，可复用能力下沉到 `internal/pkg`；测试辅助（testcontainers、mock 等）通过 `// indirect` 进入 `go.sum`，不参与生产镜像。
- 私有/内部包：项目未定义 `GOPRIVATE` 或 `replace` 重写，所有依赖均从公共 Go 代理获取；若未来引入企业内网包，需补充 `GOPRIVATE` 与私有代理配置。
- 前端独立：`web/` 子工程完全解耦于 Go 模块，使用 npm 锁文件保证构建可重现；CI 中分别执行 `go build` 与 `npm ci --prefix web`。
- 第三方二进制策略：对无法通过 go mod 管理的 C/C++ 工具（如 auditbeat），采用“源码树内 tarball + 安装脚本”方式，由 `install-deps.sh` / `build-edge-bundle.sh` 在部署阶段解压到目标路径，规避网络不可达风险。

## 4. 开发者应遵循的规则
- 新增 Go 依赖：统一在根 `go.mod` 添加，提交后同步更新 `go.sum`；仅测试用依赖保持 `// indirect` 标记，不要污染生产依赖图。
- 禁止本地 `vendor/`：本项目不使用 `go mod vendor`，所有依赖必须通过 `go.sum` 锁定，确保跨机器一致。
- 前端依赖变更：仅在 `web/package.json` 中修改，并重新生成 `web/package-lock.json`；CI 使用 `npm ci` 而非 `npm install`，严禁提交 `node_modules/`。
- Proto 契约变更：修改 `api/*.proto` 后运行 `buf generate` 更新生成的 Go 代码，并在 PR 中一并提交 diff。
- 第三方二进制升级：更新 `resource/*.tar.gz` 及对应 `install-deps.sh` / `build-edge-bundle.sh` 中的校验和与路径，确保 arm64/amd64 双架构产物齐全。
- 私有包接入：若引入企业内部 Go 模块，需在仓库环境变量或 CI 中设置 `GOPRIVATE`，并通过 GOPROXY 链指向企业代理，同时保留 `go.sum` 完整性。