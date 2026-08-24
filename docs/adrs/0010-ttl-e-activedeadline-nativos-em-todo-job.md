# ADR 0010 — `ttlSecondsAfterFinished` e `activeDeadlineSeconds` nativos em todo Job

**Status:** Aceita
**Data:** 2026-08-23
**Contexto da história:** E5.S1 — TTL nativo aplicado automaticamente

## Contexto

A E5.S1 pede que todo `batch/v1 Job` criado pelo Controller carregue `ttlSecondsAfterFinished`
e `activeDeadlineSeconds`, usando `spec.lifecycle` quando informado ou os defaults
`ttlSecondsAfterFinished: 3600`/`activeDeadlineSeconds: 1800` caso contrário. É a primeira
camada da estratégia de teardown descrita em `docs/ARCHITECTURE.md` §5.1: deixar o próprio
`kube-controller-manager` remover Jobs concluídos (TTL) e travados (deadline), sem depender de
nenhuma reconciliação ativa do Controller — que ainda não existe (E5.S2/S3).

O CRD (`deployments/minikube/crd.yaml`) já declarava `spec.lifecycle.{ttlSecondsAfterFinished,
activeDeadlineSeconds}` com exatamente esses defaults desde antes desta história — as ADRs 0005
e 0007 já registraram esses campos como deliberadamente adiados. O que faltava era só o lado Go:
`BuildJob`/`BuildPostRunJob` (`internal/controller/job_builder.go`) nunca liam
`component.Lifecycle` nem preenchiam `Spec.TTLSecondsAfterFinished`/`Spec.ActiveDeadlineSeconds`
no manifesto gerado.

## Decisão

### Defaults aplicados em Go, não só confiados ao default do OpenAPI schema do CRD

O default declarado em `deployments/minikube/crd.yaml` só é aplicado pelo *admission* do
API server do Kubernetes quando o CRD é criado/atualizado via `kubectl apply`/API diretamente.
`BuildJob` não passa por esse caminho: ele lê `component.Lifecycle` como está persistido no
SQLite (`internal/store`), que pode estar vazio se o Componente foi criado antes de qualquer
validação de schema tocar o campo. Por isso os defaults precisam ser resolvidos explicitamente
no código Go, como constantes (`defaultTTLSecondsAfterFinished`, `defaultActiveDeadlineSeconds`
em `job_builder.go`), mesmo padrão já usado para `defaultStorageClassName` (ADR 0009).

### Novo bloco `jobLifecycleBlock` e helper `parseJobLifecycle`, reaproveitado pelos dois Jobs

Segue o mesmo estilo de `jobResourcesBlock`/`jobHooksBlock`: um struct privado que espelha só os
campos de `spec.lifecycle` relevantes para o manifesto (`TTLSecondsAfterFinished
*int32`, `ActiveDeadlineSeconds *int64` — os mesmos tipos de `batchv1.JobSpec`), decodificado via
`parseJobLifecycle`. A função resolve os dois valores de uma vez (default se o ponteiro do JSON
for `nil`) e é chamada tanto por `BuildJob` quanto por `BuildPostRunJob`, evitando duplicar a
lógica de fallback.

`cleanupPolicy` não é lido por `jobLifecycleBlock`: já é decodificado e validado (enum) por
`internal/store/component_validation.go` (`lifecycleBlock`), e seu uso real — remover
ativamente os recursos do Componente conforme a política — é uma reconciliação do Controller
que só existe a partir de E5.S2/S3. Duplicar sua leitura aqui não teria efeito nenhum ainda.

### `BuildJob` e `BuildPostRunJob` recebem o mesmo tratamento

O critério de aceite fala em "todo Job criado", não só o Job principal. O Job de verificação
pós-execução (`BuildPostRunJob`, ADR 0007) é um `batch/v1 Job` como qualquer outro e fica sujeito
ao mesmo risco de travar ou de sobrar concluído no cluster — por isso `parseJobLifecycle` também
é chamado ali, logo após o `if len(hooks.PostRun) == 0 { return &PostRunPlan{}, nil }`: não há
necessidade de resolver lifecycle quando nenhum Job de verificação será de fato criado.

## Consequências

- `internal/controller/job_builder.go` ganha `jobLifecycleBlock`, `parseJobLifecycle`, e as
  constantes `defaultTTLSecondsAfterFinished`/`defaultActiveDeadlineSeconds`; `BuildJob` e
  `BuildPostRunJob` passam a preencher `Spec.TTLSecondsAfterFinished`/`Spec.ActiveDeadlineSeconds`
  em todo `batchv1.Job` retornado.
- `deployments/minikube/crd.yaml` não muda: o schema já estava correto antes desta história.
- `internal/store/component_validation.go` não muda: a validação de `lifecycle.cleanupPolicy`
  já existente é suficiente; `ttlSecondsAfterFinished`/`activeDeadlineSeconds` não passam por
  validação de faixa/sinal — o Kubernetes já rejeita valores inválidos na criação do Job (mesmo
  corte de responsabilidade já usado para outras quantidades, ver ADR 0009).
- Fica fora desta história (deferred): `cleanupPolicy` sendo de fato usado por uma reconciliação
  ativa do Controller (E5.S2/S3); CronJob de auditoria/garbage collection (`docs/ARCHITECTURE.md`
  §5.1, camada 4); teardown de PVCs (camada 6).
