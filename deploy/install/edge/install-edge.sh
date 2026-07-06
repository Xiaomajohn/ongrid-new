#!/usr/bin/env bash
# ongrid edge install-edge.sh - install ongrid-edge as a systemd service on
# the target host (NOT the cloud box). Run inside the extracted edge bundle:
#   sudo ./install-edge.sh
# Or uninstall: sudo ./install-edge.sh --uninstall

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)
cd "$SCRIPT_DIR"

if [[ -t 1 ]]; then
    C_RED=$'\033[0;31m'; C_GREEN=$'\033[0;32m'; C_YELLOW=$'\033[1;33m'
    C_CYAN=$'\033[0;36m'; C_BOLD=$'\033[1m'; C_RESET=$'\033[0m'
else
    C_RED=''; C_GREEN=''; C_YELLOW=''; C_CYAN=''; C_BOLD=''; C_RESET=''
fi

log_info()  { printf '%s[INFO]%s %s\n'  "$C_GREEN"  "$C_RESET" "$*"; }
log_warn()  { printf '%s[WARN]%s %s\n'  "$C_YELLOW" "$C_RESET" "$*"; }
log_error() { printf '%s[ERROR]%s %s\n' "$C_RED"    "$C_RESET" "$*" >&2; }

trap 'log_error "install-edge failed at line $LINENO"' ERR

SERVICE_USER=ongrid-edge
SERVICE_GROUP=ongrid-edge
# --prefix=PATH collapses every ongrid-edge install path (binary, plugin
# binaries, env file, state dir, log dir) under one root. Default
# /mnt/data/toos-temp keeps everything together so the operator can wipe
# the whole install by rm -rf /mnt/data/toos-temp/{bin,lib,etc,var}.
PREFIX="${ONGRID_EDGE_PREFIX:-/mnt/data/toos-temp}"
# Layout (matches the original /usr/local{,/lib}/..., /etc/...,
# /var/lib/..., /var/log/... split, just under PREFIX):
#   PREFIX/bin/                  ongrid-edge
#   PREFIX/lib/ongrid-edge/      plugin binaries + apply-pending-upgrade.sh
#   PREFIX/etc/ongrid-edge/      ongrid-edge.env
#   PREFIX/var/lib/ongrid-edge/  state, plugin work, upgrade stage
#   PREFIX/var/log/ongrid-edge/  logs
BIN_DEST="${PREFIX}/bin/ongrid-edge"
BIN_DIR="${PREFIX}/bin"
PLUGIN_BIN_DIR="${PREFIX}/lib/ongrid-edge"   # bundled plugin binaries (promtail, etc.)
STATE_DIR="${PREFIX}/var/lib/ongrid-edge"    # agent state root (ReadWritePaths=)
PLUGIN_WORK_DIR="${STATE_DIR}/plugins"       # rendered plugin configs + subprocess logs
CONFIG_DIR="${PREFIX}/etc/ongrid-edge"
ENV_FILE="${CONFIG_DIR}/ongrid-edge.env"
UNIT_FILE=/etc/systemd/system/ongrid-edge.service
UPGRADE_UNIT_FILE=/etc/systemd/system/ongrid-edge-upgrade.service
LOG_DIR="${PREFIX}/var/log/ongrid-edge"
APPLY_HOOK="${PLUGIN_BIN_DIR}/apply-pending-upgrade.sh"

UNINSTALL=0
while [[ $# -gt 0 ]]; do
    case "$1" in
        --uninstall) UNINSTALL=1; shift ;;
        --prefix=*)  PREFIX="${1#*=}"; shift ;;
        -h|--help)
            cat <<EOF
Usage: sudo ./install-edge.sh [OPTIONS]

Options:
  --uninstall   Stop/disable service and remove files.
  --prefix=PATH Install root (default /mnt/data/toos-temp); collapses
                bin/lib/etc/var under PATH.
  -h, --help    Show this help.

Parameters (env vars or interactive prompt):
  ONGRID_CLOUD_ADDR   cloud tunnel endpoint, e.g. ongrid.example.com:40012
  EDGE_ACCESS_KEY     access key (from cloud CreateEdge API)
  EDGE_SECRET_KEY     secret key (from cloud CreateEdge API)
  ONGRID_EDGE_PREFIX  same as --prefix=PATH; --prefix wins if both are set
EOF
            exit 0 ;;
        *) log_error "unknown flag: $1"; exit 2 ;;
    esac
done
# Re-derive paths so --prefix applies after arg parsing.
BIN_DEST="${PREFIX}/bin/ongrid-edge"
BIN_DIR="${PREFIX}/bin"
PLUGIN_BIN_DIR="${PREFIX}/lib/ongrid-edge"
STATE_DIR="${PREFIX}/var/lib/ongrid-edge"
PLUGIN_WORK_DIR="${STATE_DIR}/plugins"
CONFIG_DIR="${PREFIX}/etc/ongrid-edge"
ENV_FILE="${CONFIG_DIR}/ongrid-edge.env"
LOG_DIR="${PREFIX}/var/log/ongrid-edge"
APPLY_HOOK="${PLUGIN_BIN_DIR}/apply-pending-upgrade.sh"

if [[ $EUID -ne 0 ]]; then
    log_warn "not running as root; re-executing via sudo"
    exec sudo -E bash "$0" "$@"
fi

# ---------- uninstall path ----------
if [[ $UNINSTALL -eq 1 ]]; then
    log_info "stopping ongrid-edge"
    systemctl stop ongrid-edge 2>/dev/null || true
    systemctl disable ongrid-edge 2>/dev/null || true
    rm -f "$UNIT_FILE" "$UPGRADE_UNIT_FILE"
    systemctl daemon-reload || true
    rm -f "$BIN_DEST"
    rm -rf "$CONFIG_DIR"
    # keep logs in $LOG_DIR for post-mortem; operator can rm -rf if desired.
    log_info "ongrid-edge uninstalled (logs under $LOG_DIR preserved; full wipe: rm -rf ${PREFIX})"
    exit 0
fi

# ---------- detect OS / arch ----------
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH_RAW=$(uname -m)
case "$ARCH_RAW" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) log_error "unsupported arch: $ARCH_RAW"; exit 1 ;;
esac
case "$OS" in
    linux)  ;;
    darwin) log_warn "darwin is supported as binary host but the systemd unit only applies on Linux" ;;
    *) log_error "unsupported OS: $OS"; exit 1 ;;
esac

BIN_SRC="${SCRIPT_DIR}/ongrid-edge-${OS}-${ARCH}"
if [[ ! -f "$BIN_SRC" ]]; then
    # Fallback: try plain ./ongrid-edge.
    if [[ -f "${SCRIPT_DIR}/ongrid-edge" ]]; then
        BIN_SRC="${SCRIPT_DIR}/ongrid-edge"
    else
        log_error "no binary for ${OS}-${ARCH}; expected ${BIN_SRC} or ${SCRIPT_DIR}/ongrid-edge"
        exit 1
    fi
fi
log_info "using binary: $BIN_SRC"

# ---------- collect params ----------
prompt_if_empty() {
    local varname="$1" msg="$2" secret="${3:-0}" val="${!1:-}"
    if [[ -z "$val" ]]; then
        printf '%s' "$msg"
        if [[ "$secret" == "1" ]]; then
            read -r -s val
            printf '\n'
        else
            read -r val
        fi
    fi
    printf -v "$varname" '%s' "$val"
}

prompt_if_empty ONGRID_CLOUD_ADDR "cloud tunnel addr (e.g. ongrid.example.com:40012): " 0
prompt_if_empty EDGE_ACCESS_KEY   "edge access key: " 0
prompt_if_empty EDGE_SECRET_KEY   "edge secret key: " 1

if [[ -z "${ONGRID_CLOUD_ADDR:-}" || -z "${EDGE_ACCESS_KEY:-}" || -z "${EDGE_SECRET_KEY:-}" ]]; then
    log_error "ONGRID_CLOUD_ADDR / EDGE_ACCESS_KEY / EDGE_SECRET_KEY are all required"
    exit 1
fi

# ---------- system user ----------
if ! id "$SERVICE_USER" >/dev/null 2>&1; then
    log_info "creating system user $SERVICE_USER"
    useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER" || true
fi
# Grant read access to standard log dirs so promtail (logs plugin) can
# tail /var/log/syslog, auth.log, etc. These files are typically
# root:adm 640. systemd-journal lets promtail journal-source read
# /var/log/journal/. Both groups are membership-only — no privilege
# escalation. Idempotent: usermod is a no-op if already a member.
for grp in adm systemd-journal; do
    if getent group "$grp" >/dev/null 2>&1; then
        usermod -aG "$grp" "$SERVICE_USER" 2>/dev/null || true
    fi
done

# ---------- install binary ----------
log_info "installing binary to $BIN_DEST"
install -m 0755 -o root -g root "$BIN_SRC" "$BIN_DEST"

# ---------- install bundled plugin binaries (ADR-015) ----------
# promtail (logs plugin), otelcol-contrib (traces plugin; future), etc.
# Per-platform variant lives under SCRIPT_DIR with the same -OS-ARCH suffix
# as the main edge binary.
mkdir -p "$PLUGIN_BIN_DIR"
# Base state dir. The subdirs below (plugins/, .upgrade/) get created and
# chowned individually, but the agent also needs to own the base
# $STATE_DIR itself so it can create further state dirs at runtime. The
# unit's StateDirectory= would only guarantee that on systemd >= 235, and
# (more importantly) it always pins the path to /var/lib/<name> which
# can't follow an arbitrary --prefix — so the unit doesn't use
# StateDirectory= at all. ReadWritePaths=$STATE_DIR + this installer's
# explicit pre-create + chown is what gives the sandboxed agent writability
# on every systemd version.
mkdir -p "$STATE_DIR"
chown "$SERVICE_USER":"$SERVICE_GROUP" "$STATE_DIR" 2>/dev/null || true
chmod 0755 "$STATE_DIR"
mkdir -p "$PLUGIN_WORK_DIR"
chown "$SERVICE_USER":"$SERVICE_GROUP" "$PLUGIN_WORK_DIR" 2>/dev/null || true
chmod 750 "$PLUGIN_WORK_DIR"

PROMTAIL_SRC="${SCRIPT_DIR}/promtail-${OS}-${ARCH}"
if [[ -f "$PROMTAIL_SRC" ]]; then
    log_info "installing promtail to ${PLUGIN_BIN_DIR}/promtail"
    install -m 0755 -o root -g root "$PROMTAIL_SRC" "${PLUGIN_BIN_DIR}/promtail"
else
    log_warn "promtail-${OS}-${ARCH} not bundled; logs plugin will fail to start until present"
fi

# C11 Phase-B / ADR-024 remote upgrade hook — `apply-pending-upgrade.sh`
# runs as root via the ongrid-edge-upgrade.service oneshot (installed
# below) before every agent start, and swaps the staged bundle into place.
# Idempotent / safe with no pending file (exits 0). The oneshot unit
# references this absolute path. See ADR-018 / ADR-024 / HLD-007.
#
# Render placeholders in the template to the operator-chosen --prefix paths
# so the script knows where the agent stages its upgrade bundle + which
# directories hold the swap targets (.previous files). The template ships
# with __STAGE_DIR__ / __BIN_TARGET__ / __BIN_DIR__ / __LIB_DIR__ placeholders
# (see apply-pending-upgrade.sh header for the contract).
SWAP_SRC="${SCRIPT_DIR}/apply-pending-upgrade.sh"
if [[ -f "$SWAP_SRC" ]]; then
    log_info "installing apply-pending-upgrade.sh to ${APPLY_HOOK}"
    esc() { printf '%s' "$1" | sed -e 's/[\\|&]/\\&/g'; }
    sed \
        -e "s|__STAGE_DIR__|$(esc "${STATE_DIR}/.upgrade")|g" \
        -e "s|__BIN_TARGET__|$(esc "${BIN_DIR}/ongrid-edge")|g" \
        -e "s|__BIN_DIR__|$(esc "${BIN_DIR}")|g" \
        -e "s|__LIB_DIR__|$(esc "${PLUGIN_BIN_DIR}")|g" \
        "$SWAP_SRC" > "${APPLY_HOOK}.tmp"
    install -m 0755 -o root -g root "${APPLY_HOOK}.tmp" "${APPLY_HOOK}"
    rm -f "${APPLY_HOOK}.tmp"
else
    log_warn "apply-pending-upgrade.sh not bundled; remote upgrade (C11 Phase-B) won't work"
fi

# Stage dir for remote upgrade — edge writes pending binary here, swap
# script reads it. Pre-create so the agent doesn't need to chown at
# runtime.
mkdir -p "${STATE_DIR}/.upgrade"
chown -R "$SERVICE_USER":"$SERVICE_GROUP" "${STATE_DIR}/.upgrade"
chmod 0750 "${STATE_DIR}/.upgrade"

# otelcol-contrib (traces plugin, ADR-013). Upstream doesn't ship darwin
# builds in the contrib stream — traces plugin stays disabled on darwin
# edges until an operator drops in a custom OCB build.
OTELCOL_SRC="${SCRIPT_DIR}/otelcol-contrib-${OS}-${ARCH}"
if [[ -f "$OTELCOL_SRC" ]]; then
    log_info "installing otelcol-contrib to ${PLUGIN_BIN_DIR}/otelcol-contrib"
    install -m 0755 -o root -g root "$OTELCOL_SRC" "${PLUGIN_BIN_DIR}/otelcol-contrib"
else
    log_warn "otelcol-contrib-${OS}-${ARCH} not bundled; traces plugin will fail to start until present"
fi

# auditbeat (audit plugin). Linux-only; on darwin edges the plugin
# will simply fail to start (binary missing) and supervisor reports
# StateCrashed — no install-time abort because darwin support is
# future work, not a hard requirement. Binary comes from
# resource/auditbeat/<arch>/auditbeat via `make stage-auditbeat`.
AUDITBEAT_SRC="${SCRIPT_DIR}/auditbeat-${OS}-${ARCH}"
if [[ -f "$AUDITBEAT_SRC" ]]; then
    log_info "installing auditbeat to ${PLUGIN_BIN_DIR}/auditbeat"
    install -m 0755 -o root -g root "$AUDITBEAT_SRC" "${PLUGIN_BIN_DIR}/auditbeat"
else
    log_warn "auditbeat-${OS}-${ARCH} not bundled; audit plugin will fail to start until present"
fi

# node_exporter + process_exporter — bundled exporter binaries used by
# the hostmetrics / procmetrics edge plugins. Install-edge.sh just
# drops them under ${PLUGIN_BIN_DIR}; the ongrid-edge plugin
# supervisor owns their lifecycle (manager pushes enable + spec via
# the existing PluginConfig mechanism; no separate systemd units).
NODE_EXPORTER_SRC="${SCRIPT_DIR}/node_exporter-${OS}-${ARCH}"
if [[ -f "$NODE_EXPORTER_SRC" ]]; then
    log_info "installing node_exporter to ${PLUGIN_BIN_DIR}/node_exporter"
    install -m 0755 -o root -g root "$NODE_EXPORTER_SRC" "${PLUGIN_BIN_DIR}/node_exporter"
else
    log_warn "node_exporter-${OS}-${ARCH} not bundled; hostmetrics plugin will fail to start until present"
fi

PROCESS_EXPORTER_SRC="${SCRIPT_DIR}/process_exporter-${OS}-${ARCH}"
if [[ -f "$PROCESS_EXPORTER_SRC" ]]; then
    log_info "installing process_exporter to ${PLUGIN_BIN_DIR}/process_exporter"
    install -m 0755 -o root -g root "$PROCESS_EXPORTER_SRC" "${PLUGIN_BIN_DIR}/process_exporter"
else
    log_warn "process_exporter-${OS}-${ARCH} not bundled; procmetrics plugin will fail to start until present"
fi

# Database exporters used by databasemetrics. They are optional at install
# time because the plugin is disabled until an operator configures database
# sources, but surfacing missing binaries here makes future enablement clear.
for exporter in mysqld_exporter postgres_exporter redis_exporter mongodb_exporter; do
    src="${SCRIPT_DIR}/${exporter}-${OS}-${ARCH}"
    if [[ -f "$src" ]]; then
        log_info "installing ${exporter} to ${PLUGIN_BIN_DIR}/${exporter}"
        install -m 0755 -o root -g root "$src" "${PLUGIN_BIN_DIR}/${exporter}"
    else
        log_warn "${exporter}-${OS}-${ARCH} not bundled; databasemetrics sources using it will fail until present"
    fi
done

# ---------- render env file ----------
log_info "rendering $ENV_FILE"
mkdir -p "$CONFIG_DIR"
chmod 750 "$CONFIG_DIR"
chown root:"$SERVICE_GROUP" "$CONFIG_DIR" 2>/dev/null || true

esc() { printf '%s' "$1" | sed -e 's/[\\|&]/\\&/g'; }

TEMPLATE="${SCRIPT_DIR}/ongrid-edge.env.example"
if [[ ! -f "$TEMPLATE" ]]; then
    log_error "missing template: $TEMPLATE"
    exit 1
fi
# Render the template to $ENV_FILE with operator-prefixed plugin / state /
# upgrade paths so the agent's runtime matches the --prefix layout the
# systemd unit + apply-pending-upgrade.sh hook are bound to. If the env file
# already exists (re-run with same keys) we keep its previous values for
# non-template keys (e.g. ONGRID_EDGE_PLUGIN_LOGS_ENDPOINT that the operator
# has hand-tuned) by overwriting only the prefix-bound lines.
sed \
    -e "s|__CLOUD_ADDR__|$(esc "$ONGRID_CLOUD_ADDR")|g" \
    -e "s|__ACCESS_KEY__|$(esc "$EDGE_ACCESS_KEY")|g" \
    -e "s|__SECRET_KEY__|$(esc "$EDGE_SECRET_KEY")|g" \
    -e "s|__PLUGIN_BIN_DIR__|$(esc "$PLUGIN_BIN_DIR")|g" \
    -e "s|__PLUGIN_WORK_DIR__|$(esc "$PLUGIN_WORK_DIR")|g" \
    -e "s|__UPGRADE_STAGE_DIR__|$(esc "${STATE_DIR}/.upgrade")|g" \
    -e "s|__SCRAPE_CONFIG_FILE__|$(esc "${CONFIG_DIR}/scrape.yaml")|g" \
    "$TEMPLATE" > "$ENV_FILE"
chmod 640 "$ENV_FILE"
chown root:"$SERVICE_GROUP" "$ENV_FILE" 2>/dev/null || true

# ---------- log dir ----------
mkdir -p "$LOG_DIR"
chown "$SERVICE_USER":"$SERVICE_GROUP" "$LOG_DIR" 2>/dev/null || true
chmod 750 "$LOG_DIR"

# ---------- systemd units ----------
log_info "installing systemd units"
# Render the operator-chosen --prefix into the unit templates so the
# sandboxed agent + the privileged upgrade oneshot find their binaries +
# env file under $PREFIX instead of the historical /usr/local{,/lib}/,
# /etc/, /var/{lib,log}/ defaults. The templates ship with __PREFIX__ /
# __BIN_DIR__ / __LIB_DIR__ / __ENV_FILE__ / __STATE_DIR__ / __LOG_DIR__
# placeholders (see ongrid-edge.service / ongrid-edge-upgrade.service).
# We install to a .tmp first, then atomically install(1) over the live
# unit so a re-install never leaves the systemd tree half-rendered.
render_unit() {
    local template="$1" target="$2"
    sed \
        -e "s|__PREFIX__|$(esc "$PREFIX")|g" \
        -e "s|__BIN_DIR__|$(esc "$BIN_DIR")|g" \
        -e "s|__LIB_DIR__|$(esc "$PLUGIN_BIN_DIR")|g" \
        -e "s|__APPLY_HOOK__|$(esc "$APPLY_HOOK")|g" \
        -e "s|__ENV_FILE__|$(esc "$ENV_FILE")|g" \
        -e "s|__STATE_DIR__|$(esc "$STATE_DIR")|g" \
        -e "s|__LOG_DIR__|$(esc "$LOG_DIR")|g" \
        "$template" > "${target}.tmp"
    install -m 0644 -o root -g root "${target}.tmp" "$target"
    rm -f "${target}.tmp"
}
# Privileged apply oneshot (ADR-024) — runs apply-pending-upgrade.sh as root
# before the agent. ongrid-edge.service pulls it via Wants=. Guarded so an
# older bundle without this file still installs the agent unit.
UPGRADE_UNIT_SRC="${SCRIPT_DIR}/ongrid-edge-upgrade.service"
if [[ -f "$UPGRADE_UNIT_SRC" ]]; then
    render_unit "$UPGRADE_UNIT_SRC" "$UPGRADE_UNIT_FILE"
else
    log_warn "ongrid-edge-upgrade.service not bundled; remote whole-bundle upgrade won't apply on this host"
fi
render_unit "${SCRIPT_DIR}/ongrid-edge.service" "$UNIT_FILE"
systemctl daemon-reload

log_info "enabling + (re)starting ongrid-edge"
# `restart` (not `enable --now`) so a re-install over an already-running edge
# actually picks up the freshly-written env (new keys / cloud addr). `enable
# --now` only `start`s, which is a no-op when the old process is still up — it
# would keep dialing with the previous credentials and fail "unauthorized".
systemctl enable ongrid-edge >/dev/null 2>&1 || true
systemctl restart ongrid-edge
sleep 5

echo ""
if systemctl is-active --quiet ongrid-edge; then
    log_info "ongrid-edge is running"
else
    log_warn "ongrid-edge is NOT active; see status below"
fi
systemctl status ongrid-edge --no-pager || true

echo ""
log_info "last 20 log lines:"
journalctl -u ongrid-edge -n 20 --no-pager || true

# ---------- post-install self-check ----------
# The #1 silent failure is "agent runs but ships nothing": a plugin binary
# wasn't bundled, the service user can't read the journal, or the data
# plane host is unreachable. Surface all three loudly here instead of
# letting the operator discover empty Loki / Prometheus days later. All
# checks are guarded (inside `if`) so they never trip the ERR trap.
echo ""
echo "${C_BOLD}${C_CYAN}--- self-check ---${C_RESET}"
SELFCHECK_FAIL=0

# 1) plugin binaries present + executable
for tool in promtail otelcol-contrib node_exporter process_exporter mysqld_exporter postgres_exporter redis_exporter mongodb_exporter; do
    if [[ -x "${PLUGIN_BIN_DIR}/${tool}" ]]; then
        log_info "plugin binary present: ${tool}"
    else
        log_error "plugin binary MISSING: ${PLUGIN_BIN_DIR}/${tool} — that plugin will not run (incomplete bundle?)"
        SELFCHECK_FAIL=1
    fi
done

# 1b) state dir writable by the service user. On systemd < 235 (CentOS/RHEL 7)
# the unit's StateDirectory= is silently ignored, so this probe catches the
# "online but no data" failure: without a writable $STATE_DIR every
# collector plugin fails `configure` with EACCES. The unit no longer sets
# StateDirectory= at all (StateDirectory= always pins to /var/lib/<name> and
# can't follow --prefix); the installer pre-creating + chowning $STATE_DIR
# is what gives the sandboxed agent writability.
if command -v runuser >/dev/null 2>&1; then
    SVC_W=(runuser -u "$SERVICE_USER" -- test -w "$STATE_DIR")
else
    SVC_W=(sudo -u "$SERVICE_USER" test -w "$STATE_DIR")
fi
if [[ -d "$STATE_DIR" ]] && "${SVC_W[@]}" 2>/dev/null; then
    log_info "state dir writable by $SERVICE_USER: $STATE_DIR"
else
    log_error "$SERVICE_USER cannot write $STATE_DIR — every collector plugin will fail; edge will be online with no data"
    log_error "  fix: mkdir -p $STATE_DIR; chown $SERVICE_USER:$SERVICE_GROUP $STATE_DIR; chmod 0755 $STATE_DIR; systemctl restart ongrid-edge"
    SELFCHECK_FAIL=1
fi

# 2) service user can read the journal (logs plugin journald source).
# Group membership added above is visible to a fresh runuser/sudo session.
if command -v runuser >/dev/null 2>&1; then
    JREAD=(runuser -u "$SERVICE_USER" -- journalctl -n 1 --no-pager)
else
    JREAD=(sudo -u "$SERVICE_USER" journalctl -n 1 --no-pager)
fi
if "${JREAD[@]}" >/dev/null 2>&1; then
    log_info "journald readable by $SERVICE_USER"
else
    log_error "$SERVICE_USER cannot read the journal — journald log shipping will be empty"
    log_error "  fix: usermod -aG systemd-journal $SERVICE_USER ; ensure persistent journal (/var/log/journal)"
    SELFCHECK_FAIL=1
fi

# 3) data-plane host reachability. The exact URL comes from the manager's
# ONGRID_PUBLIC_URL at runtime, but the host is almost always the tunnel
# host — probe TCP 443 there as a smoke test. WARN (not fail) since the
# real port/host may differ.
DP_HOST="${ONGRID_CLOUD_ADDR%%:*}"
if [[ -n "$DP_HOST" ]]; then
    if timeout 5 bash -c "exec 3<>/dev/tcp/${DP_HOST}/443" 2>/dev/null; then
        log_info "data-plane host ${DP_HOST}:443 reachable (TCP)"
    else
        log_warn "data-plane host ${DP_HOST}:443 not reachable from here — logs/traces push may fail"
        log_warn "  ensure the manager's ONGRID_PUBLIC_URL points to an address THIS edge can reach"
    fi
fi

if [[ $SELFCHECK_FAIL -eq 0 ]]; then
    log_info "self-check passed"
else
    log_error "self-check found problems above — agent is up but some telemetry will be missing until fixed"
fi

echo ""
echo "${C_BOLD}${C_CYAN}===============================================================${C_RESET}"
echo "${C_BOLD}${C_GREEN}  ongrid-edge installed${C_RESET}"
echo "${C_BOLD}${C_CYAN}===============================================================${C_RESET}"
echo "Install root: $PREFIX"
echo "Binary:       $BIN_DEST"
echo "Plugin dir:   $PLUGIN_BIN_DIR"
echo "Env file:     $ENV_FILE"
echo "State dir:    $STATE_DIR"
echo "Log dir:      $LOG_DIR"
echo "Unit file:    $UNIT_FILE"
echo "Logs:         journalctl -u ongrid-edge -f"
echo "Cloud addr:   $ONGRID_CLOUD_ADDR"
echo ""
echo "Uninstall:    sudo $0 --uninstall --prefix=$PREFIX"
echo "Full wipe:    sudo rm -rf $PREFIX"
echo ""
