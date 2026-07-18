# 2026-07-17 — 代码层回滚 v3.3 ServerTimeMs 路径剩余残留

## 背景

312f2a89 (`revert(timestamp): 还原 Prom 数据流中用 ongrid 时间替换 edge 采样时间的修改`)
已回滚 v3.3 路径主链路（ingester、scrape、tunnel messages、agent 字段、heartbeat handler）。
0eada550 的 tsCounter 防撞机制在更早的 ea72a4e5 (`Revert "```"`) 已被 revert 干净，HEAD 上不存在 tsCounter 相关代码（`git grep tsCounter|tickCounter|tickOffsetMs` 0 命中）。

回滚后仍有 2 项代码层残留与回滚后实现矛盾，按用户决策"配置不动、只回滚代码"清理。

## 用户决策

duplicate sample 报错的真实根因是 192.168.25.56 本地时钟偶发毫秒级卡顿/跳变（forward drift 机器长时间未外部锚定的常见伴生现象），不是 tsCounter 缺失：
- 0eada550 即使复活也无效 — 它内部是 `if serverTimeMs > 0 { 叠加 tickOffset } else { 不加偏移 }`，312f2a89 已把 serverTimeMs 锚点整个删了
- 192.168.25.56 v3.3 之前的长期运行从未撞 duplicate sample
- 偶发毫秒级卡顿是机器时钟物理特性，比改整个数据流时间戳策略划算得多
- 结论：不复活 0eada550

## 改动项

### 1. `AGENTS.md` 时钟管理硬规则精简

文件：`AGENTS.md` 第 102-106 行 `### 时钟管理` 整段。

删除"数据时间戳策略"那条（与回滚后实现矛盾：edge 本地时间路径下没有"用 ongrid 服务端时间作数据时间戳"的机制了）。保留三条：
- **禁止 NTP 校时**（用户决策硬规则）
- **漂移机器记录**（用户决策硬规则）
- **审计**（review 拒绝 NTP/时钟修改 PR）

### 2. `internal/edgeagent/biz/agent.go` 注释简化

文件：[agent.go:379-384](file:///f:/Code/Go/运维/ongrid-new/internal/edgeagent/biz/agent.go#L379-L384)

删除注释中"不允许修改本地系统时钟, 详见 AGENTS.md 时钟管理硬规则"这段引用（与上一步联动），改为只保留 wire 兼容说明：

```go
// resp.ServerTime 字段保留以维持 register_edge wire 兼容 (老 edge 仍在读),
// agent 不消费它.
```

## 不动项

- `deploy/install/loki-config.yaml` `creation_grace_period: 720h`（按用户决策保留）
- 0eada550 的代码（当前 HEAD 不存在，"回滚"无意义）
- 192.168.25.56 时钟漂移本身（按 AGENTS.md 漂移机器记录硬规则，仅作事故登记，不在代码修）

## 验证

- `git grep "数据时间戳策略"` 0 命中
- `git grep "时钟管理硬规则"` 只剩 AGENTS.md 自身引用（agent.go 注释不再交叉引用）
- `go vet ./...` 在改动范围内不报错

## 关联文件

- 主回滚：`312f2a89 revert(timestamp): 还原 Prom 数据流中用 ongrid 时间替换 edge 采样时间的修改`
- tsCounter revert：`ea72a4e5 Revert "\`\`\""`（0eada550）
- 时钟漂移事故登记：`.record/2026-07-12-ntp-192-168-25-56.md`
- v3.3 方案设计：`.record/2026-07-12-metrics-timestamp-server-time-anchored.md`（supseded by 312f2a89 + 本文件）
