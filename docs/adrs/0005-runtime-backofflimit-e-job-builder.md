# ADR 0005 — `backoffLimit` como campo de `spec.runtime`; `BuildJob` como função pura em `internal/controller`

**Status:** Aceita
**Data:** 2026-08-21
**Contexto da história:** E4.S1 — Mapeamento Componente → manifesto Job

## Contexto

A E4.S1 pede que o sistema gere um `batch/v1 Job` a partir de
`spec.runtime` + `spec.resources`, com três critérios de aceite:
`imagePullPolicy: Never` fixado; `resources.requests/limits`, `env`,
`command`, `args` mapeados corretamente; `restartPolicy` e `backoffLimit`
aplicados conforme spec.

O terceiro critério expôs uma lacuna: `backoffLimit` nunca foi definido em
nenhum schema do projeto. Ele só aparecia em prosa em
`docs/ARCHITECTURE.md` (§4.2, ao descrever por que `Job` é o `workloadKind`
predominante) e em `docs/EPICOS_E_HISTORIAS.md` (no próprio texto do
critério de aceite) — nem o CRD (`deployments/minikube/crd.yaml`) nem o
JSON Schema resumido (`docs/ARCHITECTURE.md` §2.2) tinham o campo. Sem um
campo persistido, "aplicado conforme spec" não tem o que ler.

Esta é a primeira história do E4 (Execution Engine); `internal/controller`
até aqui só continha `doc.go` (declarando a intenção do pacote) e
`crd_test.go` (valida o schema do CRD) — nenhuma lógica de geração de
manifesto existia.

## Decisão

### `backoffLimit` é um novo campo de `spec.runtime`, não de `spec.lifecycle` nem um valor fixo

Duas alternativas foram consideradas: adicionar o campo em `lifecycle`
(agrupado com `ttlSecondsAfterFinished`/`activeDeadlineSeconds`, outros
knobs de ciclo de vida do Job) ou fixar um valor constante (mesmo
tratamento de `imagePullPolicy: Never`, sem alterar o schema).

Optou-se por `runtime.backoffLimit`, ao lado de `restartPolicy`: os dois
campos descrevem o mesmo aspecto — como o Job reage a falha do container —
e já existe precedente de agrupá-los ali (`restartPolicy` já vive em
`runtime`, não em `lifecycle`). Fixar um valor constante foi descartado
porque contradiria o próprio texto do critério de aceite ("aplicado
conforme spec"): diferente de `imagePullPolicy` (fixo porque decorre de uma
restrição de infraestrutura do Minikube, não de preferência do usuário),
retries de Job são uma escolha legítima por Componente.

`backoffLimit` é `integer`, opcional, com `default: 0` no CRD — zero
retries é o comportamento mais previsível para um workload de teste
efêmero, e casa com o `default` de `resources.requests`/`replicas` do
schema, que também assumem valores conservadores quando omitidos.

### `BuildJob` é uma função pura (`*store.Component → *batchv1.Job`), sem cluster

`internal/controller/job_builder.go` segue o mesmo estilo de
`internal/build/broker.go`: blocos privados (`jobRuntimeBlock`,
`jobResourcesBlock`, etc.) espelhando só os campos de `runtime`/`resources`
necessários, parseados de `json.RawMessage` via `json.Unmarshal`, erros
envolvidos com `fmt.Errorf`.

`BuildJob` não recebe `k8s.ClusterProvider` nem decide `Namespace` do Job:
aplicar o manifesto no cluster depende de `targetContext.cluster` — fora do
critério de aceite desta história, e melhor resolvido junto da lógica de
`ClusterProvider` (`docs/ARCHITECTURE.md` §3.2) quando essa parte for
implementada. Manter `BuildJob` como função pura também a deixa
testável sem `envtest`/fake clientset, como os testes de
`internal/build/broker_test.go` já fazem para o Build Broker.

Pela mesma razão, o Job gerado aqui não recebe as labels
`kubeforge.io/managed`/`kubeforge.io/component` (E5.S3) nem
`InitContainers` de `hooks.preRun` (E4.S2): ambos pertencem a histórias
subsequentes do mesmo epic.

### `runtime.env[].valueFrom.secretKeyRef` é mapeado, apesar de não estar no critério de aceite

O critério de aceite só cita `env` genericamente, mas o exemplo já
documentado em `docs/ARCHITECTURE.md` §2.1 (`EXTRA_SECRET` com
`valueFrom.secretKeyRef`) e o schema do CRD (`runtime.env[]` é
`x-kubernetes-preserve-unknown-fields`, aceitando esse formato) tornam
`valueFrom` parte implícita de "mapeado corretamente" — ignorá-lo faria
envs configurados desaparecerem silenciosamente do Job gerado.

### O container se chama `main`; o Job usa `component.ID` como nome

O pseudocódigo de `docs/ARCHITECTURE.md` §4.2 usa `c.Name` como nome do
container, mas `component.Nome` é texto livre validado apenas como
não-vazio (`internal/store/component_validation.go`), sem garantia de
formato DNS-1123 exigido por nomes de recursos Kubernetes. `component.ID` é
um UUID (`github.com/google/uuid`), já seguro nesse formato — usado como
`ObjectMeta.Name` do Job. O container, único no Pod, usa o nome fixo
`main`.

## Consequências

- `deployments/minikube/crd.yaml` e `docs/ARCHITECTURE.md` (exemplo YAML e
  JSON Schema resumido) ganham `runtime.backoffLimit`.
- Novo arquivo `internal/controller/job_builder.go` com `BuildJob`, sem
  nenhuma dependência de `k8s.ClusterProvider` ou `controller-runtime`.
- `go.mod` ganha `k8s.io/api` e `k8s.io/apimachinery` como dependências
  diretas (antes indiretas, via `client-go`/`apiextensions-apiserver`).
- Quando `targetContext` + aplicação no cluster forem implementados (fora
  desta história), espera-se que o chamador de `BuildJob` seja responsável
  por preencher `Namespace` e labels antes de `Create` — nenhuma mudança
  adicional em `BuildJob` é esperada só por causa disso.
