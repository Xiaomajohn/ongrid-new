#!/usr/bin/env bash
# ongrid uninstall.sh - stop stack; optionally purge data volume and install dir.

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)

if [[ -t 1 ]]; then
    C_RED=$'\033[0;31m'; C_GREEN=$'\033[0;32m'; C_YELLOW=$'\033[1;33m'
    C_BOLD=$'\033[1m'; C_RESET=$'\033[0m'
else
    C_RED=''; C_GREEN=''; C_YELLOW=''; C_BOLD=''; C_RESET=''
fi

log_info()  { printf '%s[INFO]%s %s\n'  "$C_GREEN"  "$C_RESET" "$*"; }
log_warn()  { printf '%s[WARN]%s %s\n'  "$C_YELLOW" "$C_RESET" "$*"; }
log_error() { printf '%s[ERROR]%s %s\n' "$C_RED"    "$C_RESET" "$*" >&2; }

trap 'log_error "uninstall failed at line $LINENO"' ERR

PURGE=0
ASSUME_YES=0
MODE="auto"
PURGE_EDGE=0

usage() {
    cat <<EOF
Usage: sudo ./uninstall.sh [OPTIONS]

Options:
  --mode <auto|compose|systemd>
                  Pick the uninstall path. auto (default) detects from the
                  filesystem: presence of /etc/systemd/system/ongrid.service
                  implies systemd; presence of /opt/ongrid/docker-compose.yml
                  implies compose. Specify explicitly when both exist.
  --purge         Also delete data + install dir / data dirs. DESTRUCTIVE.
  --purge-edge    On a host that also has an ongrid-edge daemon, run its
                  uninstaller too. Without this flag --purge only warns
                  about the leftover edge (which keeps running with a
                  now-invalid access_key after MySQL is wiped).
  --yes           Skip the y/N confirmation prompt (only with --purge).
  -h, --help      Print this help.

Without --purge, containers/units are stopped but data is preserved so a
later install.sh resumes where you left off.
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --mode) MODE="${2:-}"; shift 2 ;;
        --mode=*) MODE="${1#*=}"; shift ;;
        --purge) PURGE=1; shift ;;
        --purge-edge) PURGE_EDGE=1; shift ;;
        --yes|-y) ASSUME_YES=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) log_error "unknown flag: $1"; usage; exit 2 ;;
    esac
done

# -------- mode dispatch (must happen before we touch INSTALL_DIR) --------
# install.sh + upgrade.sh both default INSTALL_DIR=/opt/ongrid with
# docker-compose.yml living DIRECTLY under it (single-level; no nested
# /ongrid/ subdir — see install.sh:593-609 + comment "There is no
# separate PARENT_DIR/ongrid nesting"). Earlier versions of this script
# still checked the legacy <parent>/ongrid/docker-compose.yml path; that
# never matched real installs and forced the dispatcher to fall through
# to the "no install detected — running systemd --purge anyway" branch,
# which then ran the systemd cleaner against compose-mode hosts (zero
# effect: it removed only what install-systemd.sh would have created —
# /var/lib/ongrid*, /etc/ongrid, ongrid-* service users — none of which
# exist on a docker host). The compose stack stayed up, the bind-mount
# /opt/ongrid/data survived, and the named volumes ongrid_mysql_data /
# ongrid_logs were never reaped — exactly the failure mode users hit.
# Default to the canonical location used by install.sh today, but keep
# a best-effort fallback probe for the legacy nested location so we
# still detect an older install if one slips through.
detect_install_mode() {
    local has_systemd=0 has_compose=0
    [[ -f /etc/systemd/system/ongrid.service ]] && has_systemd=1
    local install_dir="${ONGRID_INSTALL_DIR:-/opt/ongrid}"
    [[ -f "$install_dir/docker-compose.yml" ]] && has_compose=1
    # Legacy fallback (pre single-parent-refactor installs). Without
    # this, an operator who upgraded across the refactor boundary with
    # an old install_root that still nests compose under <parent>/ongrid
    # would silently fail to detect and fall into the systemd branch.
    local parent="${ONGRID_INSTALL_DIR:-/opt/ongrid}"
    if (( ! has_compose )) && [[ -f "$parent/ongrid/docker-compose.yml" ]]; then
        log_warn "detected legacy nested compose layout at $parent/ongrid/ — consider re-installing with the current single-parent layout"
        has_compose=1
    fi
    if (( has_systemd && has_compose )); then
        log_warn "both systemd and compose installs detected — pick one with --mode"
        exit 2
    elif (( has_systemd )); then echo systemd
    elif (( has_compose )); then echo compose
    else echo none
    fi
}
if [[ "$MODE" == "auto" ]]; then
    MODE=$(detect_install_mode)
    log_info "auto-detected install mode: $MODE"
fi
case "$MODE" in
    compose) ;;   # fall through to legacy uninstall
    systemd)
        if [[ ! -x "$SCRIPT_DIR/systemd/uninstall-systemd.sh" ]]; then
            log_error "systemd uninstaller missing: $SCRIPT_DIR/systemd/uninstall-systemd.sh"
            exit 2
        fi
        log_info "dispatching to systemd uninstaller"
        purge_arg=()
        (( PURGE ))      && purge_arg+=(--purge)
        (( ASSUME_YES )) && purge_arg+=(--yes)
        exec bash "$SCRIPT_DIR/systemd/uninstall-systemd.sh" "${purge_arg[@]}"
        ;;
    none)
        if (( PURGE )); then
            # An earlier uninstall (or partial install) may have left
            # data dirs + service users behind even though the unit
            # files are gone. Honour --purge by dispatching to the
            # systemd cleaner — it no-ops for anything that's already
            # gone but reaps leftover /var/lib/ongrid* + users.
            log_warn "no live install detected — running systemd --purge anyway to clean stragglers"
            if [[ -x "$SCRIPT_DIR/systemd/uninstall-systemd.sh" ]]; then
                purge_arg=(--purge)
                (( ASSUME_YES )) && purge_arg+=(--yes)
                exec bash "$SCRIPT_DIR/systemd/uninstall-systemd.sh" "${purge_arg[@]}"
            fi
            log_error "no install detected and no systemd cleaner available"
            exit 0
        fi
        log_warn "no ongrid install detected (no systemd unit, no compose file)"
        exit 0
        ;;
    *)
        log_error "--mode must be one of: auto, compose, systemd"; exit 2 ;;
esac

if [[ $EUID -ne 0 ]]; then
    log_warn "not running as root; re-executing via sudo"
    exec sudo -E bash "$0" "$@"
fi

# Single-parent layout: install.sh/upgrade.sh now write docker-compose.yml
# directly under $INSTALL_DIR (no PARENT/ongrid/ nesting — see comment in
# detect_install_mode above). Operators who redirect ONGRID_INSTALL_DIR /
# ONGRID_DATA_DIR / ONGRID_LOG_DIR / ONGRID_WEB_DIR get the matching dirs
# cleaned here. On a legacy install whose install_root is the old
# <parent>/ongrid/ nested path, override ONGRID_INSTALL_DIR to point at
# that legacy dir (this script then treats it as the canonical install_dir).
INSTALL_DIR="${ONGRID_INSTALL_DIR:-/opt/ongrid}"
WEB_DIR="${ONGRID_WEB_DIR:-$INSTALL_DIR/ongrid-web}"
COMPOSE_FILE="$INSTALL_DIR/docker-compose.yml"
ENV_FILE="$INSTALL_DIR/.env"
DATA_DIR="${ONGRID_DATA_DIR:-$INSTALL_DIR/data}"
LOG_DIR="${ONGRID_LOG_DIR:-$INSTALL_DIR/logs}"

if [[ ! -f "$COMPOSE_FILE" ]]; then
    log_warn "no compose file at $COMPOSE_FILE; nothing to stop"
else
    log_info "stopping ongrid stack"
    (
        cd "$INSTALL_DIR"
        # No explicit -f so docker-compose.override.yml (if present) loads too.
        if [[ -f "$ENV_FILE" ]]; then
            docker compose --env-file .env down || true
        else
            docker compose down || true
        fi
    )
fi

if [[ $PURGE -eq 0 ]]; then
    log_info "stop-only (containers down, volumes and $INSTALL_DIR preserved)"
    log_info "run again with --purge to wipe data."
    exit 0
fi

# Confirm destructive action.
if [[ $ASSUME_YES -eq 0 ]]; then
    printf "%sThis will delete the MySQL data volume AND %s. Continue? [y/N] %s" \
        "$C_YELLOW" "$INSTALL_DIR" "$C_RESET"
    read -r answer
    case "$answer" in
        y|Y|yes|YES) ;;
        *) log_info "aborted by user"; exit 0 ;;
    esac
fi

VERSION_FROM_FILE=""
if [[ -f "$INSTALL_DIR/VERSION" ]]; then
    VERSION_FROM_FILE=$(tr -d '[:space:]' < "$INSTALL_DIR/VERSION" || true)
elif [[ -f "$ENV_FILE" ]]; then
    VERSION_FROM_FILE=$(grep -E '^ONGRID_VERSION=' "$ENV_FILE" | cut -d= -f2- | tr -d '[:space:]' || true)
fi

log_info "removing named volumes"
docker volume rm ongrid_mysql_data ongrid_logs 2>/dev/null || true

# /opt/ongrid/data is the bind-mount root used by ADR-026 (mysql /
# prometheus / loki / tempo / grafana / qdrant data dirs live here).
# Without this, a fresh install.sh after --purge picks up the old
# mysql data dir with the previous random password but writes a *new*
# password into .env — manager crashloops with `Access denied for user
# 'ongrid'@…`. Discovered during the 2026-05-20 reinstall smoke.
# DATA_DIR resolves to $INSTALL_DIR/data above (single-parent layout),
# so to point uninstall.sh at a non-default data root just export
# ONGRID_DATA_DIR before invoking.
if [[ -d "$DATA_DIR" ]]; then
    log_info "removing bind-mount data dirs in $DATA_DIR (mysql/qdrant/prom/loki/tempo/grafana/embeddings/skills/pages/workspace/tools)"
    for d in mysql qdrant prometheus loki tempo grafana embeddings skills pages workspace tools; do
        if [[ -d "$DATA_DIR/$d" ]]; then
            rm -rf "$DATA_DIR/$d"
        fi
    done
    # Only remove the parent dir itself if empty — operators sometimes
    # park other state under the data root (edge bundles, skill caches)
    # that we don't want to touch.
    rmdir "$DATA_DIR" 2>/dev/null || true
fi

# Remove the manager process logs dir (best-effort: only if empty after
# removing its contents; never touch host-side log shipping destinations).
if [[ -d "$LOG_DIR" ]]; then
    log_info "removing manager log dir $LOG_DIR"
    rm -rf "$LOG_DIR"
fi

log_info "removing compose install dir $INSTALL_DIR"
rm -rf "$INSTALL_DIR"

# ongrid-web (nginx) container's host inputs: nginx.conf, TLS certs/,
# one-button edge upgrade binaries. Same single-parent subdir as
# install.sh writes; --purge wipes it too so a re-install starts clean.
if [[ -d "$WEB_DIR" ]]; then
    log_info "removing ongrid-web inputs $WEB_DIR"
    rm -rf "$WEB_DIR"
fi

if [[ -n "$VERSION_FROM_FILE" ]]; then
    log_info "removing ongrid:${VERSION_FROM_FILE} image"
    docker image rm "ongrid:${VERSION_FROM_FILE}" 2>/dev/null || true
fi

# Co-resident edge daemon — when the operator installed ongrid-edge on
# the same host (test/dev convenience) the manager's MySQL wipe just
# invalidated its access_key, but the daemon keeps running with stale
# creds. The result: edge stays "online" in systemd but the new manager
# rejects every push, Grafana panels stay blank. Detect + offer to
# uninstall (or just warn loudly).
local_edge_present=0
if systemctl list-unit-files ongrid-edge.service 2>/dev/null | grep -q ongrid-edge \
        || [[ -x /usr/local/bin/ongrid-edge ]]; then
    local_edge_present=1
fi
if (( local_edge_present )); then
    if (( PURGE_EDGE )); then
        log_info "uninstalling co-resident ongrid-edge (--purge-edge given)"
        if [[ -x "$SCRIPT_DIR/edge/uninstall.sh" ]]; then
            bash "$SCRIPT_DIR/edge/uninstall.sh" 2>&1 | tail -5 || true
        else
            systemctl stop ongrid-edge ongrid-edge-upgrade 2>/dev/null || true
            systemctl disable ongrid-edge ongrid-edge-upgrade 2>/dev/null || true
            rm -f /etc/systemd/system/ongrid-edge.service /etc/systemd/system/ongrid-edge-upgrade.service
            rm -f /usr/local/bin/ongrid-edge
            rm -rf /etc/ongrid-edge /var/lib/ongrid-edge /var/log/ongrid-edge
            systemctl daemon-reload
            log_info "ongrid-edge stopped + files removed"
        fi
    else
        log_warn "ongrid-edge daemon detected on this host — it kept its OLD"
        log_warn "access_key in /etc/ongrid-edge/ongrid-edge.env, but the manager"
        log_warn "DB just got wiped. Next manager install will reject every push"
        log_warn "from this daemon and you'll see EMPTY Grafana panels until you"
        log_warn "re-enroll the edge with fresh keys."
        log_warn ""
        log_warn "Pick one:"
        log_warn "  (a) uninstall the edge too — re-run this with --purge-edge"
        log_warn "  (b) after re-installing the manager, re-run the install.sh"
        log_warn "      one-liner from the device-create modal to swap keys"
    fi
fi

echo ""
echo "${C_BOLD}${C_GREEN}uninstall complete${C_RESET}"
echo "  - stack stopped"
echo "  - named volumes removed"
echo "  - bind-mount data dirs removed ($DATA_DIR/{mysql,qdrant,prom,loki,tempo,grafana,embeddings,skills,pages,workspace,tools})"
echo "  - manager log dir removed ($LOG_DIR)"
echo "  - compose install dir removed ($INSTALL_DIR)"
echo "  - ongrid-web inputs removed ($WEB_DIR)"
if (( local_edge_present )) && (( ! PURGE_EDGE )); then
    echo "  - ongrid-edge daemon kept running (use --purge-edge or re-enroll)"
fi
echo ""
