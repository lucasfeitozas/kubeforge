# ADR 0014 — `POST /components/{id}/build`: status exposto no CRUD (supera ADR 0013), goroutine com context.Background(), sem lock de build concorrente

**Status:** Aceita
**Data:** 2026-08-25
**Contexto da história:** E6.S2 — Endpoint de ação "Build"

## Contexto

A E6.S2 pede `POST /components/{id}/build` disparando o Build Broker
(`internal/build.Broker`, já implementado desde E3 mas nunca conectado à
camada HTTP nem ao `cmd/kubeforge`) de forma assíncrona, devolvendo
imediatamente `status=Building`, com um "endpoint de consulta de status" que
reflita o progresso do build. Três decisões surgiram ao conectar o Broker à
API.

## Decisão

### `componentDTO` passa a incluir `status` (phase/buildImageDigest/errorMessage) — supera a ADR 0013

A [ADR 0013](0013-crud-http-de-componente.md) excluiu esses campos do CRUD
de Componente presumindo que `GET /components/{id}/status` já os cobria.
Essa premissa estava errada para build: `/status` (E4.S4,
`controller.GetPodStatus`) consulta ao vivo o Pod do Job de **execução**
criado pelo Runner — não existe nenhum Job/Pod durante um build, que roda só
via `docker build` no host. Sem outro canal, não havia como satisfazer
"endpoint de consulta de status reflete o progresso" desta história. A
correção: `componentDTO` (`internal/api/server.go`) ganha um campo `status`
(`{phase, buildImageDigest, errorMessage}`, mesmo formato do `status.*` do
CRD documentado em `docs/ARCHITECTURE.md`), populado em toda resposta de
`GET /components` e `GET /components/{id}` — nunca aceito em `POST
/components` (o campo é ignorado se enviado).

### A goroutine roda com `context.Background()`, não com o contexto da requisição

`handleBuildComponent` cria o diretório de clone (`os.MkdirTemp`), marca o
Componente como `Building` (síncrono, antes de responder — garante que a
resposta 202 e uma consulta imediata a `GET /components/{id}` já reflitam
`Building` sem depender de timing da goroutine) e dispara `broker.Run` em
`go func() { ... }()`. Usar `r.Context()` ali cancelaria o build assim que a
resposta HTTP fosse escrita (o `net/http` cancela o contexto da requisição
ao final do handler) — o build precisa sobreviver ao fim da requisição que o
disparou. `context.Background()` resolve isso; o diretório de clone é
removido (`defer os.RemoveAll`) ao final da goroutine, cumprindo o contrato
de `Broker.Run` (`cloneDestDir` deve existir e estar vazio antes da
chamada).

### Sem lock contra builds concorrentes do mesmo Componente

Duas chamadas próximas a `POST /components/{id}/build` para o mesmo id
disparam duas goroutines de `Broker.Run` concorrentes, cada uma criando sua
própria `Execution` e escrevendo por cima do `status` do Componente sem
coordenação — a última a terminar "vence". Não há precedente no projeto para
esse tipo de lock (idêntico em espírito à falta de mapeamento de
`ON DELETE RESTRICT` para 409, ADR 0013), e os critérios de aceite de #22
não exigem essa proteção. Fica registrado como limitação aceita e deferida.

## Consequências

- `internal/api/server.go`: `Server` ganha o campo `broker *build.Broker`;
  `NewServer` recebe um quarto parâmetro; `componentDTO`/`componentToDTO`
  ganham `status`/`componentStatusDTO`; novo handler `handleBuildComponent`
  e `buildTriggerResponse`.
- `cmd/kubeforge/main.go`: primeira wiring em produção de
  `build.Broker` — monta `build.NewGitCloner()` +
  `build.NewDockerBuilder()` + os repositories já existentes e injeta em
  `api.NewServer`.
- `docs/EPICOS_E_HISTORIAS.md`: critérios de aceite de E6.S2 marcados como
  concluídos.
- Fica fora desta história (deferred): lock contra builds concorrentes do
  mesmo Componente; `POST /components/{id}/run` e `/cleanup` (E6.S3, issue
  separada).
