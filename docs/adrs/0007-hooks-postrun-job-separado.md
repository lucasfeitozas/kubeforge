# ADR 0007 — `hooks.postRun` como Job separado, orquestrado por `Runner`

**Status:** Aceita
**Data:** 2026-08-21
**Contexto da história:** E4.S3 — Hooks `postRun` como Job separado

## Contexto

A E4.S3 pede que `spec.hooks.postRun` seja executado como um Job independente após o
término do Job principal, para rodar verificações sem acoplar ao container original, com
dois critérios de aceite: o Controller observa `phase: Succeeded/Failed` do Job principal e
dispara o Job de verificação; `continueOnError: false` interrompe o fluxo e marca a
execução como `Failed`.

Diferente de E4.S1/E4.S2, cujos critérios de aceite pediam apenas a geração correta de um
manifesto (`BuildJob`, `toInitContainers` — funções puras, sem cluster, testadas sem
`envtest`/fake clientset, ver ADR 0005/0006), esta história pede comportamento real de
orquestração. Isso expôs uma lacuna maior: **nenhum código do projeto aplica ou observa
Jobs num cluster real** — `BuildJob` nunca é chamada em produção, e `k8s.ClusterProvider`
(`internal/k8s/provider.go`) só é usado hoje para checar conectividade no startup
(`cmd/kubeforge/main.go`). `hooks.postRun` já era lido pelo CRD
(`x-kubernetes-preserve-unknown-fields`), mas o ADR 0006 deferiu explicitamente sua
interpretação para esta história.

O único idioma de orquestração já estabelecido no projeto é `internal/build.Broker`: um
`Run(ctx, ...) error` síncrono que cria uma `Execution`, avança `Component.Phase`/
`Execution.Phase` a cada transição via `UpdateBuildStatus`/`UpdatePhase`, e um helper
`fail(...)` que marca ambos `Failed` e propaga o erro causador. Não há
`controller-runtime`/reconcile loop/informers em lugar nenhum do repositório, apesar do
`docs/ARCHITECTURE.md` citar `kubebuilder` como framework alvo — o MVP implementado é uma
API síncrona sobre `client-go`, não um operator real.

## Decisão

### Escopo: orquestração funcional completa, não só o manifesto

Foi decidido junto ao usuário que esta história cobre o ciclo inteiro: aplicar o Job
principal no cluster-alvo (`k8s.ClusterProvider`), sondar sua fase até
`Succeeded`/`Failed`, e — independente do resultado do principal, já que o critério de
aceite fala em observar "Succeeded/Failed" — disparar e sondar o Job de verificação quando
`hooks.postRun` não está vazio. Isso introduz `internal/controller/runner.go`, reaproveitando
o idioma síncrono do `Broker` (`Run`/`fail`) em vez de desenhar um reconcile loop novo,
consistente com a ausência de `controller-runtime` no resto do projeto.

### Granularidade: um único Job de verificação, com um Container por item, em paralelo

`hooks.postRun` é um array, mas o critério de aceite fala de "o Job de verificação" no
singular, e o exemplo em `docs/ARCHITECTURE.md` §2.1 só mostra um item. Cada item de
`postRun` vira um `Container` comum (não `InitContainer`) do mesmo Job de verificação —
análogo a como `preRun` virou `InitContainers` do Job principal (ADR 0006), mas sem um
container "main" nesse Job para servir de gate, então os itens rodam em paralelo, não em
sequência. `continueOnError` não é um conceito nativo de `batch/v1.Job` para múltiplos
containers, então é tratado como uma política agregada calculada pelo `Runner`
(`postRunContinueOnError`, `internal/controller/job_builder.go`): `true` somente se todos
os itens declararem `continueOnError: true`; qualquer item omitindo (default `false`) ou
explicitando `false` derruba a política inteira — comportamento fail-safe, já que o
critério de aceite trata `continueOnError: false` como o caso que deve interromper o fluxo.

### `ClusterProvider.GetClientset` passa a retornar `kubernetes.Interface`, não `*kubernetes.Clientset`

Motivação puramente de testabilidade: `Runner` precisa aplicar/sondar `batchv1.Job` reais,
e a forma padrão de testar isso com `client-go` é `k8s.io/client-go/kubernetes/fake.NewSimpleClientset()`,
que devolve `*fake.Clientset` (implementa `kubernetes.Interface`, não é atribuível a
`*kubernetes.Clientset`). `k8s.io/client-go` já é dependência direta (`go.mod`), então não
há dependência nova. `internal/k8s/minikube.go` muda só a assinatura de `GetClientset` — o
corpo já retornava `*kubernetes.Clientset`, que satisfaz a interface na conversão implícita
do `return`. `cmd/kubeforge/main.go` não precisou de nenhuma mudança:
`clientset.Discovery().ServerVersion()` já usa um método que pertence a
`kubernetes.Interface`. Sem mudança de comportamento em produção.

### `determineFinalPhase` isolada como função pura

A lógica de combinar a fase do Job principal com a do Job de verificação — o núcleo do
critério de aceite 2 — foi extraída para `determineFinalPhase(mainPhase, postRunPhase
string, continueOnError bool) string`, sem receiver nem I/O, dentro de `runner.go`: uma
falha do Job de verificação só sobrepõe um Job principal bem-sucedido quando
`continueOnError` é `false`; caso contrário (incluindo quando o Job principal já é
`Failed`) a fase final reflete só o Job principal — a verificação não "salva" uma execução
que já falhou. Mantê-la pura permite testá-la por tabela sem SQLite nem fake clientset.

### Sondagem por polling sobre `status.conditions`, não `watch`

`Runner.applyAndWait` cria o Job e consulta `clientset.BatchV1().Jobs(ns).Get` a cada
`PollInterval` (default 2s) até `status.conditions` reportar `JobComplete=True` ou
`JobFailed=True`, respeitando cancelamento de `ctx`. Um `watch.Interface` foi considerado,
mas rejeitado por dois motivos: `MinikubeProvider` já configura um `restConfig.Timeout` de
5s sobre todas as chamadas do clientset (`internal/k8s/minikube.go`), incompatível com uma
conexão de watch de longa duração sem retrabalho adicional; e polling simples mantém o
mesmo estilo síncrono e sem dependências extras do resto do projeto (nenhum outro lugar do
código usa informers ou watches).

### Cuidado de persistência: nunca passar `buildImageDigest=""` para `UpdateBuildStatus` após o build

`ComponentRepository.UpdateBuildStatus(ctx, id, phase, buildImageDigest, errorMessage)`
grava `NULL` quando `buildImageDigest == ""` (`internal/store/sqlite_component_repository.go`,
helper `nullString`). Como `Runner` roda depois que o componente já tem uma imagem
buildada (`PhaseBuilt`), toda chamada a `UpdateBuildStatus` dentro de `runner.go` repassa
`component.BuildImageDigest` (capturado uma vez no início de `Run`), nunca uma string vazia
— diferente de `Broker.fail`, que passa `""` corretamente porque nesse ponto do fluxo de
build nenhum digest existe ainda.

## Consequências

- `internal/controller/job_builder.go` ganha `PostRunPlan`, `jobPostRunItemBlock`,
  `BuildPostRunJob`, `toPostRunContainers` e `postRunContinueOnError`; `jobHooksBlock` ganha
  o campo `PostRun`.
- Novo arquivo `internal/controller/runner.go` com `Runner` (`Run`, `fail`, `finalize`,
  `applyAndWait`, `jobTerminalPhase`, `determineFinalPhase`, `parseTargetContext`) — a
  primeira lógica do projeto que aplica/observa recursos num cluster real.
- `internal/k8s/provider.go` e `internal/k8s/minikube.go`: `ClusterProvider.GetClientset`
  retorna `kubernetes.Interface`. Nenhuma outra chamada no repositório precisou de ajuste.
- `internal/controller/job_builder_test.go` ganha casos para `BuildPostRunJob` e
  `postRunContinueOnError`; novo `internal/controller/runner_test.go` cobre `Runner` com
  SQLite real em memória (mesmo padrão de `internal/build/broker_test.go`) e
  `fake.NewSimpleClientset()` — a condição terminal de cada Job é injetada via
  `PrependReactor("create", "jobs", ...)`, mutando o objeto antes do tracker padrão do fake
  processá-lo, o que mantém os testes determinísticos sem sleep/goroutine.
- Fica fora desta história (deferred): `OwnerReferences` do Job de verificação para o Job
  principal e labels `kubeforge.io/managed`/`kubeforge.io/component` (E5.S3, mesmo
  adiamento já feito na ADR 0005); `TTLSecondsAfterFinished`/`ActiveDeadlineSeconds` (nunca
  implementados em nenhum Job gerado, nem antes desta história); wiring HTTP/API
  (`internal/api` continua só `doc.go` — nada chama `Runner.Run` em produção ainda, mesma
  situação em que `BuildJob` ficou após a E4.S1); idempotência/retry de `Runner.Run` (nomes
  de Job determinísticos — uma segunda chamada para o mesmo componente colide em
  `AlreadyExists`); streaming de logs; teardown/cleanup dos Jobs após o término (E5).
