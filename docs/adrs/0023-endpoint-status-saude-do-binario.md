# ADR 0023 — Endpoint GET /status: saúde do binário

**Status:** Aceita
**Data:** 2026-08-31
**Contexto da história:** E8.S1 — Endpoint de status/health do binário

## Contexto

Abre o Epic E8 (Observabilidade Mínima). A E8.S1 pede: "GET /status
retornando saúde da conexão com o Minikube e com o SQLite, para diagnosticar
problemas rapidamente", com o critério de aceite "Retorna versão do cluster,
caminho do DB, uptime do processo".

Isso é um conceito diferente do já existente `GET /components/{id}/status`
(`handleStatus`, `internal/api/server.go`), que reporta o status do **Pod**
de um Componente específico (delega a `controller.GetPodStatus`). O novo
endpoint é sobre a saúde do **processo KubeForge em si** — sem relação com
nenhum Componente.

Hoje `api.Server` já recebe um `k8s.ClusterProvider` (`s.clusters`), usado
pelos handlers de execução para obter o clientset do cluster. O `*sql.DB`
bruto, porém, não é passado para `api.Server` — só os repositories
construídos a partir dele (`store.ComponentRepository`,
`store.CleanupAuditRepository`) — e o caminho do arquivo SQLite (`cfg.dbPath`
em `cmd/kubeforge/main.go`) também não chega até lá.

## Decisão

### DTO estruturado por dependência, não um bool único

A resposta separa `cluster` e `database`, cada um com seu próprio
`healthy`/dado relevante/`error`. Um único campo `healthy: bool` agregado
obrigaria o cliente a adivinhar qual das duas dependências falhou —
justamente o oposto de "diagnosticar problemas rapidamente", que é o
objetivo explícito da história.

### 200 se ambos saudáveis, 503 se qualquer um falhar

`GET /status` devolve `503 Service Unavailable` (mantendo o corpo JSON
completo, com o campo `error` da dependência que falhou) sempre que cluster
ou banco estiverem indisponíveis, e `200 OK` caso contrário. Isso permite uso
direto como healthcheck por ferramentas externas (`curl -f`, sondas simples),
seguindo a mesma convenção já usada como exemplo de hook `preRun` no schema
de Componente (`docs/ARCHITECTURE.md`, `curl -f
http://dependencia:8080/health`) — um contrato HTTP que qualquer operador já
reconhece, sem precisar inspecionar o corpo da resposta para saber que algo
está errado.

### Uptime medido a partir da construção do `Server`, não do início do processo

`Server` ganha um campo `startedAt time.Time`, setado via `time.Now()` dentro
de `NewServer`. A alternativa — capturar o instante de início real do
processo (topo de `main()`) e plumbar isso como mais um parâmetro por
`cmd/kubeforge/main.go` e por todos os call-sites de teste — foi descartada:
a diferença entre os dois pontos é o tempo de abrir o SQLite, aplicar
migrations e validar o kubeconfig, tipicamente sub-segundo em uso local, e
não justifica mais um parâmetro numa assinatura que já está longa. "Uptime do
processo" aqui é lido como "uptime do servidor HTTP", suficiente para o
objetivo de diagnóstico da história.

### Reaproveita o `ClusterProvider` já existente; DB ganha dois novos parâmetros em `NewServer`

Para a versão do cluster, o handler chama `s.clusters.GetClientset(ctx,
k8s.MinikubeClusterKey)` seguido de `clientset.Discovery().ServerVersion()`
— o mesmo padrão já usado uma vez no startup (`cmd/kubeforge/main.go`).
`GetClientset` já falha rápido se o kubeconfig estiver ausente ou o contexto
errado, e o `restConfig.Timeout` de 5s (`internal/k8s/minikube.go`) já limita
qualquer chamada de rede feita através do clientset resultante — nenhum
timeout adicional foi necessário no handler.

Para o banco, `NewServer` passa a receber `db *sql.DB` e `dbPath string`
(inseridos antes do já existente `staticFS`). O handler chama
`s.db.PingContext(r.Context())` diretamente — `database/sql` já expõe isso
nativamente, sem necessidade de nenhum wrapper novo em `internal/store`.
`dbPath` existe como parâmetro próprio porque não é derivável do `*sql.DB`
em si.

## Consequências

- `internal/api/server.go`: `Server` ganha os campos `db`, `dbPath`,
  `startedAt`; `NewServer` ganha os parâmetros `db *sql.DB, dbPath string`;
  nova rota `GET /status` e novo handler `handleAppStatus` (nome escolhido
  para não colidir com `handleStatus`, já usado pelo status de Pod por
  Componente).
- `cmd/kubeforge/main.go`: `api.NewServer` passa a receber `db` e
  `cfg.dbPath`.
- `internal/api/server_test.go`: os dois call-sites de `NewServer`
  atualizados (`newTestServer` e `TestHandleLogs_Follow_StreamAteHttptestServer`);
  três novos testes cobrindo 200 (ambos saudáveis), 503 (cluster
  indisponível) e 503 (banco indisponível).
- `internal/api/openapi.yaml`: nova tag `status`, novo path `/status` e novo
  schema `AppStatus`.
- `docs/EPICOS_E_HISTORIAS.md`: critério de aceite de E8.S1 marcado como
  concluído.
- Fica fora desta história (deferred): endpoint `/healthz` ou `/readyz`
  separados (não pedido), métricas Prometheus, e logs estruturados (E8.S2,
  história própria).
