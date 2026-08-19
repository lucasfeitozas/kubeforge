#!/usr/bin/env bash
#
# setup_github_conventions.sh
#
# Ajustes de convenção para projeto individual:
#   - garante que 'main' é a branch padrão
#   - cria a branch 'develop' a partir de 'main' (se não existir)
#   - aplica settings simples do repositório (squash merge, auto-delete de
#     branch após merge, sem wiki/projects desnecessários)
#   - proteção BÁSICA na 'main' (sem exigir review, já que é solo — só
#     evita push force e delete acidental, e exige o CI passando)
#
# Pré-requisitos: gh CLI autenticada (gh auth login), rodar a partir da
# raiz do repositório já criado (depois do bootstrap_github.sh).
#
# Uso:
#   chmod +x scripts/setup_github_conventions.sh
#   ./scripts/setup_github_conventions.sh
#
set -euo pipefail

REPO_NAME="${REPO_NAME:-kubeforge}"

command -v gh >/dev/null 2>&1 || { echo "ERRO: gh CLI não encontrada."; exit 1; }
gh auth status >/dev/null 2>&1 || { echo "ERRO: rode 'gh auth login' antes de continuar."; exit 1; }

REPO_FULL="$(gh repo view "${REPO_NAME}" --json nameWithOwner -q .nameWithOwner)"
echo "==> Repositório: ${REPO_FULL}"

echo "==> Garantindo branch 'main' como padrão local..."
if git show-ref --verify --quiet refs/heads/master && ! git show-ref --verify --quiet refs/heads/main; then
  git branch -m master main
fi
git push -u origin main

echo "==> Definindo 'main' como default branch no GitHub..."
gh api -X PATCH "repos/${REPO_FULL}" -f default_branch="main" >/dev/null

echo "==> Criando branch 'develop' a partir de 'main' (se não existir)..."
if git show-ref --verify --quiet refs/heads/develop; then
  echo "    'develop' já existe localmente, pulando criação."
else
  git checkout -b develop main
  git push -u origin develop
  git checkout main
fi

echo "==> Ajustando settings simples do repositório..."
gh repo edit "${REPO_FULL}" \
  --enable-squash-merge \
  --enable-merge-commit=false \
  --enable-rebase-merge=false \
  --delete-branch-on-merge \
  --enable-wiki=false \
  --enable-projects=false \
  --enable-issues \
  >/dev/null

echo "==> Aplicando proteção básica na branch 'main'..."
# Proteção leve: sem exigência de review (projeto solo), só evita
# force-push/delete acidental e exige o workflow de CI passando.
gh api -X PUT "repos/${REPO_FULL}/branches/main/protection" \
  --input - <<'JSON' >/dev/null || echo "    (aviso: proteção de branch requer plano do repo; pulando se não suportado)"
{
  "required_status_checks": {
    "strict": true,
    "contexts": ["build-test"]
  },
  "enforce_admins": false,
  "required_pull_request_reviews": null,
  "restrictions": null,
  "allow_force_pushes": false,
  "allow_deletions": false
}
JSON

echo ""
echo "==> Concluído."
echo "    Branch padrão: main | Branch de trabalho: develop"
echo "    Fluxo sugerido (simples, sem exagero para projeto solo):"
echo "      - features direto em 'develop' (commits pequenos)"
echo "      - PR develop -> main quando quiser 'congelar' uma versão estável"
echo "      - CI roda em push/PR para main e develop (.github/workflows/ci.yml)"
