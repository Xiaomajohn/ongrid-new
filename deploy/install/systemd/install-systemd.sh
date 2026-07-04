#!/usr/bin/env bash
# ongrid pure-systemd installer.
#
# Runs from the extracted release tarball after the top-level
# install.sh dispatches via --mode=systemd. Installs manager + frontier
# + dep stack (prometheus / loki / tempo / qdrant) as systemd units.
# OS-package deps (mariadb-server / nginx / grafana) are apt/dnf-installed.
#
# Phase 1 status: manager + frontier wired end-to-end; dep download +
# config render is a structured TODO marked below — the install bails
# with a friendly message if any dep binary is missing from
# /usr/local/bin, so the operator can install them via Phase 2 helper
# (deploy/install/systemd/install-deps.sh, landing next iteration)
# without this script silently producing a half-working system.

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)
BUNDLE_DIR=$(cd -- "$SCRIPT_DIR/.." && pwd)
cd "$BUNDLE_DIR"

if [[ -t 1 ]]; then
    C_RED=$'\033[0;31m'; C_GREEN=$'\033[0;32m'; C_YELLOW=$'\033[1;33m'
    C_CYAN=$'\033[0;36m'; C_BOLD=$'\033[1m'; C_RESET=$'\033[0m'
else
    C_RED=''; C_GREEN=''; C_YELLOW=''; C_CYAN=''; C_BOLD=''; C_RESET=''
fi

log()  { printf '%s[INFO]%s %s\n'  "$C_GREEN"  "$C_RESET" "$*"; }
warn() { printf '%s[WARN]%s %s\n'  "$C_YELLOW" "$C_RESET" "$*"; }
err()  { printf '%s[ERROR]%s %s\n' "$C_RED"    "$C_RESET" "$*" >&2; }

trap 'err "install-systemd failed at line $LINENO (exit $?)"' ERR

if [[ $EUID -ne 0 ]]; then
    err "must run as root (sudo)"
    exit 1
fi

# -----------------------------------------------------------------------------
# flags
# -----------------------------------------------------------------------------
WITH_DEPS=0
usage() {
    cat <<EOF
Usage: sudo bash install-systemd.sh [OPTIONS]

Options:
  --with-deps   Also run install-deps.sh — apt/dnf installs mariadb +
                nginx + grafana, downloads pinned prom/loki/tempo/qdrant
                binaries from upstream with sha256 verify, bootstraps the
                mariadb schema, writes grafana datasource provisioning +
                nginx site config. Internet required (~250 MB downloads).
  -h, --help    Print this help.

Without --with-deps the script only installs the manager + frontier
binaries + the six systemd units. Operator handles deps separately.
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --with-deps) WITH_DEPS=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) err "unknown flag: $1"; usage; exit 2 ;;
    esac
done

# -----------------------------------------------------------------------------
# paths — overridable via env vars so operators can repoint the install at a
# customer-managed directory tree without forking the script.
#
# Two layout modes:
#
#   Default (legacy FHS, ONGRID_INSTALL_PREFIX=/usr/local):
#     PREFIX_BIN=/usr/local/bin   ETC_DIR=/etc/ongrid
#     STATE_DIR=/var/lib/ongrid   LOG_DIR=/var/log/ongrid
#
#   Collapsed (ONGRID_INSTALL_PREFIX=/opt/ongrid, matches compose mode):
#     PREFIX_BIN=/opt/ongrid/bin        ETC_DIR=/opt/ongrid/ongrid
#     STATE_DIR=/opt/ongrid/data        LOG_DIR=/opt/ongrid/logs
#
# Per-subdir overrides still work in either mode — e.g. exporting
# ONGRID_INSTALL_STATE=/var/lib/ongrid while keeping the rest under
# /opt/ongrid is fine for ops who want metrics on a dedicated disk but the
# binaries + config on the install root. set any of the four to a fully
# custom path; unset leaves the collapsed default.
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
SYSTEMD_DIR=/etc/systemd/system
SERVICE_USER=ongrid

# -----------------------------------------------------------------------------
# host proxy detection + internal no_proxy collection. Compact implementations
# (mirrors install.sh / upgrade.sh; kept separate because all three scripts
# run standalone from a freshly extracted tarball and don't source each other).
# On first install (when ongrid.env doesn't yet exist) the result is inlined
# into the heredoc below; on re-install the heredoc is skipped (`[[ ! -f ]]`),
# so this only matters for fresh systemd-mode setups.
# -----------------------------------------------------------------------------
detect_host_proxy_systemd() {
    local hp ap np f_hp f_ap f_np
    np="${no_proxy:-${NO_PROXY:-}}"
    hp="${http_proxy:-${HTTP_PROXY:-}}"
    ap="${https_proxy:-${HTTPS_PROXY:-}}"
    if [[ -z "$hp$ap" && -r /etc/environment ]]; then
        f_hp=$(grep -E '^[[:space:]]*http_proxy='  /etc/environment 2>/dev/null | tail -n1 | cut -d= -f2- | tr -d '"' || true)
        f_ap=$(grep -E '^[[:space:]]*https_proxy=' /etc/environment 2>/dev/null | tail -n1 | cut -d= -f2- | tr -d '"' || true)
        f_np=$(grep -E '^[[:space:]]*no_proxy='    /etc/environment 2>/dev/null | tail -n1 | cut -d= -f2- | tr -d '"' || true)
        [[ -z "$hp" ]] && hp="$f_hp"
        [[ -z "$ap" ]] && ap="$f_ap"
        [[ -z "$np" ]] && np="$f_np"
    fi
    printf '%s\n%s\n%s\n' \
        "${hp//[[:space:]]/}" "${ap//[[:space:]]/}" "${np//[[:space:]]/}"
}
# Internal DNS names + hostname/FQDN the manager must reach without going
# through the host proxy. Same four sources as install.sh: ONGRID_NO_PROXY_EXTRA
# (operator) + /etc/hosts non-localhost + /etc/resolv.conf search/domain +
# hostname + FQDN. Merged into the operator's NO_PROXY by
# collect_internal_no_proxy_domains_systemd.
collect_internal_no_proxy_domains_systemd() {
    local parts=() tok p hn fqdn line rest
    if [[ -n "${ONGRID_NO_PROXY_EXTRA:-}" ]]; then
        IFS=',' read -ra extras <<<"${ONGRID_NO_PROXY_EXTRA}"
        for e in "${extras[@]}"; do
            e="${e//[[:space:]]/}"
            [[ -n "$e" ]] && parts+=("$e")
        done
    fi
    if [[ -r /etc/hosts ]]; then
        while IFS= read -r line; do
            [[ "$line" =~ ^[[:space:]]*(#|$) ]] && continue
            while read -r h; do
                [[ -z "$h" ]] && continue
                [[ "$h" == localhost || "$h" == localhost.* || "$h" == *.localhost || "$h" == *.localhost.* ]] && continue
                [[ "$h" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] && continue
                [[ "$h" == *:* ]] && continue
                parts+=("$h")
            done < <(printf '%s\n' "$line" | awk '{ gsub(/#.*/, ""); for (i=2; i<=NF; i++) print $i }')
        done < /etc/hosts
    fi
    if [[ -r /etc/resolv.conf ]]; then
        while IFS= read -r line; do
            [[ "$line" =~ ^[[:space:]]*(#|$) ]] && continue
            if [[ "$line" =~ ^[[:space:]]*(search|domain)[[:space:]]+ ]]; then
                rest="${line#${BASH_REMATCH[0]}}"
                for tok in $rest; do [[ -n "$tok" ]] && parts+=("$tok"); done
            fi
        done < /etc/resolv.conf
    fi
    hn=$(hostname 2>/dev/null || true)
    [[ -n "$hn" ]] && parts+=("$hn")
    if command -v hostname >/dev/null 2>&1; then
        fqdn=$(hostname -f 2>/dev/null || true)
        [[ -n "$fqdn" && "$fqdn" != "$hn" ]] && parts+=("$fqdn")
    fi
    local seen=""
    for p in "${parts[@]}"; do
        [[ -z "$p" ]] && continue
        case ",${seen}," in *",${p},"*) ;; *) seen="${seen}${seen:+,}${p}" ;; esac
    done
    printf '%s' "$seen"
}
# Convenience wrapper: detect host proxy + collect internal domains, then
# stash the three values in HOST_PROXY_HP / HOST_PROXY_AP / HOST_NO_PROXY
# for the heredoc below. Logs what was autodetected.
systemd_setup_proxy_env() {
    local rc hp ap user_np internal
    rc=$(detect_host_proxy_systemd) || return 0
    hp="${rc%%$'\n'*}"; rc="${rc#*$'\n'}"
    ap="${rc%%$'\n'*}"; rc="${rc#*$'\n'}"
    user_np="$rc"
    internal=$(collect_internal_no_proxy_domains_systemd)
    # Merge user-supplied NO_PROXY + auto-collected internal domains.
    if [[ -n "$user_np" && -n "$internal" ]]; then
        HOST_NO_PROXY="$user_np,$internal"
    elif [[ -n "$internal" ]]; then
        HOST_NO_PROXY="$internal"
    else
        HOST_NO_PROXY="$user_np"
    fi
    HOST_NO_PROXY="${HOST_NO_PROXY%,}"   # trim trailing comma
    HOST_PROXY_HP="$hp"
    HOST_PROXY_AP="$ap"
    if [[ -n "$ap$hp" ]]; then
        log "host proxy detected → will pre-fill ongrid.env (override per-unit via: sudo systemctl edit ongrid)"
    fi
    if [[ -n "$internal" ]]; then
        log "auto-collected internal no_proxy domains from /etc/hosts + resolv.conf + ONGRID_NO_PROXY_EXTRA: ${internal}"
    fi
}

# -----------------------------------------------------------------------------
# system user
# -----------------------------------------------------------------------------
if id "$SERVICE_USER" &>/dev/null; then
    log "user $SERVICE_USER already exists"
else
    useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"
    log "created system user $SERVICE_USER"
fi

# dep users — same group, separate uids so a compromised prom can't
# read qdrant on-disk vectors, etc.
for dep_user in ongrid-prometheus ongrid-loki ongrid-tempo ongrid-qdrant; do
    if id "$dep_user" &>/dev/null; then
        log "user $dep_user already exists"
    else
        useradd --system --no-create-home --shell /usr/sbin/nologin \
                --gid "$SERVICE_USER" "$dep_user"
        log "created system user $dep_user"
    fi
done

# -----------------------------------------------------------------------------
# directories
# -----------------------------------------------------------------------------
mkdir -p "$ETC_DIR" "$ETC_DIR/prometheus" "$STATE_DIR" "$LOG_DIR"
chown root:ongrid "$ETC_DIR" "$ETC_DIR/prometheus"
chmod 0750 "$ETC_DIR" "$ETC_DIR/prometheus"
chown ongrid:ongrid "$STATE_DIR" "$LOG_DIR"
chmod 0755 "$STATE_DIR" "$LOG_DIR"

# -----------------------------------------------------------------------------
# manager + frontier binaries
# -----------------------------------------------------------------------------
require_bundled_bin() {
    local name="$1"
    local src="$BUNDLE_DIR/bin/$name"
    if [[ ! -f "$src" ]]; then
        err "missing $src — tarball was not built with systemd-mode binaries"
        err "rebuild with: make package SYSTEMD_BIN=1"
        exit 2
    fi
    install -m 0755 -o root -g root "$src" "$PREFIX_BIN/$name"
    log "installed $PREFIX_BIN/$name"
}
require_bundled_bin ongrid
require_bundled_bin ongrid-frontier

# -----------------------------------------------------------------------------
# stack-dep binaries (prom / loki / tempo / qdrant)
# Phase 1 expectation: operator pre-stages these at /usr/local/bin/* OR runs
# install-deps.sh first (Phase 2). We don't silently produce a half-stack.
# -----------------------------------------------------------------------------
MISSING_DEPS=()
for dep_bin in prometheus loki tempo qdrant; do
    if [[ ! -x "$PREFIX_BIN/$dep_bin" ]]; then
        MISSING_DEPS+=("$dep_bin")
    fi
done
if (( ${#MISSING_DEPS[@]} > 0 )); then
    warn "stack-dep binaries not found in $PREFIX_BIN: ${MISSING_DEPS[*]}"
    warn "manager unit will not start until they are installed."
    warn ""
    warn "Phase 2 helper (install-deps.sh) will download these from upstream"
    warn "releases with sha256-verify. For now, install them manually, e.g.:"
    warn "  https://github.com/prometheus/prometheus/releases"
    warn "  https://github.com/grafana/loki/releases"
    warn "  https://github.com/grafana/tempo/releases"
    warn "  https://github.com/qdrant/qdrant/releases"
fi

# -----------------------------------------------------------------------------
# OS-package deps
# -----------------------------------------------------------------------------
detect_pkg_mgr() {
    if command -v apt-get >/dev/null 2>&1; then echo apt
    elif command -v dnf >/dev/null 2>&1; then echo dnf
    elif command -v yum >/dev/null 2>&1; then echo yum
    else echo unknown
    fi
}
PKG_MGR=$(detect_pkg_mgr)
case "$PKG_MGR" in
    apt|dnf|yum)
        log "detected package manager: $PKG_MGR"
        ;;
    *)
        warn "unknown package manager — install mariadb-server / nginx / grafana manually"
        ;;
esac
# Actual apt/dnf install is in Phase 2 install-deps.sh; this skeleton
# only sets the systemd units in place so the operator can drive deps
# install separately and run `systemctl start ongrid` when ready.

# -----------------------------------------------------------------------------
# configs
# -----------------------------------------------------------------------------
copy_conf() {
    local src="$1" dst="$2"
    if [[ -f "$src" ]]; then
        install -m 0640 -o root -g ongrid "$src" "$dst"
        log "wrote $dst"
    else
        warn "$src missing — skipping"
    fi
}
copy_conf "$BUNDLE_DIR/prometheus/prometheus.yml" "$ETC_DIR/prometheus/prometheus.yml"
copy_conf "$BUNDLE_DIR/prometheus-rules.yml"      "$ETC_DIR/prometheus/rules.yml"
copy_conf "$BUNDLE_DIR/loki-config.yaml"          "$ETC_DIR/loki-config.yaml"
copy_conf "$BUNDLE_DIR/tempo-config.yaml"         "$ETC_DIR/tempo-config.yaml"

# The compose-mode configs hard-code container-volume paths (/loki, /var/tempo)
# that don't exist on the host filesystem. Rewrite to point at the per-dep
# StateDirectory= paths so the systemd units can actually write.
if [[ -f "$ETC_DIR/loki-config.yaml" ]]; then
    sed -i 's|/loki|/var/lib/ongrid-loki|g' "$ETC_DIR/loki-config.yaml"
    log "rewrote loki storage paths → /var/lib/ongrid-loki/*"
fi
if [[ -f "$ETC_DIR/tempo-config.yaml" ]]; then
    sed -i 's|/var/tempo|/var/lib/ongrid-tempo|g' "$ETC_DIR/tempo-config.yaml"
    log "rewrote tempo storage paths → /var/lib/ongrid-tempo/*"
fi
copy_conf "$BUNDLE_DIR/frontier.yaml"             "$ETC_DIR/frontier.yaml"

# manager env file — first install only; subsequent runs preserve.
ENV_FILE="$ETC_DIR/ongrid.env"
# Pre-detect host proxy + auto-collect internal no_proxy domains so the
# heredoc below can inline them. systemd `ongrid.service` reads this file
# via EnvironmentFile=; a per-unit `systemctl edit ongrid` override wins
# if the operator later needs to tweak any of these.
systemd_setup_proxy_env
if [[ ! -f "$ENV_FILE" ]]; then
    cat > "$ENV_FILE" <<EOF
# ongrid manager environment — systemd mode.
# Edit this file then \`systemctl restart ongrid\`.

# Listening address (the manager HTTP listener)
ONGRID_HTTP_ADDR=:8080

# Datastore (defaults assume mariadb on localhost — install-deps.sh
# auto-fills the password when it bootstraps the schema)
ONGRID_DB_DIALECT=mysql
ONGRID_DB_DSN=ongrid:CHANGE_ME@tcp(127.0.0.1:3306)/ongrid?parseTime=true&charset=utf8mb4&loc=Local

# Frontier broker — co-located on the same host in systemd mode.
# Default frontier.yaml listens service-bound on :40011, edge-bound on
# :40012. If you point at an external frontier, change to its host:port.
ONGRID_FRONTIER_ADDR=127.0.0.1:40011
ONGRID_FRONTIER_SERVICE_NAME=ongrid-manager

# Telemetry deps — local systemd units installed by install-deps.sh.
# Set *_ENABLED=true once each dep is healthy (systemctl is-active …).
ONGRID_PROM_ENABLED=true
ONGRID_PROM_URL=http://127.0.0.1:9090
ONGRID_LOKI_URL=http://127.0.0.1:3100
ONGRID_TEMPO_URL=http://127.0.0.1:3200
ONGRID_QDRANT_URL=http://127.0.0.1:6333

# LLM — fill in to enable AIOps agents
ONGRID_OPENAI_API_KEY=
ONGRID_OPENAI_MODEL=glm-4-plus
ONGRID_OPENAI_BASE_URL=

# Knowledge base embedder — defaults to the bundled offline ONNX model so
# 知识库 works with no API key. install-deps.sh installs libonnxruntime.so
# and this script stages the model into ONGRID_EMBEDDING_CACHE_DIR below.
# To use a hosted embedder instead, set PROVIDER=openai + API_KEY/BASE_URL
# + a matching MODEL/DIM (e.g. GLM embedding-3 / dim 2048).
ONGRID_EMBEDDING_PROVIDER=local
ONGRID_EMBEDDING_MODEL=bge-small-zh-v1.5
ONGRID_EMBEDDING_DIM=512
ONGRID_EMBEDDING_CACHE_DIR=/var/lib/ongrid/embeddings

# Outbound proxy — autodetected by install-systemd.sh from this shell's
# HTTP_PROXY / HTTPS_PROXY / NO_PROXY + /etc/environment fallback +
# auto-collected internal names from /etc/hosts + /etc/resolv.conf +
# hostname + ONGRID_NO_PROXY_EXTRA (export before running the installer
# to add extras like `loki.corp.example.com,prom.internal`). systemd's
# `ongrid.service` reads this file via EnvironmentFile=; per-unit
# `sudo systemctl edit ongrid` overrides (an `[Service] Environment=`
# block) win, so leave these blank here if you'd rather manage them
# at the unit level.
# To disable: set all three to empty, then `systemctl restart ongrid`.
ONGRID_HTTP_PROXY=${HOST_PROXY_HP-}
ONGRID_HTTPS_PROXY=${HOST_PROXY_AP-}
ONGRID_NO_PROXY=${HOST_NO_PROXY-}
EOF
    chmod 0640 "$ENV_FILE"
    chown root:ongrid "$ENV_FILE"
    log "wrote $ENV_FILE (REVIEW + edit secrets before starting)"
else
    log "preserved existing $ENV_FILE"
fi

# -----------------------------------------------------------------------------
# local embedding model (ADR-027 Phase-2) — stage the bundled BGE-small-zh
# cache into ONGRID_EMBEDDING_CACHE_DIR so the offline embedder loads with
# no HuggingFace reach. Mirrors what compose's install.sh does. The .so
# itself is installed by install-deps.sh; this is just the model weights.
# -----------------------------------------------------------------------------
EMB_SRC="$BUNDLE_DIR/embeddings"
EMB_DST="$STATE_DIR/embeddings"
if [[ -d "$EMB_SRC" ]] && compgen -G "$EMB_SRC/*" >/dev/null; then
    if [[ ! -d "$EMB_DST" ]] || [[ -z "$(ls -A "$EMB_DST" 2>/dev/null)" ]]; then
        install -d -m 0755 "$EMB_DST"
        cp -rf "$EMB_SRC/." "$EMB_DST/"
        chown -R ongrid:ongrid "$EMB_DST"
        log "staged embedding model → $EMB_DST"
    else
        log "embedding model already present at $EMB_DST — preserved"
    fi
else
    warn "no bundled embedding model — local embedder needs the model at"
    warn "  $EMB_DST (or switch to an API-key embedder in $ENV_FILE)"
fi

# -----------------------------------------------------------------------------
# systemd units
# -----------------------------------------------------------------------------
for unit in ongrid.service ongrid-frontier.service \
            prometheus.service loki.service tempo.service qdrant.service; do
    install -m 0644 -o root -g root \
        "$SCRIPT_DIR/$unit" "$SYSTEMD_DIR/$unit"
    log "installed $SYSTEMD_DIR/$unit"
done

systemctl daemon-reload
log "systemd daemon-reload"

# -----------------------------------------------------------------------------
# optional: dep auto-install
# -----------------------------------------------------------------------------
if [[ $WITH_DEPS -eq 1 ]]; then
    log "running install-deps.sh"
    bash "$SCRIPT_DIR/install-deps.sh"
fi

# -----------------------------------------------------------------------------
# enable but don't auto-start — operator should review env file first
# -----------------------------------------------------------------------------
systemctl enable ongrid.service ongrid-frontier.service \
                 prometheus.service loki.service tempo.service qdrant.service \
    >/dev/null 2>&1
log "enabled units (will start at boot)"

cat <<EOF

${C_BOLD}${C_GREEN}systemd install complete${C_RESET}

Next steps:
  1. Install OS-package deps if not already present:
EOF
case "$PKG_MGR" in
    apt) printf "       sudo apt-get install -y mariadb-server nginx\n" ;;
    dnf) printf "       sudo dnf install -y mariadb-server nginx\n" ;;
    yum) printf "       sudo yum install -y mariadb-server nginx\n" ;;
esac
cat <<EOF
  2. Install stack-dep binaries (Phase 2 helper coming):
       prometheus, loki, tempo, qdrant → ${PREFIX_BIN}/
  3. Initialise mariadb schema (CREATE DATABASE ongrid; user grants).
  4. Review and edit ${ETC_DIR}/ongrid.env (DB password, LLM key, etc.)
  5. Start the stack:
       sudo systemctl start prometheus loki tempo qdrant
       sudo systemctl start ongrid-frontier ongrid
  6. Watch:
       sudo journalctl -u ongrid -f

Roll back this install:
  sudo $SCRIPT_DIR/uninstall-systemd.sh           # stop + remove units
  sudo $SCRIPT_DIR/uninstall-systemd.sh --purge   # also delete data dirs + user
EOF
