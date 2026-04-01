#!/usr/bin/env bash
set -Eeuo pipefail

PROGRAM_NAME="${0##*/}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WEBSITE_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
REPO_DIR="$(cd "${WEBSITE_DIR}/.." && pwd)"

ACTION="install"
DEPLOY_DIR="/opt/harbor-relay-docs"
DATA_DIR="/data/harbor-relay-docs"
REPO_SRC="${REPO_DIR}"
DOCS_DOMAIN="docs.image.hm.metavarse.tech"
DOCS_PORT="18081"
SERVICE_NAME="harbor-relay-docs"
NGINX_IMAGE="nginx:1.29-alpine"
NODE_IMAGE="node:22-alpine"
FORCE=0

RUNTIME_DIR=""
BIN_DIR=""
SITE_DIR=""
NPM_CACHE_DIR=""
ENV_FILE=""
COMPOSE_FILE=""
NGINX_CONF_FILE=""
SERVICE_FILE=""
BUILD_ENV_FILE=""
REBUILD_SCRIPT_FILE=""
INSTALL_SCRIPT_FILE=""

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
  ./install-docs-site.sh install [options]
  ./install-docs-site.sh rebuild [options]
  ./install-docs-site.sh status [options]
  ./install-docs-site.sh print-caddy [options]

Options:
  --repo-src <path>     harbor-relay repo root (default: auto-detect)
  --deploy-dir <path>   deployment files root (default: /opt/harbor-relay-docs)
  --data-dir <path>     runtime data root (default: /data/harbor-relay-docs)
  --domain <name>       docs domain (default: docs.image.hm.metavarse.tech)
  --port <port>         local listen port for docs container (default: 18081)
  --nginx-image <img>   nginx runtime image (default: nginx:1.29-alpine)
  --node-image <img>    node builder image (default: node:22-alpine)
  --force               overwrite generated files
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

prepare_paths() {
    RUNTIME_DIR="${DEPLOY_DIR}/runtime"
    BIN_DIR="${DEPLOY_DIR}/bin"
    SITE_DIR="${DATA_DIR}/site"
    NPM_CACHE_DIR="${DATA_DIR}/npm-cache"
    ENV_FILE="${RUNTIME_DIR}/docs.env"
    COMPOSE_FILE="${RUNTIME_DIR}/docker-compose.yml"
    NGINX_CONF_FILE="${RUNTIME_DIR}/nginx.conf"
    SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
    BUILD_ENV_FILE="${DEPLOY_DIR}/build.env"
    REBUILD_SCRIPT_FILE="${BIN_DIR}/rebuild-docs-site.sh"
    INSTALL_SCRIPT_FILE="${BIN_DIR}/install-docs-site.sh"
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
NGINX_IMAGE=${NGINX_IMAGE}
DOCS_PORT=${DOCS_PORT}
SITE_DIR=${SITE_DIR}
EOF
}

render_build_env_file() {
    safe_write_file "${BUILD_ENV_FILE}" <<EOF
REPO_SRC=${REPO_SRC}
SITE_DIR=${SITE_DIR}
NPM_CACHE_DIR=${NPM_CACHE_DIR}
NODE_IMAGE=${NODE_IMAGE}
EOF
}

render_compose_file() {
    safe_write_file "${COMPOSE_FILE}" <<'EOF'
services:
  docs:
    image: ${NGINX_IMAGE}
    container_name: harbor-relay-docs
    restart: unless-stopped
    ports:
      - 127.0.0.1:${DOCS_PORT}:8080
    volumes:
      - ${SITE_DIR}:/usr/share/nginx/html:ro
      - ./nginx.conf:/etc/nginx/conf.d/default.conf:ro
EOF
}

render_nginx_conf() {
    safe_write_file "${NGINX_CONF_FILE}" <<'EOF'
server {
    listen 8080;
    server_name _;

    root /usr/share/nginx/html;
    index index.html;

    location / {
        try_files $uri $uri/ /index.html;
    }
}
EOF
}

render_service_file() {
    safe_write_file "${SERVICE_FILE}" <<EOF
[Unit]
Description=Harbor Relay Docs Site
Requires=docker.service
After=docker.service network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=${RUNTIME_DIR}
ExecStart=/usr/bin/docker compose --env-file ${ENV_FILE} -f ${COMPOSE_FILE} up -d
ExecStop=/usr/bin/docker compose --env-file ${ENV_FILE} -f ${COMPOSE_FILE} down
ExecReload=/usr/bin/docker compose --env-file ${ENV_FILE} -f ${COMPOSE_FILE} up -d
TimeoutStartSec=0

[Install]
WantedBy=multi-user.target
EOF
}

render_rebuild_script() {
    safe_write_file "${REBUILD_SCRIPT_FILE}" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
BUILD_ENV_FILE="${DEPLOY_DIR}/build.env"

[[ -f "${BUILD_ENV_FILE}" ]] || {
    printf '[ERROR] Missing build env: %s\n' "${BUILD_ENV_FILE}" >&2
    exit 1
}

# shellcheck disable=SC1090
source "${BUILD_ENV_FILE}"

printf '[INFO] Rebuilding docs from: %s\n' "${REPO_SRC}"
printf '[INFO] Output directory: %s\n' "${SITE_DIR}"

mkdir -p "${SITE_DIR}" "${NPM_CACHE_DIR}"

docker run --rm \
  -v "${REPO_SRC}:/workspace" \
  -v "${SITE_DIR}:/out" \
  -v "${NPM_CACHE_DIR}:/root/.npm" \
  -w /workspace/website \
  "${NODE_IMAGE}" \
  sh -lc '
    npm ci
    npm run build
    rm -rf /out/*
    cp -a build/. /out/
  '

printf '[INFO] Docs rebuild completed.\n'
EOF

    chmod 0755 "${REBUILD_SCRIPT_FILE}"
}

render_self_copy() {
    install -d -m 0755 "${BIN_DIR}"
    install -m 0755 "${SCRIPT_DIR}/install-docs-site.sh" "${INSTALL_SCRIPT_FILE}"
}

install_action() {
    require_root
    require_command docker
    require_command systemctl

    [[ -d "${REPO_SRC}" ]] || die "repo source does not exist: ${REPO_SRC}"
    [[ -f "${REPO_SRC}/website/package.json" ]] || die "repo source does not look like harbor-relay root: ${REPO_SRC}"

    prepare_paths

    install -d -m 0755 "${DEPLOY_DIR}" "${RUNTIME_DIR}" "${BIN_DIR}" "${DATA_DIR}" "${SITE_DIR}" "${NPM_CACHE_DIR}"

    render_env_file
    render_build_env_file
    render_compose_file
    render_nginx_conf
    render_rebuild_script
    render_self_copy
    render_service_file

    "${REBUILD_SCRIPT_FILE}"

    systemctl daemon-reload
    systemctl enable --now "${SERVICE_NAME}"

    log "Docs site install completed."
    log "Runtime dir: ${RUNTIME_DIR}"
    log "Editable repo: ${REPO_SRC}"
    log "Static site dir: ${SITE_DIR}"
    log "Local docs port: 127.0.0.1:${DOCS_PORT}"
    log "After editing docs, run: ${REBUILD_SCRIPT_FILE}"
    echo
    print_caddy_action
}

rebuild_action() {
    require_root
    require_command docker
    prepare_paths
    [[ -x "${REBUILD_SCRIPT_FILE}" ]] || die "Missing rebuild script: ${REBUILD_SCRIPT_FILE}"
    "${REBUILD_SCRIPT_FILE}"
}

status_action() {
    prepare_paths
    systemctl --no-pager --full status "${SERVICE_NAME}" || true
    echo
    docker ps --filter "name=${SERVICE_NAME}" || true
    echo
    log "Deploy dir: ${DEPLOY_DIR}"
    log "Repo source: ${REPO_SRC}"
    log "Static site dir: ${SITE_DIR}"
    log "Caddy upstream: 127.0.0.1:${DOCS_PORT}"
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
            install|rebuild|status|print-caddy)
                ACTION="$1"
                shift
                ;;
        esac
    fi

    while [[ $# -gt 0 ]]; do
        case "$1" in
            --repo-src)
                REPO_SRC="${2:-}"
                shift 2
                ;;
            --deploy-dir)
                DEPLOY_DIR="${2:-}"
                shift 2
                ;;
            --data-dir)
                DATA_DIR="${2:-}"
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
            --nginx-image)
                NGINX_IMAGE="${2:-}"
                shift 2
                ;;
            --node-image)
                NODE_IMAGE="${2:-}"
                shift 2
                ;;
            --force)
                FORCE=1
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
        rebuild)
            rebuild_action
            ;;
        status)
            status_action
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
