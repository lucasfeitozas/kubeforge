# ADR 0012 — E5.S3: fechamento das labels padronizadas (`component.ID`, escopo de ConfigMaps)

**Status:** Aceita
**Data:** 2026-08-24
**Contexto da história:** E5.S3 — Labels padronizadas em todo recurso criado

## Contexto

A E5.S3 pede que todo recurso criado pelo Controller receba `kubeforge.io/managed=true` e
`kubeforge.io/component=<valor>`, para viabilizar remoção seletiva.

Esse trabalho já foi implementado na prática durante a E5.S2 (`cleanup --all`, GH-19, ver
[ADR 0011](0011-cleanup-all-labels-e-log-de-auditoria.md)): a labeling foi identificada ali como
bloqueador funcional — sem ela, `cleanup --all` não encontraria nada para remover — e trazida
para aquela entrega como pré-requisito. `managedLabels(componentID)` (`internal/controller/job_builder.go`)
já está aplicado ao `ObjectMeta` de Jobs (`BuildJob`, `BuildPostRunJob`) e PVCs (`buildStoragePVC`),
e ao `Template.ObjectMeta` do `PodTemplateSpec` dos Jobs, propagando o label para os Pods.

O checkbox de E5.S3 em `docs/EPICOS_E_HISTORIAS.md` permaneceu desmarcado porque a ADR 0011
tratou a labeling como consequência lateral de E5.S2, sem fechar formalmente a história dedicada
a ela. Duas decisões ficaram implícitas nessa implementação e não haviam sido documentadas:
qual campo do Componente vira o valor do label `kubeforge.io/component` (a redação original da
história cita `<nome>`, mas o código usa `component.ID`), e se ConfigMaps continuam fora do
critério de aceite. Esta ADR resolve as duas e fecha E5.S3.

## Decisão

### `kubeforge.io/component` usa `component.ID`, não `component.Nome`

`internal/store.Component` tem dois campos candidatos: `ID` (identificador único, já usado como
nome do próprio Job/PVC no cluster — `Name: component.ID` em `BuildJob`/`buildStoragePVC`) e
`Nome` (campo de exibição livre, digitado pelo usuário, sem garantia de unicidade nem de formato
válido para label value do Kubernetes). `component.ID` é a escolha usada desde a implementação em
GH-19 e esta ADR confirma que é intencional: mantém o label consistente com a identidade real do
recurso no cluster e evita ter que sanitizar `Nome` para caber nas regras de label do Kubernetes
(63 caracteres, `[a-z0-9A-Z]([-_.a-z0-9A-Z]*[a-z0-9A-Z])?`). A redação da história em
`docs/EPICOS_E_HISTORIAS.md` (`kubeforge.io/component=<nome>`) é corrigida para `<id>` para
refletir essa decisão.

### ConfigMaps continuam fora do critério de aceite

Nenhum código do projeto cria `ConfigMap` hoje — o critério de aceite "Labels aplicadas em [...]
ConfigMaps criados pelo Controller" é vacuamente satisfeito, mesma leitura já usada na ADR 0011.
Isso não é um adiamento silencioso: se uma história futura introduzir criação de ConfigMap, ela
deve chamar `managedLabels` no mesmo ponto de construção do `ObjectMeta`, seguindo o padrão já
estabelecido para Jobs/Pods/PVCs.

## Consequências

- `docs/EPICOS_E_HISTORIAS.md`: critério de aceite de E5.S3 marcado como concluído; texto da
  história corrigido de `<nome>` para `<id>`; nota cruzada para esta ADR e para a ADR 0011.
- Nenhuma mudança em `internal/controller/job_builder.go` — a implementação de GH-19 já atende ao
  critério de aceite para os tipos de recurso existentes no projeto.
- Fica fora desta história (deferred, sem mudança em relação à ADR 0011): labeling de ConfigMaps
  (nenhum é criado hoje).
