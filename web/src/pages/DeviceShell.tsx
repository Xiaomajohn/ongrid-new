// DeviceShell — full-page WebSSH terminal.
//
// Flow:
//   1. Page mounts → fetch device metadata (hostname for the modal title)
//      and pop the Connect modal. localStorage may pre-fill the os user.
//   2. User submits the modal → open WS, send first `open` frame with
//      cols/rows from the freshly-fitted xterm.
//   3. Manager replies with `ready` (SSH up) → terminal becomes interactive.
//      Binary frames are stdout; binary user input is wrapped to stdin.
//   4. `auth_error` / `exit` / WS close → write a red banner, gate the
//      reconnect button. The user can hit `重连` to re-show the modal.
//
// Security:
//   - Password lives only in component state; never logged, never stored.
//   - Only the os username (opt-in) is persisted to localStorage.
//   - onbeforeunload sends a polite `{type:"close"}` so the manager can
//     finalize the audit row without waiting for TCP timeout.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useLocation, useNavigate, useParams } from 'react-router-dom';
import { Power, RotateCw, Terminal as TerminalIcon } from 'lucide-react';
import { Modal } from '@/components/Modal';
import { Button } from '@/components/ui/Button';
import { XTerminal, type XTerminalApi } from '@/components/XTerminal';
import { getDevice, type Device } from '@/api/devices';
import { getEdge, listEdges, type Edge } from '@/api/edges';
import {
  openShellSocket,
  probeShellPreflight,
  sendControl,
  type ShellControlFrameIn,
  type ShellOpenFrame,
  type ShellRoute,
} from '@/api/webshell';
import { getToken } from '@/store/auth';
import { usePermissions } from '@/store/me';
import { tr as trInline, useI18n } from '@/i18n/locale';
import { Card, EmptyState, PageHeader } from '@/components/ui';
import { Shield } from 'lucide-react';

type ConnectInputs = {
  user: string;
  password: string;
  port: number;
  remember: boolean;
};

type ConnState =
  | { kind: 'idle' }
  | { kind: 'connecting' }
  | { kind: 'open' }
  | { kind: 'closed'; reason?: string };

const REMEMBER_USER_KEY_PREFIX = 'webshell.last_user.';

function rememberUserKey(deviceId: string) {
  return `${REMEMBER_USER_KEY_PREFIX}${deviceId}`;
}

// ANSI red wrapper for inline error messages. xterm renders the escape
// sequence so we don't need a separate DOM element for status lines.
function ansiRed(s: string): string {
  return `\x1b[31m${s}\x1b[0m`;
}

function ansiDim(s: string): string {
  return `\x1b[2m${s}\x1b[0m`;
}

export default function DeviceShellPage() {
  const { tr } = useI18n();
  const { canMutate } = usePermissions();
  // viewer is read-only. Short-circuit before any WS setup
  // happens — backend rejects too (skill execute / shell open both
  // require non-viewer), but stopping at the page boundary keeps the
  // user from staring at a half-loaded terminal that 403s on connect.
  // 同一页面被两个 route 复用：
  //   /devices/:deviceId/shell[-direct] — 探针视角
  //   /hosts/:hostId/shell[-direct]    — 实体设备视角
  // 两边的路径段都是 Prom label device_id（非 edge.id），只是 param
  // 名不同。原先只读 deviceId，hosts 路由下取不到 → 拼出
  // /api/v1/devices//shell-direct 双斜杠 → 后端 400。这里统一兜底
  // 取 deviceId || hostId，保证两条入口都能解析到同一个 device id。
  const routeParams = useParams<{ deviceId?: string; hostId?: string }>();
  const deviceId = routeParams.deviceId || routeParams.hostId || '';
  const navigate = useNavigate();
  const location = useLocation();
  // 根据当前 pathname 是否以 /shell-direct 结尾，决定走哪个 transport：
  // - /shell        → tunnel（默认，监控页入口）
  // - /shell-direct → direct（设备页入口，直连设备 IP）
  // 这是页面级 route 锁定：用户手工在地址栏改 URL 也不影响其他页的默认行为。
  const shellRoute: ShellRoute = location.pathname.endsWith('/shell-direct') ? 'direct' : 'tunnel';
  if (!canMutate) {
    return (
      <main className="anim-fade flex flex-1 flex-col overflow-hidden p-6">
        <PageHeader title={tr('终端', 'Terminal')} subtitle={tr('WebSSH — admin / user only', 'WebSSH — admin / user only')} />
        <Card className="p-6">
          <EmptyState
            icon={Shield}
            title={tr('只读账号不能进入终端', 'Viewer accounts cannot open the terminal')}
            hint={tr('WebSSH 会让你直接登录设备 root shell，只有 admin / user 能打开。', 'WebSSH gives root shell access. Only admin and user roles can open it.')}
          />
        </Card>
      </main>
    );
  }

  const [edge, setEdge] = useState<Edge | null>(null);
  const [edgeError, setEdgeError] = useState<string | null>(null);
  // tunnel 模式自动连接：拉 device 详情拿 ssh_user。如果设备表里
  // 已有 ssh_user / ssh_password（或 ssh_key），tunnel 通道下后端
  // 会自己用 device 表凭据登录，前端不需要再弹 ConnectModal 收 OS
  // 凭据；direct 通道下这个字段只用来预填表单，不作主要决策依据。
  const [deviceRow, setDeviceRow] = useState<Device | null>(null);
  const [deviceRowLoaded, setDeviceRowLoaded] = useState(false);
  const [modalOpen, setModalOpen] = useState(true);
  const [conn, setConn] = useState<ConnState>({ kind: 'idle' });

  // The terminal API + ws live on refs — they're side-effectful and
  // outliving any single render is the whole point of this page.
  const termRef = useRef<XTerminalApi | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  // Latest cols/rows reported by xterm. We need them when sending the
  // first `open` frame (called from a callback that doesn't have direct
  // access to the terminal's geometry).
  const sizeRef = useRef<{ cols: number; rows: number }>({ cols: 80, rows: 24 });
  // Track whether we sent the close frame — onbeforeunload + manual close
  // should never double-fire.
  const closedSentRef = useRef(false);

  // Fetch device metadata for the title. The route param is the device_id
  // (Prom label). Manager does not yet expose GET /devices/{id}, so we
  // resolve the hostname by listing edges and matching device_id — matches
  // what Edges.tsx already does in-memory.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        // Cheap path: try direct lookup, fall back to list scan. The
        // manager's /edges/{id} expects an edge id, not a device id, so
        // we go straight to list. Limit=1000 mirrors the backend handler.
        const r = await listEdges();
        if (cancelled) return;
        const target = (r.items ?? []).find(
          (e) => String(e.device_id ?? '') === String(deviceId),
        );
        if (target) {
          // Refresh with full detail (host_info has more fields).
          try {
            const detail = await getEdge(target.id);
            if (!cancelled) setEdge(detail);
          } catch {
            if (!cancelled) setEdge(target);
          }
        } else {
          setEdgeError(tr('未找到该设备或设备未上线', 'Device not found or offline'));
        }
      } catch (err) {
        if (!cancelled) setEdgeError((err as Error).message || tr('加载设备信息失败', 'Failed to load device info'));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [deviceId]);

  // 拉 device 详情拿 ssh_user / ssh_password / ssh_auth_kind，供
  // tunnel 模式自动连接使用。出错静默（device 详情失败不该阻止
  // 页面其他部分工作，ConnectModal 兑底让用户手输）。
  useEffect(() => {
    if (!deviceId) return;
    let cancelled = false;
    (async () => {
      try {
        const d = await getDevice(deviceId);
        if (!cancelled) setDeviceRow(d as Device);
      } catch {
        /* ignore — fall through to ConnectModal */
      } finally {
        if (!cancelled) setDeviceRowLoaded(true);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [deviceId]);

  const tabTitle = useMemo(() => {
    if (!edge) return `Shell · ${deviceId}`;
    return `Shell · ${edge.name || deviceId}`;
  }, [edge, deviceId]);
  useEffect(() => {
    const prev = document.title;
    document.title = tabTitle;
    return () => {
      document.title = prev;
    };
  }, [tabTitle]);

  const writeBanner = useCallback((line: string) => {
    const t = termRef.current;
    if (!t) return;
    t.write(`\r\n${line}\r\n`);
  }, []);

  // sendCloseOnce guards against the WS-close + beforeunload double path.
  const sendCloseOnce = useCallback(() => {
    const ws = wsRef.current;
    if (!ws) return;
    if (closedSentRef.current) return;
    closedSentRef.current = true;
    try {
      sendControl(ws, { type: 'close' });
    } catch {
      /* WS may already be closing */
    }
  }, []);

  // Tear down the socket. Caller decides whether to also dispose the
  // terminal — usually we keep it so the user can read final output.
  const teardown = useCallback(() => {
    sendCloseOnce();
    const ws = wsRef.current;
    wsRef.current = null;
    if (ws && ws.readyState <= WebSocket.OPEN) {
      try {
        ws.close(1000, 'client');
      } catch {
        /* noop */
      }
    }
  }, [sendCloseOnce]);

  // beforeunload: best-effort polite close. We can't await the close
  // frame; gorilla writes it on the next read tick.
  useEffect(() => {
    const onUnload = () => {
      sendCloseOnce();
      const ws = wsRef.current;
      if (ws && ws.readyState === WebSocket.OPEN) {
        try {
          ws.close(1000, 'unload');
        } catch {
          /* noop */
        }
      }
    };
    window.addEventListener('beforeunload', onUnload);
    return () => {
      window.removeEventListener('beforeunload', onUnload);
      // Final teardown when page actually unmounts (route change).
      onUnload();
    };
  }, [sendCloseOnce]);

  // openConnection: wire WS handshake to the open frame. Called from the
  // modal Connect button.
  const openConnection = useCallback(
    async (inputs: ConnectInputs) => {
      const token = getToken();
      if (!token) {
        writeBanner(ansiRed(tr('未登录或登录已过期，请重新登录', 'Not logged in or session expired; please log in again')));
        setModalOpen(true);
        return;
      }
      // Tear down any previous socket (e.g. user hit "重连").
      teardown();
      closedSentRef.current = false;

      setConn({ kind: 'connecting' });

      // Pre-flight HTTP probe so 429 / 503 / 403 can be surfaced with
      // a real Chinese message — browsers don't expose upgrade-time
      // HTTP status to JS, only WS code 1006. If the probe returns a
      // non-OK status we abort before opening the socket.
      const probe = await probeShellPreflight(deviceId, { route: shellRoute });
      if (probe) {
        const fatal = explainPreflight(probe.status, probe.message);
        if (fatal) {
          writeBanner(ansiRed(fatal));
          setConn({ kind: 'closed', reason: 'preflight' });
          setModalOpen(true);
          return;
        }
      }

      const ws = openShellSocket(deviceId, token, { route: shellRoute });
      wsRef.current = ws;
      const encoder = new TextEncoder();

      ws.onopen = () => {
        const { cols, rows } = sizeRef.current;
        const sshHost = inputs.port && inputs.port !== 22 ? `127.0.0.1:${inputs.port}` : '';
        // tunnel 通道下，前端 open frame 里不传 ssh_user / ssh_pass，
        // 让后端 devicessh.ShellService.OpenShell 直接用 device 表里
        // 已存的 ssh_user / ssh_password（或 ssh_key）登录（详见
        // biz/devicessh/service.go:OpenShell 的 override 逻辑）。direct
        // 通道下 ssh_user / ssh_pass 必传——后端没有其他来源兑底。
        const frame: ShellOpenFrame = {
          type: 'open',
          cols,
          rows,
          term: 'xterm-256color',
        };
        if (shellRoute === 'direct' || inputs.user) {
          frame.ssh_user = inputs.user;
        }
        if (shellRoute === 'direct' || inputs.password) {
          frame.ssh_pass = inputs.password;
        }
        if (sshHost) {
          frame.ssh_host = sshHost;
        }
        sendControl(ws, frame);
        // We deliberately do NOT clear inputs.password from the closure —
        // it's already only in stack memory + the WS frame buffer. Once
        // ws.send returns, the only reference is the GCable closure.
      };

      ws.onmessage = (ev) => {
        // Binary == stdout/stderr. xterm handles it directly.
        if (ev.data instanceof ArrayBuffer) {
          termRef.current?.write(new Uint8Array(ev.data));
          return;
        }
        if (typeof ev.data !== 'string') return;
        let frame: ShellControlFrameIn | null = null;
        try {
          frame = JSON.parse(ev.data) as ShellControlFrameIn;
        } catch {
          return;
        }
        if (!frame || typeof frame.type !== 'string') return;
        switch (frame.type) {
          case 'ready':
            setConn({ kind: 'open' });
            writeBanner(ansiDim(tr(`-- SSH 已连接 (${inputs.user}@${edge?.name ?? deviceId}) --`, `-- SSH connected (${inputs.user}@${edge?.name ?? deviceId}) --`)));
            break;
          case 'auth_error':
            writeBanner(ansiRed(tr(`SSH 认证失败：${frame.message || '用户名或密码错误'}`, `SSH auth failed: ${frame.message || 'invalid username or password'}`)));
            setConn({ kind: 'closed', reason: 'auth' });
            // Re-open the modal so the user can retry without leaving.
            setModalOpen(true);
            break;
          case 'exit': {
            const code = frame.exit_code ?? 0;
            const tail = frame.message ? `: ${frame.message}` : '';
            writeBanner(
              code === 0
                ? ansiDim(tr(`-- 会话已结束 (exit code 0)${tail} --`, `-- Session ended (exit code 0)${tail} --`))
                : ansiRed(tr(`-- 会话已结束 (exit code ${code})${tail} --`, `-- Session ended (exit code ${code})${tail} --`)),
            );
            setConn({ kind: 'closed', reason: 'exit' });
            break;
          }
        }
      };

      ws.onerror = () => {
        writeBanner(ansiRed(tr('WebSocket 连接错误', 'WebSocket connection error')));
      };

      ws.onclose = (ev) => {
        // 1006 = abnormal close (no close frame); usually means the
        // upgrade failed (auth, route, network) or the server cut the
        // socket without a close frame. Run the post-mortem probe to
        // see if the manager has a real HTTP status to report.
        if (!closedSentRef.current && ev.code === 1006) {
          writeBanner(ansiRed(tr('连接异常断开', 'Connection dropped unexpectedly')));
          // Best-effort post-mortem: hit GET on the same path; if the
          // manager replies with 429 / 503 / 403 we now know what went
          // wrong and can retell the user. Probe is fire-and-forget so
          // we don't block the close handler.
          void probeShellPreflight(deviceId, { route: shellRoute }).then((p) => {
            if (!p) return;
            const detail = explainPreflight(p.status, p.message);
            if (detail) writeBanner(ansiRed(detail));
          });
          setModalOpen(true);
        } else if (ev.code !== 1000 && ev.code !== 1005) {
          writeBanner(
            ansiDim(tr(
              `-- 连接关闭 (code=${ev.code}${ev.reason ? `, ${ev.reason}` : ''}) --`,
              `-- Connection closed (code=${ev.code}${ev.reason ? `, ${ev.reason}` : ''}) --`,
            )),
          );
        }
        setConn((s) => (s.kind === 'closed' ? s : { kind: 'closed' }));
        wsRef.current = null;
      };

      // Wire xterm.onData to ws.send via the upper-scope ref. Set up here
      // so each new socket gets a fresh closure with its own encoder.
      pumpRef.current = (data: string) => {
        if (ws.readyState !== WebSocket.OPEN) return;
        ws.send(encoder.encode(data));
      };
    },
    [deviceId, edge, teardown, writeBanner],
  );

  // tunnel 模式自动连接：device 表里已有 ssh_user 且后端 OpenShell
  // 会用 device.ssh_user + device.ssh_password（或 ssh_key）直接
  // 走 SSH，前端 open frame 里省略 ssh_user / ssh_pass 即可。后端
  // 找不到 online edge 会返回 ErrTunnelNotImplemented（503），
  // 再交由 ConnectModal 兑底让用户走 direct 输入凭据。autoConnect
  // ref 防止 React 18 strict-mode 下 mount→unmount→remount 双触发，
  // 以及手点「重连」后的重跑入口。
  const autoConnectTriedRef = useRef(false);
  useEffect(() => {
    if (shellRoute !== 'tunnel') return;
    if (!deviceRowLoaded) return;
    if (conn.kind !== 'idle') return;
    if (autoConnectTriedRef.current) return;
    if (!deviceRow?.ssh_user) return;
    autoConnectTriedRef.current = true;
    setModalOpen(false);
    void openConnection({
      user: deviceRow.ssh_user,
      password: '',
      port: 22,
      remember: false,
    });
  }, [shellRoute, deviceRowLoaded, deviceRow, conn.kind, openConnection]);

  // Wire xterm onData → ws via a ref-based pump so we don't have to
  // re-mount the terminal when the socket changes (reconnect).
  const pumpRef = useRef<(data: string) => void>(() => {});

  const onTermData = useCallback((data: string) => {
    pumpRef.current(data);
  }, []);

  const onTermResize = useCallback((cols: number, rows: number) => {
    sizeRef.current = { cols, rows };
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      sendControl(ws, { type: 'resize', cols, rows });
    }
  }, []);

  const attachTerm = useCallback((api: XTerminalApi) => {
    termRef.current = api;
  }, []);

  // Modal submit handler. Persists the username choice and kicks off the
  // WS handshake; password is dropped on the floor after this returns.
  const handleConnect = useCallback(
    (inputs: ConnectInputs) => {
      if (inputs.remember) {
        try {
          localStorage.setItem(rememberUserKey(deviceId), inputs.user);
        } catch {
          /* private mode / quota — non-fatal */
        }
      } else {
        try {
          localStorage.removeItem(rememberUserKey(deviceId));
        } catch {
          /* noop */
        }
      }
      setModalOpen(false);
      openConnection(inputs);
    },
    [deviceId, openConnection],
  );

  const handleManualClose = useCallback(() => {
    if (!confirm(tr('确定要关闭终端会话？', 'Close this terminal session?'))) return;
    teardown();
    navigate('/devices');
  }, [navigate, teardown]);

  const handleReconnect = useCallback(() => {
    teardown();
    setConn({ kind: 'idle' });
    // 重置 auto-connect ref，让 device.ssh_user 路径在 tunnel 模式下
    // 重新走自动连接逻辑。点「重连」是用户明确表达「重新连」，不需
    // 要弹窗卡在那儿等他手输密码。
    autoConnectTriedRef.current = false;
    setModalOpen(true);
  }, [teardown]);

  const statusLabel = useMemo(() => {
    switch (conn.kind) {
      case 'idle':
        return tr('未连接', 'Disconnected');
      case 'connecting':
        return tr('连接中…', 'Connecting…');
      case 'open':
        return tr('已连接', 'Connected');
      case 'closed':
        return tr('已断开', 'Closed');
    }
  }, [conn, tr]);

  const hostname =
    extractHostname(edge?.host_info) || edge?.name || deviceId || tr('设备', 'device');

  return (
    <main className="anim-fade flex flex-1 flex-col overflow-hidden bg-zinc-950">
      <header className="flex items-center justify-between border-b border-zinc-800/60 bg-zinc-900/60 px-4 py-2">
        <div className="flex min-w-0 items-center gap-2 text-xs text-zinc-300">
          <TerminalIcon size={14} className="text-zinc-500" />
          <span className="truncate font-medium text-zinc-100">{hostname}</span>
          <span className="text-zinc-600">·</span>
          <span
            className={
              conn.kind === 'open'
                ? 'text-emerald-400'
                : conn.kind === 'connecting'
                  ? 'text-amber-300'
                  : 'text-zinc-500'
            }
          >
            {statusLabel}
          </span>
          {edgeError && (
            <span className="ml-2 text-red-400">· {edgeError}</span>
          )}
        </div>
        <div className="flex items-center gap-1.5">
          <Button
            variant="ghost"
            onClick={handleReconnect}
            aria-label={tr('重新连接', 'Reconnect')}
          >
            <RotateCw size={12} />
            {tr('重连', 'Reconnect')}
          </Button>
          <Button
            variant="ghost"
            onClick={handleManualClose}
            aria-label={tr('关闭终端', 'Close terminal')}
          >
            <Power size={12} />
            {tr('关闭', 'Close')}
          </Button>
        </div>
      </header>

      <div className="flex-1 overflow-hidden p-2">
        <XTerminal
          onData={onTermData}
          onResize={onTermResize}
          attachRef={attachTerm}
        />
      </div>

      <ConnectModal
        open={modalOpen}
        deviceId={deviceId}
        tunnelMode={shellRoute === 'tunnel'}
        defaultUser={deviceRow?.ssh_user}
        title={tr(`连接到 ${hostname}`, `Connect to ${hostname}`)}
        onCancel={() => {
          // If we never connected, leave the page; otherwise just hide
          // the modal (terminal is still useful for reading prior output).
          if (conn.kind === 'idle' || conn.kind === 'closed') {
            navigate('/devices');
          } else {
            setModalOpen(false);
          }
        }}
        onSubmit={handleConnect}
      />
    </main>
  );
}

// ConnectModal collects ssh user / password / port. We keep it inline so
// the password lifetime is bounded by this component's mount window.
function ConnectModal({
  open,
  deviceId,
  tunnelMode,
  defaultUser,
  title,
  onSubmit,
  onCancel,
}: {
  open: boolean;
  deviceId: string;
  // tunnel 模式下 user / password 可以为空——后端会用 device 表里
  // 已存的 ssh_user / ssh_password（或 ssh_key）登录。表单在
  // tunnel 模式下是「覆盖默认凭据」的入口，留空 = 用默认。
  tunnelMode?: boolean;
  // tunnel 模式下 deviceRow.ssh_user 作预填值，传到表单里；direct
  // 模式下被忽略。
  defaultUser?: string;
  title: string;
  onSubmit(inputs: ConnectInputs): void;
  onCancel(): void;
}) {
  const { tr } = useI18n();
  const [user, setUser] = useState('');
  const [password, setPassword] = useState('');
  const [port, setPort] = useState<string>('22');
  const [remember, setRemember] = useState(true);
  const [advanced, setAdvanced] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  // Pre-fill the username from localStorage on first open. We don't
  // depend on `deviceId` for the lifetime — the hook re-runs when the
  // modal toggles open so reconnects keep the user remembered.
  // tunnel 模式下额外优先使用 device.ssh_user 作为预填值。
  useEffect(() => {
    if (!open) return;
    setErr(null);
    setPassword('');
    try {
      // tunnel 模式下 deviceRow.ssh_user 优先，否则回落到
      // localStorage「上次记住的用户名」。direct 模式只查
      // localStorage，与原行为一致。
      const remembered = localStorage.getItem(rememberUserKey(deviceId));
      const prefill = (tunnelMode && defaultUser) || remembered;
      if (prefill) setUser(prefill);
    } catch {
      /* noop */
    }
  }, [open, deviceId, tunnelMode, defaultUser]);

  if (!open) return null;

  const submit = () => {
    const u = user.trim();
    // tunnel 模式下 user / password 可以为空——后端会用 device 表里
    // 已存的 ssh_user / ssh_password（或 ssh_key）登录。表单留空
    // 即代表「不覆盖默认凭据」。direct 模式两个字段必传。
    if (!tunnelMode && !u) {
      setErr(tr('请输入 OS 用户名', 'Please enter the OS username'));
      return;
    }
    if (!tunnelMode && !password) {
      setErr(tr('请输入密码', 'Please enter the password'));
      return;
    }
    const p = Number(port || '22');
    if (!Number.isFinite(p) || p < 1 || p > 65535) {
      setErr(tr('端口必须在 1-65535 之间', 'Port must be between 1 and 65535'));
      return;
    }
    onSubmit({ user: u, password, port: p, remember });
  };

  return (
    <Modal
      open
      onClose={onCancel}
      title={title}
      size="sm"
      footer={
        <>
          <Button variant="ghost" onClick={onCancel}>
            {tr('取消', 'Cancel')}
          </Button>
          <Button variant="subtle" onClick={submit}>
            {tr('连接', 'Connect')}
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <div>
          <label htmlFor="webssh-user" className="mb-1 block text-[11px] text-zinc-500">
            {tr('OS 用户', 'OS user')}
          </label>
          <input
            id="webssh-user"
            autoFocus
            autoComplete="username"
            value={user}
            onChange={(e) => setUser(e.target.value)}
            placeholder="root"
            className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
            onKeyDown={(e) => {
              if (e.key === 'Enter') submit();
            }}
          />
        </div>
        <div>
          <label htmlFor="webssh-pass" className="mb-1 block text-[11px] text-zinc-500">
            {tr('密码', 'Password')}
          </label>
          <input
            id="webssh-pass"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder={
              tunnelMode && defaultUser
                ? tr('留空使用设备默认凭据', 'Leave empty to use device default credentials')
                : ''
            }
            className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
            onKeyDown={(e) => {
              if (e.key === 'Enter') submit();
            }}
          />
          {tunnelMode && defaultUser && (
            <p className="mt-1 text-[11px] text-zinc-600">
              {tr(
                `tunnel 通道留空 = 使用设备默认凭据（${defaultUser}）；填入则覆盖该次会话的 OS 凭据。`,
                `tunnel: leave empty to use device default credentials (${defaultUser}); values entered here override the default for this session only.`,
              )}
            </p>
          )}
        </div>
        <div>
          <button
            type="button"
            onClick={() => setAdvanced((v) => !v)}
            className="text-[11px] text-zinc-500 hover:text-zinc-300"
          >
            {advanced ? tr('收起高级', 'Hide advanced') : tr('高级选项 ▸', 'Advanced ▸')}
          </button>
          {advanced && (
            <div className="mt-2">
              <label htmlFor="webssh-port" className="mb-1 block text-[11px] text-zinc-500">
                {tr('SSH 端口', 'SSH port')}
              </label>
              <input
                id="webssh-port"
                inputMode="numeric"
                value={port}
                onChange={(e) => setPort(e.target.value.replace(/[^0-9]/g, ''))}
                placeholder="22"
                className="w-32 rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
              />
              <p className="mt-1 text-[11px] text-zinc-600">
                {tr('默认走设备本地 sshd（127.0.0.1:22）。改端口仅在本机另起 sshd 时有用。', "Defaults to the device's local sshd (127.0.0.1:22). Change only if you've started another sshd on a different port.")}
              </p>
            </div>
          )}
        </div>
        <label className="flex cursor-pointer items-center gap-2 text-xs text-zinc-300">
          <input
            type="checkbox"
            checked={remember}
            onChange={(e) => setRemember(e.target.checked)}
            className="h-3.5 w-3.5 accent-zinc-300"
          />
          {tr('记住此用户名（仅本浏览器，不保存密码）', 'Remember this username (this browser only; password is never stored)')}
        </label>
        {err && (
          <div
            role="alert"
            className="rounded-lg border border-red-500/20 bg-red-500/10 px-3 py-2 text-xs text-red-300"
          >
            {err}
          </div>
        )}
        <p className="text-[11px] text-zinc-600">
          {tr('提示：密码不会被写入浏览器存储；关闭弹窗或刷新页面后立即丢弃。', 'Note: the password is never persisted in browser storage; it is discarded as soon as the dialog closes or the page reloads.')}
        </p>
      </div>
    </Modal>
  );
}

// extractHostname is a slimmed-down copy of the helper in Edges.tsx; we
// duplicate rather than refactor because the field shape isn't fixed
// across edge versions and inlining keeps the dependency graph simple.
function extractHostname(hostInfo: Edge['host_info']): string | null {
  if (!hostInfo) return null;
  const obj =
    typeof hostInfo === 'string' ? safeParse(hostInfo) : hostInfo;
  if (!obj || typeof obj !== 'object') return null;
  const candidates = [
    (obj as Record<string, unknown>).hostname,
    (obj as Record<string, unknown>).hostName,
    (obj as Record<string, unknown>).nodename,
    (obj as Record<string, unknown>).host,
  ];
  for (const c of candidates) {
    if (typeof c === 'string' && c.trim()) {
      const v = c.trim();
      return v.includes(':') ? v.split(':')[0] || v : v;
    }
  }
  return null;
}

// explainPreflight maps a probe HTTP status to a Chinese error string.
// Returns null when the status is fine (200/400-from-WS-upgrade-attempt
// without an Upgrade header — that's the "all good" signal).
//
// Status mapping:
//   429 → "并发会话过多（每用户最多 5 / 每设备最多 5）"
//   503 → "设备离线"
//   403 → "权限不足，请联系管理员"
//   401 → "未登录或登录已过期，请重新登录"
//   404 → "设备不存在或未上线"
//   500+ → "服务端错误"
function explainPreflight(status: number, _message: string): string | null {
  switch (status) {
    case 429:
      return trInline('并发会话过多（每用户最多 5 / 每设备最多 5）', 'Too many concurrent sessions (per user max 5 / per device max 5)');
    case 503:
      return trInline('设备离线', 'Device offline');
    case 403:
      return trInline('权限不足，请联系管理员', 'Insufficient permission; contact an admin');
    case 401:
      return trInline('未登录或登录已过期，请重新登录', 'Not logged in or session expired; please log in again');
    case 404:
      return trInline('设备不存在或未上线', 'Device not found or offline');
    default:
      if (status >= 500) return trInline('服务端错误，请稍后重试', 'Server error; please retry shortly');
      return null;
  }
}

function safeParse(s: string): Record<string, unknown> | null {
  try {
    const v = JSON.parse(s) as unknown;
    return v && typeof v === 'object' ? (v as Record<string, unknown>) : null;
  } catch {
    return null;
  }
}

