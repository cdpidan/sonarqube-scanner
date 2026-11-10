#!/usr/bin/env bash
set -euo pipefail

usage() {
    cat <<'USAGE'
Usage: ./build-docker.sh [IMAGE]

Build the Java 21 SonarQube Scanner image for linux/amd64.
Default IMAGE: plugins/sonarqube-scanner:stable-java21

Examples:
  ./build-docker.sh
  ./build-docker.sh plugins/sonarqube-scanner:stable-java21

The image is built locally. This script does not push it to a registry.
USAGE
}

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
    usage
    exit 0
fi

if [[ $# -gt 1 || ( $# -eq 1 && ( -z "$1" || "$1" == -* ) ) ]]; then
    usage >&2
    exit 2
fi

if ! command -v docker >/dev/null 2>&1; then
    echo "Error: docker is required. Install Docker and start its daemon before building." >&2
    exit 1
fi

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
image="${1:-harbor.jwell56.com/devops/plugins/sonarqube-scanner:stable-java21}"

# Match the GOOS=linux GOARCH=amd64 binary built by the Dockerfile.
echo "Building ${image} (linux/amd64)..."
docker build \
    --platform linux/amd64 \
    --file "${script_dir}/Dockerfile" \
    --tag "${image}" \
    "${script_dir}"

echo "Built ${image}"
