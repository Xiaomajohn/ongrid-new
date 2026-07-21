#!/usr/bin/env bash
# ongrid-edge curl-pipe installer.
#
# Usage:
#   curl -k -sSL https://<server>/install.sh | bash -s -- \
#       --access-key=KEY \
#       --secret-key=SECRET \
#       --server-edge-addr=<host>:40012 \
#       --server-http-addr=<host>:8443
#
#   --server-http-addr  is the same host:port your browser uses (the nginx
#                       front-door); the script downloads the right binary
#                       from https://<http-addr>/edge/ongrid-edge-<os>-<arch>.
#   --server-edge-addr  is the geminio tunnel endpoint (host:port).
#
# Supported targets: linux/amd64, linux/arm64.
#
# Idempotent: re-running with new keys replaces the env file and restarts the
# service. Re-running with the same keys is a no-op aside from the binary
# refresh.

set -euo pipefail

# --- pretty-print helpers (must come before any log_* call) -----------------

if [[ -t 1 && "${NO_COLOR:-}" == "" ]]; then
    C_RED=$'\033[0;31m'
    C_GREEN=$'\033[0;32m'
    C_YELLOW=$'\033[1;33m'
    C_CYAN=$'\033[0;36m'
    C_DIM=$'\033[2m'
    C_BOLD=$'\033[1m'
    C_RESET=$'\033[0m'
else
    C_RED=''; C_GREEN=''; C_YELLOW=''; C_CYAN=''; C_DIM=''; C_BOLD=''; C_RESET=''
fi

log_info()  { printf '%s[INFO]%s  %s\n' "$C_GREEN"  "$C_RESET" "$*"; }
log_warn()  { printf '%s[WARN]%s  %s\n' "$C_YELLOW" "$C_RESET" "$*"; }
log_error() { printf '%s[ERROR]%s %s\n' "$C_RED"    "$C_RESET" "$*" >&2; }
log_ok()    { printf '%s[OK]%s    %s\n' "$C_GREEN"  "$C_RESET" "$*"; }

trap 'log_error "install failed at line $LINENO (exit $?)"' ERR

# Print a one-line banner immediately so the manager-side onLog
# callback sees something on stdout/stderr right after the SSH pipe
# opens. This is the operator's first hint that the install actually
# started on the box; without it, a script that exits fast (e.g. on
# the EUID != 0 -> sudo re-exec fail path) gives the worker nothing
# to surface beyond "Process exited with status N". Trim secrets —
# only echo the structural fields.
log_info "install.sh starting on $(uname -srm); user=$(whoami 2>/dev/null || echo unknown)"

# --- defaults / constants ----------------------------------------------------

ACCESS_KEY=""
SECRET_KEY=""
SERVER_EDGE_ADDR=""
SERVER_HTTP_ADDR=""
# --task-name=NAME 是监控任务名，写入 env file (ONGRID_EDGE_TASK_NAME)，
# agent 启动后随 register_edge 上报给 manager，落到 edge.task_name 列。
# installjob worker 会从 installjob.options_json 透传过来；手工 install
# 时由 operator 直接在命令行给。空字符串表示不设置，edge 端 HostInfo.TaskName
# 留空，manager 端 SetTaskName 不会覆盖已有值。
TASK_NAME=""
# --prefix=PATH 给出一个 operator 指定的根目录，edge 安装路径全部嵌套在
# ${PREFIX}/ongrid-edge/ 下面，作为单一命名空间（避免污染 PREFIX 根目录里
# 的其他工具）。默认 /mnt/data/tools-temp，operator 只需清理整个命名空间：
#   rm -rf /mnt/data/tools-temp/ongrid-edge
# 所有其他路径常量都从 EDGE_ROOT=${PREFIX}/ongrid-edge 派生而来。
PREFIX="/mnt/data/tools-temp"
EDGE_ROOT="${PREFIX}/ongrid-edge"

# Layout（嵌套在 PREFIX/ongrid-edge/ 下，与原本 /usr/local{,/lib}/...、
# /etc/...、/var/lib/...、/var/log/... 的语义一致）：
#   EDGE_ROOT/bin/                  ongrid-edge
#   EDGE_ROOT/lib/ongrid-edge/      plugin binaries + apply-pending-upgrade.sh
#   EDGE_ROOT/etc/ongrid-edge/      ongrid-edge.env
#   EDGE_ROOT/var/lib/ongrid-edge/  state, plugin work, upgrade stage
#   EDGE_ROOT/var/log/ongrid-edge/  logs
BIN_DIR="${EDGE_ROOT}/bin"
LIB_DIR="${EDGE_ROOT}/lib/ongrid-edge"
ENV_DIR="${EDGE_ROOT}/etc/ongrid-edge"
ENV_FILE="${ENV_DIR}/ongrid-edge.env"
STATE_DIR="${EDGE_ROOT}/var/lib/ongrid-edge"
LOG_DIR="${EDGE_ROOT}/var/log/ongrid-edge"
APPLY_HOOK="${LIB_DIR}/apply-pending-upgrade.sh"

SERVICE_FILE="/etc/systemd/system/ongrid-edge.service"
UPGRADE_SERVICE_FILE="/etc/systemd/system/ongrid-edge-upgrade.service"
SERVICE_USER="ongrid-edge"
SERVICE_GROUP="ongrid-edge"

UNINSTALL=0

# Wait up to N seconds for systemd-managed agent to log "registered with cloud"
# before declaring success. Connect handshake is sub-second on a healthy box;
# 20s leaves headroom for slow DNS / network. Set ONGRID_INSTALL_WAIT to override.
WAIT_SECS="${ONGRID_INSTALL_WAIT:-20}"

# --- arg parsing -------------------------------------------------------------

usage() {
    cat <<EOF
Usage: install.sh [OPTIONS]

Required (install):
  --access-key=KEY
  --secret-key=SECRET
  --server-edge-addr=HOST:PORT     edge geminio endpoint, e.g. ongrid.example.com:40012
  --server-http-addr=HOST[:PORT]   http endpoint, e.g. ongrid.example.com:8443

Other:
  --prefix=PATH                     install root (default /mnt/data/tools-temp);
                                    consolidates bin/lib/etc/var under PATH
  --task-name=NAME                  监控任务名（写入 env file，agent 启动后上报给 manager）
  --uninstall                      stop + remove ongrid-edge (keeps PREFIX/var/log)
  -h, --help                       this help

Env:
  ONGRID_INSTALL_WAIT=20           seconds to poll journal for connect-success (default 20)
  NO_COLOR=1                       disable ANSI colors
EOF
}

for arg in "$@"; do
    case "$arg" in
        --access-key=*)        ACCESS_KEY="${arg#*=}" ;;
        --secret-key=*)        SECRET_KEY="${arg#*=}" ;;
        --server-edge-addr=*)  SERVER_EDGE_ADDR="${arg#*=}" ;;
        --server-http-addr=*)  SERVER_HTTP_ADDR="${arg#*=}" ;;
        --prefix=*)            PREFIX="${arg#*=}" ;;
        --task-name=*)         TASK_NAME="${arg#*=}" ;;
        --uninstall)           UNINSTALL=1 ;;
        -h|--help)             usage; exit 0 ;;
        *) log_error "unknown arg: $arg"; usage; exit 2 ;;
    esac
done

# Re-derive paths after --prefix may have overridden the default.
EDGE_ROOT="${PREFIX}/ongrid-edge"
BIN_DIR="${EDGE_ROOT}/bin"
LIB_DIR="${EDGE_ROOT}/lib/ongrid-edge"
ENV_DIR="${EDGE_ROOT}/etc/ongrid-edge"
ENV_FILE="${ENV_DIR}/ongrid-edge.env"
STATE_DIR="${EDGE_ROOT}/var/lib/ongrid-edge"
LOG_DIR="${EDGE_ROOT}/var/log/ongrid-edge"
APPLY_HOOK="${LIB_DIR}/apply-pending-upgrade.sh"

# --- root check --------------------------------------------------------------
#
# Resolve the actual script file path. Under `bash install.sh ARGS` $0
# is the file path. Under `curl URL | bash -s -- ARGS` $0 is the FIRST
# arg after `--` (not a file), so the old
#   exec sudo -E bash "$0" "$@"
# would point sudo at a non-existent file. BASH_SOURCE[0] in bash -s
# mode also isn't usable (returns "bash" or empty depending on bash
# version), so the only reliable thing we can do under the curl-pipe
# path is refuse to re-exec and tell the operator to stage the script
# to a file or re-run as root. Root users skip the if entirely.
SCRIPT_PATH="${BASH_SOURCE[0]:-$0}"
if [[ $EUID -ne 0 ]]; then
    if [[ -z "$SCRIPT_PATH" || "$SCRIPT_PATH" == -* || "$SCRIPT_PATH" == "bash" || ! -f "$SCRIPT_PATH" ]]; then
        log_error "non-root invocation but no script file path is resolvable (likely 'curl ... | bash -s -- ...' mode)."
        log_error "  fix one of:"
        log_error "    - re-run as root"
        log_error "    - or:  curl -k -sSL https://${SERVER_HTTP_ADDR:-<server>}/install.sh -o /tmp/ongrid-install.sh && bash /tmp/ongrid-install.sh --access-key=... ..."
        exit 2
    fi
    log_info "re-executing with sudo (script=$SCRIPT_PATH)"
    exec sudo -E bash "$SCRIPT_PATH" "$@"
fi

# --- uninstall path ----------------------------------------------------------

if [[ $UNINSTALL -eq 1 ]]; then
    log_info "stopping ongrid-edge"
    systemctl disable --now ongrid-edge 2>/dev/null || true
    rm -f "$SERVICE_FILE" "$UPGRADE_SERVICE_FILE" "${BIN_DIR}/ongrid-edge"
    rm -rf "$ENV_DIR"
    systemctl daemon-reload || true
    log_ok "uninstalled (logs under $LOG_DIR preserved; full wipe: rm -rf ${EDGE_ROOT})"
    exit 0
fi

# --- arg validation ----------------------------------------------------------

[[ -n "$ACCESS_KEY"       ]] || { log_error "missing --access-key";       usage; exit 2; }
[[ -n "$SECRET_KEY"       ]] || { log_error "missing --secret-key";       usage; exit 2; }
[[ -n "$SERVER_EDGE_ADDR" ]] || { log_error "missing --server-edge-addr"; usage; exit 2; }
[[ -n "$SERVER_HTTP_ADDR" ]] || { log_error "missing --server-http-addr"; usage; exit 2; }

# --- detect OS / arch --------------------------------------------------------

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
    x86_64|amd64)  ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) log_error "unsupported arch: $(uname -m)"; exit 1 ;;
esac
if [[ "$OS" != "linux" ]]; then
    log_error "only linux is supported by this installer; got: $OS"
    exit 1
fi

BINARY="ongrid-edge-${OS}-${ARCH}"
URL="https://${SERVER_HTTP_ADDR}/edge/${BINARY}"

# --- download ----------------------------------------------------------------

# Stop the running agent before overwriting the binary. ETXTBSY is rare on
# linux for ELF replacement but `systemctl stop` is cheap and cleaner.
if systemctl is-active --quiet ongrid-edge 2>/dev/null; then
    log_info "stopping running ongrid-edge to refresh binary"
    systemctl stop ongrid-edge || true
fi

log_info "downloading ${URL}"
TMP_BIN=$(mktemp /tmp/ongrid-edge.XXXXXX)
trap 'rm -f "$TMP_BIN"; log_error "install failed at line $LINENO (exit $?)"' ERR
if ! curl -fLk --retry 3 --retry-delay 2 -o "$TMP_BIN" "$URL"; then
    log_error "download failed: ${URL}"
    log_error "  - check that the http endpoint is correct and reachable"
    log_error "  - try: curl -kI https://${SERVER_HTTP_ADDR}/install.sh"
    rm -f "$TMP_BIN"; exit 1
fi
if [[ ! -s "$TMP_BIN" ]]; then
    log_error "downloaded binary is empty: $TMP_BIN"
    rm -f "$TMP_BIN"; exit 1
fi
mkdir -p "$BIN_DIR"
install -m 0755 -o root -g root "$TMP_BIN" "${BIN_DIR}/ongrid-edge"
rm -f "$TMP_BIN"
trap 'log_error "install failed at line $LINENO (exit $?)"' ERR

# --- SELinux relabel (openEuler/RHEL enforcing + custom --prefix) ------------
# When --prefix puts the binary somewhere like /mnt/data/.../bin/ongrid-edge,
# the file inherits mnt_t from /mnt. systemd ExecStart= requires bin_t to
# actually exec; under SELinux enforcing the service then crashes 203/EXEC
# within ~1s of start (verified on openEuler 24.03 / RHEL-family). Detect
# SELinux and re-label the bin/lib trees so plugin-supervisor-spawned binaries
# also get bin_t. Standard /usr, /usr/local, /opt are already bin_t by default
# so we skip them — chcon on a standard path only adds noise.
if command -v getenforce >/dev/null 2>&1 && [[ "$(getenforce 2>/dev/null)" != "Disabled" ]]; then
    case "$PREFIX" in
        /usr|/usr/local|/opt|/srv) : ;;  # already bin_t under default policy
        *)
            log_info "relabeling SELinux bin_t on ${BIN_DIR} and ${LIB_DIR}"
            chcon -R -h -t bin_t "${BIN_DIR}" "${LIB_DIR}" 2>/dev/null \
                || log_warn "chcon -t bin_t failed; if SELinux is enforcing, ongrid-edge.service will exit 203/EXEC"
            ;;
    esac
fi

# --- ADR-024 ExecStartPre hook ----------------------------------------------
#
# apply-pending-upgrade.sh runs before ongrid-edge each boot. It looks for a
# staged bundle dropped by the edge agent (during MethodFetchPackage), swaps
# every file in MANIFEST.txt atomically, then on the NEXT boot rolls back if
# no healthy_marker landed. Without this script installed remote whole-bundle
# upgrades are silently no-ops. Anonymous /edge/ static path serves it.
APPLY_URL="https://${SERVER_HTTP_ADDR}/edge/apply-pending-upgrade.sh"
log_info "installing ${APPLY_HOOK}"
mkdir -p "$LIB_DIR"
TMP_HOOK=$(mktemp /tmp/apply-pending-upgrade.XXXXXX)
if curl -fLk --retry 3 --retry-delay 2 -o "$TMP_HOOK" "$APPLY_URL"; then
    install -m 0755 -o root -g root "$TMP_HOOK" "$APPLY_HOOK"
else
    log_warn "could not fetch ${APPLY_URL}; ADR-024 whole-bundle upgrade won't apply"
fi
rm -f "$TMP_HOOK"

# --- bundled plugin binaries (ADR-015) --------------------------------------
#
# The agent's plugin supervisor runs promtail (logs), node_exporter
# (hostmetrics), process_exporter (procmetrics), otelcol-contrib (traces),
# auditbeat (audit) and database exporters (databasemetrics)
# as subprocesses, expecting them under ${LIB_DIR}. The old curl-pipe
# installer fetched ONLY the agent binary, so every edge enrolled via the UI
# one-liner came up with an empty plugin dir → all plugins "crashed: binary
# missing" → silent empty Logs / Monitor / Traces. (install-edge.sh, run from
# an extracted tarball, did install them — but nobody uses that for
# enrollment.) Fetch them here from the same /edge/ static path the agent
# binary came from. Best-effort per binary: a missing one only disables its
# plugin, surfaced loudly in the self-check below. auditbeat is offline-only
# (Elastic closed-source) and only exists when an operator staged it into
# the tarball / nginx path; the curl-pipe installer tolerates its absence so
# dev boxes without auditbeat still get the other plugins installed.
fetch_plugin_bin() {
    local name="$1" dest="${LIB_DIR}/$1"
    local url="https://${SERVER_HTTP_ADDR}/edge/${name}-${OS}-${ARCH}"
    local tmp
    tmp=$(mktemp "/tmp/${name}.XXXXXX")
    if curl -fLk --retry 3 --retry-delay 2 -o "$tmp" "$url" && [[ -s "$tmp" ]]; then
        install -m 0755 -o root -g root "$tmp" "$dest"
        log_info "installed plugin binary: ${name}"
    else
        log_warn "could not fetch ${url}; the ${name} plugin will not run until present"
    fi
    rm -f "$tmp"
}
for pbin in promtail node_exporter process_exporter otelcol-contrib mysqld_exporter postgres_exporter redis_exporter mongodb_exporter auditbeat; do
    fetch_plugin_bin "$pbin"
done

# --- stop OS-shipped auditd so auditbeat owns the audit subsystem -----------
#
# auditbeat 9.x 的 auditd 模块通过 audit_open() → audit_get_status() 订阅
# NETLINK_AUDIT multicast；如果 OS 自带的 auditd.service 也开着，两者
# 抢 audit 子系统，auditbeat 子进程会 EPERM（servernode 验证过，详见
# .record/2026-07-20-edge-install-heredoc-mismatch-and-audit-caps.md）。
# 装机时把 OS 自带 auditd stop + disable，让 auditbeat 独占。
#
# Silent skip：systemctl 不存在（容器、darwin、init=/bin/bash）、
# auditd.service unit 不存在（极简镜像、WSL）、stop/disable 失败均
# 不阻断装机主流程（warn 但 continue）。不动 audit 规则文件，保留现场
# 便于事后排查。
stop_auditd_for_auditbeat() {
    command -v systemctl >/dev/null 2>&1 || return 0
    # auditd.service unit 不存在 → 静默跳过
    systemctl cat auditd.service >/dev/null 2>&1 || return 0
    if systemctl is-active --quiet auditd.service 2>/dev/null; then
        if systemctl stop auditd.service 2>/dev/null; then
            log_info "stopped auditd.service (auditbeat's auditd module owns the audit subsystem now)"
        else
            log_warn "failed to stop auditd.service — audit plugin may conflict; continue anyway"
        fi
    fi
    if systemctl is-enabled --quiet auditd.service 2>/dev/null; then
        if systemctl disable auditd.service 2>/dev/null; then
            log_info "disabled auditd.service (auditbeat replaces it on next boot)"
        else
            log_warn "failed to disable auditd.service — it'll restart on next boot; continue anyway"
        fi
    fi
}
stop_auditd_for_auditbeat

# --- service user ------------------------------------------------------------

if ! id -u "$SERVICE_USER" >/dev/null 2>&1; then
    log_info "creating system user ${SERVICE_USER}"
    useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"
fi

# Grant log-read group membership so the logs plugin (promtail) can read
# /var/log/* (root:adm 640) and the journal (systemd-journal). Idempotent.
# Re-asserted on every root start by apply-pending-upgrade.sh, so bundle
# upgrades that skip this installer don't silently lose it.
for grp in adm systemd-journal; do
    if getent group "$grp" >/dev/null 2>&1; then
        usermod -aG "$grp" "$SERVICE_USER" 2>/dev/null || true
    fi
done

# --- log dir -----------------------------------------------------------------

mkdir -p "$LOG_DIR"
chown "$SERVICE_USER":"$SERVICE_GROUP" "$LOG_DIR"
chmod 750 "$LOG_DIR"

# --- env dir + file ----------------------------------------------------------
#
# Group ownership matters here: the service runs as ${SERVICE_USER} and must
# be able to traverse ${ENV_DIR} (mode 750 needs the group bit). Without the
# explicit chown, the dir stays root:root → service can't read scrape.yaml,
# you see the misleading "permission denied" in the journal, and the agent
# silently runs without any scrape config.

mkdir -p "$ENV_DIR"
chown "root:${SERVICE_GROUP}" "$ENV_DIR"
chmod 750 "$ENV_DIR"

cat > "$ENV_FILE" <<EOF
ONGRID_EDGE_CLOUD_ADDR=${SERVER_EDGE_ADDR}
ONGRID_EDGE_ACCESS_KEY=${ACCESS_KEY}
ONGRID_EDGE_SECRET_KEY=${SECRET_KEY}
# 监控任务名（空表示不设置；agent 启动后随 register_edge 上报给 manager）。
# 不加引号，env file 由 systemd 直接 source，特殊字符由 operator 自负责。
ONGRID_EDGE_TASK_NAME=${TASK_NAME}
# Override the agent-side defaults so plugin binaries, plugin work dir, and
# upgrade stage dir all land under \${PREFIX} (= ${PREFIX}) instead of the
# hardcoded /usr/local/lib, /var/lib/ongrid-edge, /var/lib/ongrid-edge/.upgrade.
# Matches the prefix-relative paths the systemd unit + apply-pending-upgrade.sh
# hook use (see ongrid-edge.service / ongrid-edge-upgrade.service).
ONGRID_EDGE_PLUGIN_BIN_DIR=${LIB_DIR}
ONGRID_EDGE_PLUGIN_WORK_DIR=${STATE_DIR}/plugins
ONGRID_EDGE_UPGRADE_STAGE_DIR=${STATE_DIR}/.upgrade
# scrape config (used by internal/pkg/config Edge.ScrapeConfigFile default
# when this var is unset) also moves under \${PREFIX} for consistency.
ONGRID_EDGE_SCRAPE_CONFIG_FILE=${ENV_DIR}/scrape.yaml
EOF
chmod 640 "$ENV_FILE"
chown "root:${SERVICE_GROUP}" "$ENV_FILE"

# --- state dir ---------------------------------------------------------------
#
# StateDirectory= is NOT used in the rendered unit (it always maps to
# ${LOCALSTATEDIR}/lib/<name> = /var/lib/ongrid-edge on default systemd builds
# and can't follow an arbitrary --prefix). We rely on:
#   (a) the installer pre-creating + chowning $STATE_DIR below, and
#   (b) ReadWritePaths=$STATE_DIR $LOG_DIR inside the unit,
# both honored even on systemd 219 (CentOS 7) where StateDirectory= would be
# silently ignored anyway. The agent can always write its plugin work dir
# + .upgrade stage + log dir regardless of systemd version.
mkdir -p "$STATE_DIR"
chown "$SERVICE_USER":"$SERVICE_GROUP" "$STATE_DIR"
chmod 0755 "$STATE_DIR"

# --- systemd units -----------------------------------------------------------

# ADR-024 privileged apply oneshot. Runs apply-pending-upgrade.sh as root
# (no sandbox) before the agent, so it can write the root-owned binary paths
# under ${BIN_DIR}. ongrid-edge.service pulls it via Wants=, which re-runs it
# on every Restart=always auto-restart (verified on systemd 219). This
# replaces the old `ExecStartPre=-+...`: the `+` root-exec prefix is
# unsupported on systemd < 231 and was silently ignored there, so upgrades
# never applied on CentOS 7's systemd 219.
cat > "$UPGRADE_SERVICE_FILE" <<EOF
[Unit]
Description=ongrid edge pending-upgrade apply (root, pre-start)
# ${APPLY_HOOK} — rendered from --prefix (default /mnt/data/tools-temp).
Documentation=file://${APPLY_HOOK}
Before=ongrid-edge.service
After=local-fs.target

[Service]
Type=oneshot
RemainAfterExit=no
ExecStart=${APPLY_HOOK}
EOF

cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=ongrid edge agent
After=network-online.target
Wants=network-online.target
# ADR-024 remote upgrade: the privileged "apply staged bundle + rollback
# check" step runs as the separate root oneshot ongrid-edge-upgrade.service
# (this unit runs as User=root, sandboxed by ProtectSystem=strict, and
# therefore cannot write ${BIN_DIR} without breaking the sandbox). Wants=
# pulls it on every (re)start incl. Restart=always; After= guarantees the
# swap lands before the agent execs.
Wants=ongrid-edge-upgrade.service
After=ongrid-edge-upgrade.service

[Service]
Type=simple
EnvironmentFile=${ENV_FILE}
ExecStart=${BIN_DIR}/ongrid-edge
Restart=always
RestartSec=5
# User=root / Group=root — required by the audit plugin (auditbeat 9.x
# auditd module calls audit_open() → audit_get_status() via NETLINK_AUDIT
# and reads /var/log/audit/audit.log mode 0600 root:audit; both gated on
# CAP_AUDIT_* + DAC bypass). NoNewPrivileges=true makes ambient caps the
# only cap propagation channel to children, so the four caps below MUST be
# listed in AmbientCapabilities= in one shot — dropping any one of them
# re-EPERMs the audit module on first connect.
#
# CAP_NET_ADMIN        历史保留（d809b3fc）；NETLINK_AUDIT 在部分发行版也走 netlink
# CAP_AUDIT_READ       audit_open() 订阅 NETLINK_AUDIT multicast，必须
# CAP_AUDIT_WRITE      auditbeat system module / kernel-audit 写路径，加稳
# CAP_DAC_READ_SEARCH  绕过 DAC 读 audit.log 0600；不解 DAC_OVERRIDE 避免放成
#                      「任何文件都能读写」，最小授权
#
# install-edge.sh still creates the `ongrid-edge` system user for file
# ownership (STATE_DIR / PLUGIN_WORK_DIR chown'd to it), but the service
# itself no longer drops to it. Dual-source sync: this heredoc MUST mirror
# deploy/install/edge/ongrid-edge.service (the offline-tarball template);
# the self-check below greps both for User=root + AmbientCapabilities
# CAP_AUDIT_READ to catch drift. See .record/2026-07-20-edge-install-
# heredoc-mismatch-and-audit-caps.md.
User=root
Group=root
AmbientCapabilities=CAP_NET_ADMIN CAP_AUDIT_READ CAP_AUDIT_WRITE CAP_DAC_READ_SEARCH
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
# StateDirectory auto-creates /var/lib/ongrid-edge (mode 0755 owned by
# User=) at start and implicitly adds it to ReadWritePaths. Without
# this, ProtectSystem=strict makes /var/lib read-only and the agent's
# runtime mkdir of /var/lib/ongrid-edge/.upgrade fails EROFS.
#
# StateDirectory= needs systemd >= 235. On 233/234 ProtectSystem=strict and
# ReadWritePaths= are honored but StateDirectory= is not, so its implicit
# writable path is lost and the sandboxed agent still can't write the state
# dir even after the installer pre-created it. List it in ReadWritePaths=
# explicitly so writability never depends on StateDirectory= taking effect.
# We don't set StateDirectory= here at all: it pins the path to
# /var/lib/ongrid-edge and can't follow --prefix. The ReadWritePaths= below
# plus the installer pre-creating + chowning $STATE_DIR (above) is enough.
# plugin binaries dir, plugin work dir, .upgrade stage dir, and log dir
# regardless of systemd version. auditbeat also needs this to create its data/
# subdir under the lib directory (e.g. /mnt/data/tools-temp/ongrid-edge/lib/ongrid-edge/data).
ReadWritePaths=${STATE_DIR} ${LOG_DIR} ${LIB_DIR}
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
log_info "starting ongrid-edge"
systemctl enable ongrid-edge >/dev/null 2>&1 || true
systemctl restart ongrid-edge

# --- post-start verification -------------------------------------------------

# Resolve version line. The agent logs "ongrid-edge vX.Y.Z starting" to the
# journal as the very first line on boot — extract it from there rather than
# re-invoking the binary (which would race the live systemd process and is
# how the old install.sh leaked a misleading "unauthorized" into its summary).
# Falls back to the binary path if we can't find the line in time.
VERSION_LINE=""

# Poll for either a "registered with cloud" success line, a fatal "unauthorized"
# line, or a service crash. Bail at WAIT_SECS.
START_TS=$(date +%s)
STATUS="pending"
EDGE_ID=""
FAIL_REASON=""
printf '%s[INFO]%s  ' "$C_GREEN" "$C_RESET"
printf 'waiting for tunnel handshake (up to %ss)' "$WAIT_SECS"
while :; do
    NOW=$(date +%s)
    if (( NOW - START_TS >= WAIT_SECS )); then break; fi

    JOURNAL=$(journalctl -u ongrid-edge --since "-${WAIT_SECS}s" --no-pager 2>/dev/null || true)

    # Capture version line as soon as the agent prints it on boot.
    if [[ -z "$VERSION_LINE" ]]; then
        VERSION_LINE=$(printf '%s\n' "$JOURNAL" | grep -oE 'ongrid-edge v[0-9][^ ]* starting' | tail -1 | sed 's/ starting$//' || true)
    fi

    # Failure: service flapped or fatal error logged.
    if ! systemctl is-active --quiet ongrid-edge 2>/dev/null; then
        STATUS="failed"
        FAIL_REASON="service not active"
        break
    fi

    # Success: agent printed registered-with-cloud (covers both fresh and
    # warm reconnect). Capture edge_id from the JSON.
    REG_LINE=$(printf '%s\n' "$JOURNAL" | grep -F 'agent: registered with cloud' | tail -1 || true)
    if [[ -n "$REG_LINE" ]]; then
        STATUS="active"
        EDGE_ID=$(printf '%s' "$REG_LINE" | grep -oE '"edge_id":[0-9]+' | head -1 | cut -d: -f2 || true)
        break
    fi

    # Fast-fail on unauthorized — no point in waiting out the timeout.
    if printf '%s\n' "$JOURNAL" | grep -q 'unauthorized'; then
        STATUS="failed"
        FAIL_REASON="cloud rejected access_key/secret_key (unauthorized)"
        break
    fi

    printf '.'
    sleep 1
done
printf '\n'

[[ -z "$VERSION_LINE" ]] && VERSION_LINE="ongrid-edge ($(stat -c '%y' ${BIN_DIR}/ongrid-edge 2>/dev/null | cut -d. -f1))"

# --- self-check --------------------------------------------------------------
#
# Turn the three silent failure modes (missing plugin binary, unreadable
# journal, unreachable data plane) into loud, actionable output. All checks
# are guarded inside `if` so they never trip the ERR trap.
echo
echo "${C_BOLD}${C_CYAN}--- self-check ---${C_RESET}"
SELFCHECK_FAIL=0
for tool in promtail otelcol-contrib node_exporter process_exporter mysqld_exporter postgres_exporter redis_exporter mongodb_exporter auditbeat; do
    if [[ -x "${LIB_DIR}/${tool}" ]]; then
        log_ok "plugin binary present: ${tool}"
    else
        log_warn "plugin binary missing: ${LIB_DIR}/${tool} — that plugin will not run (auditbeat is optional; rest surface a hard problem)"
        # auditbeat 缺席不阻断 self-check（离线资源包可能不含），其他 plugin 缺席计 hard-fail
        if [[ "${tool}" != "auditbeat" ]]; then
            SELFCHECK_FAIL=1
        fi
    fi
done
# State dir must exist and be writable by the service user. On systemd < 235
# (CentOS/RHEL 7) the unit's StateDirectory= is silently ignored, so this is
# the probe that catches the "online but no data" failure: without a writable
# /var/lib/ongrid-edge every collector plugin fails `configure` with EACCES.
if command -v runuser >/dev/null 2>&1; then
    SVC_W=(runuser -u "$SERVICE_USER" -- test -w "$STATE_DIR")
else
    SVC_W=(sudo -u "$SERVICE_USER" test -w "$STATE_DIR")
fi
if [[ -d "$STATE_DIR" ]] && "${SVC_W[@]}" 2>/dev/null; then
    log_ok "state dir writable by ${SERVICE_USER}: ${STATE_DIR}"
else
    log_error "${SERVICE_USER} cannot write ${STATE_DIR} — every collector plugin will fail; edge will be online with no data"
    log_error "  fix: mkdir -p ${STATE_DIR}; chown ${SERVICE_USER}:${SERVICE_GROUP} ${STATE_DIR}; chmod 0755 ${STATE_DIR}; systemctl restart ongrid-edge"
    SELFCHECK_FAIL=1
fi
if command -v runuser >/dev/null 2>&1; then
    JREAD=(runuser -u "$SERVICE_USER" -- journalctl -n 1 --no-pager)
else
    JREAD=(sudo -u "$SERVICE_USER" journalctl -n 1 --no-pager)
fi
if "${JREAD[@]}" >/dev/null 2>&1; then
    log_ok "journald readable by ${SERVICE_USER}"
else
    log_error "${SERVICE_USER} cannot read the journal — journald log shipping will be empty"
    log_error "  fix: usermod -aG systemd-journal ${SERVICE_USER}; ensure persistent journal (/var/log/journal)"
    SELFCHECK_FAIL=1
fi
DP_HOST="${SERVER_HTTP_ADDR%%:*}"
if [[ -n "$DP_HOST" ]] && timeout 5 bash -c "exec 3<>/dev/tcp/${DP_HOST}/443" 2>/dev/null; then
    log_ok "data-plane host ${DP_HOST}:443 reachable (TCP)"
else
    log_warn "data-plane host ${DP_HOST}:443 not reachable from here — logs/traces push may fail"
fi
# Validate the freshly-written service file. If the heredoc drifts from the
# .service template again (e.g. a partial sed, or someone re-renders only one
# of the two sources of truth), User= or AmbientCapabilities= would silently
# regress to ongrid-edge / CAP_NET_ADMIN-only and auditbeat would crash on
# every fresh install with EPERM. Make that loud here. See
# .record/2026-07-20-edge-install-heredoc-mismatch-and-audit-caps.md.
if [[ -f "$SERVICE_FILE" ]]; then
    if ! grep -qE '^User=root$' "$SERVICE_FILE"; then
        log_error "${SERVICE_FILE}: User= is not root (audit plugin will fail)"; SELFCHECK_FAIL=1
    fi
    if ! grep -qE '^AmbientCapabilities=.*\bCAP_AUDIT_READ\b' "$SERVICE_FILE"; then
        log_error "${SERVICE_FILE}: AmbientCapabilities= missing CAP_AUDIT_READ (audit_open will EPERM)"; SELFCHECK_FAIL=1
    fi
    if ! grep -qE '^AmbientCapabilities=.*\bCAP_DAC_READ_SEARCH\b' "$SERVICE_FILE"; then
        log_error "${SERVICE_FILE}: AmbientCapabilities= missing CAP_DAC_READ_SEARCH (audit.log read will EPERM)"; SELFCHECK_FAIL=1
    fi
else
    log_error "${SERVICE_FILE} not found after install"; SELFCHECK_FAIL=1
fi
if [[ $SELFCHECK_FAIL -eq 0 ]]; then
    log_ok "self-check passed"
else
    log_warn "self-check found problems above — agent is up but some telemetry will be missing until fixed"
fi

# --- final report ------------------------------------------------------------

echo
case "$STATUS" in
    active)
        log_ok "installed:    ${VERSION_LINE}"
        if [[ -n "$EDGE_ID" ]]; then
            log_ok "connected:    edge_id=${EDGE_ID} via ${SERVER_EDGE_ADDR}"
        else
            log_ok "connected:    via ${SERVER_EDGE_ADDR}"
        fi
        log_ok "install root: ${PREFIX}"
        log_ok "binary:       ${BIN_DIR}/ongrid-edge"
        log_ok "plugin dir:   ${LIB_DIR}"
        log_ok "env file:     ${ENV_FILE}"
        log_ok "state dir:    ${STATE_DIR}"
        log_ok "log dir:      ${LOG_DIR}"
        log_ok "tail logs:    journalctl -u ongrid-edge -f"
        log_ok "uninstall:    curl -k -sSL https://${SERVER_HTTP_ADDR}/install.sh | bash -s -- --uninstall --prefix=${PREFIX}"
        ;;
    failed)
        log_ok "installed:    ${VERSION_LINE}"
        log_warn "service did not reach connected state: ${FAIL_REASON}"
        echo
        echo "${C_DIM}---- last 20 journal lines ----${C_RESET}"
        journalctl -u ongrid-edge -n 20 --no-pager 2>/dev/null | sed 's/^/    /' || true
        echo
        if [[ "$FAIL_REASON" == *unauthorized* ]]; then
            log_warn "next step: confirm the access_key/secret_key match what the UI shows."
            log_warn "  the secret_key was only displayed once — if lost, rotate the edge in the UI."
        else
            log_warn "next step: tail the journal to diagnose:"
            log_warn "  journalctl -u ongrid-edge -f"
        fi
        log_ok "install root: ${PREFIX}"
        log_ok "env file:     ${ENV_FILE}"
        log_ok "uninstall:    curl -k -sSL https://${SERVER_HTTP_ADDR}/install.sh | bash -s -- --uninstall --prefix=${PREFIX}"
        exit 1
        ;;
    pending)
        log_ok "installed:    ${VERSION_LINE}"
        log_warn "service is running but did not log a connect within ${WAIT_SECS}s"
        log_warn "this can happen on slow networks; tail the journal to confirm:"
        log_warn "  journalctl -u ongrid-edge -f"
        log_ok "install root: ${PREFIX}"
        log_ok "env file:     ${ENV_FILE}"
        log_ok "uninstall:    curl -k -sSL https://${SERVER_HTTP_ADDR}/install.sh | bash -s -- --uninstall --prefix=${PREFIX}"
        ;;
esac
