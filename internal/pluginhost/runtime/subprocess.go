package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// stdioEnvelope 是写入子进程 stdin 的 JSON 帧。
type stdioEnvelope struct {
	ID     string          `json:"id"`
	Cap    string          `json:"cap"`
	Params json.RawMessage `json:"params,omitempty"`
}

// stdioResponse 是从子进程 stdout 读取的 JSON 帧。
type stdioResponse struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// defaultSubprocessTimeout 是 Plugin.TimeoutSeconds 未设置或 <=0 时的兜底超时。
const defaultSubprocessTimeout = 30 * time.Second

// ErrTimeout 子进程调用超时(超过 Plugin.TimeoutSeconds 或 ctx deadline)。
var ErrTimeout = errors.New("subprocess: timeout")

// ErrSubprocessCrashed 子进程在响应到达前已退出。
var ErrSubprocessCrashed = errors.New("subprocess: crashed")

// ErrSubprocessPanic transport 自身 panic 被 recover 兜底。
var ErrSubprocessPanic = errors.New("subprocess: panic")

// SubprocessRuntime 通过 stdin/stdout JSON-RPC 与一个长生命周期 C 插件
// 子进程通信。
//
// 生命周期:
//   - 首次 Invoke 时启动子进程;
//   - 进程崩溃后下一次 Invoke 自动重启;
//   - Close 时 Kill + Wait,回收句柄。
//
// 并发模型:同一 SubprocessRuntime 不保证并发 Invoke 安全(stdio 是
// 顺序流,响应行可能与并发请求交错)。InvokeRouter 在 Phase 4 负责
// 串行化同一 pluginID 的调用;本类型仅负责单次 Invoke 的执行。
type SubprocessRuntime struct {
	Plugin  *PluginInstance
	Cap     *Capability
	Timeout time.Duration // 默认 30s,从 Plugin.TimeoutSeconds 推导

	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *bytes.Buffer
	closed chan struct{}
}

// NewSubprocessRuntime 构造 SubprocessRuntime;timeout <= 0 走默认 30s。
func NewSubprocessRuntime(p *PluginInstance, cap *Capability) *SubprocessRuntime {
	timeout := time.Duration(p.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultSubprocessTimeout
	}
	return &SubprocessRuntime{
		Plugin:  p,
		Cap:     cap,
		Timeout: timeout,
		stderr:  &bytes.Buffer{},
		closed:  make(chan struct{}),
	}
}

// start 启动子进程并接好三条管道;调用方需持 s.mu。
func (s *SubprocessRuntime) start(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, s.Plugin.Entry)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("subprocess: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("subprocess: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return fmt.Errorf("subprocess: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return fmt.Errorf("subprocess: cmd start: %w", err)
	}
	s.cmd = cmd
	s.stdin = stdin
	s.stdout = bufio.NewReader(stdout)
	// 后台持续把 stderr 写入 buffer(直到 EOF);Close 时 Kill 会触发 EOF。
	go s.drainStderr(stderr)
	return nil
}

// drainStderr 持续读 stderr 到 s.stderr buffer,直到子进程关闭流。
func (s *SubprocessRuntime) drainStderr(r io.ReadCloser) {
	defer r.Close()
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			s.mu.Lock()
			s.stderr.Write(buf[:n])
			s.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// stderrSnapshot 返回当前 stderr buffer 的拷贝,用于错误信息。
func (s *SubprocessRuntime) stderrSnapshot() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stderr == nil {
		return ""
	}
	return s.stderr.String()
}

// appendStderr 线程安全地把 msg 追加到 stderr buffer。
func (s *SubprocessRuntime) appendStderr(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stderr != nil {
		s.stderr.WriteString(msg)
	}
}

// Invoke 同步向子进程发送一帧 envelope,阻塞读取匹配 ID 的响应。
// 错误返回语义:
//   - ErrTimeout:ctx 超时或超过 s.Timeout,子进程已被 Kill。
//   - ErrSubprocessCrashed:子进程在响应到达前已退出。
//   - ErrSubprocessPanic:本函数 panic 被 recover。
//   - 其他 fmt.Errorf 包装:管道 / marshal / unmarshal 等底层错误。
func (s *SubprocessRuntime) Invoke(ctx context.Context, plugin *PluginInstance, cap *Capability, req Request) (resp Response, err error) {
	defer func() {
		if r := recover(); r != nil {
			s.appendStderr(fmt.Sprintf("panic recovered: %v\n", r))
			resp = Response{}
			err = ErrSubprocessPanic
		}
	}()

	// 计算本次调用 timeout:取 s.Timeout 与 ctx deadline 中较短者。
	timeout := s.Timeout
	if dl, ok := ctx.Deadline(); ok {
		if remain := time.Until(dl); remain > 0 && remain < timeout {
			timeout = remain
		}
	}
	invokeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 启动或重启子进程;全程持锁以保护 cmd/stdin/stdout 字段。
	s.mu.Lock()
	if s.cmd == nil || s.cmd.ProcessState != nil {
		// 重启前清空旧 stderr(避免把上一轮的错日志混入)
		s.stderr.Reset()
		if startErr := s.start(invokeCtx); startErr != nil {
			s.mu.Unlock()
			return Response{}, startErr
		}
	}
	stdin := s.stdin
	stdout := s.stdout
	s.mu.Unlock()

	if stdin == nil || stdout == nil {
		return Response{}, errors.New("subprocess: stdin/stdout not initialized")
	}

	// 构造 envelope 写入 stdin。
	env := stdioEnvelope{ID: req.ID, Cap: req.CapName, Params: req.Params}
	payload, mErr := json.Marshal(env)
	if mErr != nil {
		return Response{}, fmt.Errorf("subprocess: marshal envelope: %w", mErr)
	}
	payload = append(payload, '\n')
	if _, wErr := stdin.Write(payload); wErr != nil {
		// 写入失败通常意味着子进程已死,标记以便下次 Invoke 重启。
		s.mu.Lock()
		if s.cmd != nil && s.cmd.ProcessState == nil && s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		s.mu.Unlock()
		return Response{}, fmt.Errorf("subprocess: write stdin: %w", wErr)
	}

	// 同步读 stdout,跳过 ID 不匹配的行(应对与并发调用交错),
	// 直到命中 req.ID 或 EOF/错误。
	type readResult struct {
		frame stdioResponse
		err   error
	}
	done := make(chan readResult, 1)
	go func() {
		for {
			line, rerr := stdout.ReadString('\n')
			if len(line) > 0 {
				var f stdioResponse
				if jerr := json.Unmarshal([]byte(line), &f); jerr == nil {
					if f.ID == req.ID {
						done <- readResult{frame: f}
						return
					}
					// ID 不匹配:丢弃,继续读下一帧
				}
				// 解析失败也继续读下一帧(可能是协议外输出)
			}
			if rerr != nil {
				done <- readResult{err: rerr}
				return
			}
		}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			// 检查子进程是否已退出(若是 → crashed,否则管道错误)
			s.mu.Lock()
			crashed := s.cmd != nil && s.cmd.ProcessState != nil
			s.mu.Unlock()
			if crashed {
				s.appendStderr(fmt.Sprintf("subprocess exited before response; last stderr: %s\n", s.stderrSnapshot()))
				return Response{}, ErrSubprocessCrashed
			}
			return Response{}, fmt.Errorf("subprocess: read stdout: %w", res.err)
		}
		return Response{ID: res.frame.ID, Result: res.frame.Result, Error: res.frame.Error}, nil

	case <-invokeCtx.Done():
		// 超时或 ctx 取消:kill 子进程,写 stderr 警告。
		s.mu.Lock()
		if s.cmd != nil && s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		s.appendStderr(fmt.Sprintf("invoke timeout/cancel after %s, killed subprocess\n", timeout))
		s.mu.Unlock()
		return Response{}, ErrTimeout
	}
}

// Close 终止子进程;允许多次调用,Close 后再 Invoke 会在下次启动新进程。
func (s *SubprocessRuntime) Close() error {
	s.mu.Lock()
	select {
	case <-s.closed:
		s.mu.Unlock()
		return nil
	default:
		close(s.closed)
	}
	cmd := s.cmd
	stdin := s.stdin
	s.cmd = nil
	s.stdin = nil
	s.stdout = nil
	s.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	return nil
}
