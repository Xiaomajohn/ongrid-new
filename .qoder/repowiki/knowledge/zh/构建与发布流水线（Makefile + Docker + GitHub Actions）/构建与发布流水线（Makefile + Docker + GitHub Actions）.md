---
kind: build_system
name: 构建与发布流水线（Makefile + Docker + GitHub Actions）
category: build_system
scope:
    - '**'
source_files:
    - Makefile
    - dist/package.sh
    - dist/build-edge-bundle.sh
    - deploy/Dockerfile.ongrid
    - deploy/Dockerfile.ongrid-edge
    - deploy/Dockerfile.web
    - api/buf.yaml
    - .github/workflows/ci.yml
    - .github/workflows/release.yml
    - VERSION
---

## 体系概览

Ongrid 采用“单一 Makefile 为唯一入口”的构建模型：所有 CI、Dockerfile、README 都只调用 make target，禁止裸 go build / docker build。Go 后端、前端 SPA、边缘二进制、上游第三方插件（promtail/otelcol-contrib/node_exporter 等）以及 systemd 安装包全部由 Makefile 串联，最终产出单架构或全架构 tarball，并通过 GitHub Actions 在 tag 推送时并行构建 amd64/arm64 并创建 Release。

## 核心工件与目标

- 云端 ongrid：cmd/ongrid → bin/ongrid；Docker 镜像 deploy/Dockerfile.ongrid（多阶段，bookworm-slim 运行时，CGO 启用以加载 libonnxruntime）
- 边缘 ongrid-edge：cmd/ongrid-edge → bin/<os>-<arch>/ongrid-edge；镜像 deploy/Dockerfile.ongrid-edge（distroless/static-debian12，CGO=0）
- Web 前端 SPA：web/ 通过 Vite + TypeScript 编译到 web/dist/，镜像 deploy/Dockerfile.web（node:20-alpine 构建 + nginx:alpine 运行）
- Edge 插件二进制：promtail、otelcol-contrib、auditbeat、node_exporter、process_exporter、mysqld/postgres/redis/mongodb exporter，按 EDGE_PLUGIN_ARCHES（默认 linux-amd64+linux-arm64）拉取或从 resource/ 离线注入
- Release tarball：dist/out/ongrid-<VERSION>-linux-{amd64,arm64}.tar.xz + .sha256，内含 docker image tar、systemd 安装脚本、edge 安装器、stack-deps（prometheus/loki/tempo/qdrant）、可选嵌入模型

## 关键文件与职责

- Makefile：统一入口，build/test/lint/proto/migrate/docker/compose/package/patch 等全部 target
- dist/package.sh：打包编排，收集 stage 目录、docker save、提取 bin、组装 tar.xz 并生成 sha256
- dist/build-edge-bundle.sh：将 edge 松散二进制重打包为 ADR-024 升级 bundle
- deploy/Dockerfile.ongrid：云端服务镜像（CGO + onnxruntime）
- deploy/Dockerfile.ongrid-edge：边缘 agent 镜像（distroless）
- deploy/Dockerfile.web：前端 SPA + nginx 镜像
- api/buf.yaml：Proto lint/breaking 规则，配合 make proto 优先走 buf，回退 protoc
- VERSION：版本来源，被 $(shell cat VERSION) 读取并注入 -X main.version=
- .github/workflows/ci.yml：PR 快速门，go build/vet/test（不含 e2e）
- .github/workflows/release.yml：tag 触发，双架构并行 make package → 上传 artifact → 合并发布

## 架构与约定

1. 版本策略：VERSION 文件必须与 git tag vMAJOR.MINOR.PATCH 一致，release workflow 会校验二者相等，否则直接失败
2. 交叉编译矩阵：TARGET_OS/TARGET_ARCH 控制 manager 包目标；PLATFORM 需与之匹配（check-release-target 强制校验）。Edge 插件默认仅 linux-amd64+arm64（darwin 已丢弃），通过 EDGE_PLUGIN_ARCHES 覆盖
3. 依赖获取：GOPROXY 默认 https://goproxy.cn,https://goproxy.io,proxy.golang.org,direct，支持 --build-arg GOPROXY= 覆盖。HTTP_PROXY/HTTPS_PROXY/NO_PROXY 通过 FETCH_PROXY_FLAGS 自动注入 curl，同时作为 DOCKER_BUILD_PROXY_ARGS 传给 buildkit，使 apt/apk/npm/go 均可见
4. 离线约束：auditbeat 不在线下载，要求 operator 手动放入 resource/auditbeat/<arch>/auditbeat 后执行 make stage-auditbeat；缺失则硬失败
5. 增量补丁：make patch 基于 git diff 生成前后端+edge+sql 的轻量补丁包，适用于开发期反复迭代与紧急修复
6. 镜像缓存：Dockerfile 使用 --mount=type=cache 缓存 /go/pkg/mod、/root/.cache/go-build、/root/.npm、Vite cache，无变更构建可降至秒级

## 开发者应遵循的规则

- 一律通过 make 调用：不要直接跑 go build / docker build / npm run build，以免遗漏 ldflags、版本号注入或缓存优化
- 新增外部二进制依赖：在 Makefile 中增加对应的 fetch-xxx target（遵循现有 EDGE_PLUGIN_ARCHES 循环模式），并在 package 依赖链中注册
- Proto 变更：先 make proto 重新生成 stub，确保 buf lint 通过；若本地缺 buf，回退到 protoc 路径也需保证可用
- 跨平台构建：指定 TARGET_ARCH=arm64 PLATFORM=linux/arm64 即可打 arm64 包；make package-all 一次产出两个架构
- 离线 RAG 模型：首次运行 make fetch-embedding-model 预拉 BGE 模型到 .cache/，再 make package 才会将其打入 tarball
- CI 门禁：PR 仅触发 ci.yml（build/vet/test），e2e 需要单独环境，不应阻塞 PR 合并
- 发布流程：仅在 main 上打 v*.*.* tag 触发 release，workflow 会校验 VERSION 与 tag 一致并生成 latest.json 元数据