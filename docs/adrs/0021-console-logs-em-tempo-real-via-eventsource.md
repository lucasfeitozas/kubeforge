# ADR 0021 — Console: logs em tempo real via `EventSource`, sem reconexão automática silenciosa

**Status:** Aceita
**Data:** 2026-08-27
**Contexto da história:** E7.S4 — Tela de logs em tempo real

## Contexto

A ADR 0020 (E7.S3) já havia reservado esta próxima peça do Console: "a
futura E7.S4 (logs), que também vai querer sua própria rota por
Componente". O backend necessário está pronto desde a E6.S4 (ADR 0016):
`GET /components/{id}/logs?follow=true` transmite os logs do Pod mais
recente do Job daquele Componente via Server-Sent Events
(`text/event-stream`, um evento `data: <linha>\n\n` por chunk de
`Write`), com fallback síncrono em `follow=false` (texto puro, últimas
`tailLines` linhas). A E7.S4 pede o consumidor frontend dessa capacidade:
"visualizar os logs da execução atual de um Componente, para depurar sem
abrir terminal", com auto-scroll pausável. Nenhuma mudança de backend foi
necessária.

## Decisão

### `EventSource` nativo, não `fetch` + `ReadableStream` manual

O framing SSE que a ADR 0016 implementou (`data: ...` por linha, evento
fechado por linha em branco) é exatamente o que `EventSource` já
interpreta nativamente em todo browser evergreen — reimplementar esse
parsing sobre `fetch`/`ReadableStream` seria trabalho redundante sem
ganho, e destoaria da filosofia zero-build já estabelecida (ADR
0017/0018/0019): preferir a API da plataforma em vez de código próprio
quando ela já resolve o problema.

### Sem reconexão automática silenciosa

`EventSource` tenta reconectar sozinho por padrão sempre que a conexão
cai — inclusive quando o Pod atinge uma fase terminal e o servidor
simplesmente fecha a resposta (fim normal do stream, não uma falha). O
protocolo desenhado pela ADR 0016 não implementa `id:`/cursor de
continuidade (explicitamente deferido naquela ADR); sem isso, uma
reconexão automática reabriria `GET .../logs?follow=true` do zero e
duplicaria no visualizador todo o conteúdo já exibido. `logs.html` evita
isso fechando a conexão explicitamente (`EventSource.close()`) dentro de
`onerror`, mostrando um estado "Conexão encerrada" e um botão manual
"Reconectar" — uma parada visível, em vez de um loop de reconexão
silencioso que duplica histórico.

### Botão de auto-scroll, sem heurística de posição de scroll

O critério de aceite ("auto-scroll com opção de pausar") é atendido com
um botão simples alternando um booleano `autoScrollPaused`, sem tentar
inferir a intenção do usuário a partir da posição do scroll (ex.: pausar
sozinho quando ele rola para cima). Essa inferência é uma fonte comum de
comportamento surpreendente (ex.: uma rolagem acidental pausando o
acompanhamento sem o usuário perceber) e não foi pedida pelo critério.

### Nome/fase buscados via `GET /components/{id}`, não propagados pela URL

`logs.html?id=<id>` carrega só o identificador do Componente; nome e fase
exibidos no cabeçalho vêm de uma busca própria a `GET /components/{id}`
(já existente, `handleGetComponent`) ao carregar a página, em vez de
serem passados como parâmetros adicionais de querystring — evita exibir
um nome ou fase potencialmente desatualizados caso a URL tenha sido
salva/compartilhada.

### Visualizador de log com altura fixa (`calc(100vh - ...)`), não `flex-grow`

A primeira versão usava `flex-grow-1` no `<pre>` dentro de um container
flex-column, esperando que ele preenchesse o espaço vertical restante da
página. Isso não funciona neste shell: `#app-shell`, `.kf-main` e
`.kf-page-content` (ADR 0019) usam `min-height`, não `height` — não
formam uma cadeia de altura limitada. Sem um ancestral de altura
definida, um item flex sem conteúdo próprio de altura fixa só cresce
junto com o conteúdo (o `min-height:auto` default de item flex evita que
ele encolha abaixo do tamanho do conteúdo, mesmo com `overflow-y:auto`
declarado) — confirmado visualmente: com um log de dezenas de linhas, a
página inteira crescia e rolava, o `<pre>` nunca ficava menor que seu
conteúdo, e o auto-scroll interno (`scrollTop = scrollHeight` no próprio
elemento) não tinha efeito nenhum porque o elemento nunca chegava a
estourar sua própria caixa. A correção: `.kf-log-viewer` recebe uma
altura fixa via `calc(100vh - 14rem)` (medida empiricamente a partir do
topbar + padding do `.kf-page-content` + cabeçalho da própria tela),
independente da cadeia de altura dos ancestrais — assim `overflow-y:auto`
tem uma caixa realmente limitada para rolar, e o auto-scroll interno
funciona. Alternativa descartada: tornar `#app-shell`/`.kf-main`/
`.kf-page-content` `height:100%` (em vez de `min-height`) para formar uma
cadeia de altura própria — resolveria de forma mais "correta", mas
mudaria o comportamento de rolagem das telas já existentes (E7.S2/E7.S3),
fora do escopo desta história.

### Sem item de navegação na sidebar

Diferente de `/componentes/index.html` e `/componentes/novo.html`
(destinos gerais de navegação, com entrada em `NAV_ITEMS`), a tela de
logs é contextual a um Componente específico — o acesso é sempre via o
novo link "Logs" na linha correspondente da listagem (E7.S3), não por um
item fixo da sidebar.

## Consequências

- `web/static/componentes/logs.html` (novo): markup da tela — cabeçalho,
  visualizador de log em estilo terminal, botões de auto-scroll/reconexão.
- `web/static/js/componente-logs.js` (novo): busca do cabeçalho,
  `EventSource`, auto-scroll, reconexão manual.
- `web/static/js/componente-lista.js` (modificado): link "Logs" por linha,
  apontando para `logs.html?id=<id>`.
- `web/static/componentes/index.html` (modificado): `<template>` da linha
  ganha o link.
- `web/static/css/app.css` (modificado): `.kf-log-viewer` (visualizador
  estilo terminal) e `.kf-logs-page` (altura da página).
- Nenhuma mudança em `internal/api`, `internal/store` ou `web/embed.go` —
  o endpoint de streaming já existia (E6.S4/ADR 0016) e `//go:embed
  static` já embute qualquer arquivo novo sob `web/static/`.
- `docs/EPICOS_E_HISTORIAS.md`: critérios de aceite de E7.S4 marcados
  como concluídos.
- Fica fora desta história (deferred): fallback sem `EventSource` (não
  pedido pelos critérios de aceite e suportado por todo browser
  evergreen), busca/filtro textual nos logs, download do log, badge
  colorido de fase no cabeçalho (só texto simples, diferente da badge
  colorida da listagem — E7.S3/ADR 0020).
