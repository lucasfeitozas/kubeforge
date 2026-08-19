#!/usr/bin/env bash
#
# bootstrap_github.sh
#
# Cria o repositório KubeForge na SUA conta GitHub e popula labels,
# milestones (épicos) e issues (histórias) a partir do backlog em
# docs/EPICOS_E_HISTORIAS.md.
#
# Pré-requisitos:
#   1. GitHub CLI instalada: https://cli.github.com/
#   2. Autenticado localmente:  gh auth login
#   3. Rodar este script a partir da RAIZ do repositório (onde está o README.md)
#
# Uso:
#   chmod +x scripts/bootstrap_github.sh
#   ./scripts/bootstrap_github.sh
#
# Variáveis configuráveis (opcional, via env):
#   REPO_NAME   (default: kubeforge)
#   VISIBILITY  (default: private)   # private | public
#
set -euo pipefail

REPO_NAME="${REPO_NAME:-kubeforge}"
VISIBILITY="${VISIBILITY:-private}"

command -v gh >/dev/null 2>&1 || { echo "ERRO: gh CLI não encontrada. Instale em https://cli.github.com/"; exit 1; }
gh auth status >/dev/null 2>&1 || { echo "ERRO: rode 'gh auth login' antes de continuar."; exit 1; }

echo "==> Criando/verificando repositório '${REPO_NAME}' (${VISIBILITY})..."
if gh repo view "${REPO_NAME}" >/dev/null 2>&1; then
  echo "    Repositório já existe, pulando criação."
else
  gh repo create "${REPO_NAME}" --"${VISIBILITY}" --source=. --remote=origin --push
fi

REPO_FULL="$(gh repo view "${REPO_NAME}" --json nameWithOwner -q .nameWithOwner)"
echo "==> Repositório: ${REPO_FULL}"

echo "==> Criando labels..."
create_label () {
  local name="$1" color="$2" desc="$3"
  gh label create "$name" --repo "$REPO_FULL" --color "$color" --description "$desc" --force
}

create_label "type:story"        "0E8A16" "História de usuário"
create_label "priority:high"     "B60205" "Prioridade alta"
create_label "priority:medium"   "FBCA04" "Prioridade média"
create_label "priority:low"      "C5DEF5" "Prioridade baixa"
create_label "epic:E1"           "5319E7" "Épico 1 - Fundação do Projeto"
create_label "epic:E2"           "5319E7" "Épico 2 - Modelo de Dados e Metadata Store"
create_label "epic:E3"           "5319E7" "Épico 3 - Build Engine (Opção A)"
create_label "epic:E4"           "5319E7" "Épico 4 - Execution Engine"
create_label "epic:E5"           "5319E7" "Épico 5 - Teardown / Cleanup"
create_label "epic:E6"           "5319E7" "Épico 6 - API Backend"
create_label "epic:E7"           "5319E7" "Épico 7 - Console Web"
create_label "epic:E8"           "5319E7" "Épico 8 - Observabilidade Mínima"

echo "==> Criando milestones (um por épico)..."
create_milestone () {
  local title="$1" desc="$2"
  gh api "repos/${REPO_FULL}/milestones" -f title="$title" -f description="$desc" -f state="open" >/dev/null 2>&1 \
    || echo "    (milestone '$title' já existe, pulando)"
}

create_milestone "E1 - Fundação do Projeto"          "Repositório, binário base, conexão com Minikube, CRD e RBAC mínimo."
create_milestone "E2 - Modelo de Dados"              "Schema SQLite, repositório de dados, validação."
create_milestone "E3 - Build Engine"                 "Clone, docker build via minikube docker-env, status de build."
create_milestone "E4 - Execution Engine"              "Mapeamento para Job, hooks pré/pós, status e logs, storage."
create_milestone "E5 - Teardown / Cleanup"             "TTL nativo, comando cleanup, labels padronizadas."
create_milestone "E6 - API Backend"                     "CRUD de Componente, ações build/run/cleanup, logs."
create_milestone "E7 - Console Web"                      "UI embutida: cadastro, listagem, logs."
create_milestone "E8 - Observabilidade Mínima"            "Status/health e logs estruturados."

echo "==> Criando issues (histórias)..."
create_issue () {
  local title="$1" epic="$2" priority="$3" milestone="$4" body="$5"
  gh issue create --repo "$REPO_FULL" \
    --title "$title" \
    --label "type:story,epic:${epic},priority:${priority}" \
    --milestone "$milestone" \
    --body "$body" >/dev/null
  echo "    + $title"
}

# --- E1 - Fundação do Projeto ---
M="E1 - Fundação do Projeto"
create_issue "E1.S1 - Setup do repositório e estrutura de pastas" "E1" "high" "$M" \
"Como desenvolvedor, quero um repositório Go inicializado com a estrutura de pastas definida na arquitetura, para começar a implementar as features sobre uma base organizada.

**Critérios de aceite**
- [ ] go.mod criado (module github.com/<usuario>/kubeforge)
- [ ] Estrutura cmd/, internal/, web/, deployments/, docs/ presente
- [ ] README.md e docs/ARCHITECTURE.md versionados
- [ ] .gitignore cobrindo binários, *.db (SQLite), .env"

create_issue "E1.S2 - Binário único com bootstrap de configuração" "E1" "high" "$M" \
"Como desenvolvedor, quero um main.go em cmd/kubeforge que carregue configuração básica (porta HTTP, kubeconfig, caminho do SQLite), para ter um ponto de entrada único para API + Controller + Build Broker.

**Critérios de aceite**
- [ ] Flags/env vars: --http-port (default 8080), --kubeconfig (default ~/.kube/config), --db-path (default ./kubeforge.db)
- [ ] Log estruturado inicial (slog)"

create_issue "E1.S3 - Conexão com o cluster Minikube via client-go" "E1" "high" "$M" \
"Como desenvolvedor, quero validar a conexão com o Minikube ao iniciar o binário, para falhar rápido se o cluster não estiver acessível.

**Critérios de aceite**
- [ ] internal/k8s expõe ClusterProvider com um único provider registrado (minikube)
- [ ] Startup faz um ServerVersion() do client-go e loga a versão do cluster
- [ ] Erro claro se ~/.kube/config não existir ou contexto minikube não estiver ativo"

create_issue "E1.S4 - CRD KubeForgeComponent aplicada manualmente" "E1" "high" "$M" \
"Como desenvolvedor, quero o manifesto YAML da CRD versionado em deployments/minikube/crd.yaml, para aplicá-la manualmente no cluster antes de qualquer execução.

**Critérios de aceite**
- [ ] deployments/minikube/crd.yaml criado com o schema completo
- [ ] kubectl apply -f deployments/minikube/crd.yaml funciona sem erros no Minikube"

create_issue "E1.S5 - Namespace e RBAC mínimo do MVP" "E1" "medium" "$M" \
"Como desenvolvedor, quero o Namespace (kubeforge-workloads, com PodSecurity baseline) e a Role/RoleBinding simplificados, para rodar com um mínimo de isolamento sem complexidade multi-tenant.

**Critérios de aceite**
- [ ] deployments/minikube/namespace.yaml
- [ ] deployments/minikube/rbac.yaml (Role escopada ao namespace)"

# --- E2 - Modelo de Dados ---
M="E2 - Modelo de Dados"
create_issue "E2.S1 - Schema SQLite inicial" "E2" "high" "$M" \
"Como desenvolvedor, quero as tabelas components e executions criadas via migration, para persistir cadastro e histórico.

**Critérios de aceite**
- [ ] Migration inicial (internal/store/migrations)
- [ ] Tabela components reflete o schema da seção 2.2 da arquitetura
- [ ] Tabela executions referencia component_id, guarda phase, started_at, completed_at, image_digest"

create_issue "E2.S2 - Repositório de dados (CRUD de Componente)" "E2" "high" "$M" \
"Como desenvolvedor, quero funções Go para criar, ler, listar, atualizar e remover Componentes no SQLite, para que a camada de API não acesse SQL diretamente.

**Critérios de aceite**
- [ ] internal/store expõe interface ComponentRepository com Create/Get/List/Update/Delete
- [ ] Testes unitários cobrindo os métodos com SQLite em memória"

create_issue "E2.S3 - Validação do payload de Componente" "E2" "medium" "$M" \
"Como usuário, quero que campos obrigatórios sejam validados no cadastro, para evitar Componentes malformados chegando ao build/execução.

**Critérios de aceite**
- [ ] Validação rejeita repoUrl inválida, workloadKind fora do enum, ausência de resources.requests
- [ ] Mensagens de erro específicas por campo"

# --- E3 - Build Engine ---
M="E3 - Build Engine"
create_issue "E3.S1 - Clonagem do repositório (branch/tag/commit)" "E3" "high" "$M" \
"Como sistema, quero clonar o repositório informado em source.repoUrl respeitando source.ref (branch, tag ou commit), para obter o código-fonte a ser buildado.

**Critérios de aceite**
- [ ] Clone raso (--depth=1) quando ref.type=branch ou tag
- [ ] Checkout específico quando ref.type=commit
- [ ] Suporte a repositório privado via credentialsSecretRef"

create_issue "E3.S2 - Build via docker build apontando para o daemon do Minikube" "E3" "high" "$M" \
"Como sistema, quero executar docker build usando as variáveis equivalentes a 'minikube docker-env', para que a imagem fique disponível diretamente ao cluster (Opção A, decisão definitiva do MVP).

**Critérios de aceite**
- [ ] Build Broker injeta as env vars do minikube docker-env no processo do build automaticamente
- [ ] Tag da imagem segue imageTagStrategy (commit-sha como padrão)
- [ ] Logs do build persistidos e associados à execution correspondente"

create_issue "E3.S3 - Atualização de status de build" "E3" "high" "$M" \
"Como usuário, quero ver o status do build (Pending -> Building -> Built/Failed) refletido no Componente, para saber quando ele está pronto para execução.

**Critérios de aceite**
- [ ] status.phase e status.buildImageDigest atualizados ao final do build
- [ ] Falha de build resulta em Failed explícito + mensagem de erro"

create_issue "E3.S4 - Cache de build simples" "E3" "low" "$M" \
"Como usuário, quero que builds sucessivos do mesmo Componente reaproveitem cache de camadas Docker, para builds mais rápidos durante experimentação iterativa.

**Critérios de aceite**
- [ ] Reutiliza o cache padrão do daemon Docker do Minikube
- [ ] Opção explícita para forçar rebuild sem cache"

# --- E4 - Execution Engine ---
M="E4 - Execution Engine"
create_issue "E4.S1 - Mapeamento Componente -> manifesto Job" "E4" "high" "$M" \
"Como sistema, quero gerar um batch/v1 Job a partir de spec.runtime + spec.resources, para executar o Componente como workload de teste.

**Critérios de aceite**
- [ ] imagePullPolicy: Never fixado
- [ ] resources.requests/limits, env, command, args mapeados corretamente
- [ ] restartPolicy e backoffLimit aplicados conforme spec"

create_issue "E4.S2 - Suporte a Init Containers (hooks preRun)" "E4" "medium" "$M" \
"Como usuário, quero que spec.hooks.preRun seja traduzido em Init Containers do Job, para validar pré-condições antes da execução principal.

**Critérios de aceite**
- [ ] Cada item de preRun vira um Init Container na ordem declarada
- [ ] Falha de um Init Container impede o container principal de rodar"

create_issue "E4.S3 - Hooks postRun como Job separado" "E4" "medium" "$M" \
"Como usuário, quero que spec.hooks.postRun seja executado como um Job independente após o término do Job principal, para rodar verificações sem acoplar ao container original.

**Critérios de aceite**
- [ ] Controller observa phase Succeeded/Failed do Job principal e dispara o Job de verificação
- [ ] continueOnError: false interrompe o fluxo e marca a execução como Failed"

create_issue "E4.S4 - Acompanhamento de status e logs" "E4" "high" "$M" \
"Como usuário, quero consultar o status (Pending/Running/Succeeded/Failed) e os logs do Pod em execução, para acompanhar o teste sem usar kubectl diretamente.

**Critérios de aceite**
- [ ] Endpoint/consulta que reflete o status atual do Job/Pod
- [ ] Stream ou tail de logs disponível via API"

create_issue "E4.S5 - Suporte a armazenamento efêmero e PVC" "E4" "low" "$M" \
"Como usuário, quero que resources.storage (ephemeral ou pvc) seja aplicado ao manifesto, para testar comportamentos que dependem de armazenamento.

**Critérios de aceite**
- [ ] type: ephemeral usa emptyDir com sizeLimit
- [ ] type: pvc cria/reaproveita um PersistentVolumeClaim com a storageClassName padrão do Minikube"

# --- E5 - Teardown / Cleanup ---
M="E5 - Teardown / Cleanup"
create_issue "E5.S1 - TTL nativo aplicado automaticamente" "E5" "high" "$M" \
"Como sistema, quero aplicar ttlSecondsAfterFinished e activeDeadlineSeconds em todo Job criado, para que o próprio Kubernetes limpe recursos concluídos ou travados.

**Critérios de aceite**
- [ ] Valores default aplicados se spec.lifecycle não informar (ttlSecondsAfterFinished: 3600, activeDeadlineSeconds: 1800)"

create_issue "E5.S2 - Comando/endpoint cleanup --all" "E5" "medium" "$M" \
"Como usuário, quero um comando único que remova todos os recursos com label kubeforge.io/managed=true no namespace, para liberar CPU/memória do meu laptop quando quiser.

**Critérios de aceite**
- [ ] CLI: kubeforge cleanup --all remove Jobs, Pods e PVCs órfãos
- [ ] Endpoint equivalente exposto para o Console Web
- [ ] Log de auditoria simples (o que foi removido e quando)"

create_issue "E5.S3 - Labels padronizadas em todo recurso criado" "E5" "high" "$M" \
"Como sistema, quero que todo recurso criado pelo Controller receba kubeforge.io/managed=true e kubeforge.io/component=<nome>, para viabilizar o cleanup seletivo.

**Critérios de aceite**
- [ ] Labels aplicadas em Jobs, Pods (via template), PVCs e ConfigMaps criados pelo Controller"

# --- E6 - API Backend ---
M="E6 - API Backend"
create_issue "E6.S1 - Endpoints CRUD de Componente" "E6" "high" "$M" \
"Como usuário, quero endpoints REST para criar, listar, obter e remover Componentes, para gerenciá-los sem editar YAML manualmente.

**Critérios de aceite**
- [ ] POST /components, GET /components, GET /components/{id}, DELETE /components/{id}
- [ ] Respostas em JSON seguindo o schema da seção 2.2"

create_issue "E6.S2 - Endpoint de ação Build" "E6" "high" "$M" \
"Como usuário, quero POST /components/{id}/build para disparar o Build Broker, para buildar a imagem sob demanda.

**Critérios de aceite**
- [ ] Retorna imediatamente com status=Building; build roda de forma assíncrona
- [ ] Endpoint de consulta de status reflete o progresso"

create_issue "E6.S3 - Endpoint de ação Run e Cleanup" "E6" "high" "$M" \
"Como usuário, quero POST /components/{id}/run e POST /components/{id}/cleanup, para executar e limpar um Componente específico via API.

**Critérios de aceite**
- [ ] run falha com mensagem clara se o Componente não tiver status.phase=Built
- [ ] cleanup remove os recursos da execução mais recente"

create_issue "E6.S4 - Endpoint de logs" "E6" "medium" "$M" \
"Como usuário, quero GET /components/{id}/logs (com suporte a streaming), para acompanhar a execução em tempo real.

**Critérios de aceite**
- [ ] Streaming via Server-Sent Events (SSE) ou WebSocket
- [ ] Fallback para retorno estático (últimas N linhas)"

# --- E7 - Console Web ---
M="E7 - Console Web"
create_issue "E7.S1 - Servidor de arquivos estáticos embutido" "E7" "medium" "$M" \
"Como desenvolvedor, quero servir os assets de web/static via embed.FS no próprio binário, para não depender de Nginx ou build step separado no MVP.

**Critérios de aceite**
- [ ] go:embed configurado em cmd/kubeforge
- [ ] Console acessível em http://localhost:8080"

create_issue "E7.S2 - Tela de cadastro de Componente" "E7" "medium" "$M" \
"Como usuário, quero um formulário simples para preencher source, resources, runtime e lifecycle, para cadastrar Componentes sem escrever JSON manualmente.

**Critérios de aceite**
- [ ] Formulário cobre os campos obrigatórios do schema
- [ ] Validação client-side básica antes do submit"

create_issue "E7.S3 - Tela de listagem e acompanhamento" "E7" "medium" "$M" \
"Como usuário, quero ver a lista de Componentes com seu status.phase atual, para acompanhar builds e execuções em andamento.

**Critérios de aceite**
- [ ] Lista com badge de status
- [ ] Botões de ação: Build, Run, Cleanup"

create_issue "E7.S4 - Tela de logs em tempo real" "E7" "low" "$M" \
"Como usuário, quero visualizar os logs da execução atual de um Componente, para depurar sem abrir terminal.

**Critérios de aceite**
- [ ] Consome o endpoint de streaming de logs (E6.S4)
- [ ] Auto-scroll com opção de pausar"

# --- E8 - Observabilidade Mínima ---
M="E8 - Observabilidade Mínima"
create_issue "E8.S1 - Endpoint de status/health do binário" "E8" "low" "$M" \
"Como usuário, quero GET /status retornando saúde da conexão com o Minikube e com o SQLite, para diagnosticar problemas rapidamente.

**Critérios de aceite**
- [ ] Retorna versão do cluster, caminho do DB, uptime do processo"

create_issue "E8.S2 - Logs estruturados do binário" "E8" "low" "$M" \
"Como desenvolvedor, quero logs estruturados de todas as operações (build, run, cleanup), para depurar o próprio KubeForge.

**Critérios de aceite**
- [ ] Uso consistente de slog em todos os pacotes
- [ ] Nível de log configurável via LOG_LEVEL"

echo ""
echo "==> Concluído! Repositório: https://github.com/${REPO_FULL}"
echo "    Labels, 8 milestones e 29 issues criadas."
