# ADR 0006 — `hooks.preRun` como Init Containers do Job

**Status:** Aceita
**Data:** 2026-08-21
**Contexto da história:** E4.S2 — Suporte a Init Containers (hooks `preRun`)

## Contexto

A E4.S2 pede que `spec.hooks.preRun` seja traduzido em `InitContainers` do
Job gerado por `BuildJob`, com dois critérios de aceite: cada item de
`preRun` vira um Init Container na ordem declarada; a falha de um Init
Container impede o container principal de rodar.

`BuildJob` (`internal/controller/job_builder.go`, E4.S1) já lia
`spec.runtime` + `spec.resources`, mas ignorava `component.Hooks` — a ADR
0005 já havia antecipado essa lacuna, adiando `InitContainers` de
`hooks.preRun` explicitamente para esta história. `store.Component.Hooks`
já existe como `json.RawMessage` persistido (`internal/store/component.go`)
desde o Metadata Store, sem nunca ter sido parseado. O CRD
(`deployments/minikube/crd.yaml`) já define `hooks.preRun` como array com
`x-kubernetes-preserve-unknown-fields: true`, e `docs/ARCHITECTURE.md`
(§2.1) já documenta o formato de cada item com um exemplo:
`{name, image, command}`.

## Decisão

### `hooks.preRun` é parseado no mesmo estilo de `runtime`/`resources`; `postRun` é ignorado

Seguindo o padrão já estabelecido em `job_builder.go`: um bloco privado
`jobHooksBlock` (com `jobPreRunItemBlock` aninhado, campos `name`, `image`,
`command`) é parseado de `component.Hooks` via `json.Unmarshal`, com erro
envolvido em `fmt.Errorf("interpretando hooks do componente %q: %w", ...)`,
mesmo formato usado para `runtime`/`resources`.

`hooks.postRun` não é lido por `BuildJob`. Por design
(`docs/ARCHITECTURE.md` §4.2), hooks pós-execução viram um Job separado,
disparado pelo Controller ao observar `phase: Succeeded/Failed` do Job
principal — isso pertence à E4.S3, uma história distinta, e não afeta o
manifesto que `BuildJob` gera.

### Ordem preservada diretamente da ordem do array JSON, sem lógica de sequenciamento

`toInitContainers` apenas percorre `[]jobPreRunItemBlock` em ordem e
converte cada item para `corev1.Container`, sem reordenar. Isso é
suficiente para o critério de aceite porque `PodSpec.InitContainers` já é
um slice ordenado — o kubelet os executa sequencialmente na ordem em que
aparecem nesse slice.

### Falha de um Init Container bloqueando o principal é comportamento nativo do Kubernetes, não lógica do Controller

O segundo critério de aceite ("falha de um Init Container impede o
container principal de rodar") não exige nenhum código adicional: é assim
que o Kubernetes executa `initContainers` — sequencialmente, e se um falhar
o Pod não avança para os containers principais (sujeito a `restartPolicy`
do Pod). `BuildJob` só precisa popular `InitContainers` corretamente; a
semântica de bloqueio já vem de graça da API do Kubernetes.

### `ImagePullPolicy` não é fixado para os Init Containers de `preRun`

O container `main` usa `ImagePullPolicy: Never` porque sua imagem é
buildada pelo Build Broker e só existe no daemon Docker do Minikube (ADR
0002) — nunca em um registry. Os itens de `preRun`, ao contrário, referenciam
imagens de terceiros/registry normais (ex.: `curlimages/curl:8.9.0` no
exemplo de `docs/ARCHITECTURE.md`), sem essa restrição. Por isso os Init
Containers ficam com `ImagePullPolicy` no valor zero, deixando o Kubernetes
aplicar sua resolução padrão (`IfNotPresent`, ou `Always` se a tag for
`latest`).

### Nenhuma mudança de schema em `crd.yaml` ou `docs/ARCHITECTURE.md`

`hooks.preRun` já é `x-kubernetes-preserve-unknown-fields: true` no CRD e já
documentado com o formato `{name, image, command}` em `docs/ARCHITECTURE.md`
— ambos escritos quando o schema de `hooks` foi criado, antes mesmo da
E4.S1. Nenhum dos dois precisou de alteração para esta história.

## Consequências

- `internal/controller/job_builder.go` ganha `jobHooksBlock`,
  `jobPreRunItemBlock` e `toInitContainers`; `BuildJob` parseia
  `component.Hooks` e popula `podSpec.InitContainers`.
- `internal/controller/job_builder_test.go` ganha casos cobrindo `preRun`
  (ordem, ausência, JSON inválido); o helper `newComponent` ganha uma
  variante `newComponentComHooks` para os novos cenários.
- `hooks.postRun` continua sem nenhum código associado — fica para a E4.S3.
