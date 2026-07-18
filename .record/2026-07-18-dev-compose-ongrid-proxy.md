# 2026-07-18 — 本地开发 compose 给 ongrid 容器加入代理环境变量

## 需求

`deploy/docker-compose.yml`（本地开发用 compose）的 `ongrid` 服务在企业内网环境下会因没有代理配置而失败：要么拉不到 LLM / 嵌入模型 / 外部 webhook，要么把对 `loki` / `prometheus` 等内部服务的访问也错误地走了出口代理。

要求：
1. `HTTP_PROXY` / `HTTPS_PROXY` 默认沿用宿主系统的值（系统没设就不加，不强加代理）
2. `NO_PROXY` **不**沿用宿系统的值，必须按项目内部服务名硬编码屏蔽，确保 ongrid → loki / prometheus / mysql / grafana / ... 走 docker 内部网络

## 设计要点

- **代理源 = 宿主机环境**：`HTTP_PROXY: ${HTTP_PROXY:-}` 这种写法让 docker-compose 直接从宿主进程环境继承；空值时 docker 会写入空字符串，Go net/http / curl / Python urllib 都把空字符串当作「无代理」处理，等价于"不加代理"
- **NO_PROXY 不走宿主**：直接给项目默认列表作为兜底值，覆盖本 compose 文件里出现的全部 service 名 + `localhost` / `127.0.0.1` + `.svc` / `.cluster.local`（为以后接 k8s 留余地）
- **大小写同时写**：Go `net/http.ProxyFromEnvironment` 默认读小写，curl / Python urllib / 各语言 SDK 大小写敏感情况不一；同一值同时写到 `HTTP_PROXY` / `http_proxy` 两份，避免「容器跑起来发现代理没生效」的隐性 bug
- **回退链**：小写优先用宿主小写，没设再回退宿主大写 — 兼顾两种主流 shell 习惯

## 与生产 compose 的差异

`deploy/install/docker-compose.yml` 的 ongrid 服务已经有一套代理配置（`ONGRID_HTTP_PROXY` 中间变量 + install.sh/upgrade.sh 自动检测写盘）。本次改动**不复用** `ONGRID_*_PROXY` 中间变量，原因：
- `deploy/docker-compose.yml` 是开发 compose，没有 install.sh 在前台跑、自动写 `.env` 这一步
- 开发场景里用户希望"启动 `docker compose up` 直接走宿主代理"，引入中间变量反而要手工编辑 `.env`，违背了"零配置"的诉求
- 两份 compose 隔离的目的就是 dev 链路简单直白、prod 链路交给 install.sh 兜底

## 改动

只动一个文件：`deploy/docker-compose.yml`，ongrid 服务的 `environment` 块。

```yaml
      # Outbound proxy — HTTP_PROXY / HTTPS_PROXY default to the host's
      # own proxy so a developer running on a corporate network doesn't
      # have to edit this file. If the host has neither set, the values
      # are left empty (no proxy — Go net/http / curl / Python urllib all
      # treat an empty string as "no proxy"). NO_PROXY is intentionally
      # NOT inherited from the host: it is hard-pinned to the docker-
      # internal service names so ongrid → loki / prometheus / mysql /
      # grafana / ... traffic stays on the compose network and never
      # traverses the corp proxy. Both case-spellings are written so
      # Go's net/http (lowercase by default) + Python urllib / curl
      # (case-sensitive on the convention) all see the same value.
      HTTP_PROXY: ${HTTP_PROXY:-}
      HTTPS_PROXY: ${HTTPS_PROXY:-}
      NO_PROXY: ${NO_PROXY:-localhost,127.0.0.1,mysql,prometheus,loki,tempo,qdrant,searxng,grafana,ongrid,nginx,frontier,.svc,.cluster.local}
      http_proxy: ${http_proxy:-${HTTP_PROXY:-}}
      https_proxy: ${https_proxy:-${HTTPS_PROXY:-}}
      no_proxy: ${no_proxy:-${NO_PROXY:-localhost,127.0.0.1,mysql,prometheus,loki,tempo,qdrant,searxng,grafana,ongrid,nginx,frontier,.svc,.cluster.local}}
```

插入位置：ongrid service 的 `environment:` 内、`ONGRID_ADMIN_PASSWORD` 之后、`# No ./data bind mount ...` 注释之前（即与现有 notify / admin 一段连成一片，新增字段不再额外分段）。

## NO_PROXY 默认列表的服务名来源

来自 `deploy/docker-compose.yml` `services:` 下实际定义的容器名：

| 容器名 | 角色 |
|--------|------|
| mysql | 数据库 |
| prometheus | 指标 |
| loki | 日志 |
| tempo | 链路 |
| qdrant | 向量库 |
| searxng | 外部搜索聚合 |
| grafana | 仪表盘 |
| ongrid | manager 本体 |
| nginx | 反代 |
| frontier | tunnel broker |

加上 `localhost` / `127.0.0.1` 兜底 loopback，加 `.svc` / `.cluster.local` 为以后切 k8s 留口（不改默认值即可生效）。

## 验证

按项目开发规则：
- 本地不跑 .sh / make / docker compose（参考规则第 6 条：禁止在 Windows 上调试 Linux 上的脚本与打包程序）
- 本地不写单元测试（参考规则第 3 条）
- 静态校验：Python `yaml.safe_load` 解析通过、缩进与同段其他键一致（6 空格）、`${VAR:-default}` 是 docker-compose 标准 .env 替换语法被内置支持
- 实际端到端：开发机在企业内网下 `export HTTPS_PROXY=http://proxy.corp:8080 && docker compose up -d ongrid`，再 `docker exec ongrid env | grep -i proxy` 应能看到代理已注入；`docker exec ongrid wget -qO- http://loki:3100/ready` 应能直连不走代理（命中 NO_PROXY）

## 关联文件

- `deploy/docker-compose.yml` — ongrid service `environment` 块新增 17 行（含注释）
- `deploy/install/docker-compose.yml` — 生产 compose 已有的代理参考，本次未改动
- `deploy/install/install.sh` — 生产侧自动检测代理写盘逻辑，本次未改动
- `deploy/install/upgrade.sh` — 生产侧升级时合并 NO_PROXY 逻辑，本次未改动
