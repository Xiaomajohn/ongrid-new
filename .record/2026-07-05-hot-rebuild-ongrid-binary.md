# 2026-07-05 手动重打 ongrid 云端二进制并热替换

## 背景
日常开发需要快速重打 `ongrid` 云端二进制打到当前线上 arm64 部署，跳过 `make package` 完整链路（不重建 docker 镜像，不走 image load；docker-compose 把 `${ONGRID_APP_DIR}/ongrid` bind-mount 到容器内的 `/ongrid:ro`，直接替换文件后 `restart` 即可拿到新逻辑）。

## 改动
未改任何源码。本文件是构建/部署操作日志，不是变更记录。

## 关键命令（从 Makefile 提取）

1. 编译（取自 `build-ongrid`，即 `$(GO_BUILD) -o bin/ongrid ./cmd/ongrid`）：
```bash
VERSION=$(cat VERSION)
mkdir -p bin
go build -trimpath -ldflags "-X main.version=$VERSION" -o bin/ongrid ./cmd/ongrid
```
   - `GO_BUILD := go build -trimpath -ldflags '$(LDFLAGS)'`
   - `LDFLAGS := -X main.version=$(VERSION)`
   - 当前环境 `linux/arm64, CGO_ENABLED=1`（fastembed-go/onnxruntime 是 cgo，必须开 cgo，与 Dockerfile.ongrid 的 `CGO_ENABLED=1` 保持一致）

2. 替换运行目录二进制（先停容器，否则触发 "文本文件忙" — bind-mount 文件被进程持有）：
```bash
cd /opt/ongrid
docker compose stop ongrid
cp -v /path/to/bin/ongrid /opt/ongrid/ongrid-app/ongrid
docker compose start ongrid
```

3. 验证：
```bash
docker exec ongrid /ongrid --version          # 期望: ongrid v0.9.0 starting + 配置 loaded
docker exec ongrid curl -sf http://127.0.0.1:8080/healthz   # 期望: ok
docker exec ongrid curl -sf http://127.0.0.1:8080/readyz    # 期望: ready
curl -sf http://127.0.0.1:9100/metrics | head              # metrics 端口映射到 host
```

## 目的
- 重打本地变更加载到现有 arm64 部署时，跳过完整镜像重建
- `ongrid` 容器 `image: ongrid:${ONGRID_VERSION}` 不变，但 `/ongrid` 由 bind-mount 覆盖 → 重启容器即拿到新逻辑
- 适用于源码热调试、紧急修复、镜像 tag 不变的灰度

## 不要做的事
- 不要用 `make build-linux`（CGO_ENABLED=0）替代上面命令：`build-linux` 是 host-side 交叉编译变体，缺 fastembed-go cgo 依赖，链接出来的二进制在容器内会因缺 onnxruntime .so 起不来
- 不要直接 `cp` 覆盖正在运行的 bind-mount 文件，必然 "文本文件忙" — 必须先 `docker compose stop`
- 不要改 `${ONGRID_VERSION}` 来换镜像路径：本流程的目标就是绕过镜像层做 hot-swap
