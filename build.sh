#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORK_DIR=""
INSTALLER_STUB="${ROOT_DIR}/install.sh"
README_FILE="${ROOT_DIR}/README.md"

ARCH=""
OUTPUT=""
VERSION=""

log() {
    printf '[INFO] %s\n' "$*"
}

die() {
    printf '[ERROR] %s\n' "$*" >&2
    exit 1
}

usage() {
    cat <<'EOF'
Usage:
  ./build.sh --arch amd64
  ./build.sh --arch arm64

Options:
  --arch ARCH       Target architecture: amd64 or arm64
  --output PATH     Output .run path
  --version VALUE   Version string written to the manifest
  -h, --help        Show this help message
EOF
}

normalize_arch() {
    case "${1:-}" in
        amd64|x86_64)
            printf 'amd64\n'
            ;;
        arm64|aarch64)
            printf 'arm64\n'
            ;;
        *)
            return 1
            ;;
    esac
}

sha256_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    else
        die "sha256sum or shasum is required"
    fi
}

build_binary() {
    local arch="$1"
    local output="$2"
    GOOS=linux GOARCH="${arch}" go build -o "${output}" "$3"
}

main() {
    local payload_dir payload_tar installer_tmp installer_path version manifest

    while [[ $# -gt 0 ]]; do
        case "$1" in
            --arch)
                ARCH="$(normalize_arch "${2:-}")" || die "unsupported architecture: ${2:-}"
                shift 2
                ;;
            --output)
                OUTPUT="${2:-}"
                shift 2
                ;;
            --version)
                VERSION="${2:-}"
                shift 2
                ;;
            -h|--help)
                usage
                exit 0
                ;;
            *)
                if [[ -z "${ARCH}" ]]; then
                    ARCH="$(normalize_arch "$1")" || die "unknown argument: $1"
                else
                    die "unknown argument: $1"
                fi
                shift
                ;;
        esac
    done

    [[ -n "${ARCH}" ]] || die "missing --arch"
    [[ -f "${INSTALLER_STUB}" ]] || die "missing install.sh"
    [[ -f "${README_FILE}" ]] || die "missing README.md"

    version="${VERSION:-$(git -C "${ROOT_DIR}" describe --tags --always --dirty 2>/dev/null || printf 'dev')}"
    installer_path="${OUTPUT:-${ROOT_DIR}/dist/linux-${ARCH}/harbor-relay-toolkit-linux-${ARCH}.run}"

    WORK_DIR="${ROOT_DIR}/.payload-build-${ARCH}"
    rm -rf "${WORK_DIR}"
    mkdir -p "${WORK_DIR}" "$(dirname "${installer_path}")"
    payload_dir="${WORK_DIR}/payload"
    mkdir -p "${payload_dir}/configs" "${payload_dir}/deploy/systemd"

    log "building binaries for ${ARCH}"
    build_binary "${ARCH}" "${payload_dir}/harbor-relay" ./cmd/relay
    build_binary "${ARCH}" "${payload_dir}/harbor-relay-agent" ./cmd/agent
    chmod 0755 "${payload_dir}/harbor-relay" "${payload_dir}/harbor-relay-agent"

    cp "${README_FILE}" "${payload_dir}/README.md"
    cp "${ROOT_DIR}/configs/relay.yaml.example" "${payload_dir}/configs/relay.yaml.example"
    cp "${ROOT_DIR}/configs/agent.yaml.example" "${payload_dir}/configs/agent.yaml.example"
    cp "${ROOT_DIR}/deploy/systemd/harbor-relay.service" "${payload_dir}/deploy/systemd/harbor-relay.service"
    cp "${ROOT_DIR}/deploy/systemd/harbor-relay-agent.service" "${payload_dir}/deploy/systemd/harbor-relay-agent.service"

    manifest="${payload_dir}/manifest.txt"
    cat > "${manifest}" <<EOF
name=harbor-relay
version=${version}
arch=${ARCH}
built_at=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
git_commit=$(git -C "${ROOT_DIR}" rev-parse HEAD 2>/dev/null || printf 'unknown')
relay_sha256=$(sha256_file "${payload_dir}/harbor-relay")
agent_sha256=$(sha256_file "${payload_dir}/harbor-relay-agent")
EOF

    payload_tar="${WORK_DIR}/payload.tar.gz"
    tar -czf "${payload_tar}" -C "${payload_dir}" .

    installer_tmp="${WORK_DIR}/install.sh"
    tr -d '\r' < "${INSTALLER_STUB}" > "${installer_tmp}"
    grep -q '^__PAYLOAD_BELOW__$' "${installer_tmp}" || die "install.sh is missing the __PAYLOAD_BELOW__ marker"

    cat "${installer_tmp}" "${payload_tar}" > "${installer_path}"
    chmod +x "${installer_path}"
    sha256_file "${installer_path}" > "${installer_path}.sha256"

    log "installer created: ${installer_path}"
    log "installer sha256: $(cat "${installer_path}.sha256")"
}

main "$@"
