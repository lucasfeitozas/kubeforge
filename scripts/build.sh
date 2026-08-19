#!/usr/bin/env bash
#
# build.sh — compila o binário do KubeForge em bin/kubeforge
#
set -euo pipefail
cd "$(dirname "$0")/.."

mkdir -p bin
go build -o bin/kubeforge ./cmd/kubeforge

echo "==> binário gerado em bin/kubeforge"
