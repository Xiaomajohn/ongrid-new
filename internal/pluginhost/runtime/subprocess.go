package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/ongridio/ongrid/internal/pluginhost/registry"
)

// defaultSubprocessTimeout 是 Timeout 未设置或 <= 0 时的兜底超时。
const defaultSubprocessTimeout = 30 * time.Second

// stdioEnvelope 写入子进程 stdin 的 JSON 帧(请求体)。
//
// 字段名遵循 plan §7.5 / §9:用 "id" / "cap" / "params"。
type stdioEnvelope struct {
	ID     string          `json:"id"`
	Cap    string          `json:"cap"`
	Params json.RawMessage `json:"params,omitempty"`
}

// stdioResponse 从子进程 stdout 读到的 JSON 帧(响应体)。
//
// 与 stdioEnvelope 共享 "id" 字段用于多路复用匹配;Result 与
// Error 互斥(空 Error 表示成功)。
type stdioResponse struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// SubprocessRuntime 通过 stdin/stdout JSON-RPC 与 C 插件子进程通信。
//
// 本 phase 只实现"单次 Invoke 即拉起一个子进程,Invoke 完成后
// 进程退出"的简洁模型;**长生命周期子进程 + 空闲超时 + 自动
// restart 留给后续 phase**(见 §9 上方 TODO)。
//
// panic recover:Invoke 内 defer recover + slog.Error + 返 error。
type SubprocessRuntime struct {
	Entry     string        // 可执行入口(plugin.json:entry)
	Args      []string      // 启动参数
	Env       []string      // 额外环境变量(base = os.Environ())
	Timeout   time.Duration // 单次 Invoke 超时(默认 30s)
	stderrBuf *bytes.Buffer // stderr 收集(写 audit 用)
	cmd       *exec.Cmd     // 最近一次拉起的子进程(Close 时 Kill + Wait)
}

// NewSubprocessRuntime 构造 SubprocessRuntime。
//
// timeout <= 0 时走默认 30s。Env 默认空切片,append 时行为可预期。
// stderrBuf 总是非 nil,Invoke 时 Reset 复用。
func NewSubprocessRuntime(entry string, timeout time.Duration) *SubprocessRuntime {
	if timeout <= 0 {
		timeout = defaultSubprocessTimeout
	}
	return &SubprocessRuntime{
		Entry:     entry,
		Env:       []string{},
		Timeout:   timeout,
		stderrBuf: &bytes.Buffer{},
	}
}

// Invoke 同步执行一次 invoke:拉起子进程,写 envelope,读 envelope,
// wait 进程,返回响应。任何环节失败:cmd.Process.Kill() + 返回 error。
//
// 并发模型:本次实现是**单进程一次性**调用,不复用底层 cmd 字段。
// cmd 字段仅用于 Close 兜底 Kill(本 phase 不预拉长生命周期进程)。
//
// 错误归类:
//   - marshal / pipe / start / write / read / unmarshal:fmt.Errorf 包装
//   - panic:recover 后返 fmt.Errorf("subprocess: panic: %v")
//   - 非 nil exit + 空 stdout:fmt.Errorf("subprocess: process exited with error: %w; stderr: %s")
//
// 成功路径(子进程无错退出且 stdout 是合法 envelope):
//   Response{ID, Result, Error}, nil
func (s *SubprocessRuntime) Invoke(ctx context.Context, plugin *registry.PluginInstance, cap *registry.Capability, req Request) (resp Response, err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("subprocess: panic recovered",
				"plugin_id", plugin.PackID,
				"capability", req.CapName,
				"panic", r,
			)
			resp = Response{}
			err = fmt.Errorf("subprocess: panic: %v", r)
		}
	}()

	// 1. 准备 cmd = exec.CommandContext(ctx, Entry, Args...)
	cmd := exec.CommandContext(ctx, s.Entry, s.Args...)
	s.cmd = cmd

	// 2. cmd.Env = append(os.Environ(), Env...) + PLUGIN_TRACE_ID=req.TraceID
	cmd.Env = append(os.Environ(), s.Env...)
	cmd.Env = append(cmd.Env, fmt.Sprintf("PLUGIN_TRACE_ID=%s", req.TraceID))

	// 接 stdin / stdout / stderr pipe
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Response{}, fmt.Errorf("subprocess: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return Response{}, fmt.Errorf("subprocess: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return Response{}, fmt.Errorf("subprocess: stderr pipe: %w", err)
	}

	// 清空上一轮的 stderr buffer
	s.stderrBuf.Reset()

	// 启动子进程
	if startErr := cmd.Start(); startErr != nil {
		return Response{}, fmt.Errorf("subprocess: cmd start: %w", startErr)
	}

	// 后台收 stderr 到 buffer(直到子进程关闭 stderr 流)
	go drain(stderr, s.stderrBuf)

	// 3. cmd.Stdin 写 envelope
	env := stdioEnvelope{ID: req.ID, Cap: req.CapName, Params: req.Params}
	payload, mErr := json.Marshal(env)
	if mErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return Response{}, fmt.Errorf("subprocess: marshal envelope: %w", mErr)
	}
	payload = append(payload, '\n')
	if _, wErr := stdin.Write(payload); wErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return Response{}, fmt.Errorf("subprocess: write stdin: %w", wErr)
	}
	// 写完 stdin 立即关,让子进程读到 EOF
	_ = stdin.Close()

	// 4. cmd.Stdout 读 envelope(io.ReadAll 一次性读完所有输出)
	raw, rErr := io.ReadAll(stdout)
	if rErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return Response{}, fmt.Errorf("subprocess: read stdout: %w", rErr)
	}

	// 等子进程退出,回收句柄
	waitErr := cmd.Wait()

	// 拼出 stderr 摘要(失败时方便排查)
	stderrMsg := s.stderrBuf.String()
	if stderrMsg != "" {
		slog.Warn("subprocess: stderr captured",
			"plugin_id", plugin.PackID,
			"capability", req.CapName,
			"stderr", stderrMsg,
		)
	}

	// 子进程异常退出且 stdout 没拿到响应 → 视为崩溃
	if waitErr != nil && len(raw) == 0 {
		return Response{}, fmt.Errorf("subprocess: process exited with error: %w; stderr: %s", waitErr, stderrMsg)
	}

	// 5. 解析 envelope
	var frame stdioResponse
	if uErr := json.Unmarshal(raw, &frame); uErr != nil {
		return Response{}, fmt.Errorf("subprocess: unmarshal response: %w; raw: %s", uErr, string(raw))
	}

	return Response{ID: frame.ID, Result: frame.Result, Error: frame.Error}, nil
}

// drain 持续读 r 到 buf,直到 r 关闭 / 出错。
func drain(r io.Reader, buf *bytes.Buffer) {
	chunk := make([]byte, 4096)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			buf.Write(chunk[:n])
		}
		if err != nil {
			return
		}
	}
}

// Close 终止最近一次拉起的子进程(若有)。
//
// 多次调用安全:cmd 为 nil 或 ProcessState 已 Exited 时 no-op。
func (s *SubprocessRuntime) Close() error {
	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	if s.cmd.ProcessState != nil {
		return nil
	}
	_ = s.cmd.Process.Kill()
	_ = s.cmd.Wait()
	return nil
}
