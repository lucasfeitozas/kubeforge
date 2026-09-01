# ADR 0024 — Logs estruturados com `slog` em todos os pacotes e `LOG_LEVEL`

**Status:** Aceita
**Data:** 2026-09-01
**Contexto da história:** E8.S2 — Logs estruturados do binário

## Contexto

A E8.S2 pede logs estruturados "de todas as operações (build, run, cleanup)", com dois critérios
de aceite: "uso consistente de `slog` em todos os pacotes" e "nível de log configurável via
`LOG_LEVEL`".

`log/slog` já era usado no projeto — `cmd/kubeforge/main.go`, `cmd/kubeforge/cleanup.go` e
`internal/api/server.go` já logavam via `slog.Info`/`slog.Error`, com `slog.SetDefault` fixando um
`TextHandler` sem nível configurável (`main.go`, antes desta história). Os pacotes que executam as
operações em si, porém, não logavam nada: `internal/build` (clone + docker build),
`internal/controller` (aplicar/sondar Jobs, cleanup por seletor de label) e `internal/k8s` (obter
clientset) devolviam só erros encadeados via `%w`, sem nenhuma linha de log até o erro borbulhar
até `cmd/`/`internal/api`. `LOG_LEVEL=info` já estava documentado em `.env.example`, antecipando
esta história, mas nada no binário lia essa variável.

## Decisão

### `slog.Default()` global, sem injetar `*slog.Logger` nos structs

`Broker`, `Runner`, `MinikubeProvider` etc. chamam `slog.Info`/`slog.Debug`/`slog.Error`
diretamente (mesmo idioma já usado em `cmd/`/`internal/api`), em vez de ganhar um campo
`Logger *slog.Logger` plumbado por todos os construtores/call sites de teste. Não há necessidade
de múltiplos loggers/destinos distintos neste binário single-process, então o custo de mudar
assinaturas (e todos os testes que já constroem esses structs literalmente) não se paga.

### Dois níveis de detalhe: `Info` para eventos de operação, `Debug` para passos internos

Cada operação (build, run, cleanup) loga em `Info` o início, cada sub-etapa observável
externamente (Job aplicado e sua fase final, recurso removido) e o desfecho
(sucesso/`Error` na falha) — essas linhas por si só já respondem "o que o KubeForge fez e quando"
com `LOG_LEVEL=info` (default). Passos internos que só importam para depurar o próprio binário
(clone concluído com seu commit SHA, clientset k8s obtido, banco SQLite aberto, migrations
aplicadas) usam `Debug`, ficando fora do volume de log padrão. Erros de infraestrutura
(kubeconfig ausente, contexto errado) continuam sem log próprio em `internal/k8s`: o erro
encadeado via `%w` já chega com contexto completo no `Error` logado por quem chama
`GetClientset` (`Runner.fail`, `runCleanupWithSelector`, `cmd/kubeforge/main.go`).

### Falha logada uma única vez, no ponto que decide a fase `Failed`

`Broker.fail` e `Runner.fail`/`finalize` (os únicos pontos que persistem `status.phase=Failed`)
logam `slog.Error` com `component_id`/`execution_id`/o erro — é o único lugar dentro de
`internal/build`/`internal/controller` que loga aquela falha. Os handlers assíncronos em
`internal/api/server.go` (`handleBuildComponent`/`handleRunComponent`) continuam com seu próprio
`slog.Error` na goroutine: mantido deliberadamente, pois adiciona o contexto de que a falha
aconteceu numa goroutine desacoplada da resposta HTTP já enviada — não é o mesmo texto nem
redundante o suficiente para remover.

### `RunCleanup`/`RunComponentCleanup` passam a logar por recurso removido

Antes, só `cmd/kubeforge/cleanup.go` logava `"recurso removido"` por `CleanupResult`, iterando a
lista retornada — o endpoint HTTP (`handleCleanup`/`handleCleanupComponent`) não logava nenhum
sucesso, só erro de auditoria. Mover esse log para dentro de
`runCleanupWithSelector` (`internal/controller/cleanup.go`) cobre os dois call sites com a mesma
linha; o loop equivalente em `cmd/kubeforge/cleanup.go` foi removido para não duplicar a mesma
linha de log quando o cleanup roda via CLI.

### `LOG_LEVEL`: lida depois de `loadDotenv`, mesmo default de `.env.example`

`cmd/kubeforge/main.go` configura o `slog.Default()` duas vezes: um `TextHandler`/`Info`/`stdout`
de bootstrap antes de `loadDotenv()` (para poder logar `slog.Warn` se o `.env` falhar ao carregar),
e de novo logo depois com o nível resolvido por `logLevel()` — que lê `LOG_LEVEL` (já populada
pelo `.env`, se presente) via o mesmo `envString` usado por `loadConfig`. Aceita
`debug`/`info`/`warn`|`warning`/`error`, case-insensitive; ausente ou valor não reconhecido cai
para `info` (mesmo default já documentado em `.env.example`), com um `slog.Warn` avisando do valor
inválido. `runCleanup` (subcomando `cleanup`) roda depois dessa configuração em `main()`, herdando
o mesmo nível sem precisar resolvê-lo de novo. O formato do handler continua texto
(`slog.NewTextHandler`) — não JSON —, mesma escolha já em uso; não há pedido explícito por log
estruturado em JSON, e mudar o formato é uma decisão independente do nível configurável.

## Consequências

- `cmd/kubeforge/main.go`: `logLevel()` (novo) resolve `LOG_LEVEL`; `slog.SetDefault` é chamado
  de novo após `loadDotenv()`; log de startup ganha o campo `log_level`.
- `internal/build/broker.go`: `Broker.Run` loga `Info` no início/clone concluído (`Debug`)/build
  concluída; `Broker.fail` loga `Error`.
- `internal/controller/runner.go`: `Runner.Run` loga `Info` no início e após cada Job (principal e
  de verificação) atingir fase terminal; `Runner.fail`/`finalize` logam `Error` (falha) ou `Info`
  (sucesso).
- `internal/controller/cleanup.go`: `runCleanupWithSelector` loga `Info` no início, por recurso
  removido e ao concluir; `Error` em qualquer list/delete que falhe.
- `internal/k8s/minikube.go`: `GetClientset` loga `Debug` ao obter o clientset com sucesso.
- `internal/store/store.go`: `Open`/`Migrate` logam `Debug`.
- `cmd/kubeforge/cleanup.go`: loop que logava `"recurso removido"` removido (agora logado dentro
  de `internal/controller`, evitando duplicação).
- `docs/EPICOS_E_HISTORIAS.md`: critérios de aceite de E8.S2 marcados como concluídos.
- Fica fora desta história (deferred): handler JSON, nível de log por pacote/override em runtime,
  log estruturado por linha da saída do `docker build` (já persistida inteira em
  `Execution.BuildLog`, sem necessidade de duplicá-la via `slog`).
