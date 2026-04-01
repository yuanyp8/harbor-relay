#!/usr/bin/env bash
set -Eeuo pipefail

PROGRAM_NAME="${0##*/}"

ACTION="install"
FORCE=0
PURGE=0

SERVICE_NAME="harbor-relay-docs-image"
CONTAINER_NAME="harbor-relay-docs-image"
DEPLOY_DIR="/opt/harbor-relay-docs-image"
RUNTIME_DIR="${DEPLOY_DIR}/runtime"
BIN_DIR="${DEPLOY_DIR}/bin"
ENV_FILE="${RUNTIME_DIR}/docs-image.env"
COMPOSE_FILE="${RUNTIME_DIR}/docker-compose.yml"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"

DOCS_PORT="18081"
DOCS_DOMAIN="docs.image.hm.metavarse.tech"
DOCS_IMAGE="ghcr.io/yuanyp8/harbor-relay-docs:main"

log() {
    printf '[INFO] %s\n' "$*"
}

warn() {
    printf '[WARN] %s\n' "$*" >&2
}

die() {
    printf '[ERROR] %s\n' "$*" >&2
    exit 1
}

usage() {
    cat <<'EOF'
Usage:
  ./install-docs-image.sh install [options]
  ./install-docs-image.sh status [options]
  ./install-docs-image.sh uninstall [--purge]
  ./install-docs-image.sh print-caddy [options]

Options:
  --image <ref>         docs image reference (default: ghcr.io/yuanyp8/harbor-relay-docs:main)
  --deploy-dir <path>   deployment root (default: /opt/harbor-relay-docs-image)
  --domain <name>       docs domain (default: docs.image.hm.metavarse.tech)
  --port <port>         local listen port (default: 18081)
  --service-name <n>    systemd service name (default: harbor-relay-docs-image)
  --container-name <n>  container name (default: harbor-relay-docs-image)
  --force               overwrite generated files
  --purge               with uninstall, also remove deploy files
  -h, --help            show this help
EOF
}

require_root() {
    if [[ "$(id -u)" -ne 0 ]]; then
        die "Please run as root or with sudo."
    fi
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || die "Missing required command: $1"
}

refresh_paths() {
    RUNTIME_DIR="${DEPLOY_DIR}/runtime"
    BIN_DIR="${DEPLOY_DIR}/bin"
    ENV_FILE="${RUNTIME_DIR}/docs-image.env"
    COMPOSE_FILE="${RUNTIME_DIR}/docker-compose.yml"
    SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
}

safe_write_file() {
    local target="$1"
    local backup=""

    install -d -m 0755 "$(dirname "${target}")"

    if [[ -f "${target}" && "${FORCE}" -eq 0 ]]; then
        warn "Keeping existing file: ${target}"
        return 0
    fi

    if [[ -f "${target}" && "${FORCE}" -eq 1 ]]; then
        backup="${target}.bak.$(date +%Y%m%d%H%M%S)"
        cp -a "${target}" "${backup}"
        log "Backed up ${target} -> ${backup}"
    fi

    cat > "${target}"
}

render_env_file() {
    safe_write_file "${ENV_FILE}" <<EOF
DOCS_IMAGE=${DOCS_IMAGE}
DOCS_PORT=${DOCS_PORT}
CONTAINER_NAME=${CONTAINER_NAME}
EOF
}

render_compose_file() {
    safe_write_file "${COMPOSE_FILE}" <<'EOF'
services:
  docs:
    image: ${DOCS_IMAGE}
    container_name: ${CONTAINER_NAME}
    restart: unless-stopped
    ports:
      - 127.0.0.1:${DOCS_PORT}:8080
EOF
}

render_service_file() {
    safe_write_file "${SERVICE_FILE}" <<EOF
[Unit]
Description=Harbor Relay Docs Image
Requires=docker.service
After=docker.service network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=${RUNTIME_DIR}
ExecStartPre=/usr/bin/docker compose --env-file ${ENV_FILE} -f ${COMPOSE_FILE} pull
ExecStart=/usr/bin/docker compose --env-file ${ENV_FILE} -f ${COMPOSE_FILE} up -d
ExecStop=/usr/bin/docker compose --env-file ${ENV_FILE} -f ${COMPOSE_FILE} down
ExecReload=/usr/bin/docker compose --env-file ${ENV_FILE} -f ${COMPOSE_FILE} pull
ExecReload=/usr/bin/docker compose --env-file ${ENV_FILE} -f ${COMPOSE_FILE} up -d
TimeoutStartSec=0

[Install]
WantedBy=multi-user.target
EOF
}

render_self_copy() {
    install -d -m 0755 "${BIN_DIR}"
    install -m 0755 "$0" "${BIN_DIR}/install-docs-image.sh"
}

install_action() {
    require_root
    require_command docker
    require_command systemctl

    refresh_paths
    install -d -m 0755 "${DEPLOY_DIR}" "${RUNTIME_DIR}" "${BIN_DIR}"

    render_env_file
    render_compose_file
    render_self_copy
    render_service_file

    systemctl daemon-reload
    systemctl enable --now "${SERVICE_NAME}"

    log "Docs image deployment installed."
    log "Image: ${DOCS_IMAGE}"
    log "Local docs port: 127.0.0.1:${DOCS_PORT}"
    echo
    print_caddy_action
}

status_action() {
    refresh_paths
    systemctl --no-pager --full status "${SERVICE_NAME}" || true
    echo
    docker ps --filter "name=${CONTAINER_NAME}" || true
    echo
    log "Image: ${DOCS_IMAGE}"
    log "Compose file: ${COMPOSE_FILE}"
    log "Env file: ${ENV_FILE}"
}

uninstall_action() {
    require_root
    refresh_paths
    systemctl disable --now "${SERVICE_NAME}" >/dev/null 2>&1 || true
    rm -f "${SERVICE_FILE}"
    systemctl daemon-reload
    systemctl reset-failed >/dev/null 2>&1 || true

    if [[ "${PURGE}" -eq 1 ]]; then
        rm -rf "${DEPLOY_DIR}"
    fi

    log "Docs image deployment removed."
    if [[ "${PURGE}" -eq 0 ]]; then
        warn "Deploy files were kept. Re-run with --purge to remove them."
    fi
}

print_caddy_action() {
    cat <<EOF
${DOCS_DOMAIN}:9443 {
    encode zstd gzip

    header {
        -Server
        X-Frame-Options "SAMEORIGIN"
        X-Content-Type-Options "nosniff"
        Referrer-Policy "strict-origin-when-cross-origin"
        Strict-Transport-Security "max-age=31536000; includeSubDomains; preload"
    }

    reverse_proxy 127.0.0.1:${DOCS_PORT}
}
EOF
}

parse_args() {
    if [[ $# -gt 0 ]]; then
        case "$1" in
            install|status|uninstall|print-caddy)
                ACTION="$1"
                shift
                ;;
        esac
    fi

    while [[ $# -gt 0 ]]; do
        case "$1" in
            --image)
                DOCS_IMAGE="${2:-}"
                shift 2
                ;;
            --deploy-dir)
                DEPLOY_DIR="${2:-}"
                shift 2
                ;;
            --domain)
                DOCS_DOMAIN="${2:-}"
                shift 2
                ;;
            --port)
                DOCS_PORT="${2:-}"
                shift 2
                ;;
            --service-name)
                SERVICE_NAME="${2:-}"
                shift 2
                ;;
            --container-name)
                CONTAINER_NAME="${2:-}"
                shift 2
                ;;
            --force)
                FORCE=1
                shift
                ;;
            --purge)
                PURGE=1
                shift
                ;;
            -h|--help)
                usage
                exit 0
                ;;
            *)
                die "Unknown argument: $1"
                ;;
        esac
    done
}

main() {
    parse_args "$@"

    case "${ACTION}" in
        install)
            install_action
            ;;
        status)
            status_action
            ;;
        uninstall)
            uninstall_action
            ;;
        print-caddy)
            print_caddy_action
            ;;
        *)
            die "Unsupported action: ${ACTION}"
            ;;
    esac
}

main "$@"
