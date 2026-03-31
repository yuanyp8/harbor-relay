#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARCH="${1:-amd64}"
OUT_DIR="${ROOT_DIR}/dist/linux-${ARCH}"

case "${ARCH}" in
    amd64|arm64) ;;
    *)
        echo "[ERROR] unsupported arch: ${ARCH}" >&2
        exit 1
        ;;
esac

mkdir -p "${OUT_DIR}"

GOOS=linux GOARCH="${ARCH}" go build -o "${OUT_DIR}/harbor-relay" ./cmd/relay
GOOS=linux GOARCH="${ARCH}" go build -o "${OUT_DIR}/harbor-relay-agent" ./cmd/agent

echo "[INFO] build completed: ${OUT_DIR}"
