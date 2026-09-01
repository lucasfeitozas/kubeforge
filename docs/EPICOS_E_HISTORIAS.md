# KubeForge — Épicos e Histórias (Backlog do MVP)

Escopo: MVP pessoal, 100% local, custo zero, Minikube apenas, Build Engine = Opção A (docker daemon do Minikube). Baseado em `docs/ARCHITECTURE.md`, seção 7.

Convenção de rotulagem usada no `scripts/bootstrap_github.sh`:
- Label de épico: `epic:E1` … `epic:E8`
- Label de tipo: `type:story`
- Label de prioridade: `priority:high` | `priority:medium` | `priority:low`
- Milestone: nome do épico

---

## E1 — Fundação do Projeto

**Objetivo:** ter o esqueleto do repositório, o binário rodando e conectado ao Minikube, antes de qualquer feature de negócio.

### E1.S1 — Setup do repositório e estrutura de pastas
**Como** desenvolvedor, **quero** um repositório Go inicializado com a estrutura de pastas definida na arquitetura, **para** começar a implementar as features sobre uma base organizada.
- Critérios de aceite:
  - [ ] `go.mod` criado (`module github.com/<usuario>/kubeforge`)
  - [ ] Estrutura `cmd/`, `internal/`, `web/`, `deployments/`, `docs/` presente
  - [ ] `README.md` e `docs/ARCHITECTURE.md` versionados
  - [ ] `.gitignore` cobrindo binários, `*.db` (SQLite), `.env`
- Prioridade: alta

### E1.S2 — Binário único com bootstrap de configuração
**Como** desenvolvedor, **quero** um `main.go` em `cmd/kubeforge` que carregue configuração básica (porta HTTP, caminho do kubeconfig, caminho do SQLite), **para** ter um ponto de entrada único para API + Controller + Build Broker.
- Critérios de aceite:
  - [ ] Flags/env vars: `--http-port` (default 8080), `--kubeconfig` (default `~/.kube/config`), `--db-path` (default `./kubeforge.db`)
  - [ ] Log estruturado inicial (`slog`)
- Prioridade: alta

### E1.S3 — Conexão com o cluster Minikube via client-go
**Como** desenvolvedor, **quero** validar a conexão com o Minikube ao iniciar o binário, **para** falhar rápido se o cluster não estiver acessível.
- Critérios de aceite:
  - [ ] `internal/k8s` expõe `ClusterProvider` com um único provider registrado (`minikube`)
  - [ ] Comando de startup faz um `ServerVersion()` do client-go e loga a versão do cluster
  - [ ] Erro claro se `~/.kube/config` não existir ou contexto `minikube` não estiver ativo
- Prioridade: alta

### E1.S4 — CRD `KubeForgeComponent` aplicada manualmente
**Como** desenvolvedor, **quero** o manifesto YAML da CRD (seção 2.1 da arquitetura) versionado em `deployments/minikube/crd.yaml`, **para** aplicá-la manualmente no cluster antes de qualquer execução.
- Critérios de aceite:
  - [ ] `deployments/minikube/crd.yaml` criado com o schema completo (source, build, resources, runtime, hooks, targetContext, lifecycle, status)
  - [ ] `kubectl apply -f deployments/minikube/crd.yaml` funciona sem erros no Minikube
- Prioridade: alta

### E1.S5 — Namespace e RBAC mínimo do MVP
**Como** desenvolvedor, **quero** o `Namespace` (`kubeforge-workloads`, com `pod-security.kubernetes.io/enforce: baseline`) e a `Role`/`RoleBinding` simplificados (seção 7.1), **para** rodar com um mínimo de isolamento sem a complexidade de RBAC multi-tenant.
- Critérios de aceite:
  - [ ] `deployments/minikube/namespace.yaml`
  - [ ] `deployments/minikube/rbac.yaml` (Role escopada ao namespace)
- Prioridade: média

---

## E2 — Modelo de Dados e Metadata Store

**Objetivo:** persistir os Componentes cadastrados e o histórico de execuções em SQLite.

### E2.S1 — Schema SQLite inicial
**Como** desenvolvedor, **quero** as tabelas `components` e `executions` criadas via migration, **para** persistir cadastro e histórico.
- Critérios de aceite:
  - [ ] Migration inicial (`internal/store/migrations`)
  - [ ] Tabela `components` reflete o schema JSON da seção 2.2 (source, build, resources, runtime, hooks, target_context, lifecycle)
  - [ ] Tabela `executions` referencia `component_id`, guarda `phase`, `started_at`, `completed_at`, `image_digest`
- Prioridade: alta

### E2.S2 — Repositório de dados (CRUD de Componente)
**Como** desenvolvedor, **quero** funções Go para criar, ler, listar, atualizar e remover Componentes no SQLite, **para** que a camada de API não acesse SQL diretamente.
- Critérios de aceite:
  - [ ] `internal/store` expõe interface `ComponentRepository` com `Create/Get/List/Update/Delete`
  - [ ] Testes unitários cobrindo os métodos com um SQLite em memória
- Prioridade: alta

### E2.S3 — Validação do payload de Componente
**Como** usuário, **quero** que campos obrigatórios (`source.repoUrl`, `resources`, `runtime.workloadKind`, `targetContext`) sejam validados no cadastro, **para** evitar Componentes malformados chegando ao build/execução.
- Critérios de aceite:
  - [ ] Validação rejeita `repoUrl` inválida, `workloadKind` fora do enum, ausência de `resources.requests`
  - [ ] Mensagens de erro específicas por campo
- Prioridade: média

---

## E3 — Build Engine (Opção A — docker local via Minikube)

**Objetivo:** transformar o código-fonte de um Componente em imagem disponível para o Minikube, sem registry externo.

### E3.S1 — Clonagem do repositório (branch/tag/commit)
**Como** sistema, **quero** clonar o repositório informado em `source.repoUrl` respeitando `source.ref` (branch, tag ou commit), **para** obter o código-fonte a ser buildado.
- Critérios de aceite:
  - [x] Clone raso (`--depth=1`) quando `ref.type=branch` ou `tag`
  - [x] Checkout específico quando `ref.type=commit`
  - [x] Suporte a repositório privado via `credentialsSecretRef` (token lido de env/secret local)
- Prioridade: alta

### E3.S2 — Build via `docker build` apontando para o daemon do Minikube
**Como** sistema, **quero** executar `docker build` usando as variáveis de ambiente equivalentes a `minikube docker-env`, **para** que a imagem fique disponível diretamente ao cluster.
- Critérios de aceite:
  - [x] Build Broker injeta as env vars do `minikube docker-env` no processo do build (sem depender do usuário rodar `eval` manualmente)
  - [x] Tag da imagem segue `imageTagStrategy` (`commit-sha` como padrão do MVP)
  - [x] Logs do build persistidos e associados à `execution` correspondente
- Prioridade: alta

### E3.S3 — Atualização de status de build
**Como** usuário, **quero** ver o status do build (`Pending → Building → Built/Failed`) refletido no Componente, **para** saber quando ele está pronto para execução.
- Critérios de aceite:
  - [x] Campo `status.phase` e `status.buildImageDigest` atualizados ao final do build
  - [x] Falha de build não deixa o Componente em estado ambíguo (`Failed` explícito + mensagem de erro)
- Prioridade: alta

### E3.S4 — Cache de build simples
**Como** usuário, **quero** que builds sucessivos do mesmo Componente reaproveitem cache de camadas Docker, **para** builds mais rápidos durante experimentação iterativa.
- Critérios de aceite:
  - [x] Reutiliza o cache padrão do daemon Docker do Minikube (sem flag `--no-cache`)
  - [x] Opção explícita para forçar rebuild sem cache
- Prioridade: baixa

---

## E4 — Execution Engine (Controller/Reconciler)

**Objetivo:** traduzir a especificação do Componente em um manifesto nativo (`Job`, no MVP) e aplicá-lo no Minikube.

### E4.S1 — Mapeamento Componente → manifesto Job
**Como** sistema, **quero** gerar um `batch/v1 Job` a partir de `spec.runtime` + `spec.resources`, **para** executar o Componente como workload de teste.
- Critérios de aceite:
  - [x] `imagePullPolicy: Never` fixado (imagem já está no daemon do Minikube)
  - [x] `resources.requests/limits`, `env`, `command`, `args` mapeados corretamente
  - [x] `restartPolicy` e `backoffLimit` aplicados conforme spec
- Prioridade: alta

### E4.S2 — Suporte a Init Containers (hooks `preRun`)
**Como** usuário, **quero** que `spec.hooks.preRun` seja traduzido em Init Containers do Job, **para** validar pré-condições antes da execução principal.
- Critérios de aceite:
  - [x] Cada item de `preRun` vira um Init Container na ordem declarada
  - [x] Falha de um Init Container impede o container principal de rodar (comportamento nativo do K8s)
- Prioridade: média

### E4.S3 — Hooks `postRun` como Job separado
**Como** usuário, **quero** que `spec.hooks.postRun` seja executado como um Job independente após o término do Job principal, **para** rodar verificações sem acoplar ao container original.
- Critérios de aceite:
  - [x] Controller observa `phase: Succeeded/Failed` do Job principal e dispara o Job de verificação
  - [x] `continueOnError: false` interrompe o fluxo e marca a execução como `Failed`
- Prioridade: média

### E4.S4 — Acompanhamento de status e logs
**Como** usuário, **quero** consultar o status (`Pending/Running/Succeeded/Failed`) e os logs do Pod em execução, **para** acompanhar o teste sem usar `kubectl` diretamente.
- Critérios de aceite:
  - [x] Endpoint/consulta que reflete o status atual do Job/Pod
  - [x] Stream ou tail de logs disponível via API
- Prioridade: alta

### E4.S5 — Suporte a armazenamento efêmero e PVC
**Como** usuário, **quero** que `resources.storage` (ephemeral ou pvc) seja aplicado ao manifesto, **para** testar comportamentos que dependem de armazenamento.
- Critérios de aceite:
  - [x] `type: ephemeral` usa `emptyDir` com `sizeLimit`
  - [x] `type: pvc` cria/reaproveita um `PersistentVolumeClaim` com a `storageClassName` padrão do Minikube
- Prioridade: baixa

---

## E5 — Teardown / Cleanup

**Objetivo:** garantir que nenhum recurso de teste fique preso consumindo CPU/memória do Minikube além do necessário.

### E5.S1 — TTL nativo aplicado automaticamente
**Como** sistema, **quero** aplicar `ttlSecondsAfterFinished` e `activeDeadlineSeconds` em todo Job criado, **para** que o próprio Kubernetes limpe recursos concluídos ou travados.
- Critérios de aceite:
  - [x] Valores default aplicados se `spec.lifecycle` não informar (`ttlSecondsAfterFinished: 3600`, `activeDeadlineSeconds: 1800`)
- Prioridade: alta

### E5.S2 — Comando/endpoint `cleanup --all`
**Como** usuário, **quero** um comando único que remova todos os recursos com label `kubeforge.io/managed=true` no namespace, **para** liberar CPU/memória do meu laptop quando quiser.
- Critérios de aceite:
  - [x] CLI: `kubeforge cleanup --all` remove Jobs, Pods e PVCs órfãos
  - [x] Endpoint equivalente exposto para o Console Web
  - [x] Log de auditoria simples (o que foi removido e quando)
- Prioridade: média

### E5.S3 — Labels padronizadas em todo recurso criado
**Como** sistema, **quero** que todo recurso criado pelo Controller receba `kubeforge.io/managed=true` e `kubeforge.io/component=<id>`, **para** viabilizar o cleanup seletivo.
- Critérios de aceite:
  - [x] Labels aplicadas em Jobs, Pods (via template), PVCs e ConfigMaps criados pelo Controller (`managedLabels` em `internal/controller/job_builder.go`, implementado como pré-requisito da E5.S2/GH-19 — ver ADR 0011; ConfigMaps não se aplicam hoje, nenhum é criado pelo projeto — ver ADR 0012)
- Prioridade: alta

---

## E6 — API Backend (CRUD de Componente)

**Objetivo:** expor as operações de cadastro/build/execução via HTTP para o Console Web (e uso via `curl`/Postman).

### E6.S1 — Endpoints CRUD de Componente
**Como** usuário, **quero** endpoints REST para criar, listar, obter e remover Componentes, **para** gerenciá-los sem editar YAML manualmente.
- Critérios de aceite:
  - [x] `POST /components`, `GET /components`, `GET /components/{id}`, `DELETE /components/{id}`
  - [x] Respostas em JSON seguindo o schema da seção 2.2 (`componentDTO` em `internal/api/server.go` — ver ADR 0013)
- Prioridade: alta

### E6.S2 — Endpoint de ação "Build"
**Como** usuário, **quero** `POST /components/{id}/build` para disparar o Build Broker, **para** buildar a imagem sob demanda.
- Critérios de aceite:
  - [x] Retorna imediatamente com `status=Building`; build roda de forma assíncrona (goroutine)
  - [x] Endpoint de consulta de status reflete o progresso (`status` em `componentDTO`, `GET /components/{id}` — ver ADR 0014)
- Prioridade: alta

### E6.S3 — Endpoint de ação "Run" e "Cleanup"
**Como** usuário, **quero** `POST /components/{id}/run` e `POST /components/{id}/cleanup`, **para** executar e limpar um Componente específico via API.
- Critérios de aceite:
  - [x] `run` falha com mensagem clara se o Componente ainda não tiver `status.phase=Built`
  - [x] `cleanup` remove os recursos da execução mais recente (`RunComponentCleanup`, escopado por `kubeforge.io/component=<id>` — ver ADR 0015)
- Prioridade: alta

### E6.S4 — Endpoint de logs
**Como** usuário, **quero** `GET /components/{id}/logs` (com suporte a streaming), **para** acompanhar a execução em tempo real.
- Critérios de aceite:
  - [x] Streaming via Server-Sent Events (SSE) ou WebSocket (`sseWriter`, `text/event-stream` em `?follow=true` — ver ADR 0016)
  - [x] Fallback para retorno estático (últimas N linhas) se streaming não for suportado pelo client (`?follow=false`, já existente desde E4.S4/ADR 0008)
- Prioridade: média

---

## E7 — Console Web (embutido no binário)

**Objetivo:** interface mínima para cadastro, acompanhamento e logs, sem dependências externas de hospedagem.

### E7.S1 — Servidor de arquivos estáticos embutido
**Como** desenvolvedor, **quero** servir os assets de `web/static` via `embed.FS` no próprio binário, **para** não depender de Nginx ou build step separado no MVP.
- Critérios de aceite:
  - [x] `go:embed` configurado em `cmd/kubeforge` (pacote `web`, importado e conectado em `cmd/kubeforge/main.go` — ver ADR 0018)
  - [x] Console acessível em `http://localhost:8080` (`web/static/index.html`, placeholder — UI real em E7.S2-S4)
- Prioridade: média

### E7.S2 — Tela de cadastro de Componente
**Como** usuário, **quero** um formulário simples para preencher `source`, `resources`, `runtime` e `lifecycle`, **para** cadastrar Componentes sem escrever JSON manualmente.
- Critérios de aceite:
  - [x] Formulário cobre os campos obrigatórios do schema (seção 2.2) — `web/static/componentes/novo.html`, ver ADR 0019
  - [x] Validação client-side básica antes do submit
- Prioridade: média

### E7.S3 — Tela de listagem e acompanhamento
**Como** usuário, **quero** ver a lista de Componentes com seu `status.phase` atual, **para** acompanhar builds e execuções em andamento.
- Critérios de aceite:
  - [x] Lista com badge de status (`Pending/Building/Built/Running/Succeeded/Failed/CleanedUp`) — `web/static/componentes/index.html`, ver ADR 0020
  - [x] Botões de ação: Build, Run, Cleanup — `web/static/js/componente-lista.js`
- Prioridade: média

### E7.S4 — Tela de logs em tempo real
**Como** usuário, **quero** visualizar os logs da execução atual de um Componente, **para** depurar sem abrir terminal.
- Critérios de aceite:
  - [x] Consome o endpoint de streaming de logs (E6.S4) — `EventSource` em `GET /components/{id}/logs?follow=true`, `web/static/js/componente-logs.js`, ver ADR 0021
  - [x] Auto-scroll com opção de pausar — `web/static/componentes/logs.html`
- Prioridade: baixa

---

## E8 — Observabilidade Mínima

**Objetivo:** visibilidade básica sem subir Prometheus/Grafana/Loki no MVP.

### E8.S1 — Endpoint de status/health do binário
**Como** usuário, **quero** `GET /status` retornando saúde da conexão com o Minikube e com o SQLite, **para** diagnosticar problemas rapidamente.
- Critérios de aceite:
  - [x] Retorna versão do cluster, caminho do DB, uptime do processo
- Prioridade: baixa

### E8.S2 — Logs estruturados do binário
**Como** desenvolvedor, **quero** logs estruturados (JSON ou texto legível) de todas as operações (build, run, cleanup), **para** depurar o próprio KubeForge.
- Critérios de aceite:
  - [x] Uso consistente de `slog` em todos os pacotes
  - [x] Nível de log configurável via `LOG_LEVEL`
- Prioridade: baixa

---

## Ordem sugerida de execução

```
E1 (Fundação) → E2 (Dados) → E3 (Build) → E4 (Execução) → E5 (Cleanup) → E6 (API) → E7 (Console) → E8 (Observabilidade)
```

E8 e partes de E7 (S4) podem ficar para depois do primeiro ciclo funcional ponta a ponta (cadastro → build → run → cleanup via API/CLI, mesmo sem UI completa).
