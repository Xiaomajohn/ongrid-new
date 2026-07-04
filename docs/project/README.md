# docs/project — 项目级文档总览

> 本目录是 ongrid 仓库的**项目文档**层，遵循「按仓库目录层级结构组织的递归鸟瞰式文档」约定：覆盖一级根目录、二级子目录、三级 BC（bounded context）入口目录，**只描述对应目录/文件的名称、作用及包含内容，不深入实现细节**。与 `docs/install/`（部署/安装）、`docs/test/`（测试目录）、`docs/workflow-catalog.md`（业务流程目录）正交分工。

---

## 项目一句话定位

ongrid 是一个**云端托管、本地落地的智能体运维（AIOps）平台**：Manager（云端）通过 [frontier](https://github.com/singchia/frontier) 反向隧道接入分布在边缘主机上的 `ongrid-edge` 探针，提供设备纳管、可观测（指标/日志/链路）、告警评估、AI 调查诊断（Investigator / Reporter）、可编程 Workflow（Flow）、IM 多端接入（飞书/钉钉/Slack/Telegram）等一体化能力。

---

## 三篇核心文档导航

| 文档 | 主题 | 主要回答的问题 |
| --- | --- | --- |
| [data-model.md](./data-model.md) | 数据模型 | 系统持久化了哪些实体？它们的关系如何？哪里是真值源？ |
| [backend-data-flow.md](./backend-data-flow.md) | 后端与数据交互 | Manager / EdgeAgent 内部怎么分层？请求一次 API 走过了哪些层？ |
| [frontend-backend-contract.md](./frontend-backend-contract.md) | 前后端交互 | SPA 怎么调用后端？API 路由表怎么对应 BC？数据契约是什么？ |

---

## 仓库根目录鸟瞰（`./`）

仓库根只承载"跨多个领域"的工程级产物，每个子目录都有自己的职责：

| 目录/文件 | 作用 | 包含内容 |
| --- | --- | --- |
| `agents/` | 仓库自带的多 Agent 提示词（personas / 任务分配） | incident-investigator、reporter、reviewer、specialist-compute/disk/network/ops/sre 等 markdown 文件。运行时由 `cmd/ongrid` 装载到 AIOps agent 体系 |
| `api/` | Proto 接口定义（单一事实源） | `iam/v1/`、`manager/{aiops,alert,edge,metric,notification}/v1/`、`tunnel/v1/`；`buf.yaml`、`buf.gen.yaml`、`README.md` |
| `cmd/` | 可执行入口二进制 | `ongrid/`（Manager 入口）、`ongrid-edge/`（Edge 探针入口）；含若干 `*_test.go` 单测 |
| `deploy/` | 一键安装与运维物料 | `Dockerfile.{ongrid,ongrid-edge,web,frontier}`、`docker-compose.yml`、`install/{install,uninstall,upgrade}.sh`、`install/systemd/`、`install/edge/`、`prometheus/`、`grafana/`、`searxng/`、`nginx/`、`loki-config.yaml`、`prometheus.yml`、`tempo-config.yaml` |
| `dist/` | 构建产物（gitignored） | `make build` 输出的二进制 |
| `docs/` | 文档目录（**本文档所在层**） | `install/`、`test/`、`project/`、`workflow-catalog.md`、`assets/` |
| `internal/` | 业务代码主目录 | 见下文「BC 结构鸟瞰」 |
| `scripts/` | 一次性 / 辅助脚本 | `eval_aiops_full.py`、`sync-builtin-vault.sh` |
| `skills/` | Edge 端内置 skill 物料 | `bash/SKILL.md`、`host-files/SKILL.md`、`restart-service/SKILL.md` |
| `tests/` | 端到端与集成测试 | `e2e/`（Go E2E）、`integration/`（manager/edge 链路集成） |
| `web/` | 前端 SPA | React + Vite + Tailwind + Zustand，详见 [frontend-backend-contract.md](./frontend-backend-contract.md) |
| `AGENTS.md` | AI Agent 协作规约 | 项目内的硬约束（架构 / 编码 / API / 测试 / Git / 运维 / 数据） |
| `CODEOWNERS` | 代码所有权 | 每个目录的责任人映射 |
| `Makefile` | 构建 / 发布 / Proto 重新生成 | `make build`、`make proto`、`make test`、`make lint` 等 |
| `go.mod` / `go.sum` | Go 模块依赖 | 主入口模块 `github.com/ongridio/ongrid` |
| `README*.md` | 多语言 README | 8 种语言版本，中英文为主入口 |
| `ROADMAP.md` / `ROADMAP.zh-CN.md` | 版本路线图 | 公开的近期 / 中期 / 远期规划 |
| `SECURITY.md` | 安全漏洞披露流程 | 报告漏洞的方式与时效承诺 |
| `VERSION` | 当前版本号 | 由 `Makefile` 的 release 流程更新 |
| `CHANGELOG.md` | 版本变更日志 | 人工维护 |
| `.github/workflows/` | CI / CD | `ci.yml`（PR + 推送）、`release.yml`（发版） |
| `.golangci.yml` / `.go-arch-lint.yml` | Lint / 架构 lint 配置 | 强制分层、导入方向 |
| `.tool-versions` | 工具版本锁定 | Go、Node 等 |
| `.env.example` | 环境变量样例 | 所有 `ONGRID_*` 变量的默认值 |

---

## BC 结构鸟瞰（`internal/`）

`internal/` 按 bounded context（BC）划分，每个顶层目录 = 一个 BC，`monorepo` 约束下 BC 间**禁止直接 import**，统一走 `internal/pkg/`（共享工具）或经 API 通信：

| BC 目录 | 职责 | 入口 / 主要子包 |
| --- | --- | --- |
| `internal/iam/` | **身份与多租户**（Manager 启动即存在） | `biz/{user,org,membership,authz}`、`data/{user,org,membership}/...`、`model/`、`server/`、`service/` |
| `internal/manager/` | **运维主 BC**（设备/指标/告警/AI/Flow/IM…） | 子目录见下节「manager BC 鸟瞰」 |
| `internal/edgeagent/` | **边缘探针**（在每个被纳管主机上运行） | `bash/`、`biz/`（agent 主循环、upgrade、JSON 编解码）、`collector/`（指标采集）、`cmdpolicy/`（白名单/审计）、`host_files/`（文件操作）、`model/`、`plugins/`（插件注册中心）、`restart_service/`、`service/`、`skill/`、`webshell/` |
| `internal/skill/` | **跨 BC 共享的 skill 装载框架** | `loader.go` / `subprocess.go` / `registry.go` / `schema.go` / `types.go` + `builtin/`（Manager 自带 skill：bash、host_files、restart_service、web_search 等） |
| `internal/pkg/` | **横切工具库**（无业务依赖） | `auth`/`authzmw`、`config`、`dbx`、`docextract`、`embedding`、`errs`、`grafana`、`httpserver`、`llm`、`logger`、`logquery`、`mcpclient`、`notify`、`passwd`、`prom`/`promauth`/`promquery`/`promwrite`、`qdrantx`、`runner`、`secretbox`、`tenantctx`、`tracequery`、`tracing`、`tunnel`、`workspace`、`zhipuauth` |

---

## manager BC 鸟瞰（`internal/manager/`）

manager BC 内部按 **cmd → web → controlplane → repo → model** 五层划分（详见 [backend-data-flow.md](./backend-data-flow.md)），下表给出各子包对应的业务域与入口：

| 子包（`internal/manager/...`） | 业务域 | 入口 |
| --- | --- | --- |
| `model/` | 持久化实体（每域一个子目录） | `edge/`、`device/`、`alert/`、`flow/`、`metric/`、`aiops/`、`mcp/`、`knowledge/`、`secret/`、`marketplace/`、`topology/`、`audit/`、`monitor/`、`report/`、`setting/`、`approval/`、`imbridge/`、`webshell/` |
| `biz/` | 业务用例 + Repository 接口 | 与 model 平行同名的 18 个子目录 |
| `data/` | GORM 持久化实现 | 与 model 同名的 18 个 `store/` 子目录 |
| `server/` | chi HTTP handler（路由） | 18+ 个子目录，含 `middleware/`、`integration/`、`edgeauth/`、`systemhealth/`、`systemupgrade/` 等 |
| `service/` | 服务层（DTO 转换、参数校验、调用 biz） | `edge/`、`alert/`、`aiops/`、`aiopsconfig/`、`frontierbound/`（反向调用 Edge）、`metric/`、`prometheus/`、`systemhealth/`、`systemupgrade/` |
| `cmd/ongrid/` | Manager 二进制入口 | `main.go`（编连所有 BC，启动 HTTP + 反向隧道客户端） |

---

## 相关约定速查

- **架构**：`cmd → web → controlplane → repo → model` 单向依赖；接口在消费方定义；`utils/`/`errs/` 不依赖业务包；依赖通过构造函数注入，不使用全局变量。详见 `AGENTS.md` 与 [backend-data-flow.md](./backend-data-flow.md)。
- **API**：所有 API 变更先更新 `api/*.proto`；Handler 必须有 Swagger 注释；响应统一 `{code, message, data}`；破坏性变更走新版本。
- **测试**：单测必须带 `-race`；E2E 必须清理数据；详见 `docs/test/e2e-catalog.md`。
- **Git**：`<type>(<scope>): <desc>` Conventional Commits；禁止敏感信息入仓；禁止 force push main/master。
- **可观测性**：所有对外服务必须暴露 `/healthz`、`/readyz`、`/metrics`；日志结构化 + `trace_id`；敏感字段禁止明文入日志。
- **数据存储**：MySQL 是生产库（兼容 SQLite 开发）；PII 加密存储；测试环境禁止生产数据明文。

---

## 与其他文档目录的关系

- `docs/install/` — 部署、安装、运维一次 / 升级 / 回滚相关；与本文档平行但不重叠
- `docs/test/` — 测试用例与测试环境搭建；包含 `e2e-catalog.md`
- `docs/workflow-catalog.md` — 业务流程目录（incident → investigation → RCA → resolution 等），从业务视角组织，与本文档从工程视角组织正交互补
- `docs/assets/` — 文档用图片、GIF 等静态资源