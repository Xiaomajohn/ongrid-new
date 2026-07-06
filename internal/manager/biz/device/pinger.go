// Package device — Pinger 周期性对所有有 ssh_host 的设备跑 ICMP ping，
// 把结果通过 Repo.UpdateReachability 写回 devices 表的 reachable /
// last_reachable_at 字段。UI 端（web/src/pages/Hosts.tsx）按 reachable
// 渲染状态列，跟 edge agent 上报的 Online 解耦——Online 表达"agent 进程
// 在跑"，Reachable 表达"网络层 ping 这个 host 通了"，operator 视角
// 关心的是后者。
//
// 实现选择：exec 调系统 `ping` 命令而不是 golang.org/x/net/icmp raw
// socket。理由：
//   1. manager 在 distroless-ish 镜像里以 nonroot 跑（uid 65532），没有
//      CAP_NET_RAW，raw ICMP socket 会被内核拒绝；
//   2. iputils-ping 自带 cap_net_raw 文件属性（file capability），nonroot
//      也能用；我们只需要在 Dockerfile 安装 iputils-ping 包；
//   3. 跨部署环境（容器 / 物理机 / k8s）行为一致，统一走 binary。
package device

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Pinger 包装 exec 调用的并发 worker pool。Pinger 无状态——每次 RunAll
// 都从传入的 targets 重新构建 goroutine 池，单台 host 的 Ping 互不干扰。
type Pinger struct {
	// Timeout 单台 ping 的硬超时。默认 2s 覆盖最常见的"主机不可达"和
	// "网络丢包"两种场景；通过 ONGRID_PING_TIMEOUT_SEC 覆盖。
	Timeout time.Duration

	// Concurrency 一次 RunAll 中同时跑的 ping 数量。默认 16——规模在
	// 数百到数千台机器的部署下不会卡住 5min 周期。
	Concurrency int

	// Log 接受结构化 logger；nil-safe（fallback 到 discard）。
	Log *slog.Logger
}

// NewPinger 构造一个带默认参数的 Pinger。
func NewPinger(log *slog.Logger) *Pinger {
	return &Pinger{
		Timeout:     2 * time.Second,
		Concurrency: 16,
		Log:         log,
	}
}

// PingResult 是单台 host 一次 ping 的产出。reachable = false 也会带
// err 字段（人读得懂的诊断信息），写库时只取 reachable，err 只走日志。
type PingResult struct {
	Host      string
	Reachable bool
	Err       error
}

// Ping 对单台 host 跑一次系统 ping 命令。
//
// Linux 上用 `ping -c 1 -W <sec> <host>`；Darwin 上 `ping -c 1 -W <ms> <host>`
// 参数语义不同（Darwin 的 -W 单位是 ms）。为了部署可移植性，统一传秒，运行时
// 切到毫秒。Windows 留白（manager 主要跑在 Linux 容器内）。
//
// 返回值：reachable 表示网络层能否 ping 通；err 是诊断信息（timeout /
// exit code != 0 / binary 缺失），永远 non-nil 时 reachable=false。
func (p *Pinger) Ping(ctx context.Context, host string) PingResult {
	if host == "" {
		return PingResult{Host: host, Reachable: false, Err: fmt.Errorf("empty host")}
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	// 把 ctx-deadline 跟我们的 timeout 取最短值，避免在 pinger 启动后
	// 调用方取消（egCtx 关闭时）但子进程还在跑。
	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		// -W 单位是秒
		cmd = exec.CommandContext(pingCtx, "ping", "-c", "1", "-W", fmt.Sprintf("%d", int(timeout.Seconds())), host)
	case "darwin":
		// -W 单位是毫秒
		cmd = exec.CommandContext(pingCtx, "ping", "-c", "1", "-W", fmt.Sprintf("%d", int(timeout.Milliseconds())), host)
	default:
		// 其他 OS 退化到 -c 1（无超时）
		cmd = exec.CommandContext(pingCtx, "ping", "-c", "1", host)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return PingResult{Host: host, Reachable: false, Err: fmt.Errorf("ping %s: %w (out=%s)", host, err, truncate(string(out), 200))}
	}
	// 一些平台 `ping` 0 退出但 stdout 里有 "100% packet loss"——双保险。
	if isPacketLoss(out) {
		return PingResult{Host: host, Reachable: false, Err: fmt.Errorf("ping %s: 100%% packet loss", host)}
	}
	return PingResult{Host: host, Reachable: true}
}

// RunAll 对 targets 中每台 host 并发跑一次 ping，返回的 slice 顺序与
// targets 一致。空 targets 走 fast path 返回 nil。
//
// concurrency<=0 退化到 min(16, len(targets))。
func (p *Pinger) RunAll(ctx context.Context, targets []PingTarget) []PingResult {
	if len(targets) == 0 {
		return nil
	}
	conc := p.Concurrency
	if conc <= 0 {
		conc = 16
	}
	if conc > len(targets) {
		conc = len(targets)
	}
	results := make([]PingResult, len(targets))
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	for i, t := range targets {
		i, t := i, t
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = p.Ping(ctx, t.Host)
		}()
	}
	wg.Wait()
	return results
}

// PingTarget 是 RunAll 的入参形态——和 model.Device 解耦，让 pinger 不
// 直接依赖 model，方便单独 unit test。
type PingTarget struct {
	ID   uint64
	Host string
}

// truncate 把过长的 ping stdout 截断到 n 字节，避免日志被一个误填的
// 巨大 hostname 撑爆。n<=0 时返回原串。
func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// isPacketLoss 兜底检测"ping 命令退出 0 但实际全部丢包"的边界情况。
// 大多数实现不会触发，但 IPutils 之外的 ping（musl busybox 等）有先例。
func isPacketLoss(out []byte) bool {
	s := string(out)
	// 简单包含匹配；不要做正则避免无谓的 regexp 编译开销。
	subs := []string{"100% packet loss", "100.0% packet loss", "unreachable"}
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
