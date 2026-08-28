# ADR 0020 — Console: listagem de Componentes com status e ações (Build/Run/Cleanup)

**Status:** Aceita
**Data:** 2026-08-26
**Contexto da história:** E7.S3 — Tela de listagem e acompanhamento

## Contexto

A E7.S2 (ADR 0019) entregou o shell de dashboard compartilhado e o formulário
de cadastro de Componente. A E7.S3 pede a tela que faltava para o Console ser
útil de fato: "ver a lista de Componentes com seu `status.phase` atual, para
acompanhar builds e execuções em andamento", com badge de status e botões de
ação (Build, Run, Cleanup).

Nenhuma mudança de backend é necessária. `GET /components`
(`handleListComponents`, `internal/api/server.go`) já devolve um array
(nunca `null`) de Componentes com `status: {phase, buildImageDigest,
errorMessage}` embutido em cada item. Os três endpoints de ação já existem
desde os épicos E4/E5/E6: `POST /components/{id}/build`
(`handleBuildComponent`, sempre permitido, 202 assíncrono), `POST
/components/{id}/run` (`handleRunComponent`, 409 se `status.phase !=
"Built"`, senão 202 assíncrono) e `POST /components/{id}/cleanup`
(`handleCleanupComponent`, sempre permitido, 200 síncrono). Um detalhe
importante para a implementação: ao contrário de `POST /components`
(cadastro), que devolve erros 400 estruturados em JSON
(`{"errors":[...]}"`), os erros desses três endpoints de ação (404/409/500)
são texto puro (`http.Error`, via `writeError`) — o padrão de tratamento de
erro de `componente-form.js`, que assume JSON, não se aplica diretamente aos
botões de ação desta tela.

A ADR 0019 havia registrado a intenção de a futura listagem (E7.S3) ocupar a
própria home do Console (`web/static/index.html`), para não competir com o
formulário de cadastro por espaço na mesma página. Esta ADR revisa esse
detalhe: a listagem ganha rota própria.

## Decisão

### Rota própria (`/componentes/index.html`), não a home — supera parcialmente a ADR 0019

Em vez de substituir o conteúdo de `web/static/index.html` pela listagem, ela
vira uma página própria, no mesmo diretório de `novo.html`, com item de
navegação dedicado na sidebar. Razões para o desvio: (1) mantém o padrão já
estabelecido pela E7.S2, em que cada tela relevante do CRUD tem sua própria
rota; (2) evita sobrepor "Início" e "Componentes" como conceitos concorrentes
na sidebar; (3) simplifica a futura E7.S4 (logs), que também vai querer sua
própria rota por Componente. `web/static/index.html` passa a ser só um ponto
de entrada com CTAs para "Ver Componentes" e "+ Novo Componente" — a
mensagem "a listagem com status chega na E7.S3" é removida, já deixando de
ser verdade.

### Tabela, não grade de cards

Diferente do formulário (conteúdo sequencial, seções grandes), a listagem é
dado de monitoramento — nome, descrição, fase, detalhe e três botões por
item. Uma tabela permite escanear rapidamente quais Componentes estão em
`Building`/`Failed`, e acomoda um grupo de botões por linha sem repetir a
estrutura de card da tela de cadastro.

### Badges via classes utilitárias `badge bg-*` do Bootstrap

Seguindo a mesma filosofia da ADR 0019 ("sobrescrever variáveis do Bootstrap
em vez de reimplementar componentes"), as 7 fases usam classes prontas do
Bootstrap: `Pending` → `bg-secondary` (neutro); `Building`/`Running` →
`bg-info text-dark`, com um `spinner-border spinner-border-sm` prefixado
(mesma cor para as duas fases "em andamento" — elas nunca aparecem
simultaneamente na mesma linha, então a cor compartilhada não é ambígua);
`Built` → `bg-primary` (usa `--kf-accent`, já roxo por sobrescrita em
`app.css` — estado estável, não transitório); `Succeeded` → `bg-success`;
`Failed` → `bg-danger` (acompanhado de `status.errorMessage` na coluna
Detalhe); `CleanedUp` → `bg-light text-dark border` (fase mais "arquivada",
intencionalmente a de menor destaque visual — precisou de uma única regra
CSS nova, `.badge.bg-light { border: 1px solid var(--kf-border); }`, porque
`bg-light` sozinho não contrasta com `--kf-bg`).

### Botão Run desabilitado fora da fase `Built`; Build e Cleanup sempre habilitados

O botão Run espelha no cliente a mesma regra que o backend já aplica
(`handleRunComponent`: 409 se `phase != "Built"`), evitando o caso comum de
clique inútil. Build e Cleanup ficam sempre habilitados porque o backend não
impõe pré-condição de fase para nenhum dos dois — inclusive Cleanup em um
Componente `Pending` (sem recursos ainda) já é tratado no servidor como
sucesso com `{"removed":[],"count":0}`.

### Estado de "ocupado" por linha, independente entre linhas

Esta é a primeira tela do Console com múltiplos botões assíncronos
concorrentes na mesma página. Ao clicar em uma ação, os três botões daquela
linha desabilitam e o botão clicado ganha um spinner; as demais linhas
continuam totalmente interativas. Um atributo `data-busy` na `<tr>` marca
esse estado, e o próximo `refreshList()` (poll ou manual) pula a
reconstrução de qualquer linha ainda ocupada, para não apagar o spinner no
meio de uma requisição em andamento.

### Tratamento de erro: 404/409/500 são texto puro, diferente do formulário

`componente-lista.js` nunca chama `response.json()` no branch de erro dos
endpoints de ação — faria isso lançar um erro de parse mascarando a
mensagem real do servidor (que vem via `http.Error`, texto puro). A mensagem
crua do backend é exibida diretamente na linha do Componente que originou o
erro, sem tentar mapear campo a campo como no cadastro (não há
`fieldErrorDTO` aqui).

### Atualização: botão manual + polling leve (5s, pausado com a aba oculta)

Build e Run são assíncronos — o `status.phase` muda no servidor depois da
resposta 202. Um botão "Atualizar" sozinho obrigaria o usuário a clicar
repetidamente para acompanhar o progresso; a tela adiciona um
`setInterval` de 5s que só dispara quando `document.visibilityState ===
"visible"`, evitando requisições desnecessárias com a aba em segundo plano.
Isso não é streaming em tempo real (SSE/WebSocket) — essa capacidade já
existe no backend só para logs (`GET /components/{id}/logs?follow=true`,
ADR 0016) e fica reservada para a E7.S4, que é sobre logs, não sobre
`status.phase`.

## Consequências

- `web/static/componentes/index.html` (novo): markup da listagem.
- `web/static/js/componente-lista.js` (novo): busca, renderização, ações,
  polling.
- `web/static/js/layout.js` (modificado): item de navegação "Componentes".
- `web/static/index.html` (modificado): CTA atualizado, mensagem "chega na
  E7.S3" removida.
- `web/static/css/app.css` (modificado): regra de contraste do badge
  `bg-light`.
- Nenhuma mudança em `internal/api`, `internal/store` ou `web/embed.go` —
  `//go:embed static` já embute qualquer arquivo novo sob `web/static/`.
- Fica fora desta história (deferred): atualização em tempo real via
  streaming (E7.S4), paginação/filtro da lista (não pedido pelos critérios
  de aceite), confirmação modal antes de Cleanup (não pedido), exclusão de
  Componente pela listagem (endpoint `DELETE /components/{id}` já existe,
  mas não faz parte dos critérios de aceite desta história).
