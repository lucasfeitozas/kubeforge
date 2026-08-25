# ADR 0013 — CRUD HTTP de Componente: DTO fiel à seção 2.2, lista sem envelope, FK-restrict não vira 409

**Status:** Aceita
**Data:** 2026-08-25
**Contexto da história:** E6.S1 — Endpoints CRUD de Componente

## Contexto

A E6.S1 pede quatro endpoints REST (`POST /components`, `GET /components`,
`GET /components/{id}`, `DELETE /components/{id}`) com respostas JSON
seguindo o schema "Componente" documentado em `docs/ARCHITECTURE.md` §2.2.
Toda a persistência e validação já existiam antes desta história —
`store.ComponentRepository` (`Create/Get/List/Delete`) e
`(*store.Component).Validate()` foram implementados em épicos anteriores
(E2/E3) e só precisaram ser conectados à camada HTTP em
`internal/api/server.go`. Três decisões de design surgiram nesse trabalho de
conexão e são registradas aqui.

## Decisão

### O DTO HTTP espelha a seção 2.2 exatamente, sem campos de status

`componentDTO` (`internal/api/server.go`) expõe só os campos do schema §2.2:
`id, nome, descricao, source, build, resources, runtime, hooks,
targetContext, lifecycle`. Os campos de status do `store.Component`
(`Phase`, `BuildImageDigest`, `ErrorMessage`, `CreatedAt`, `UpdatedAt`) não
aparecem nas respostas do CRUD — eles pertencem à camada de status/CRD, já
expostos por `GET /components/{id}/status` (E4.S4), e mudam de forma
assíncrona (via `UpdateBuildStatus`, fora do fluxo de criação/edição do
Componente). Misturá-los no mesmo corpo JSON criaria duas fontes de verdade
para o mesmo dado e acoplaria o contrato de CRUD ao ciclo de vida de build.

### `GET /components` devolve um array JSON no nível raiz, sem envelope

Diferente de `cleanupResponse` (que envelopa em `{"removed": [...], "count":
N}` porque carrega uma contagem e um verbo de ação), a listagem de
Componentes devolve `[...]` diretamente — coerente com o schema §2.2, que
descreve o recurso "Componente" no singular sem definir um schema de
coleção, e sem exigência de paginação/metadados nesta história.
`store.ComponentRepository.List` já devolve `[]*Component{}` (nunca `nil`),
então o handler nunca serializa `null` para uma lista vazia.

### Falha de `DELETE` por `ON DELETE RESTRICT` não é mapeada para 409

`sqliteComponentRepository.Delete` propaga o erro cru do driver
(`modernc.org/sqlite`) quando existem execuções referenciando o componente
sendo removido (`executions.component_id REFERENCES components(id) ON
DELETE RESTRICT`). Esse erro não é um dos casos tratados por `writeError` e
cai no `default` (500 genérico, com o detalhe apenas logado). Não há
precedente hoje no projeto para inspecionar códigos de erro do driver SQLite
e mapeá-los semanticamente (ex.: para 409 Conflict), e os critérios de
aceite de #21 não exigem esse tratamento — fica registrado como limitação
aceita e deferida, não como omissão silenciosa.

## Consequências

- `docs/EPICOS_E_HISTORIAS.md`: critérios de aceite de E6.S1 marcados como
  concluídos.
- `internal/api/server.go`: `writeError` ganha um caso para
  `*store.ValidationError` (400, com a lista de campos inválidos), reusado
  tanto pelo `POST /components` desta história quanto por qualquer handler
  futuro que chame `Create`/`Update`.
- Nenhuma mudança em `internal/store/` — toda a lógica de persistência e
  validação já existia e só foi conectada à API.
- Fica fora desta história (deferred): mapear o erro de `ON DELETE
  RESTRICT` em `DELETE /components/{id}` para 409 Conflict; endpoint de
  atualização (`PUT`/`PATCH /components/{id}`), não pedido pelos critérios
  de aceite de #21 apesar de `ComponentRepository.Update` já existir.
