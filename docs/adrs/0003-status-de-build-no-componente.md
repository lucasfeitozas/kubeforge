# ADR 0003 — Status de build persistido no Componente, com orquestração (`Broker`) fechando o ciclo Clone→Build

**Status:** Aceita
**Data:** 2026-08-20
**Contexto da história:** E3.S3 — Atualização de status de build

## Contexto

As histórias anteriores (ADR 0001, ADR 0002) deixaram duas peças isoladas e
sem chamador em produção: `internal/build.Cloner`/`Builder` (cada um só
exercitado pelo próprio teste) e `ExecutionRepository.UpdateBuildLog`,
persistido mas nunca invocado fora de teste. O comentário original em
`internal/store/execution.go` já apontava essa lacuna: "a máquina de estados
completa (transições, mensagens de falha) é escopo de E3.S3".

A E3.S3 exige que `status.phase`/`status.buildImageDigest` do Componente
reflitam o resultado de um build (`Pending → Building → Built/Failed`), com
falha explícita + mensagem de erro. `status.phase` já está publicado no CRD
(`deployments/minikube/crd.yaml` §status, enum de 7 valores), mas sem
nenhuma implementação em `internal/store` até aqui — `store.Component` só
espelhava o `spec`.

## Decisão

### 1. Escopo: além do Componente, fechar também a máquina de estados da `Execution`

Em vez de só adicionar colunas/método de status ao Componente, esta história
também implementa `ExecutionRepository.UpdatePhase` (`Pending → Running →
Succeeded/Failed`, com `started_at`/`completed_at`) e introduz
`internal/build.Broker`, o primeiro código de produção a encadear
`Cloner.Clone` → `Builder.Build` → persistência.

Alternativa descartada: implementar só `Component.UpdateBuildStatus`, sem
orquestrador nem tocar em `Execution`. Rejeitada porque, sem algo que
efetivamente rode um build e chame a atualização "ao final", o critério de
aceite não é demonstrável ponta a ponta — e deixaria `UpdateBuildLog`
permanentemente morto, adiando para uma história futura hipotética o mesmo
trabalho que o comentário em `execution.go` já atribuía a esta.

`Broker` não é instanciado em `cmd/kubeforge/main.go` nem exposto via API —
quem vai chamá-lo (`POST /components/{id}/build`) é a E6.S2, que ainda não
existe. Este ADR cobre só a peça de orquestração e sua persistência.

### 2. `status.phase` do Componente é um conceito próprio, com enum completo do CRD

`Component` ganha `Phase`, `BuildImageDigest`, `ErrorMessage`, com o enum de
7 valores do CRD (`Pending, Building, Built, Running, Succeeded, Failed,
CleanedUp`) replicado como `CHECK` na migration e como consts Go
(`store.Phase*`), mesmo só `Pending/Building/Built/Failed` sendo usados pela
lógica desta história.

Alternativas consideradas:

| Opção | Prós | Contras |
|---|---|---|
| **Enum completo de uma vez (escolhida)** | Casa exatamente com o contrato já publicado no CRD; evita uma segunda migration (`ALTER TABLE ... CHECK`) quando E4 (Running/Succeeded) e E5 (CleanedUp) forem implementadas | Consts/valores do enum sem uso real até essas histórias existirem |
| Enum restrito a `Pending/Building/Built/Failed` | Migration mínima, sem valores "mortos" | Nova migration necessária em E4/E5 só para ampliar o `CHECK`; risco de o enum do CRD e o do banco divergirem entre commits |

`Component.Phase` é deliberadamente um campo separado de `Execution.Phase`
(que mantém seu enum próprio, `Pending/Running/Succeeded/Failed`, sem
`Building`/`Built`) — um Componente pode ter múltiplas `Execution`s (uma de
build, futuras de run); `status.phase` do Componente é a visão agregada que
o usuário lê, não uma cópia da fase de uma execution específica.

### 3. `status.buildImageDigest` usa o Image ID local do Docker, não um digest de registry

Como o Build Engine do MVP (Opção A, ADR 0002) builda direto no daemon do
Minikube sem `docker push`, não existe um digest de registry real a
capturar. `DockerBuilder.Build` roda `docker image inspect --format
'{{.Id}}' <tag>` logo após um build bem-sucedido (mesmo `cmd.Env` do build,
para apontar ao daemon certo) e usa esse Image ID como
`BuildResult.Digest`.

Alternativas consideradas:

| Opção | Prós | Contras |
|---|---|---|
| **Image ID via `docker image inspect` (escolhida)** | Identificador real, estável, do daemon onde a imagem efetivamente existe; sem infraestrutura nova | Não é um digest OCI de registry (`sha256:` de manifest) — só faz sentido enquanto a Opção A (sem push) for o Build Engine ativo |
| Calcular um digest sintético (hash da tag/commit) | Não depende de subprocesso extra | Não identifica a imagem real no daemon; poderia divergir silenciosamente do que `docker run`/o Execution Engine (E4) realmente usa |

Se `docker image inspect` falhar, `Build` retorna erro (não um `BuildResult`
"de sucesso" com `Digest` vazio) — mantém a garantia de "Failed explícito"
ponta a ponta: uma imagem que não conseguimos identificar não deveria virar
`Built`. Quando a Opção B (Kaniko/registry, `docs/ARCHITECTURE.md` §7.3.1)
for implementada, essa captura muda junto com a troca de `Builder`, sem
afetar o contrato de `BuildResult.Digest` nem o `Broker`.

### 4. `Broker` importa `internal/store` diretamente; sem interfaces de repositório locais a `internal/build`

`Broker{Cloner, Builder, Components store.ComponentRepository, Executions
store.ExecutionRepository}` usa os tipos concretos de `internal/store`
(interfaces já definidas lá), em vez de `internal/build` declarar suas
próprias interfaces menores de persistência.

Justificativa: `internal/store` não importa `internal/build` (sem risco de
ciclo), e `ComponentRepository`/`ExecutionRepository` já são interfaces
enxutas o suficiente para o uso do `Broker` — duplicar um subconjunto delas
em `internal/build` só para "desacoplar" pacotes que já não têm dependência
circular seria indireção sem benefício real, na mesma linha do ADR 0002
(item 5) de evitar abstração sem necessidade de produção.

### 5. Mapeamento `Component.Source`/`Component.Build` (JSON) → `CloneSpec`/`BuildSpec`: structs locais ao `Broker`

`buildCloneSpec`/`buildBuildSpec` fazem `json.Unmarshal` de
`component.Source`/`component.Build` usando structs privadas ao pacote
`build` (`brokerSourceBlock`, `brokerBuildBlock`), no mesmo estilo dos
blocos privados de `internal/store/component_validation.go`, em vez de
reaproveitar/exportar os tipos de validação do `store`.

Alternativa descartada: expor os structs de `component_validation.go`
publicamente para reuso entre `store` e `build`. Rejeitada porque os dois
usos têm propósitos diferentes (validação de enum vs. extração de campos
para montar specs) e cada bloco só lê o subconjunto de campos que
efetivamente precisa — acoplar os dois pacotes a um único tipo JSON
aumentaria o raio de mudança de qualquer ajuste futuro no schema.

## Consequências

- `internal/store` ganha `Component.Phase/BuildImageDigest/ErrorMessage` +
  `UpdateBuildStatus`, e `Execution` ganha `UpdatePhase` — ambos partial
  updates, seguindo o mesmo padrão de `UpdateBuildLog` (target `UPDATE`,
  `RowsAffected() == 0` → not-found).
- `internal/build` ganha uma dependência de `internal/store` (só nessa
  direção) e seu primeiro código de orquestração de produção
  (`Broker.Run`), testado com stubs de `Cloner`/`Builder` contra um SQLite
  real em memória.
- `status.buildImageDigest` hoje é um Image ID local, não um digest de
  registry — documentar isso é importante para quem for consumir esse campo
  via API (E6) ou UI (E7): não é comparável a um digest OCI de outro
  ambiente.
- A integração via HTTP/CLI do `Broker` (quem dispara `POST
  /components/{id}/build`) permanece em aberto para E6.S2 — nenhuma mudança
  em `cmd/kubeforge/main.go`, `internal/api` ou `internal/controller` nesta
  história.
