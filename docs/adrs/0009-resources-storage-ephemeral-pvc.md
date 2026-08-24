# ADR 0009 — `resources.storage`: `emptyDir` efêmero e `PersistentVolumeClaim` reaproveitável

**Status:** Aceita
**Data:** 2026-08-23
**Contexto da história:** E4.S5 — Suporte a armazenamento efêmero e PVC

## Contexto

A E4.S5 pede que `resources.storage` (`ephemeral` ou `pvc`) seja aplicado ao manifesto, com
dois critérios de aceite: `type: ephemeral` usa `emptyDir` com `sizeLimit`; `type: pvc`
cria/reaproveita um `PersistentVolumeClaim` com a `storageClassName` padrão do Minikube.

O CRD (`deployments/minikube/crd.yaml`) já aceita `resources.storage.{type,sizeLimit,pvc}`
desde antes desta história, e `internal/store/component_validation.go` já valida o enum
`storage.type` — mas nada disso era traduzido em manifesto: `BuildJob`
(`internal/controller/job_builder.go`) ignorava `resources.storage` por completo, e nenhum
código do projeto criava `PersistentVolumeClaim`s (apesar do RBAC em
`deployments/minikube/rbac.yaml` já listar o recurso desde o início do projeto).

Como em E4.S3 (ADR 0007), esta história mistura manifesto puro (o `Volume`/`VolumeMount` do
Job) com orquestração real de cluster (o PVC precisa existir *antes* do Job ser aplicado,
porque o Job o referencia pelo nome) — então a decisão de design central é onde cortar essa
fronteira entre `BuildJob`/`BuildStoragePVC` (funções puras) e `Runner` (que já aplica e
sonda o Job principal e o de verificação, ver ADR 0007).

## Decisão

### Um único `Volume`/`VolumeMount` fixo, não configurável pelo usuário

O Job principal só tem um container (`main`), então um único `Volume` chamado `storage`,
montado sempre em `/data`, cobre os dois tipos. O mount path não é um campo do schema (nem
do CRD, nem de `docs/ARCHITECTURE.md`) — a história define *que tipo* de armazenamento é
usado, não onde ele aparece no filesystem do container. Tornar o mount path configurável foi
descartado por não ser pedido pelo critério de aceite e por não haver precedente no schema
existente.

### `BuildJob` ganha um novo helper `buildStoragePlan`; `BuildStoragePVC` é uma função pública nova, tão pura quanto `BuildJob`

`resources.storage.type == "pvc"` produz dois artefatos a partir do mesmo parsing: o
`Volume` do Job (que só referencia o PVC pelo nome via `PersistentVolumeClaimVolumeSource`,
sem precisar que o PVC já exista) e o manifesto do próprio `PersistentVolumeClaim`. Ambos
são montados por `buildStoragePlan`, chamada tanto por `BuildJob` quanto pela nova função
pública `BuildStoragePVC(component) (*corev1.PersistentVolumeClaim, error)` — que segue o
mesmo contrato de `BuildJob`/`BuildPostRunJob`: não aplica nada no cluster, só monta o
manifesto. O parsing de `component.Resources` (antes inline em `BuildJob`) foi extraído para
`parseJobResources`, reaproveitado pelas duas funções públicas.

### `Runner.Run` garante o PVC antes de construir/aplicar o Job principal

Novo método `Runner.ensurePVC`: `Get` pelo nome e, se `apierrors.IsNotFound`, `Create`;
qualquer outro erro de `Get` (inclusive erros de cluster genéricos) propaga e falha o
componente via `r.fail`, mesmo padrão de erro de infraestrutura já usado para
`ClusterProvider.GetClientset` e `applyAndWait`. Chamado logo depois de obter o `clientset`
e antes de `BuildJob`, porque o Job já referencia o PVC pelo nome no seu `Volume` — se o PVC
não existisse ainda, o Pod ficaria preso em `Pending` esperando o volume ser montável.

**"Reaproveitar" significa não tocar no PVC existente, não sincronizar spec.** Quando o
`Get` encontra o PVC, `ensurePVC` retorna sem comparar ou atualizar `size`/`storageClassName`
contra o que está em `resources.storage` no momento — `PersistentVolumeClaimSpec` é
majoritariamente imutável após a criação (`storageClassName` e a maioria dos campos não
podem ser alterados; `resources.requests.storage` só pode crescer, e só se a StorageClass
suportar expansão), então tentar reconciliar divergências abriria uma classe de erros fora
do escopo desta história (ela pede "cria/reaproveita", não "mantém sincronizado"). Fica
implícito: mudar `resources.storage.pvc.size` de um Componente já executado antes não afeta
um PVC que já existe.

### `storageClassName` padrão hardcoded como `"standard"`, não lido de um `clusterProfiles.yaml`

`docs/ARCHITECTURE.md` §4.2 já desenha um conceito de `clusterProfiles.yaml` (mapa
cluster → `defaultStorageClass`, `imagePullPolicy`) para isolar diferenças Minikube/EKS do
Controller, mas esse arquivo nunca foi implementado — nem em código, nem como config real.
Como `internal/k8s` hoje só tem um `ClusterProvider` funcional (Minikube;
`targetContext.cluster: "eks"` passa na validação mas não tem provider), o valor
`"standard"` (a StorageClass padrão de qualquer instalação padrão do Minikube) foi embutido
como constante `defaultStorageClassName` em `job_builder.go`, usado só quando
`pvc.storageClassName` não é informado — o usuário pode sempre sobrescrever. Implementar o
`clusterProfiles.yaml` de verdade fica fora de escopo até existir um segundo
`ClusterProvider` real que precise de um valor diferente.

### `pvc.size` é obrigatório quando `type: pvc`; não há default implícito de capacidade

Diferente de `storageClassName`/`accessModes` (onde um default é inofensivo — só afeta
*onde*/*como* o volume é provisionado), a capacidade de um PVC é um recurso com custo e
persiste além da execução do Job. `buildStoragePVC` retorna erro explícito
("`resources.storage.pvc.size` é obrigatório...") quando ausente, em vez de assumir um
valor arbitrário — mesma filosofia de "campo obrigatório" já aplicada a
`resources.requests` em `validateResourcesRequests`
(`internal/store/component_validation.go`), só que aqui a validação acontece em `BuildJob`/
`BuildStoragePVC` (que já é onde `resources.requests`/`limits` são validados como
quantidades Kubernetes válidas via `resource.ParseQuantity`), não na camada `store.Validate`
— consistente com o corte de responsabilidade já estabelecido: `store` valida forma/enums
rasos, `controller` valida quantidades Kubernetes de fato.

### `accessModes` default `["ReadWriteOnce"]`

Único valor coerente com o provisionador dinâmico padrão do Minikube (`standard`, baseado em
`hostPath`), que não suporta `ReadWriteMany`; também é o único exemplo mostrado em
`docs/ARCHITECTURE.md` §2.1. Sobrescrito quando `pvc.accessModes` é informado.

## Consequências

- `internal/controller/job_builder.go` ganha `jobStorageBlock`, `jobPvcBlock` (novo campo
  `Storage` em `jobResourcesBlock`), `parseJobResources`, `buildStoragePlan`,
  `buildStoragePVC`, `toAccessModes`, `BuildStoragePVC`, e as constantes
  `storageVolumeName`, `storageMountPath`, `defaultStorageClassName`, `storagePVCSuffix`.
- `internal/controller/runner.go` ganha `Runner.ensurePVC` e uma nova etapa em `Run` entre
  obter o `clientset` e montar o Job principal; novo import
  `apierrors "k8s.io/apimachinery/pkg/api/errors"`.
- Nome do PVC é sempre `component.ID + "-data"` — mesmo estilo de `postRunJobSuffix`
  (ADR 0007). Colisão com um PVC de outro componente é impossível dado que `component.ID` já
  é único; colisão com um PVC criado manualmente pelo usuário com esse nome exato não é
  tratada (mesmo risco aceito para nomes de Job desde a ADR 0005).
- `deployments/minikube/crd.yaml` não muda: o schema de `resources.storage.pvc` já era
  `x-kubernetes-preserve-unknown-fields: true`, então já aceitava `storageClassName`/
  `accessModes`/`size` antes desta história — só não os documentava formalmente. Tipar esse
  sub-schema explicitamente fica como melhoria futura, não bloqueando esta história.
- Fica fora desta história (deferred): `clusterProfiles.yaml` real (só existe como constante
  hardcoded, ver acima); reconciliação de `resources.storage.pvc.size`/`storageClassName`
  contra um PVC já existente; múltiplos volumes por Componente; `OwnerReferences`/labels
  `kubeforge.io/*` no PVC (mesmo adiamento de E5.S3 já feito nas ADRs 0005/0007); expansão de
  PVC (`allowVolumeExpansion`); teardown do PVC após o término do Job (E5).
