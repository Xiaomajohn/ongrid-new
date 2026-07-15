package runtime

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// 默认熔断参数。
const (
	defaultBreakerThreshold    = 5
	defaultBreakerOpenDuration = 30 * time.Second
	defaultHTTPTimeout         = 30 * time.Second
)

// ErrCircuitOpen 熔断器打开,请求被快速失败。
var ErrCircuitOpen = errors.New("http runtime: circuit open")

// ErrRemote5xx 远程 5xx 响应(计入熔断失败计数)。
var ErrRemote5xx = errors.New("http runtime: remote 5xx")

// CircuitBreaker 简易熔断器。
//
// 状态机:
//   - Closed:failureCount < threshold,所有请求放行。
//   - Open:failureCount >= threshold,openUntil 之前所有请求拒绝。
//   - Half-Open:openUntil 过期后下一次 Allow 进入探测期,放行 1 次;
//     探测成功 → RecordSuccess 关闭;探测失败 → RecordFailure 重新打开。
//
// 计数器只对 transport 层错误(5xx / 网络错误)递增,4xx 等客户端错误不计数。
type CircuitBreaker struct {
	mu            sync.Mutex
	failureCount  int
	openUntil     time.Time
	halfOpenToken bool
	threshold     int           // 触发打开的连续失败次数,默认 5
	openDuration  time.Duration // 打开后保持时间,默认 30s
}

// NewCircuitBreaker 构造默认配置的熔断器(threshold=5,openDuration=30s)。
func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{
		threshold:    defaultBreakerThreshold,
		openDuration: defaultBreakerOpenDuration,
	}
}

// Allow 判断当前请求是否被允许。
//
//   - Open 期内直接拒绝(openUntil > now 且无探测 token);
//   - Open 期刚结束(now > openUntil)→ 进入 Half-Open,放行 1 次探测;
//   - Closed / 已冷却 → 直接放行。
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	now := time.Now()
	if now.Before(cb.openUntil) {
		// Open 期内的探测 token(用于半开)
		if cb.halfOpenToken {
			cb.halfOpenToken = false
			return true
		}
		return false
	}
	// openUntil 已过:进入 Half-Open,发放 1 个探测 token,重置计数。
	if !cb.openUntil.IsZero() {
		cb.halfOpenToken = true
		cb.openUntil = time.Time{}
		cb.failureCount = 0
		return true
	}
	return true
}

// RecordSuccess 记录一次成功:清零失败计数、关闭熔断。
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failureCount = 0
	cb.openUntil = time.Time{}
	cb.halfOpenToken = false
}

// RecordFailure 记录一次失败。Half-Open 探测失败立即重新打开;
// Closed 状态累加计数,达到阈值后打开。
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	// Half-Open 探测失败:无论阈值直接重开,确保熔断"真起作用"。
	if cb.halfOpenToken == false && !cb.openUntil.IsZero() {
		// 不可能进入此分支(openUntil 重置后才设 halfOpenToken),保留防御
	}
	if !cb.openUntil.IsZero() && cb.failureCount >= cb.threshold {
		// 已开,再失败只是续期 openUntil
		cb.openUntil = time.Now().Add(cb.openDuration)
		cb.halfOpenToken = false
		return
	}
	cb.failureCount++
	if cb.failureCount >= cb.threshold {
		cb.openUntil = time.Now().Add(cb.openDuration)
		cb.halfOpenToken = false
	}
}

// httpEnvelope 是 HTTP 远端的 JSON-RPC 请求体。
type httpEnvelope struct {
	ID     string          `json:"id"`
	Cap    string          `json:"cap"`
	Params json.RawMessage `json:"params,omitempty"`
}

// httpResponseFrame 是 HTTP 远端返回的 JSON 帧。
type httpResponseFrame struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// HTTPRemoteRuntime 通过 HTTPS + HMAC-SHA256 签名与远程 C 插件通信,
// 自带熔断保护。
//
// 协议:POST {URL}/invoke,body 为 JSON envelope;
// 头部 X-Plugin-Signature = hex(HMAC-SHA256(Secret, body));
// 响应 body 为 JSON,字段同 stdio。
type HTTPRemoteRuntime struct {
	URL     string        // 远端基础 URL(如 https://plugin.example.com)
	Secret  string        // HMAC 共享密钥
	Timeout time.Duration // 单次调用超时,默认 30s
	Client  *http.Client  // 可由调用方注入自定义 transport
	Breaker *CircuitBreaker
}

// NewHTTPRemoteRuntime 构造 HTTP remote runtime;timeout <= 0 走默认 30s;
// Breaker 缺省自动 NewCircuitBreaker。
func NewHTTPRemoteRuntime(url, secret string, timeout time.Duration) *HTTPRemoteRuntime {
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	return &HTTPRemoteRuntime{
		URL:     url,
		Secret:  secret,
		Timeout: timeout,
		Client:  &http.Client{Timeout: timeout},
		Breaker: NewCircuitBreaker(),
	}
}

// Invoke 同步调用远端 C 插件的 capability。
//
// 错误返回语义:
//   - ErrCircuitOpen:熔断打开,未发出请求。
//   - ErrTimeout:ctx 超时或客户端 timeout。
//   - ErrRemote5xx:远端返回 5xx,已计入熔断失败。
//   - 其他 fmt.Errorf:网络错误、4xx 响应、解析错误等。
func (h *HTTPRemoteRuntime) Invoke(ctx context.Context, plugin *PluginInstance, cap *Capability, req Request) (Response, error) {
	if h.Breaker != nil && !h.Breaker.Allow() {
		return Response{}, ErrCircuitOpen
	}

	// 1. 构造 envelope body
	env := httpEnvelope{ID: req.ID, Cap: req.CapName, Params: req.Params}
	body, err := json.Marshal(env)
	if err != nil {
		return Response{}, fmt.Errorf("http runtime: marshal envelope: %w", err)
	}

	// 2. 计算 HMAC-SHA256(secret, body) → hex
	mac := hmac.New(sha256.New, []byte(h.Secret))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	// 3. 构造请求
	endpoint := h.URL + "/invoke"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("http runtime: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Plugin-ID", plugin.PackID)
	httpReq.Header.Set("X-Plugin-Signature", sig)
	httpReq.Header.Set("X-Request-ID", req.ID)

	// 4. 执行
	resp, err := h.Client.Do(httpReq)
	if err != nil {
		if h.Breaker != nil {
			h.Breaker.RecordFailure()
		}
		if ctx.Err() == context.DeadlineExceeded {
			return Response{}, ErrTimeout
		}
		return Response{}, fmt.Errorf("http runtime: do request: %w", err)
	}
	defer resp.Body.Close()

	// 5. 状态码分发
	if resp.StatusCode >= 500 {
		if h.Breaker != nil {
			h.Breaker.RecordFailure()
		}
		return Response{}, ErrRemote5xx
	}
	if resp.StatusCode >= 400 {
		// 4xx 是客户端错误(参数 / 鉴权 / 路由错),不计入熔断。
		raw, _ := io.ReadAll(resp.Body)
		return Response{}, fmt.Errorf("http runtime: status %d: %s", resp.StatusCode, string(raw))
	}

	// 6. 解析 2xx 响应
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		if h.Breaker != nil {
			h.Breaker.RecordFailure()
		}
		return Response{}, fmt.Errorf("http runtime: read body: %w", err)
	}
	var frame httpResponseFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		if h.Breaker != nil {
			h.Breaker.RecordFailure()
		}
		return Response{}, fmt.Errorf("http runtime: unmarshal response: %w", err)
	}
	if h.Breaker != nil {
		h.Breaker.RecordSuccess()
	}
	return Response{ID: frame.ID, Result: frame.Result, Error: frame.Error}, nil
}

// Close 关闭底层 http.Client 的空闲连接(CloseIdleConnections 不会返错)。
func (h *HTTPRemoteRuntime) Close() error {
	if h.Client != nil {
		h.Client.CloseIdleConnections()
	}
	return nil
}
