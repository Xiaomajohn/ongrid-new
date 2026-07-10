---
generated_by: repo-wiki-agent
baseline_commit: "47bad98d46a1d70231d237285784a8192d7756c7"
last_updated: "2026-07-06"
managed_sections:
  - "## One-Click Install"
  - "## Edge Install"
  - "## Upgrade"
---

# 部署指南

<!-- BEGIN:REPO_WIKI_MANAGED -->
## One-Click Install

控制面一键安装：

```bash
cd deploy
./install/install.sh
```

脚本会：

1. 检测 OS / 架构
2. 拉起 Docker Compose（mysql / redis / prometheus / grafana / nginx / ongrid / frontier）
3. 等待 healthz 通过
4. 输出访问地址与默认账号

## Edge Install

边缘节点安装有两种入口：

1. **手动 SSH 安装**：`deploy/install/edge/install.sh <controller-url>`（参见 `docs/install/edge.md`）
2. **平台下发**：通过 Manager 的 install job，自动 curl-pipe 到目标主机

## Upgrade

```bash
./deploy/install/upgrade.sh
```

升级脚本会：

1. 备份当前 compose / 数据卷
2. 拉取新镜像 / 二进制
3. 滚动重启
4. 校验健康状态

<!-- END:REPO_WIKI_MANAGED -->

## 回滚

任何变更必须有回滚方案；P0/P1 事故需产 blameless postmortem（见 AGENTS.md 运维约束）。