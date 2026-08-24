# ADR 0008 — Status/logs do Pod via `net/http` puro, follow por polling sobre `GetLogs`

**Status:** Aceita
**Data:** 2026-08-21
**Contexto da história:** E4.S4 — Acompanhamento de status e logs

## Contexto

A E4.S4 pede que o usuário consulte o status (`Pending/Running/Succeeded/Failed`) e os
logs do Pod em execução sem usar `kubectl` diretamente, com dois critérios de aceite: um
endpoint/consulta que reflete o status atual do Job/Pod, e stream ou tail de logs
disponível via API.

Até esta história, `internal/api` continha só `doc.go` — nenhum handler HTTP existia no
projeto, e `cmd/kubeforge/main.go` conectava ao cluster no startup só para checar
`ServerVersion()` e encerrava em seguida (`KUBEFORGE_HTTP_PORT` já é lido de
`.env.example`/`loadConfig`, mas nunca foi usado para subir um servidor). O ADR 0007 já
apontava essa lacuna como "fora desta história" ao concluir a E4.S3, junto de
"streaming de logs" — ambos ficam resolvidos aqui.

Dois obstáculos concretos, específicos desta história, vieram do que já existe no projeto:

1. **`k8s.MinikubeProvider.GetClientset` fixa `restConfig.Timeout = 5s`**
   (`internal/k8s/minikube.go`) sobre *todas* as chamadas feitas através do clientset
   devolvido — não é um timeout por chamada via `context.Context`, é
   `http.Client.Timeout`, que corta a leitura da resposta inteira, inclusive um
   `io.ReadCloser` de streaming. O ADR 0007 já rejeitou usar `watch.Interface` para sondar
   Jobs por esse motivo exato. `PodLogOptions.Follow: true` tem a mesma natureza — uma
   conexão HTTP que fica aberta indefinidamente — e seria cortada aos 5s.
2. **`hooks.postRun` roda como um segundo Job** (`internal/controller/job_builder.go`,
   `BuildPostRunJob`, ADR 0007), então "o Pod em execução" citado no critério de aceite é
   ambíguo entre o Job principal e o de verificação. A história fala de "acompanhar o
   teste", no singular, alinhado ao Job principal — o mesmo que já é `component.ID` em
   `BuildJob`.

O idioma síncrono do projeto (`internal/build.Broker`, `internal/controller.Runner`) não
usa filas nem callbacks assíncronos — reforça que a solução aqui também deve ser uma
chamada HTTP direta, não um mecanismo de pub/sub como o `docs/ARCHITECTURE.md` (§1, fluxo
7) sugere para UI consumir via "streaming (SSE/WebSocket) ou polling".

## Decisão

### Novo pacote de lógica em `internal/controller/status.go`, não em `internal/api`

`GetPodStatus` e `StreamPodLogs` ficam em `internal/controller`, ao lado de `Runner` e
`BuildJob`: ambos precisam da mesma interpretação de `spec.targetContext`
(`parseTargetContext`, já privada ao pacote) e do mesmo `k8s.ClusterProvider` usados por
`Runner.Run`. `internal/api` fica só com tradução HTTP (parsing de query string,
`json.Encoder`, mapeamento de erro para status code) — o mesmo particionamento sugerido
pela arquitetura original (API orquestra, Controller fala com o cluster), aplicado agora
pela primeira vez.

Só o Job principal é observado (`component.ID`, não `component.ID + "-postrun"`): resolve a
ambiguidade do parágrafo anterior sem introduzir um parâmetro `?job=` que nenhum critério
de aceite pede.

### Localização do Pod: lista por rótulo `batchv1.JobNameLabel`, usa o mais recente

Nem `Execution` nem `Component` guardam o nome do Pod (só o nome do Job é determinístico —
`component.ID`) — o nome do Pod é gerado pelo controller do Job no cluster. `GetPodStatus`/
`StreamPodLogs` listam `Pods(namespace)` com `LabelSelector: batchv1.JobNameLabel=<jobName>`
(rótulo `batch.kubernetes.io/job-name`, aplicado nativamente a todo Pod criado por um Job,
sem exigir que `BuildJob` declare nada a mais) e escolhem o de maior
`CreationTimestamp`. Um Job com `backoffLimit > 0` pode ter mais de um Pod ao longo de
retries; o mais recente é o que reflete o estado atual — os demais são histórico que este
endpoint não expõe.

`ErrPodNotFound` (novo, `internal/controller`) cobre o caso de nenhum Pod existir ainda
(`Runner.Run` nunca chamado para o componente, ou o Job já foi limpo) e vira HTTP 404, ao
lado do já existente `store.ErrComponentNotFound`.

### Status é a projeção direta de `corev1.PodStatus.Phase`, sem reinterpretação

`PodStatus.Phase` devolve `string(pod.Status.Phase)` sem tradução: `PodPending`,
`PodRunning`, `PodSucceeded`, `PodFailed` do client-go já usam exatamente o vocabulário do
critério de aceite (`Pending/Running/Succeeded/Failed`), mais `Unknown` (nativo do
Kubernetes, sem equivalente pedido explicitamente, mas incorreto suprimir). Inventar um
mapeamento aqui só adicionaria uma camada de tradução sem necessidade — `Component.Phase`
(persistido, agregado por `Runner.finalize`) e `PodStatus.Phase` (ao vivo, direto do Pod)
respondem perguntas diferentes e deliberadamente não são unificados nesta história.

### Logs "follow" por polling sobre `GetLogs`, não `PodLogOptions.Follow`

Em vez de abrir uma única chamada de streaming de longa duração (cortada aos 5s pelo
`restConfig.Timeout`, problema descrito no Contexto), `StreamPodLogs` com `follow=true`
repete chamadas curtas e não-streaming a `Pods(ns).GetLogs(pod, opts).Stream(ctx)` a cada
`pollInterval` (default 2s, mesmo valor de `defaultPollInterval` do `Runner`), usando
`PodLogOptions.SinceTime` como cursor para não reenviar linhas já escritas, até
`pod.Status.Phase` virar `Succeeded`/`Failed` ou o `context.Context` da requisição HTTP ser
cancelado (cliente desconectou). Mesma escolha de "polling sobre `status`, não watch/stream
nativo" já feita pelo `Runner.applyAndWait` na ADR 0007, pela mesma razão de
infraestrutura — nenhuma mudança em `k8s.MinikubeProvider` foi necessária.

Efeito colateral aceito: a granularidade de 1s de `SinceTime` pode reenviar até ~1s de
linhas já mostradas na leitura seguinte (mesma limitação de `kubectl logs -f
--since-time`). Sem impacto no critério de aceite, que pede acompanhar o log, não
exactly-once.

Como o critério de aceite aceita "Stream **ou** tail" (não exige as duas coisas), `follow`
é opcional (`?follow=true`, default `false`): sem ele, a resposta é só o snapshot atual dos
logs (com `?tailLines=N` opcional), uma única chamada, sem polling.

### Transporte HTTP: texto plano chunked, sem framing SSE

Com `follow=true`, o handler não define `Content-Length`, ativa `http.Flusher` a cada
escrita (`trackingWriter`, `internal/api/server.go`) e deixa o Go `net/http` responder com
`Transfer-Encoding: chunked` — funciona com `curl` puro, sem cliente HTTP com suporte a
Server-Sent Events, consistente com "acompanhar o teste sem usar kubectl diretamente": o
usuário troca `kubectl logs -f` por `curl .../logs?follow=true`. Um framing SSE
(`text/event-stream`, `data: ...`) foi considerado e descartado por não ter nenhum consumidor
no projeto hoje (`internal/api` não tinha nenhum endpoint antes desta história) — adicionar
esse formato sem necessidade concreta contradiria o restante do projeto, que evita
abstrações sem uso imediato (ver ADRs anteriores).

### Roteamento HTTP: `net/http.ServeMux` da stdlib (Go 1.22+), sem framework novo

`go.mod` já é enxuto — nenhuma dependência de roteamento HTTP existe hoje, e o projeto está
em Go 1.23 (`go.mod`), que suporta padrões com método e variáveis de path
(`"GET /components/{id}/status"`) nativamente desde 1.22. Introduzir `chi`/`gorilla/mux`
para duas rotas não se justifica; o padrão nativo já cobre o que é preciso
(`r.PathValue("id")`).

### `cmd/kubeforge/main.go` passa a subir um `http.Server` de verdade, com shutdown gracioso

Antes desta história, `main()` conectava ao cluster e retornava — o processo não fazia
nada além de validar conectividade no startup. Agora, depois da checagem de conectividade,
`main()` sobe `http.Server{Addr: fmt.Sprintf(":%d", cfg.httpPort), Handler:
api.NewServer(...)}` em uma goroutine, e bloqueia esperando `SIGINT`/`SIGTERM`
(`signal.NotifyContext`) ou uma falha do `ListenAndServe`, encerrando com
`http.Server.Shutdown` (timeout de 5s) — necessário para que conexões de log em
`follow=true` tenham chance de ser fechadas de forma limpa em vez de abortadas.

## Consequências

- Novo arquivo `internal/controller/status.go`: `PodStatus`, `ErrPodNotFound`,
  `GetPodStatus`, `StreamPodLogs`, e os helpers privados `locateComponentJob`,
  `latestJobPod`, `fetchLogs`, `podTerminal`. Novo `internal/controller/status_test.go`,
  mesmo padrão de `fake.NewSimpleClientset()`/`PrependReactor` de `runner_test.go`.
- `internal/api/doc.go` deixa de ser o único arquivo do pacote: novo `server.go` com
  `Server`, `NewServer`, `GET /components/{id}/status` e `GET /components/{id}/logs`; novo
  `server_test.go` via `httptest`. CRUD de Componente e as ações build/run/cleanup
  continuam sem handler — só status/logs foram implementados.
- `cmd/kubeforge/main.go` ganha o primeiro `http.Server` do projeto, com desligamento
  gracioso; `KUBEFORGE_HTTP_PORT` passa a ser efetivamente usado.
- `k8s.MinikubeProvider`/`ClusterProvider` não mudaram — o `restConfig.Timeout = 5s`
  continua valendo para toda chamada individual do polling (cada uma é curta o bastante
  para nunca se aproximar do limite), só não é mais usado para uma única chamada de
  streaming de longa duração.
- Fica fora desta história (deferred): status/logs do Job de verificação
  (`hooks.postRun`); histórico de Pods de tentativas anteriores (só o mais recente é
  exposto); paginação/limite de tamanho de resposta para `tailLines` omitido (log muito
  grande é devolvido inteiro); autenticação/autorização do servidor HTTP (nenhuma story do
  MVP pediu isso ainda); CRUD de Componente via API (`internal/api` continua sem esses
  handlers).
