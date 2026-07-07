#!/usr/bin/env bash
# ongrid-edge curl-pipe uninstaller.
#
# Usage:
#   curl -k -sSL https://<server>/uninstall.sh | bash
#
# Wipes the agent end-to-end: systemd units (agent + bundled exporters),
# binary, env, logs, bundled plugin binaries, plugin work dir (rendered
# configs + subprocess logs + .upgrade stage dir), and the service user.
# Idempotent — safe to re-run.

set -euo pipefail

# --prefix=PATH 与 install.sh / install-edge.sh 一致（默认 /mnt/data/tools-temp）。
# edge 的所有路径嵌套在 ${PREFIX}/ongrid-edge/ 下面，卸载时同样依 PREFIX 拼接。
PREFIX="${ONGRID_EDGE_PREFIX:-/mnt/data/tools-temp}"
while [[ $# -gt 0 ]]; do
    case "$1" in
        --prefix=*) PREFIX="${1#*=}"; shift ;;
        -h|--help)
            cat <<EOF
Usage: sudo $0 [OPTIONS]

Options:
  --prefix=PATH   Install root to uninstall (default /mnt/data/tools-temp).
                  Must match the --prefix used at install time; otherwise
                  the wipe misses the actual on-disk install.
  -h, --help      Show this help.
EOF
            exit 0 ;;
        *) echo "[ERROR] unknown arg: $1" >&2; exit 2 ;;
    esac
done

# 所有路径从 EDGE_ROOT=${PREFIX}/ongrid-edge 派生，与 install.sh / install-edge.sh 保持一致。
EDGE_ROOT="${PREFIX}/ongrid-edge"
BIN_DIR="${EDGE_ROOT}/bin"
LIB_DIR="${EDGE_ROOT}/lib/ongrid-edge"
ENV_DIR="${EDGE_ROOT}/etc/ongrid-edge"
LOG_DIR="${EDGE_ROOT}/var/log/ongrid-edge"
STATE_DIR="${EDGE_ROOT}/var/lib/ongrid-edge"
SERVICE_FILE="/etc/systemd/system/ongrid-edge.service"
UPGRADE_SERVICE_FILE="/etc/systemd/system/ongrid-edge-upgrade.service"
SERVICE_USER="ongrid-edge"
# Wholesale plugin dirs: bundled binaries (promtail, node_exporter,
# process_exporter, ...) and plugin work state (configs + textfile
# producer outputs + .upgrade stage). Both are agent-owned; leaving
# either behind makes reinstall non-deterministic.

if [[ $EUID -ne 0 ]]; then
    echo "[INFO] re-executing with sudo"
    exec sudo -E bash "$0" "$@"
fi

# Stop + disable units unconditionally. The previous "list-unit-files
# | grep -q ^name.service" precondition silently skipped the stop on
# hosts where the output formatting (leading whitespace, deprecated/
# masked decorations, paged output) confused the anchored grep. The
# uninstaller then went on to rm -f the binary while the supervisor
# was still running — agent + every subprocess survived, printed [OK].
# `systemctl stop` is safe to call on an unknown unit (logs to stderr,
# returns non-zero) so we just suppress + ignore failures.
systemctl stop    ongrid-edge ongrid-edge-upgrade ongrid-node-exporter ongrid-process-exporter 2>/dev/null || true
systemctl disable ongrid-edge ongrid-edge-upgrade ongrid-node-exporter ongrid-process-exporter 2>/dev/null || true

# Defensive: if systemd never actually managed the agent (manual
# install, broken unit file, etc.), kill the supervisor and any
# subprocess plugins by binary path. Matches every plugin (promtail,
# otelcol-contrib, node_exporter, process_exporter, ...) without
# enumerating them. The path is prefix-bound; for the default
# --prefix=/mnt/data/tools-temp it becomes:
#   /mnt/data/tools-temp/bin/ongrid-edge
#   /mnt/data/tools-temp/lib/ongrid-edge/...
pkill -9 -f "${BIN_DIR}/ongrid-edge|${LIB_DIR}/" 2>/dev/null || true

rm -f "$SERVICE_FILE" "$UPGRADE_SERVICE_FILE" "${BIN_DIR}/ongrid-edge"
rm -f /etc/systemd/system/ongrid-node-exporter.service
rm -f /etc/systemd/system/ongrid-process-exporter.service
rm -rf "$ENV_DIR"
rm -rf "$LOG_DIR"
rm -rf "$LIB_DIR"
rm -rf "$STATE_DIR"

systemctl daemon-reload 2>/dev/null || true

# Remove the dedicated service user (best-effort).
if id -u "$SERVICE_USER" >/dev/null 2>&1; then
    userdel "$SERVICE_USER" 2>/dev/null || true
fi

echo "[OK] ongrid-edge uninstalled (install root was $EDGE_ROOT; full wipe: rm -rf $EDGE_ROOT)"
