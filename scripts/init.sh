#!/usr/bin/env bash
#
# init.sh — inicializa o ambiente de desenvolvimento local do KubeForge
#
set -euo pipefail
cd "$(dirname "$0")/.."

command -v go >/dev/null 2>&1 || { echo "ERRO: Go não encontrado. Instale em https://go.dev/dl/"; exit 1; }

echo "==> Resolvendo dependências (go mod tidy)..."
go mod tidy

if ! command -v minikube >/dev/null 2>&1; then
  echo "AVISO: minikube não encontrado no PATH. Instale para rodar o cluster local: https://minikube.sigs.k8s.io/"
fi

if [ -f .env.example ] && [ ! -f .env ]; then
  cp .env.example .env
  echo "==> .env criado a partir de .env.example"
fi

echo "==> Ambiente inicializado."
echo "    Compilar: scripts/build.sh"
echo "    Rodar:    go run ./cmd/kubeforge"
