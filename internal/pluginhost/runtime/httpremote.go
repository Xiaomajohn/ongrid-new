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

	"github.com/ongridio/ongrid/internal/pluginhost/registry"
)

// 默认熔断 + 客户端超时参数。
const (
	defaultHTTPTimeout          = 30 * time.Second
	circuitBreakerFailThreshold = 5 // 连续失败次数,触发打开
	circuitBreakerOpenDuration  = 30 * time.Second
)

// ErrBreakerOpen 熔断器打开期间调用被快速失败。
var ErrBreakerOpen = errors.New("http runtime: circuit breaker open")

// httpRequest 是 POST {URL}/invoke 的请求 body。
type httpRequest struct {
	ID     string          `json:"id"`
	Cap    string          `json:"cap"`
	Params json.RawMessage `json:"params,omitempty"`
}

// httpResponseFrame 是 2xx 响应的解析形态。
type httpResponseFrame struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// CircuitBreaker 简易计数器式熔断器。
//
// 状态机(见 plan §9 + plan §17 可观测性):
//
//   - Closed(failures < threshold):
//       全部放行;RecordFailure 把 failures++。
//   - Open(failures >= threshold 且 time.Now() < openUntil):
//       全部拒绝 → 返 ErrBreakerOpen;探测期内不计数(已在开期)。
//   - Half-Open(time.Now() >= openUntil,failures >= threshold):
//       放行 1 次探测;探测成功 → RecordSuccess 关闭;
//       探测失败 → RecordFailure 重新打开(openUntil 续期)。
//
// 计数器只对 transport 层错误(5xx / 网络错误 / 解析失败)递增;
// 4xx 等客户端错误(参数错 / 鉴权错)不计数。
type CircuitBreaker struct {
	mu        sync.Mutex
	failures  int
	openUntil time.Time
}

// allow 判断本次调用是否被允许;true 表示可以发出请求。
//
// 副作用:进入 Half-Open 时**消耗唯一探测 token**(用一个微秒
// 级 openUntil 标记),防止探测并发跑飞。
func (cb *CircuitBreaker) allow() bool {
	if cb == nil {
		return true
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	now := time.Now()

	// failures 还在阈值下 → Closed,无条件放行
	if cb.failures < circuitBreakerFailThreshold {
		return true
	}

	// failures 触顶,但开期未到 → Open,直接拒绝
	if now.Before(cb.openUntil) {
		return false
	}

	// Open 期刚结束(openUntil 已过)→ 转入 Half-Open,放行 1 次探测;
	// 将 openUntil 置为 now+1us 标记探测已发,后续并发请求仍判 Open。
	cb.openUntil = now.Add(time.Microsecond)
	return true
}

// recordFailure 失败回调:递增 + 触顶时打开 openUntil。
func (cb *CircuitBreaker) recordFailure() {
	if cb == nil {
		return
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures++
	if cb.failures >= circuitBreakerFailThreshold {
		cb.openUntil = time.Now().Add(circuitBreakerOpenDuration)
	}
}

// recordSuccess 成功回调:清零失败计数 + 关闭熔断。
func (cb *CircuitBreaker) recordSuccess() {
	if cb == nil {
		return
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures = 0
	cb.openUntil = time.Time{}
}

// HTTPRemoteRuntime 通过 HTTPS + HMAC-SHA256 签名与远程 C 插件通信,
// 自带熔断保护。
//
// 协议(plan §9 + §7.5):
//   - method:POST {URL}/invoke
//   - body:JSON envelope {"id":...,"cap":...,"params":...}
//   - headers:
//        X-Plugin-ID        = plugin.PackID
//        X-Plugin-Signature = hex(HMAC-SHA256(Secret, body))
//        X-Trace-ID         = req.TraceID
//   - 响应:JSON {"id":...,"result":...} 或 {"id":...,"error":...}
//   - 超时:30s(可注入 Client 自定义 timeout)
type HTTPRemoteRuntime struct {
	URL     string          // 远端基础 URL(如 https://plugin.example.com)
	Secret  string          // HMAC 共享密钥
	Client  *http.Client    // http 客户端;默认 30s 超时
	Breaker *CircuitBreaker // 熔断器;可注入自定义
}

// NewHTTPRemoteRuntime 构造 HTTP remote runtime。
//
// Client 默认 30s 超时;Breaker 缺省自动初始化一个空熔断器
// (默认阈值 5 / 开期 30s)。
func NewHTTPRemoteRuntime(url string, secret string) *HTTPRemoteRuntime {
	return &HTTPRemoteRuntime{
		URL:     url,
		Secret:  secret,
		Client:  &http.Client{Timeout: defaultHTTPTimeout},
		Breaker: &CircuitBreaker{},
	}
}

// Invoke 同步调用远端 C 插件的 capability。
//
// 错误归类:
//   - ErrBreakerOpen:熔断打开,未发出请求。
//   - 其他 fmt.Errorf:网络错误 / 4xx / 5xx / 解析错误等。
//
// 熔断计数语义:
//   - 5xx → 计入失败;
//   - 网络错误(io.ErrUnexpectedEOF / timeout / connection refused)
//     → 计入失败;
//   - 4xx → 不计入失败(调用方参数错误,不是 transport 故障);
//   - 解析失败(body 不是合法 envelope)→ 计入失败。
func (h *HTTPRemoteRuntime) Invoke(ctx context.Context, plugin *registry.PluginInstance, cap *registry.Capability, req Request) (Response, error) {
	// 0. 熔断检查
	if !h.Breaker.allow() {
		return Response{}, ErrBreakerOpen
	}

	// 1. 构造请求 body
	env := httpRequest{ID: req.ID, Cap: req.CapName, Params: req.Params}
	body, err := json.Marshal(env)
	if err != nil {
		return Response{}, fmt.Errorf("http runtime: marshal envelope: %w", err)
	}

	// 2. 计算 HMAC-SHA256(secret, body) → hex
	mac := hmac.New(sha256.New, []byte(h.Secret))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	// 3. 构造请求(30s timeout via context)
	httpCtx, cancel := context.WithTimeout(ctx, defaultHTTPTimeout)
	defer cancel()
	endpoint := h.URL + "/invoke"
	httpReq, err := http.NewRequestWithContext(httpCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("http runtime: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Plugin-ID", plugin.PackID)
	httpReq.Header.Set("X-Plugin-Signature", sig)
	httpReq.Header.Set("X-Trace-ID", req.TraceID)

	// 4. 发送
	resp, err := h.Client.Do(httpReq)
	if err != nil {
		h.Breaker.recordFailure()
		return Response{}, fmt.Errorf("http runtime: do request: %w", err)
	}
	defer resp.Body.Close()

	// 5. 状态码分发
	if resp.StatusCode >= 500 {
		// 5xx:计入熔断失败计数
		h.Breaker.recordFailure()
		raw, _ := io.ReadAll(resp.Body)
		return Response{}, fmt.Errorf("http runtime: 5xx status %d: %s", resp.StatusCode, string(raw))
	}
	if resp.StatusCode >= 400 {
		// 4xx:客户端错误,不计入熔断
		raw, _ := io.ReadAll(resp.Body)
		return Response{}, fmt.Errorf("http runtime: 4xx status %d: %s", resp.StatusCode, string(raw))
	}

	// 6. 解析 2xx 响应
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		h.Breaker.recordFailure()
		return Response{}, fmt.Errorf("http runtime: read body: %w", err)
	}
	var frame httpResponseFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		h.Breaker.recordFailure()
		return Response{}, fmt.Errorf("http runtime: unmarshal response: %w; raw: %s", err, string(raw))
	}

	// 7. 成功:清零失败计数
	h.Breaker.recordSuccess()
	return Response{ID: frame.ID, Result: frame.Result, Error: frame.Error}, nil
}

// Close 是 no-op:HTTP transport 不持有长生命周期资源;连接池
// 由 http.Client 自身管理。若需主动释放空闲连接可后续扩展
// 为 client.CloseIdleConnections。
func (h *HTTPRemoteRuntime) Close() error {
	return nil
}
