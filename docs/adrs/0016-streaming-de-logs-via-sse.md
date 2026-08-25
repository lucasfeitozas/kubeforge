# ADR 0016 — Streaming de logs via Server-Sent Events, framing por chunk (não por linha acumulada)

**Status:** Aceita
**Data:** 2026-08-25
**Contexto da história:** E6.S4 — Endpoint de logs

## Contexto

A E6.S4 pede que `GET /components/{id}/logs` (já existente desde E4.S4)
ofereça streaming via Server-Sent Events (SSE) ou WebSocket, com fallback
para retorno estático (últimas N linhas) quando o client não suporta
streaming. A [ADR 0008](0008-status-e-logs-http-poll.md) já havia
implementado o `follow=true`/`tailLines=N` atuais, mas considerou e
**rejeitou explicitamente** o framing SSE na época, por "não ter nenhum
consumidor no projeto hoje" — decisão tomada quando `internal/api` ainda
não tinha nenhum handler HTTP. `docs/ARCHITECTURE.md` (§1, fluxo 7) já
previa "UI consome via streaming (SSE/WebSocket) ou polling" desde a
arquitetura original; esta história cumpre essa parte que ficou deliberada
e explicitamente adiada.

## Decisão

### SSE, não WebSocket

`controller.StreamPodLogs` (ADR 0008) já faz todo o trabalho de produzir
logs incrementalmente via polling sobre `GetLogs` — falta só formatar a
saída. SSE é só um framing de texto sobre a mesma conexão HTTP simples já
em uso (`text/event-stream`, linhas `data: ...` terminadas em linha em
branco): nenhuma dependência nova, nenhuma mudança de protocolo, e
continua funcionando com `curl` (o texto SSE é perfeitamente legível cru).
WebSocket exigiria uma biblioteca nova (nenhuma dependência de
websocket existe em `go.mod` hoje) e um handshake/protocolo full-duplex que
este endpoint — só leitura, servidor→cliente — não precisa. Como o
critério de aceite pede "SSE **ou** WebSocket", SSE sozinho já satisfaz.

Isso supera a rejeição de SSE da ADR 0008: o motivo daquela rejeição
("nenhum consumidor no projeto hoje") deixou de valer — E6.S4 é
precisamente a história que introduz esse consumidor.

### Framing por chunk de `Write`, não por linha acumulada entre chamadas

`sseWriter` (`internal/api/server.go`) transforma cada chamada de `Write`
recebida de `controller.StreamPodLogs` em **um evento SSE completo e
imediato** — sem reter bytes de uma chamada para a próxima à espera de um
`\n` que feche a linha. A alternativa óbvia (acumular um buffer entre
chamadas e só emitir ao encontrar `\n`) foi descartada porque não é só uma
simplificação: ela quebraria com dados reais. O fixture `"fake logs"` do
`k8s.io/client-go/kubernetes/fake` (usado em todos os testes deste
projeto) nunca termina em `\n`, e um `docker build`/log de container real
também pode legitimamente entregar um chunk parcial sem newline final no
meio de uma escrita — um design que dependesse de `\n` para fechar o evento
ficaria com esse conteúdo preso no buffer indefinidamente.

### `follow=false` já era o fallback estático — nenhuma mudança

O critério de aceite "fallback para retorno estático (últimas N linhas) se
streaming não for suportado" já era exatamente o que `follow=false`
(default, com `?tailLines=N` opcional) fazia desde a ADR 0008: uma única
resposta em texto plano, sem streaming, servida em qualquer client HTTP.
Não foi criado nenhum mecanismo de negociação de conteúdo (`Accept` header
etc.) — o parâmetro `follow` já era essa escolha explícita do cliente.

## Consequências

- `internal/api/server.go`: novo tipo `sseWriter`; `trackingWriter.w` muda
  de `http.ResponseWriter` para `io.Writer` (único ajuste estrutural, para
  poder envolver um `*sseWriter`); `handleLogs` define
  `Content-Type: text/event-stream` + `Cache-Control: no-cache` só quando
  `follow=true` — `follow=false` continua com `text/plain; charset=utf-8`
  inalterado.
- `internal/controller/status.go`: nenhuma mudança — `StreamPodLogs`/
  `fetchLogs` continuam produzindo bytes de log crus, sem saber que existe
  HTTP ou SSE, preservando a separação de responsabilidades estabelecida
  pela ADR 0008.
- `docs/adrs/0008-status-e-logs-http-poll.md`: nota adicionada marcando a
  seção "Transporte HTTP: texto plano chunked, sem framing SSE" como
  superada por esta ADR.
- `docs/EPICOS_E_HISTORIAS.md`: critérios de aceite de E6.S4 marcados como
  concluídos.
- Fica fora desta história (deferred): `id:`/`retry:` (reconexão nativa de
  SSE) — o cursor de continuidade já é o parâmetro `tailLines`/`SinceTime`
  interno, não algo que o protocolo SSE precise gerenciar sozinho aqui;
  WebSocket como alternativa, caso uma história futura precise de
  comunicação bidirecional (não é o caso de logs, só leitura).
