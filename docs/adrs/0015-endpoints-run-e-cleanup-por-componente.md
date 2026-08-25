# ADR 0015 — `POST /components/{id}/run` assíncrono com 409 se não `Built`; `/cleanup` por Componente reaproveita `RunCleanup` via label

**Status:** Aceita
**Data:** 2026-08-25
**Contexto da história:** E6.S3 — Endpoint de ação "Run" e "Cleanup"

## Contexto

A E6.S3 pede `POST /components/{id}/run` (falhando com mensagem clara se
`status.phase != Built`) e `POST /components/{id}/cleanup` (removendo os
recursos da execução mais recente do Componente). Toda a lógica de domínio
já existia — `controller.Runner.Run` (E4.S3) e `controller.RunCleanup`
(E5.S2) — só precisava ser conectada à camada HTTP. Três decisões surgiram
nesse trabalho de conexão.

## Decisão

### `run` é assíncrono, mesma razão da ADR 0014 para `build`

`Runner.Run` aplica o Job principal, sonda seu status a cada
`PollInterval` até uma fase terminal, dispara o Job de verificação
(`hooks.postRun`) e sonda de novo — um ciclo que pode levar minutos.
Bloquear o handler HTTP até o fim arriscaria timeout do cliente pelo mesmo
motivo já registrado para `build` na [ADR 0014](0014-endpoint-build-sob-demanda.md).
`handleRunComponent` marca `status.phase=Running` de forma síncrona (mesmo
padrão: resposta e um `GET /components/{id}` imediato já refletem o novo
estado) e dispara `runner.Run` em uma goroutine com `context.Background()`,
respondendo `202`. A resposta reaproveita `buildTriggerResponse`
(`{componentId, phase}`) criado para `build` — o formato é idêntico, não
há razão para um tipo novo.

### `run` responde `409 Conflict` quando `phase != Built`

O corpo da requisição é válido (não há corpo); é o **estado atual do
recurso** que impede a ação — o caso de uso canônico de 409, diferente de
400 (corpo malformado) ou 404 (recurso inexistente). A mensagem inclui a
fase atual e a esperada (E6.S3, AC1: "mensagem clara"). Efeito colateral
útil: como o handler já marca `Running` antes de responder, uma segunda
chamada de `run` enquanto a primeira ainda está em andamento cai
automaticamente nessa mesma checagem de fase (fase atual = `Running` ≠
`Built`) — funciona como guarda contra execuções concorrentes do mesmo
Componente sem precisar de lock dedicado.

### `cleanup` por Componente reaproveita `RunCleanup` via um label selector adicional, sem consultar `executions`

`RunCleanup` foi refatorado para delegar a `runCleanupWithSelector`,
parametrizada pelo label selector usado para listar Jobs/Pods/PVCs.
`RunComponentCleanup` (`internal/controller/cleanup.go`) monta o selector
`kubeforge.io/managed=true,kubeforge.io/component=<id>` e reusa a mesma
lógica de remoção. Como `BuildJob`/`BuildPostRunJob`/`buildStoragePVC`
nomeiam os recursos deterministicamente a partir de `component.ID`
(`internal/controller/job_builder.go`), só existe **uma geração** de
recursos rotulados por Componente viva no cluster a qualquer momento — uma
segunda `run` sem `cleanup` antes falharia com `AlreadyExists` no `Create`
do Job. Por isso, "recursos rotulados para este Componente agora" e
"recursos da execução mais recente" são a mesma coisa por construção: não
foi necessário consultar a tabela `executions` para descobrir qual foi a
"mais recente".

## Consequências

- `internal/controller/cleanup.go`: `RunCleanup` delega a
  `runCleanupWithSelector`; nova função exportada `RunComponentCleanup`.
- `internal/api/server.go`: `Server` ganha o campo
  `runner *controller.Runner`; `NewServer` recebe um quinto parâmetro;
  novos handlers `handleRunComponent` e `handleCleanupComponent` — este
  último reaproveita `cleanupResponse`/`cleanupItem`, já existentes para
  `POST /cleanup` global, sem nenhuma mudança neles.
- `cmd/kubeforge/main.go`: constrói e injeta `*controller.Runner` em
  `api.NewServer` (mesmas `components`/`executions` já usadas pelo
  `broker`).
- `docs/EPICOS_E_HISTORIAS.md`: critérios de aceite de E6.S3 marcados como
  concluídos.
- Fica fora desta história (deferred): `GET /components/{id}/logs` já
  existe desde E4.S4 (streaming via polling HTTP simples, não SSE/WebSocket
  — ver [ADR 0008](0008-status-e-logs-http-poll.md)); a história de logs
  streaming "de verdade" (E6.S4) permanece separada.
