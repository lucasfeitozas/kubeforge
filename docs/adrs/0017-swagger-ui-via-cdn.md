# ADR 0017 — Swagger UI via CDN sobre uma spec OpenAPI escrita à mão e embutida no binário

**Status:** Aceita
**Data:** 2026-08-25
**Contexto:** Issue #54 — "Configurar swagger nos endpoints", para testar a API interativamente (fora da numeração de Épicos/Histórias de `docs/EPICOS_E_HISTORIAS.md`: é uma melhoria de tooling/DX sobre os endpoints já entregues em E6, não uma história de produto nova).

## Contexto

Depois de E6 (S1-S4), a API tem 10 rotas (`internal/api/server.go`) sem
nenhuma forma de exploração/teste interativo — só `curl` manual ou os
testes automatizados. A issue pede "Swagger configurado na aplicação" para
testar os endpoints. Três decisões concretas precisaram ser tomadas para
ir de "nada" a "Swagger UI funcionando".

## Decisão

### Spec OpenAPI escrita à mão, não gerada por comentários/codegen

`swaggo/swag` (o gerador mais comum no ecossistema Go, que lê comentários
`// @Summary`/`@Param` em cada handler e roda `swag init` como passo de
build) foi descartado: exigiria anotar de forma não-idiomática todos os 10
handlers de `internal/api/server.go` com uma sintaxe de comentário
específica, e adicionar `swag` como dependência de build — nenhuma outra
documentação deste projeto é gerada (ARCHITECTURE.md, EPICOS_E_HISTORIAS.md
e todas as ADRs são texto escrito à mão). `internal/api/openapi.yaml` segue
a mesma convenção: um documento OpenAPI 3.0.3 escrito à mão, cobrindo as 10
rotas e os schemas reais aceitos/devolvidos (incluindo os campos que
`docs/ARCHITECTURE.md` §2.2 documenta como "resumido" mas o código de fato
aceita — ex.: `resources.storage.pvc.*`, `runtime.env[].valueFrom`,
`hooks.postRun[].continueOnError` — necessários para a spec ser realmente
utilizável no "Try it out" do Swagger UI, não só decorativa).

### UI servida via `swaggo/files` descartada: embute o pacote inteiro do swagger-ui-dist

Duas alternativas de biblioteca Go foram avaliadas para servir a UI:

- `swaggo/http-swagger` traz `swaggo/swag` como dependência transitiva —
  toda a cadeia de ~10 módulos do gerador (`go-openapi/spec`,
  `go-openapi/jsonpointer`, `easyjson`, `golang.org/x/tools` etc.), mesmo
  usando só a parte de servir a UI.
- `swaggo/files` sozinho evita essa cadeia (só `golang.org/x/net` como
  dependência nova), mas embute o pacote `swagger-ui-dist` **inteiro** via
  arquivos Go gerados: os bundles `swagger-ui-bundle.js`,
  `swagger-ui-es-bundle.js` e `swagger-ui-es-bundle-core.js` (três variantes
  redundantes do mesmo bundle) mais **todos os sourcemaps** — mais de 30MB
  de código Go gerado embutido no binário para servir ~3 arquivos
  realmente necessários (`swagger-ui-bundle.js`,
  `swagger-ui-standalone-preset.js`, `swagger-ui.css`).

Ambas descartadas: desproporcional para um projeto que já rejeitou
`chi`/`gorilla` (`net/http.ServeMux` já bastava) e uma lib de WebSocket
(ADR 0016) pelo mesmo tipo de raciocínio — dependência nova só quando
stdlib genuinamente não resolve, e aqui o ganho não compensa o peso.

### UI via CDN (unpkg), não vendorizada manualmente

Diante disso, a escolha foi carregar os assets do Swagger UI
(`swagger-ui-bundle.js`, `swagger-ui-standalone-preset.js`,
`swagger-ui.css`) via CDN (`unpkg.com/swagger-ui-dist@5`) numa página HTML
mínima (`swaggerUIHTML`, `internal/api/server.go`), em vez de baixar e
vendorizar manualmente só os arquivos necessários com `go:embed`. A
alternativa vendorizada evitaria a dependência de rede em tempo de
execução, mas exigiria fixar e baixar arquivos de terceiros via `curl`
avulso, sem o checksum/integridade que `go.sum` daria a uma dependência de
verdade — trocaria uma dependência de rede (só para abrir `/swagger`) por
um processo de atualização manual e menos verificável. Trade-off aceito:
`GET /swagger/` exige internet para carregar a UI; todos os outros 10
endpoints da API continuam 100% locais, sem depender de rede alguma.

### Spec embutida em `internal/api/openapi.yaml`, não em `docs/`

`go:embed` só aceita caminhos dentro da árvore do próprio pacote — não é
possível `//go:embed ../../docs/openapi.yaml` a partir de
`internal/api/server.go`. Servir a spec embutida no binário (em vez de ler
do disco em `docs/` em tempo de execução) foi preferida por manter o
binário `kubeforge` autocontido, consistente com a direção já declarada em
`docs/ARCHITECTURE.md` (Console Web futuro via `embed.FS`). O arquivo vive
em `internal/api/openapi.yaml`, não em `docs/`, como consequência direta
dessa restrição.

## Consequências

- `internal/api/server.go`: duas rotas novas —
  `GET /openapi.yaml` (serve `openAPISpecYAML`, embutida via `go:embed`) e
  `GET /swagger/{$}` (serve `swaggerUIHTML`, apontando para
  `/openapi.yaml`).
- `internal/api/openapi.yaml` (novo): contrato OpenAPI 3.0.3 completo das
  10 rotas existentes até E6.S4.
- `go.mod`: nenhuma dependência nova.
- Este documento não corresponde a nenhuma história em
  `docs/EPICOS_E_HISTORIAS.md` — é tooling sobre a API já entregue, sem
  checkbox para marcar.
- Fica fora desta entrega (deferred): manter `openapi.yaml` sincronizado
  manualmente conforme novas rotas forem adicionadas (sem verificação
  automática de que a spec bate com `server.go` — mesmo risco que qualquer
  documentação escrita à mão neste projeto).
