#!/usr/bin/env bash
# ongrid pure-systemd uninstaller. Mirror of install-systemd.sh.
#
# Default: stop + disable + remove unit files; preserve data dirs + env.
# --purge: also nuke data/log/config roots (driven by ONGRID_INSTALL_*,
#          same dual-mode layout as install-systemd.sh) + the four
#          fixed systemd-managed StateDirectory= subdirs + service users.

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)

if [[ -t 1 ]]; then
    C_RED=$'\033[0;31m'; C_GREEN=$'\033[0;32m'; C_YELLOW=$'\033[1;33m'
    C_BOLD=$'\033[1m'; C_RESET=$'\033[0m'
else
    C_RED=''; C_GREEN=''; C_YELLOW=''; C_BOLD=''; C_RESET=''
fi

log()  { printf '%s[INFO]%s %s\n'  "$C_GREEN"  "$C_RESET" "$*"; }
warn() { printf '%s[WARN]%s %s\n'  "$C_YELLOW" "$C_RESET" "$*"; }
err()  { printf '%s[ERROR]%s %s\n' "$C_RED"    "$C_RESET" "$*" >&2; }

PURGE=0
ASSUME_YES=0
usage() {
    cat <<EOF
Usage: sudo uninstall-systemd.sh [OPTIONS]

Options:
  --purge   Also delete the manager / dep data dirs, log dir, config dir
            (paths resolved from ONGRID_INSTALL_PREFIX / ONGRID_INSTALL_BIN
            / ONGRID_INSTALL_ETC / ONGRID_INSTALL_STATE / ONGRID_INSTALL_LOG
            — same layout as install-systemd.sh) plus the four fixed
            systemd-managed StateDirectory= subdirs (/var/lib/ongrid-prometheus,
            /var/lib/ongrid-loki, /var/lib/ongrid-tempo, /var/lib/ongrid-qdrant)
            and the ongrid service users.
            Manager + dep data (DB, vectors, metrics, logs) is lost.
  --yes     Skip the confirmation prompt (only with --purge).
  -h        Print this help.

Without --purge, units are stopped + removed but data + users remain so a
later install-systemd.sh resumes where you left off.
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --purge) PURGE=1; shift ;;
        --yes|-y) ASSUME_YES=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) err "unknown flag: $1"; usage; exit 2 ;;
    esac
done

if [[ $EUID -ne 0 ]]; then
    err "must run as root (sudo)"
    exit 1
fi

# -----------------------------------------------------------------------------
# paths — same dual-mode resolution as install-systemd.sh so --purge cleans
# whatever install-systemd.sh wrote. Override any of the four ONGRID_INSTALL_*
# vars before invoking this script to match a non-default layout.
#
# Default (FHS, ONGRID_INSTALL_PREFIX=/usr/local):
#   PREFIX_BIN=/usr/local/bin   ETC_DIR=/etc/ongrid
#   STATE_DIR=/var/lib/ongrid   LOG_DIR=/var/log/ongrid
#
# Collapsed (ONGRID_INSTALL_PREFIX=/opt/ongrid, matches compose mode):
#   PREFIX_BIN=$PREFIX/bin      ETC_DIR=$PREFIX/ongrid
#   STATE_DIR=$PREFIX/data      LOG_DIR=$PREFIX/logs
#
# The four per-dep StateDirectory= subdirs (/var/lib/ongrid-prometheus /
# -loki / -tempo / -qdrant) are managed by systemd, not by us — they always
# map to /var/lib/<name> regardless of mode and are purged separately below.
# -----------------------------------------------------------------------------
PREFIX="${ONGRID_INSTALL_PREFIX:-/usr/local}"
if [[ "$PREFIX" == "/usr/local" ]]; then
    PREFIX_BIN="${ONGRID_INSTALL_BIN:-/usr/local/bin}"
    ETC_DIR="${ONGRID_INSTALL_ETC:-/etc/ongrid}"
    STATE_DIR="${ONGRID_INSTALL_STATE:-/var/lib/ongrid}"
    LOG_DIR="${ONGRID_INSTALL_LOG:-/var/log/ongrid}"
else
    PREFIX_BIN="${ONGRID_INSTALL_BIN:-$PREFIX/bin}"
    ETC_DIR="${ONGRID_INSTALL_ETC:-$PREFIX/ongrid}"
    STATE_DIR="${ONGRID_INSTALL_STATE:-$PREFIX/data}"
    LOG_DIR="${ONGRID_INSTALL_LOG:-$PREFIX/logs}"
fi

UNITS=(ongrid.service ongrid-frontier.service \
       prometheus.service loki.service tempo.service qdrant.service)
SYSTEMD_DIR=/etc/systemd/system

# -----------------------------------------------------------------------------
# stop + disable
# -----------------------------------------------------------------------------
for u in "${UNITS[@]}"; do
    if systemctl is-active --quiet "$u" 2>/dev/null; then
        systemctl stop "$u" || warn "stop $u failed"
        log "stopped $u"
    fi
    if systemctl is-enabled --quiet "$u" 2>/dev/null; then
        systemctl disable "$u" >/dev/null 2>&1 || warn "disable $u failed"
        log "disabled $u"
    fi
done

# -----------------------------------------------------------------------------
# stragglers — manager + frontier might be wedged outside systemd's view
# -----------------------------------------------------------------------------
for proc in "$PREFIX_BIN/ongrid" "$PREFIX_BIN/ongrid-frontier"; do
    pids=$(pgrep -f "^$proc" 2>/dev/null || true)
    if [[ -n "$pids" ]]; then
        warn "killing straggler $proc (pids: $pids)"
        kill -TERM $pids 2>/dev/null || true
        sleep 2
        pids=$(pgrep -f "^$proc" 2>/dev/null || true)
        if [[ -n "$pids" ]]; then
            warn "force-killing $proc (pids: $pids)"
            kill -KILL $pids 2>/dev/null || true
        fi
    fi
done

# -----------------------------------------------------------------------------
# unit files + binaries
# -----------------------------------------------------------------------------
for u in "${UNITS[@]}"; do
    if [[ -f "$SYSTEMD_DIR/$u" ]]; then
        rm -f "$SYSTEMD_DIR/$u"
        log "removed $SYSTEMD_DIR/$u"
    fi
done
for bin in ongrid ongrid-frontier; do
    if [[ -f "$PREFIX_BIN/$bin" ]]; then
        rm -f "$PREFIX_BIN/$bin"
        log "removed $PREFIX_BIN/$bin"
    fi
done
systemctl daemon-reload

# -----------------------------------------------------------------------------
# stop-only short-circuit
# -----------------------------------------------------------------------------
if [[ $PURGE -eq 0 ]]; then
    echo ""
    echo "${C_BOLD}${C_GREEN}stop-only uninstall complete${C_RESET}"
    echo "  - units stopped + removed"
    echo "  - data dirs preserved (STATE=$STATE_DIR, LOG=$LOG_DIR)"
    echo "  - configs preserved (ETC=$ETC_DIR)"
    echo "  - service users preserved (ongrid, ongrid-prometheus, ...)"
    echo ""
    echo "Re-install with: sudo bash install-systemd.sh"
    echo "Wipe data with:  sudo bash uninstall-systemd.sh --purge"
    exit 0
fi

# -----------------------------------------------------------------------------
# purge — confirm + delete
# -----------------------------------------------------------------------------
if [[ $ASSUME_YES -eq 0 ]]; then
    printf "%sThis deletes ALL ongrid data:\n" "$C_YELLOW"
    printf "  - STATE_DIR=%s (manager state + embedding cache)\n" "$STATE_DIR"
    printf "  - systemd-managed dep data: /var/lib/ongrid-prometheus, /var/lib/ongrid-loki, /var/lib/ongrid-tempo, /var/lib/ongrid-qdrant\n"
    printf "  - LOG_DIR=%s (all logs)\n" "$LOG_DIR"
    printf "  - ETC_DIR=%s (configs, secrets, unit-bundled prometheus/loki/tempo configs)\n" "$ETC_DIR"
    printf "  - service users (ongrid, ongrid-prometheus, ongrid-loki, ongrid-tempo, ongrid-qdrant)\n"
    printf "Continue? [y/N] %s" "$C_RESET"
    read -r answer
    case "$answer" in
        y|Y|yes|YES) ;;
        *) log "aborted"; exit 0 ;;
    esac
fi

# Config root (etc), state root (data), log root — resolved from
# ONGRID_INSTALL_* so non-FHS installs clean up where they wrote.
for d in "$STATE_DIR" "$LOG_DIR" "$ETC_DIR"; do
    if [[ -d "$d" ]]; then
        rm -rf "$d"
        log "removed $d"
    fi
done
# systemd-managed StateDirectory= subdirs — fixed paths regardless of
# install mode (StateDirectory= maps to /var/lib/<name> in FHS even when
# the rest of the install is collapsed under a custom prefix).
for d in /var/lib/ongrid-prometheus /var/lib/ongrid-loki \
         /var/lib/ongrid-tempo /var/lib/ongrid-qdrant; do
    if [[ -d "$d" ]]; then
        rm -rf "$d"
        log "removed $d (systemd StateDirectory=)"
    fi
done

for u in ongrid ongrid-prometheus ongrid-loki ongrid-tempo ongrid-qdrant; do
    if id "$u" &>/dev/null; then
        userdel "$u" 2>/dev/null || warn "userdel $u failed"
        log "removed user $u"
    fi
done

echo ""
echo "${C_BOLD}${C_GREEN}purge complete${C_RESET}"
echo "  - units removed"
echo "  - data + log + config roots removed (STATE=$STATE_DIR, LOG=$LOG_DIR, ETC=$ETC_DIR)"
echo "  - systemd-managed dep StateDirectory= subdirs removed"
echo "  - service users removed"
echo ""
echo "Note: OS-package deps (mariadb-server, nginx, grafana, the prom/loki/"
echo "tempo/qdrant binaries you may have placed in $PREFIX_BIN) were NOT"
echo "touched. Remove with your package manager if no longer needed."
