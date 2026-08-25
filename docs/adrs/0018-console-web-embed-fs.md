# ADR 0018 — Console Web: pacote `web` próprio para o `go:embed`, servido como catch-all no mesmo mux da API

**Status:** Aceita
**Data:** 2026-08-25
**Contexto da história:** E7.S1 — Servidor de arquivos estáticos embutido

## Contexto

A E7.S1 pede: servir os assets de `web/static` via `embed.FS` no próprio
binário (`go:embed` configurado em `cmd/kubeforge`), tornando o Console
acessível em `http://localhost:8080` — sem Nginx nem build step separado.
`web/static/` e `web/templates/` já existiam desde o Epic E1 (só com
`.gitkeep`), e o `README.md` já documenta `web/` como diretório de topo,
irmão de `cmd/`/`internal/`.

`//go:embed` não pode referenciar um caminho fora da árvore do pacote que
contém a diretiva (sem `..`) — o mesmo obstáculo já resolvido na
[ADR 0017](0017-swagger-ui-via-cdn.md) para `internal/api/openapi.yaml`.
Como `web/static` precisa continuar na raiz do repo (documentada, e usada
pelas próximas histórias E7.S2-S4), a diretiva não pode viver fisicamente
em `cmd/kubeforge/` sem mover `web/static` para lá.

## Decisão

### `//go:embed` em um pacote `web` próprio, não em `cmd/kubeforge`

Em vez de mover `web/static` para `cmd/kubeforge/web/static` (o que
quebraria a estrutura já publicada desde o Epic E1 e a convenção já
estabelecida no projeto de manter `cmd/kubeforge` só como wiring, com toda
lógica em pacotes próprios — `internal/api`, `internal/build`,
`internal/controller`, `internal/k8s`, `internal/store`), a diretiva
`//go:embed static` vive em `web/embed.go` (`package web`), no próprio
diretório que já continha os assets. `cmd/kubeforge/main.go` importa esse
pacote e passa `web.StaticFS()` para `api.NewServer` — o *embedding* existe
e funciona (`go:embed` "configurado"), e o *wiring* (import + conexão ao
servidor HTTP, que é o que a história realmente pede de `cmd/kubeforge`)
acontece exatamente ali. Mesmo raciocínio já registrado na ADR 0017, agora
aplicado a `web/` em vez de `internal/api/`.

`web.StaticFS()` usa `fs.Sub` para remover o prefixo `"static/"` dos
caminhos do `embed.FS`, para que `index.html` sirva na raiz do site
(`http://localhost:8080/`) em vez de exigir `/static/index.html`.

### Rota estática registrada como catch-all no mesmo mux da API

`internal/api.NewServer` ganha um parâmetro `staticFS fs.FS` e registra
`s.mux.Handle("GET /", http.FileServer(http.FS(staticFS)))` como a última
rota — no mesmo `*http.ServeMux` que já serve `/components/...`,
`/cleanup`, `/openapi.yaml` e `/swagger/`, em vez de compor dois
`http.Handler` separados (um para a API, outro para os estáticos) com
alguma lógica de fallback manual. O `net/http.ServeMux` do Go 1.22+ já
resolve a precedência por especificidade de padrão automaticamente: um
caminho como `/components` sempre casa com o padrão exato antes do
catch-all `/`, então não há ambiguidade nem necessidade de ordenar as
chamadas de registro de forma especial.

### `index.html` desta entrega é um placeholder

Não existe ainda nenhuma UI real — cadastro (E7.S2), listagem com status
(E7.S3) e logs em tempo real (E7.S4) são histórias futuras e separadas.
`web/static/index.html` só precisa existir para que "Console acessível em
`http://localhost:8080`" (critério de aceite) seja verificável de fato, não
só a infraestrutura de embedding estar pronta sem nada para servir.

## Consequências

- Novo pacote `web` (`web/embed.go`): `//go:embed static` +
  `StaticFS() fs.FS`.
- `web/static/index.html` (novo): placeholder; `web/static/.gitkeep`
  removido (não é mais necessário com um arquivo real no diretório).
- `internal/api/server.go`: `NewServer` ganha o parâmetro `staticFS
  fs.FS`; rota catch-all `GET /`.
- `cmd/kubeforge/main.go`: importa `web` e passa `web.StaticFS()` para
  `api.NewServer`.
- Nenhuma mudança no `README.md` — a estrutura de diretórios documentada
  desde o Epic E1 continua exatamente igual.
- Fica fora desta história (deferred): a UI de verdade (E7.S2-S4);
  `web/templates/` continua sem uso (reservado para se uma história futura
  optar por HTMX + `html/template` em vez de servir só estático).
