# ADR 0022 — Vendorizar Bootstrap e Swagger UI localmente, revertendo a decisão por CDN (ADR 0017/0019)

**Status:** Aceita
**Data:** 2026-08-27
**Contexto:** Continuação de troubleshooting sobre GH-28/GH-24 — não corresponde a uma história nova de `docs/EPICOS_E_HISTORIAS.md`.

## Contexto

A ADR 0017 (Swagger UI, unpkg.com) e a ADR 0019 (Bootstrap, cdn.jsdelivr.net)
tinham decidido conscientemente por CDN em vez de vendorizar, aceitando o
trade-off "exige internet para abrir a página" em troca de zero dependência
Go nova e nenhum processo manual de atualização "sem o checksum/integridade
que `go.sum` daria a uma dependência de verdade" (ADR 0017).

Na prática, esse trade-off se provou pior do que previsto: em uma rede real
(a usada nesta sessão de desenvolvimento), `unpkg.com` e `cdn.jsdelivr.net` —
ambos atrás da Cloudflare — ficaram completamente inalcançáveis (timeout de
conexão TCP, confirmado via `curl`, Python e um Chromium real), enquanto
`api.github.com` e `registry.npmjs.org` continuaram respondendo normalmente.
O resultado não foi só `/swagger/` em branco: **toda a navegação do Console**
(`index.html`, `componentes/index.html`, `novo.html`, `logs.html`) travava por
~20s no carregamento do Bootstrap e renderizava sem nenhum estilo
(`border-radius: 0`, fonte Arial padrão do browser) — um script headless
confirmou o `page.goto` estourando timeout esperando o `<script>` do CDN.

## Decisão

### Vendorizar os 5 arquivos realmente usados, não todo o pacote

`web/static/vendor/bootstrap/` (`bootstrap.min.css`,
`bootstrap.bundle.min.js`) e `web/static/vendor/swagger-ui/`
(`swagger-ui.css`, `swagger-ui-bundle.js`,
`swagger-ui-standalone-preset.js`) — exatamente os mesmos 5 arquivos que já
eram carregados via CDN, nada além disso (sem sourcemaps, sem as variantes
ES/core redundantes que a ADR 0017 rejeitou ao descartar `swaggo/files`).
Total: ~2,3MB, servidos via `//go:embed static` (`web/embed.go`) já
existente — nenhuma mudança de embed necessária, qualquer arquivo novo sob
`web/static/` já é automaticamente embutido no binário.

### Buscados via tarball do registry npm, não `curl` avulso do CDN

Isso resolve diretamente a objeção original da ADR 0017 ("sem o
checksum/integridade que `go.sum` daria"): os arquivos vieram de
`registry.npmjs.org/<pkg>/-/<pkg>-<versão>.tgz` (endpoint de tarball do
próprio registry, íntegro e versionado), não de uma cópia manual do CDN.
Versões e checksums SHA-256 dos arquivos vendorizados, para auditoria/
atualização futura:

| Arquivo | Origem (pacote@versão) | SHA-256 |
|---|---|---|
| `vendor/bootstrap/bootstrap.min.css` | `bootstrap@5.3.3` | `3c8f27e6009ccfd710a905e6dcf12d0ee3c6f2ac7da05b0572d3e0d12e736fc8` |
| `vendor/bootstrap/bootstrap.bundle.min.js` | `bootstrap@5.3.3` | `0833b2e9c3a26c258476c46266e6877fc75218625162e0460be9a3a098a61c6c` |
| `vendor/swagger-ui/swagger-ui.css` | `swagger-ui-dist@5.32.14` | `d7f39f764aa18c7b47dd05b9af5613e373e4ac0f3557c2693d52d0abc2464d76` |
| `vendor/swagger-ui/swagger-ui-bundle.js` | `swagger-ui-dist@5.32.14` | `16d93d5cc19e54c98fb0b81157dbb3bd90780aa36b914e128a643b31e54a93f4` |
| `vendor/swagger-ui/swagger-ui-standalone-preset.js` | `swagger-ui-dist@5.32.14` | `b6c3e519a7b920b9bfd48eb92521a768da1bc86343b926ef74ba6d481da6844b` |

`bootstrap@5.3.3` mantém a versão já pinada pela ADR 0019.
`swagger-ui-dist@5.32.14` é a última release 5.x no momento desta ADR (a
ADR 0017 usava `@5`, sem pin de patch, via unpkg).

### Sem pipeline npm no repositório

Os pacotes foram baixados uma vez, manualmente, para popular
`web/static/vendor/` — não há `package.json` nem `node_modules` versionados
no projeto. Atualizar os arquivos vendorizados no futuro é um processo
manual (baixar o tarball da nova versão, copiar os mesmos arquivos,
atualizar a tabela acima), o mesmo tipo de manutenção que qualquer ADR
anterior já assumia para documentação escrita à mão.

## Consequências

- `web/static/vendor/bootstrap/*`, `web/static/vendor/swagger-ui/*` (novos,
  binários/minificados, não editar à mão).
- `web/static/index.html`, `componentes/index.html`, `componentes/novo.html`,
  `componentes/logs.html`: `<link>`/`<script>` do Bootstrap apontam para
  `/vendor/bootstrap/...` em vez do CDN.
- `internal/api/server.go` (`swaggerUIHTML`): aponta para
  `/vendor/swagger-ui/...` em vez de `unpkg.com`.
- `docs/adrs/0017-swagger-ui-via-cdn.md` e
  `docs/adrs/0019-console-cadastro-de-componentes.md`: seções sobre CDN
  marcadas como superadas por esta ADR.
- O Console e o Swagger UI agora funcionam 100% offline, igual aos demais
  endpoints da API — nenhuma parte do produto depende mais de rede externa
  em tempo de execução.
- Fica fora desta entrega (deferred): automatizar a atualização dos
  arquivos vendorizados (ex.: um script `make vendor-refresh`) — não existe
  hoje, e não é urgente para um MVP que já fixa quase toda dependência por
  versão exata.
