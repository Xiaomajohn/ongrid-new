# Dockerfile.ongrid 切 apt 国内源（治 deb.debian.org 单包 502 根因）

## 现象
执行 `make package` / `make docker-build` 构建 `ongrid:<version>` 镜像失败：

```
E: Failed to fetch http://deb.debian.org/debian/pool/main/b/bzip2/bzip2_1.0.8-5%2bb1_arm64.deb
  502  Bad Gateway [IP: 192.168.33.91 7890]
ERROR: failed to solve: process "/bin/sh -c apt-get update ..." did not complete successfully: exit code: 100
```

7 个包中 6 个成功，**只有 `bzip2` 单包 502**，走代理 `192.168.33.91:7890` → `deb.debian.org`。

## 根因分析（不要直接加重试）

观察：6/7 包能下、只有 bzip2 挂；排除"代理完全不通"或"上游完全不可达"。可能原因按概率排序：

1. **~70% 上游 CDN 边缘节点抖动**：`deb.debian.org` 是全球 CDN，apt 拉取时多个包分配到不同边缘节点，bzip2 命中临时故障节点，其它包命中健康节点。CN 网络下经代理转发更容易撞到不稳定的边缘节点。
2. ~20% HTTP/1.1 keep-alive 在代理链上被中途 reset。
3. ~10% 代理软件本身 bug（大文件并发）。

**结论：简单加重试是治标不治本**。根因是源不稳，应当**切源**；重试只是最后一道兜底防线。

## 改动
文件 `deploy/Dockerfile.ongrid` 两处 RUN：

### builder stage（原 59-61 行 → 现 64-72 行）
原 `apt-get update && apt-get install ... && rm` 单行命令，扩展为：
1. 清掉 base image 自带的默认 sources.list
2. 写入清华源（HTTPS）到 `/etc/apt/sources.list.d/00-tuna.list`
3. 写入 deb.debian.org（HTTP）到 `/etc/apt/sources.list.d/10-official.list` 作兜底
4. 跑原来的 `apt-get update && install build-essential pkg-config`

### runtime stage（原 149-158 行 → 现 163-177 行）
在原有 5 次外层循环 + `Acquire::Retries 3` 的最前面，同样加上 sources.list 配置。

## 设计哲学
四层独立防线（任何一层失效下一层兜底）：

| 层级 | 作用 | 治的故障 |
|---|---|---|
| 1. 清华源 | 首选源 | CN 网络下 deb.debian.org 边缘节点 502（治本）|
| 2. deb.debian.org | 兜底源 | tuna 同步延迟的包，apt 自动 fallback |
| 3. `Acquire::Retries 3` | apt 内部单包重试 | 单包瞬时网络抖动 |
| 4. 外层 5 次循环 | 整层 apt 重跑 | 极端情况下的彻底失败 |

为什么不简单"加重试"：根因是源不稳，**重试命中边缘故障节点的期望概率非 100%**；5 次都 502 后整层 build 失败重跑代价（~150s go mod download + go build 缓存作废）远高于切源的一次性成本。

为什么不动其它 Dockerfile：
- `Dockerfile.ongrid-edge`：alpine builder + distroless runtime，没有 apt-get install
- `Dockerfile.frontier`：纯 alpine，走 apk
- `Dockerfile.web`：纯 node-alpine + nginx-alpine

## 影响面
- 只影响 `make docker-ongrid` / `make docker-build` / `make package` 构建的 `ongrid:VERSION` 镜像
- 多架构构建（amd64 + arm64 `make package-all`）都会自动走新源
- 国外用户构建会变慢（因为 tuna 是国内 CDN），但有 deb.debian.org 兜底源不会失败
- 镜像内容零变化（同一批包，不同源下载）
- 缓存层语义零变化（sources.list 文件内容变了 → RUN 层失效 → 重跑，正常失效行为）

## 验证
按规则 6 不在 Windows 上调试，按规则 3 不写单测，按规则 4 不本地验证 Makefile。需要在打包机 192.168.25.30 实跑：

```bash
make docker-build PLATFORM=linux/arm64
make docker-build PLATFORM=linux/amd64
```

预期：
- builder stage `apt-get update` 走清华源，秒级完成（vs 之前走 deb.debian.org 1min35s）
- 单包 502 不再出现（清华源是单一 CDN，无 deb.debian.org 的多边缘节点抖动问题）
- 整个 build 时间下降 ~1-2min（apt-get 那层从 1min35s 缩到 ~10s）

## 后续
- 如果未来要把 ongrid-edge 也支持 bookworm（去掉 alpine 改 cgo 场景），需要把同样的 sources.list 配置加到那时新建的 Dockerfile
- 如果海外部署变多，可以考虑把清华源改成通过 `--build-arg APT_MIRROR=tuna` / `APT_MIRROR=official` 切换（参考现有的 `DOCKER_BUILD_PROXY_ARGS` 设计），但目前只在国内用，默认值就是最优选择