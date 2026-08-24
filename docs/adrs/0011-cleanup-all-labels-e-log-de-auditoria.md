# ADR 0011 — `cleanup --all`: labels mínimas, remoção por seletor e log de auditoria

**Status:** Aceita
**Data:** 2026-08-24
**Contexto da história:** E5.S2 — Comando/endpoint `cleanup --all`

## Contexto

A E5.S2 pede um comando único que remova todo recurso rotulado `kubeforge.io/managed=true` no
namespace — Jobs, Pods e PVCs órfãos —, exposto como `kubeforge cleanup --all` (CLI) e como
endpoint HTTP equivalente para o Console Web, registrando um log de auditoria simples do que foi
removido e quando.

Investigação inicial revelou um bloqueador: o label `kubeforge.io/managed=true` **não é aplicado
a nenhum recurso hoje** — `BuildJob`/`BuildPostRunJob`/`buildStoragePVC`
(`internal/controller/job_builder.go`) nunca preenchiam `ObjectMeta.Labels`. Essa labeling é a
história E5.S3 ("Labels padronizadas em todo recurso criado"), separada e ainda não
implementada, citada no próprio backlog como pré-requisito ("para viabilizar o cleanup
seletivo"). Sem ela, `cleanup --all` não encontraria nada para remover — a história ficaria
funcionalmente vazia.

## Decisão

### Labeling mínimo incluído nesta entrega, como pré-requisito funcional

Em vez de entregar um `cleanup --all` que não remove nada até uma história futura, esta entrega
inclui o essencial de E5.S3 para os tipos de recurso que o projeto realmente cria hoje: Jobs
(principal e postRun) e PVCs. `managedLabels(componentID)` (`job_builder.go`) monta
`kubeforge.io/managed=true` + `kubeforge.io/component=<id>`, aplicado ao `ObjectMeta` de cada
Job/PVC **e** ao `Template.ObjectMeta` do `PodTemplateSpec` dos Jobs — é esse último que propaga
o label para os Pods que o Job cria, satisfazendo "remove Jobs, Pods e PVCs". ConfigMaps (também
citados em E5.S3) ficam de fora: nenhum código do projeto cria ConfigMaps hoje, então não há o
que rotular.

### `RunCleanup`/`PersistCleanupAudit` como funções livres, não um struct com estado

Diferente de `Runner` (que carrega `PollInterval` e múltiplos repositórios ao longo de um fluxo
assíncrono de aplicar-e-esperar), o cleanup é uma operação síncrona de listar+remover sem estado
próprio — o mesmo idioma de `BuildJob`/`GetPodStatus`. `RunCleanup(ctx, provider, clusterKey,
namespace)` (`internal/controller/cleanup.go`) só depende do `ClusterProvider` recebido por
parâmetro; `PersistCleanupAudit(ctx, audit, results)` é uma segunda função, deliberadamente
separada, para que a lógica de remoção continue testável com só um `fake.Clientset` (sem SQLite)
e a lógica de auditoria continue testável com um repositório em memória (sem cluster).

### Ordem Jobs → Pods → PVCs, best-effort

Jobs são removidos primeiro com `DeletePropagationBackground` explícito: a exclusão em cascata do
Kubernetes já remove os Pods que o Job possui, então a segunda passada (lista+remove Pods
rotulados) tolera `NotFound` — o Pod já pode ter sumido via GC assíncrono antes do `Delete`
explícito rodar. Um erro em qualquer lista/remoção interrompe a varredura, mas os
`CleanupResult`s já coletados são retornados junto do erro: o chamador (CLI/API) ainda registra a
auditoria do que foi de fato removido antes da falha, em vez de perder o rastro de uma limpeza
parcial.

### Log de auditoria: tabela nova, só `Record`

Nova tabela `cleanup_audit_log` (migration `0004`, mesmo padrão numerado das migrations
existentes) com `resource_kind`/`resource_name`/`namespace`/`removed_at` — exatamente "o que foi
removido e quando", sem campos além dos pedidos pelo critério de aceite (nada de
`triggered_by`/`cleanup_reason`, que pertenciam à estratégia corporativa multi-tenant já removida
do MVP pessoal em `docs/ARCHITECTURE.md` §7.4). `CleanupAuditRepository` só expõe `Record`: não
há critério de aceite pedindo uma forma de consultar o log pela API/CLI, e a tabela continua
inspecionável diretamente no SQLite quando necessário.

### Namespace via flag/query param; cluster fixo em `"minikube"`

Diferente do `Runner`, que resolve namespace a partir de `spec.targetContext` de um Componente
específico, `cleanup --all` é namespace-wide e não está associado a nenhum Componente — precisa
de sua própria forma de escolher o namespace. CLI usa `--namespace` (default `"default"`); o
endpoint usa `?namespace=` (mesmo default). O cluster é fixado em `k8s.MinikubeClusterKey`: é o
único `ClusterProvider` real implementado hoje, mesma decisão já tomada para
`defaultStorageClassName` na ADR 0009.

## Consequências

- `internal/controller/job_builder.go` ganha `managedLabelKey`/`managedLabelValue`/
  `componentLabelKey`/`managedLabels`, aplicados em `BuildJob`, `BuildPostRunJob` e
  `buildStoragePVC`.
- `internal/controller/cleanup.go` (novo): `CleanupResult`, `RunCleanup`,
  `PersistCleanupAudit`.
- `internal/store`: migration `0004_create_cleanup_audit_log`, `CleanupAuditEntry`,
  `CleanupAuditRepository`, `sqlite_cleanup_audit_repository.go`.
- `internal/api/server.go`: `Server` ganha o campo `cleanupAudit`; `NewServer` ganha um novo
  parâmetro obrigatório; nova rota `POST /cleanup`.
- `cmd/kubeforge/`: `main()` passa a despachar `cleanup` antes do fluxo de servidor (extraído
  para `runServer()`); novo `cleanup.go` com o subcomando.
- Fica fora desta história (deferred): `POST /components/{id}/cleanup` (E6.S3, história
  separada — remove os recursos da execução mais recente de *um* Componente, não o namespace
  inteiro); labeling de ConfigMaps (nenhum é criado hoje); CronJob de garbage
  collection/ResourceQuota corporativos (fora do MVP pessoal, `docs/ARCHITECTURE.md` §7.4);
  consulta ao log de auditoria via API/CLI.
