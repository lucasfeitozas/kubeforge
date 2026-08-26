# ADR 0019 — Console: cadastro de Componentes via formulário Bootstrap + shell de dashboard compartilhado

**Status:** Aceita
**Data:** 2026-08-25
**Contexto da história:** E7.S2 — Tela de cadastro de Componente

## Contexto

A E7.S1 (ADR 0018) entregou `web/static/index.html` como placeholder, servido
via `embed.FS`, estabelecendo um site zero-build (HTML/CSS/JS puros, sem
bundler, sem templating engine). A E7.S2 pede: "um formulário simples para
preencher `source`, `resources`, `runtime` e `lifecycle`, para cadastrar
Componentes sem escrever JSON manualmente", com validação client-side básica.

O texto da issue cita apenas quatro blocos, mas o schema real (
`docs/ARCHITECTURE.md` §2.2, validado por
`internal/store/component_validation.go`) também exige `nome` e
`targetContext` no nível raiz — `POST /components` responde 400 sem eles. O
formulário cobre o schema real, não só a lista resumida da issue. Nenhuma
mudança de backend é necessária: `POST /components`
(`internal/api/server.go`, `handleCreateComponent`) já aceita exatamente
esse payload e já devolve erros 400 estruturados como
`{"errors":[{"field":"source.repoUrl","message":"..."}]}` (`fieldErrorDTO`,
`internal/api/server.go`).

Além do formulário, a issue pede um dashboard responsivo com sidebar
retrátil, Bootstrap 5, paleta cinza-gelo + roxo suave, e um visual que fuja
do genérico. Como E7.S3 (listagem) e E7.S4 (logs) vão precisar da mesma
sidebar/topbar, essa é a primeira história a decidir como montar um shell de
dashboard compartilhado sem introduzir build step.

## Decisão

### Bootstrap 5 via CDN, não vendorizado

`web/static/*.html` carrega Bootstrap 5 (CSS + JS bundle) via `<link>`/
`<script>` apontando para o CDN oficial (`cdn.jsdelivr.net`), em vez de
vendorizar os arquivos no repositório ou introduzir um pipeline npm/asset
bundler. Mesmo raciocínio já registrado na ADR 0017 para o Swagger UI (único
precedente de dependência CDN no projeto): zero-build, sem dependência Go
nova, ao custo de exigir internet para abrir o Console (aceitável, mesmo
trade-off já aceito para `/swagger/`).

### Shell de dashboard compartilhado via `layout.js`, não HTML duplicado

HTML puro não tem mecanismo de include, e duplicar a marcação de
sidebar/topbar em cada página faria as páginas divergirem conforme E7.S3/S4
forem adicionadas. Em vez disso, toda página do Console carrega o mesmo
esqueleto mínimo:

```html
<div id="app-shell" style="visibility:hidden">
  <main id="page-content"> ...conteúdo específico da página... </main>
</div>
<script src="/js/layout.js" defer></script>
```

`web/static/js/layout.js` roda em `DOMContentLoaded`, constrói a sidebar e a
topbar via DOM API, move (não clona) o `#page-content` para dentro da nova
estrutura — preservando a identidade de qualquer elemento nele, importante
porque outros scripts da página (`componente-form.js`) prendem listeners a
esses elementos — marca o link ativo pela URL atual, e só então remove
`visibility:hidden`. Isso mantém uma única fonte de verdade para a
marcação do shell, sem template engine nem build step, ao custo de um
flash breve antes da injeção (mitigado pelo `visibility:hidden` inicial).

### `index.html` vira home do dashboard; formulário em página própria

`web/static/index.html` deixa de ser um placeholder de texto e passa a ser a
home do dashboard (shell + estado vazio + CTA para "Novo Componente"),
mantendo o link para `/swagger/` como secundário. O formulário de cadastro
fica em `web/static/componentes/novo.html`, uma página própria — não uma
seção da home — para deixar espaço para a futura listagem (E7.S3) ocupar a
home sem competir com o formulário. A sidebar (`layout.js`) já traz um item
de navegação para "Novo Componente" e um comentário indicando onde o link de
listagem de E7.S3 entrará.

### JS vanilla sem framework, um arquivo por responsabilidade

Sem bundler, vanilla JS (Fetch API, DOM API) é a única opção consistente com
o zero-build da ADR 0018. Três arquivos, cada um com uma responsabilidade:
`layout.js` (shell/navegação), `validation.js` (validadores reutilizáveis:
regex de quantidade Kubernetes, helpers de marcação de campo inválido) e
`componente-form.js` (específico da página de cadastro: montagem do
payload, submit, mapeamento de erros do servidor).

### Validação client-side: HTML5 + Bootstrap `is-invalid`, mais regras customizadas para casos cross-field

Camada base: atributos nativos (`required`, `type="url"`, `pattern` para
quantidades Kubernetes como `250m`/`256Mi`) combinados com o padrão
documentado do Bootstrap (`novalidate` + `form.checkValidity()` +
classe `.was-validated` + `<div class="invalid-feedback">` por campo).
Camada customizada (`validation.js` + lógica em `componente-form.js`) cobre
regras que HTML5 não expressa: exigir ao menos um de
`resources.requests.cpu`/`.memory`, exigir `resources.storage.pvc.size`
apenas quando `resources.storage.type === "pvc"`, e consistência das linhas
repetíveis de `runtime.env` (nome e valor preenchidos juntos ou ambos
vazios). Qualquer falha bloqueia o submit (`preventDefault`) e foca o
primeiro campo inválido.

Como fallback best-effort (não uma segunda engine de validação — o critério
de aceite só exige validação client-side), erros 400 do servidor são
mapeados de volta aos campos via `[data-field="source.repoUrl"]`, um
atributo espelhando exatamente o `field` dot-path devolvido por
`fieldErrorDTO`.

## Consequências

- `web/static/index.html` (modificado): vira home do dashboard.
- `web/static/componentes/novo.html` (novo): formulário de cadastro.
- `web/static/css/app.css` (novo): paleta (`#f4f6f9` / `#7c3aed`), layout do
  shell (sidebar retrátil, topbar), estilo de cards.
- `web/static/js/layout.js` (novo): injeção do shell, colapso/off-canvas da
  sidebar, link ativo.
- `web/static/js/validation.js` (novo): validadores reutilizáveis.
- `web/static/js/componente-form.js` (novo): montagem do payload, submit,
  mapeamento de erros do servidor.
- Nenhuma mudança em `internal/api` ou `web/embed.go` — `//go:embed static`
  já embute qualquer arquivo/subdiretório novo sob `web/static/`, e o
  catch-all `GET /` já serve qualquer caminho do FS embutido.
- Fica fora desta história (deferred): listagem de Componentes com status
  (E7.S3), logs em tempo real (E7.S4), redirecionamento pós-cadastro para a
  listagem (ainda não existe), suporte a `hooks` no formulário (bloco
  opcional do schema, não citado nos quatro blocos da issue).
