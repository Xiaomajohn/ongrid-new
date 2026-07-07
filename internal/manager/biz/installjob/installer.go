package installjob

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	devicessh "github.com/ongridio/ongrid/internal/manager/biz/devicessh"
	devicemodel "github.com/ongridio/ongrid/internal/manager/model/device"
)

// SSHInstaller 是生产环境 Installer 的具体实现。它从 wiring 层拿一个
// devicessh.Router；install 路径总是使用 devicessh.PurposeInstallEdge，
// 强制 router 走直连 dialer（此时不可能有 online edge——我们就是要
// 装它）。
//
// 与手工安装的语义对齐：不在 manager 里装 install.sh，设备 SSH 上去后
// 自己 curl https://<server-http-addr>/install.sh | bash 一条命令干完。
// 这种方式下：
//   - install.sh 的“脚本源”是 cloud（nginx 暴露的 /install.sh），
//     不与 binary 版本绑定，跟手工部署同一条升级路径；
//   - binary 里不再装 install.sh，免除了 Dockerfile 多阶段 COPY /
//     //go:embed 的双重维护点（历史在这个点踩过 readInstallScript
//     ENOENT 的坑）；
//   - 设备需要能访问 cloud 的 HTTPS 端点（默认同 LAN / 公网
//     代理均可）；不适用环境请在 wire 层判一下。
type SSHInstaller struct {
	router         *devicessh.Router
	serverEdgeAddr string
	serverHTTPAddr string
	log            *slog.Logger

	// connectTimeout 限定每次 TCP+SSH 握手。
	connectTimeout time.Duration
}

// NewSSHInstaller 装配具体实现。serverEdgeAddr / serverHTTPAddr
// 以 --server-edge-addr= / --server-http-addr= 形式传给设备上跑
// 的 install.sh，install.sh 自己再从 <server-http-addr>/edge/...
// 下载 binary。log 可传 nil（默认 slog.Default()）。
func NewSSHInstaller(
	router *devicessh.Router,
	serverEdgeAddr string,
	serverHTTPAddr string,
	log *slog.Logger,
) *SSHInstaller {
	if log == nil {
		log = slog.Default()
	}
	return &SSHInstaller{
		router:         router,
		serverEdgeAddr: serverEdgeAddr,
		serverHTTPAddr: serverHTTPAddr,
		log:            log.With(slog.String("comp", "installjob-installer")),
		connectTimeout: 30 * time.Second,
	}
}

// Install implements Installer.
//
// Flow:
//
//  1. Build a transient *device.Device snapshot from the Job row (no DB
//     read — host:port:user:credential live on the Job as a snapshot).
//  2. router.MustConnect(ctx, dev, PurposeInstallEdge, RouteKindAuto) → *ssh.Client.
//     The InstallEdge purpose forces the direct path; tunnel is rejected
//     by the router because no edge can be online yet (that's what we're
//     installing).
//  3. client.NewSession() + StdoutPipe/StderrPipe.
//  4. Run the canonical curl-pipe command on the remote:
//
//     curl -k -sSL https://<server-http-addr>/install.sh \
//       | bash -s -- --access-key=K --secret-key=S \
//                  --server-edge-addr=E --server-http-addr=H \
//                  [--task-name=T]
//
//     This mirrors the manual install path one-to-one (see
//     deploy/install/edge/install.sh header docstring) so the binary
//     never carries install.sh itself — single source of truth lives
//     in <nginx>/usr/share/nginx/html/edge/install.sh.
//
//     task-name 是“一键安装任务名”透传：仅在 taskName 非空时拼上
//     `--task-name=...`。安装脚本对未识别参数会报错，所以这里用
//     “判空才拼” 而不是无条件拼——未来 install.sh 添加更多可选参数时
//     同一模式继续可用。
//  5. Stream stdout + stderr through onLog as chunks arrive.
// cmd 是前端 buildInstallCommand() 拼好的完整 curl 命令,installer 直接执行。
// 零兜底:cmd 为空时直接返回错误,不尝试自己拼装。
func (i *SSHInstaller) Install(ctx context.Context, job *InstallJob, accessKey, secretKey, taskName, cmd string, onLog func(chunk string)) error {
	if i.router == nil {
		return fmt.Errorf("installjob: installer: router not wired")
	}
	if accessKey == "" || secretKey == "" {
		// Worker 用前端传的 cmd（cmd 里已经嵌入 access/secret），
		// 不再 worker 创建新 edge 后把凭证传进来；access/secret 参数
		// 现在是 worker 用的，installer 仅走 cmd 拼装的路径，不再校验。
		// 保留参数签名避免改接口；这里转成 debug log，不阻断 install。
		if i.log != nil {
			i.log.Debug("installjob: access-key/secret-key empty (cmd embedded); proceeding",
				slog.String("note", "前端已把凭证嵌入 cmd,worker 不再传 access/secret"))
		}
	}
	if cmd == "" {
		return fmt.Errorf("installjob: command is empty — 前端必须通过 POST /install-edge 的 command 字段塞入完整的 curl 安装命令,后端不再自己拼装")
	}
	if onLog == nil {
		// Be defensive — Worker always passes a callback, but a test may
		// not. Falling back to a no-op lets the install proceed; log
		// output just won't land anywhere.
		onLog = func(string) {}
	}

	dev := buildDeviceSnapshot(job)
	i.log.Info("installjob: ssh connect start",
		slog.Uint64("device_id", job.DeviceID),
		slog.String("host", dev.SSHHost),
		slog.Int("port", dev.SSHPort),
		slog.String("user", dev.SSHUser),
	)

	if err := i.runCurlPipe(ctx, dev, cmd, onLog); err != nil {
		return fmt.Errorf("installjob: run curl-pipe install: %w", err)
	}
	return nil
}

// buildDeviceSnapshot assembles the transient device row used by the
// devicessh router + dialer. The fields populated here are exactly
// the ones checkDeviceForDirect reads; everything else is left zero.
//
// Notes:
//   - SSHPassword / SSHKey come from the Job's plaintext snapshot, not
//     from the persisted device row, because the user may have rotated
//     the credential after the install was queued.
//   - SSHPort defaults to 22 when the Job snapshot has 0; the install
//     row's port column is NOT NULL but a manually-built row from an
//     older HTTP handler may have slipped 0 through.
func buildDeviceSnapshot(job *InstallJob) *devicemodel.Device {
	port := job.Port
	if port == 0 {
		port = 22
	}
	return &devicemodel.Device{
		ID:          job.DeviceID,
		SSHHost:     job.Host,
		SSHPort:     port,
		SSHUser:     job.User,
		SSHPassword: job.PasswordSnap,
		SSHKey:      job.KeySnap,
	}
}

// runCurlPipe 打开一个 SSH session，执行前端塞进来的 curl | bash 命令，
// 并流式回传 stdout + stderr。cmd 是前端 buildInstallCommand() 拼好的完整字符串，
// installer 不再做任何字符串构造。
//
// 诊断能力:
//   - stderr 流被同时写一份到 tailBuf（8 KB 环缓冲）。sess.Run 返回非零时，
//     stderr 末尾被以 ERROR 级 log 出来。
func (i *SSHInstaller) runCurlPipe(ctx context.Context, dev *devicemodel.Device, cmd string, onLog func(chunk string)) error {
	client, err := i.dial(ctx, dev)
	if err != nil {
		return err
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("installjob: new session: %w", err)
	}
	defer sess.Close()

	stdout, err := sess.StdoutPipe()
	if err != nil {
		return fmt.Errorf("installjob: stdout pipe: %w", err)
	}
	stderr, err := sess.StderrPipe()
	if err != nil {
		return fmt.Errorf("installjob: stderr pipe: %w", err)
	}

	// cmd 已经是前端拼好的完整 curl | bash 命令,installer 直接执行。
	runCmd := strings.TrimSpace(cmd)
	
	// 截 stderr 到一个 in-memory tailBuf，错误时 dump 出来。
	stderrTail := newTailBuf(8 << 10) // 8 KB
	logOnLog := func(prefixChunk string) {
		if strings.HasPrefix(prefixChunk, "[stderr]") {
			_, _ = stderrTail.Write([]byte(strings.TrimPrefix(prefixChunk, "[stderr]")))
		}
		onLog(prefixChunk)
	}

	// 并发抽 stdout / stderr 到 logOnLog；与以前 heredoc 模式相同。
	var wg sync.WaitGroup
	wg.Add(2)
	go i.pumpStream(&wg, stdout, logOnLog, "[stdout]")
	go i.pumpStream(&wg, stderr, logOnLog, "[stderr]")

	runErr := sess.Run(runCmd)
	wg.Wait()

	if runErr != nil {
		// sess.Run 返回的 err 在远端命令非零退出时是 *ssh.ExitError。
		// 拼上 install.sh stderr 末尾能让 worker log 直接看到 install.sh
		// 的 [ERROR] / set -e 失败行（之前只能从 install_jobs.log_output
		// 查，定位不直观）。stderr 为空时只返回原 err。
		tail := strings.TrimSpace(stderrTail.String())
		if tail != "" {
			i.log.Error("installjob: remote install.sh stderr tail",
				slog.String("device_host", dev.SSHHost),
				slog.String("user", dev.SSHUser),
				slog.String("exit", runErr.Error()),
				slog.String("stderr_tail", tail),
			)
		}
		return fmt.Errorf("installjob: run curl-pipe install: %w", runErr)
	}
	return nil
}


// tailBuf keeps the last cap bytes written to it, thread-safe. Used
// to retain a tail of the remote install.sh's stderr so the worker's
// installer-error log line can include the actual [ERROR] / set -e
// message instead of just the generic "Process exited with status N"
// from the SSH layer.
type tailBuf struct {
	mu  sync.Mutex
	buf []byte
	cap int
}

func newTailBuf(capBytes int) *tailBuf { return &tailBuf{cap: capBytes} }

func (b *tailBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.cap {
		// Drop the head. b.buf[len(b.buf)-b.cap:] 是 Go 原生切片表达
		// 式，底数组还在，但我们不再保留头部的元素，GC 会回收。
		b.buf = b.buf[len(b.buf)-b.cap:]
	}
	return len(p), nil
}

func (b *tailBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// pumpStream copies one pipe into onLog until EOF. Prefix is prepended
// to every chunk so the worker's UI can colour-code stdout vs. stderr
// without re-parsing the body. The wait group is decremented after
// the stream is fully drained so the caller's WG.Wait captures any
// late log chunk before it returns.
func (i *SSHInstaller) pumpStream(wg *sync.WaitGroup, r io.Reader, onLog func(string), prefix string) {
	defer wg.Done()
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			onLog(prefix + string(buf[:n]))
		}
		if err != nil {
			return
		}
	}
}

// dial opens an *ssh.Client via the router. The InstallEdge purpose
// forces the direct dialer; the tunnel path is not yet implemented
// for this purpose and would surface as ErrTunnelNotImplemented.
func (i *SSHInstaller) dial(ctx context.Context, dev *devicemodel.Device) (*ssh.Client, error) {
	// Wrap the dial in a timeout so a stuck remote can't pin a worker
	// slot forever; the worker's execCtx is the overall ceiling, this
	// is just the per-attempt ceiling.
	dialCtx, cancel := context.WithTimeout(ctx, i.connectTimeout)
	defer cancel()

	client, err := i.router.MustConnect(dialCtx, dev, devicessh.PurposeInstallEdge, devicessh.RouteKindAuto)
	if err != nil {
		return nil, fmt.Errorf("installjob: connect %s:%d: %w", dev.SSHHost, dev.SSHPort, err)
	}
	return client, nil
}