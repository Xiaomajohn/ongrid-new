package hostcall

import (
	"sync"
	"time"
)

// 默认 token bucket 速率(每分钟 N 次)。
const defaultRatePerMinute = 60

// tokenBucket 是单 plugin 的 token bucket 状态。
//
// 算法:每次 Allow 时按 elapsed * rate/60s 补充 token(上界 rate),
// 消耗 1 token;tokens < 1 → Allow 返 false。
//
// dead 为 true 时 Kill 触发的紧急隔离,Allow 永远 false,直到 Unkill
// 重新激活(release 在 Phase 3 admin 工具里实现,这里只暴露 Kill)。
type tokenBucket struct {
	tokens     int
	lastRefill time.Time
	dead       bool
}

// Limiter 是 plugin → hostcall 的限流器,token bucket 实现。
//
// rate 是初始 / 满桶 token 数,默认 60/min;refill 速率与 rate 等比
// (rate/60 token 每秒)。Phase 1 阶段 rate 在 NewLimiter 一次性设死,
// 暂不暴露 SetRate;Plugin 进程重启后限流状态自然清空。
//
// Kill 是紧急隔离入口(回滚矩阵 plan §14.6):某个 plugin 的 hostcall
// 触发 A panic / 雪崩 / 异常耗时,admin 工具可以调 Kill 让该 plugin
// 暂时调不动 A(Allow 永远 false),无需重启 pluginhost。
type Limiter struct {
	mu sync.Mutex

	buckets map[uint64]*tokenBucket

	// rate 是单 bucket 的 token 数上限 + 每分钟补充 token 数(双语义)。
	// 默认 60;<=0 视为 60。
	rate int
}

// NewLimiter 构造空 Limiter。
//
// ratePerMinute <= 0 走默认 60;单 bucket 容量 = ratePerMinute,refill
// 速率 = ratePerMinute/60 token/s(linear refill,无 burst 上限调整)。
func NewLimiter(ratePerMinute int) *Limiter {
	if ratePerMinute <= 0 {
		ratePerMinute = defaultRatePerMinute
	}
	return &Limiter{
		buckets: make(map[uint64]*tokenBucket),
		rate:    ratePerMinute,
	}
}

// Allow 判断 pluginID 是否还有 token 可用。
//
// 首次见到的 pluginID 自动 lazy-create bucket(满 token);dead bucket
// 永远 false。
func (l *Limiter) Allow(pluginID uint64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[pluginID]
	if !ok {
		b = &tokenBucket{
			tokens:     l.rate,
			lastRefill: now,
		}
		l.buckets[pluginID] = b
		return true
	}

	if b.dead {
		return false
	}

	// 距离上次 refill 的时间(秒),按 rate/60 补 token。
	elapsed := now.Sub(b.lastRefill).Seconds()
	if elapsed > 0 {
		refill := int(elapsed * float64(l.rate) / 60.0)
		if refill > 0 {
			b.tokens += refill
			if b.tokens > l.rate {
				b.tokens = l.rate
			}
			b.lastRefill = now
		}
	}

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Kill 紧急隔离 pluginID:后续 Allow 永远返 false。
//
// 用于:该 plugin 触发 A panic / 雪崩 / 异常耗时,admin 临时封禁。
// 不释放 bucket;Unkill 是 Phase 3 admin 工具的事,这里只暴露 Kill。
func (l *Limiter) Kill(pluginID uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[pluginID]
	if !ok {
		b = &tokenBucket{lastRefill: time.Now()}
		l.buckets[pluginID] = b
	}
	b.dead = true
	b.tokens = 0
}

// Reset 摘除 pluginID 的 bucket,下次 Allow 会重新 lazy-create 一个满 bucket。
//
// 与 Kill 不同,Reset 用于"重置状态"而非"隔离";常用于 Phase 3 admin
// 工具的"解封"。
func (l *Limiter) Reset(pluginID uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, pluginID)
}

// Rate 返回当前配置的 ratePerMinute(只读)。
func (l *Limiter) Rate() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rate
}

// Tokens 返回 pluginID 当前剩余 token(诊断/调试用)。
//
// dead bucket 返 -1;未创建过的 bucket 返 -2(表示未触发任何 hostcall)。
func (l *Limiter) Tokens(pluginID uint64) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[pluginID]
	if !ok {
		return -2
	}
	if b.dead {
		return -1
	}
	return b.tokens
}
