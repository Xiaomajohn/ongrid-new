# syntax=docker/dockerfile:1.7
#
# gitlab-issue demo plugin 的打包示意(Phase 5+ 真实实现).
# 多阶段构建:builder 阶段编译 binary,最终镜像只保留 binary + manifest + README.
# 真实实现可改 source 路径(本 demo 不带 cmd 源码,见 README §1).
#

###############################
# Stage 1: builder
###############################
FROM golang:1.23-bookworm AS builder

# 实际实现:把 plugin 的 Go 源码 COPY 进来.
# 例如: ./gitlab-issue/cmd → /src/cmd
# WORKDIR /src
# COPY go.mod go.sum ./
# RUN go mod download
# COPY . .
# RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/gitlab-issue ./cmd/gitlab-issue
#
# 本 demo 任务 P1 不带源码,本阶段用占位 shell script 替代真实 binary:
RUN cat > /out/gitlab-issue <<'EOF'
#!/usr/bin/env bash
# 占位 binary:读取 stdin 上 B 发来的 JSON-RPC 请求,echo 一个空 result 回去.
# Phase 5+:替换成真实 Go 程序,用 stdin/stdout JSON 协议跑 plugin 逻辑.
while IFS= read -r line; do
  id=$(printf '%s' "$line" | grep -oE '"id"[[:space:]]*:[[:space:]]*"[^"]+"' | head -1 | sed -E 's/.*"([^"]+)"$/\1/')
  if [ -n "$id" ]; then
    printf '{"id":"%s","result":{"status":"demo_stub","note":"replace this binary in Phase 5+"}}\n' "$id"
  fi
done
EOF
RUN chmod +x /out/gitlab-issue

###############################
# Stage 2: runtime
###############################
FROM debian:bookworm-slim

# 真实实现可能需要:
#   ca-certificates: 走 https 调 gitlab.com
#   dumb-init:       PID 1 信号处理
#   tzdata:          跨时区时间戳
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates dumb-init tzdata \
 && rm -rf /var/lib/apt/lists/*

# 把 plugin 一切需要的东西搬运过来:manifest + 二进制 + (可选 docs)
COPY plugin.json /plugin/plugin.json
COPY README.md   /plugin/README.md
COPY --from=builder /out/gitlab-issue /plugin/bin/gitlab-issue

# pluginhost loader 按相对路径 `entry: ./bin/gitlab-issue` 找到 binary.
# 在 ongrid 服务器上,这个镜像的 /plugin 目录会被 bind-mount / COPY 到
# /etc/ongrid/plugins/gitlab-issue/ 子目录(pluginhost 启动时 LoadDirs 扫描).
WORKDIR /plugin

# dumb-init 接管 PID 1,正确转发 SIGTERM 给 binary
ENTRYPOINT ["/usr/bin/dumb-init", "--"]
CMD ["/plugin/bin/gitlab-issue"]
