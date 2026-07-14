# 2026-07-14 — 移除 ingester 写入 edge_ts_ms 动态 label

## 问题
监控详情页（/edges/1）的 CPU、网络入/出三个 panel 都显示 "无数据"，磁盘 panel 也有图例重复问题。

根因在 **internal/manager/biz/promwrite/ingester.go**：
- ingester 把每个 sample 的 `edge_ts_ms` (edge 本地事件时间) 注入为 Prom label
- `edge_ts_ms` 是个**动态 label** — 每次 scrape 都不同
- 同 `(cpu, mode, device)` 的样本被拆成 N 个独立 series，每个 series 内仅 1 个点
- 结果：所有依赖时间序列 monotonic 性的 PromQL 函数（rate / irate / deriv 等）全部返回空
- `avg by(cpu) (rate(node_cpu_seconds_total[2m]))` 返回 `matrix: []`

原意是 post-hoc 校准（查漂移机器用），但**没有读取消费方**，反而把核心监控指标打坏了。

## 修复
去掉 ingester 中 edge_ts_ms label 注入。同时去掉 `for k, v := range s.Labels` 里 "edge_ts_ms" reserved case。
Prom sample 时间戳已锚定 manager 时钟（ServerTimeMs），是权威时间。edge 本地事件时间如有 drift
需求，应走独立 `ongrid_internal_clock_skew_seconds` 指标，不污染主指标 series。

## 兼容性
- 老 edge 不会因新 manager 丢 label 而失败（label 是云端加的，不存在双向协议）
- 老 Prom 数据里有 edge_ts_ms series，不影响新查询（新查询会直接 ignore 老 series）
- DeviceMetricsPanels 前端的 `avg by(cpu/device)` 显式聚合仍然保留作为兜底

## 部署
- 重新 build manager 二进制
- 替换 /opt/ongrid/ongrid-manager/manager 二进制
- 重启 ongrid 容器
- 等下一个 scrape 周期（edge push 间隔通常 15s-30s）后所有 panel 即可出数据
