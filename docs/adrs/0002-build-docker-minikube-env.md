# ADR 0002 — Build via `docker build` local, com env vars do `minikube docker-env` injetadas por subprocesso e logs persistidos em coluna SQLite

**Status:** Aceita
**Data:** 2026-08-19
**Contexto da história:** E3.S2 — Build via `docker build` apontando para o daemon do Minikube

## Contexto

A E3.S1 (`docs/adrs/0001-clonagem-repositorio-git-cli.md`) já entrega o
`CloneResult{Dir, CommitSHA, DockerfilePath}` de um Componente clonado. A
E3.S2 precisa transformar isso em uma imagem disponível ao Minikube, sem
depender de registry (`docs/ARCHITECTURE.md` §7.3, Opção A — decisão já
fechada em favor do daemon Docker interno do Minikube, com Kaniko descartado
do MVP). Três critérios de aceite guiam as decisões abaixo:

1. Injetar automaticamente as env vars equivalentes a `minikube docker-env`
   no processo do build, sem exigir `eval $(minikube docker-env)` manual.
2. Taguear a imagem seguindo `imageTagStrategy` (`commit-sha` como padrão).
3. Persistir o log do build associado à `execution` correspondente.

## Decisão

### 1. Resolução do `docker-env` via `os/exec` chamando o binário `minikube`, com `--shell none`

`internal/build.MinikubeDockerEnv` invoca `minikube -p <profile> docker-env
--shell none` e faz o parsing da saída (`CHAVE=VALOR` por linha, sem
`export`/aspas/comentários) em um `map[string]string`.

Alternativas consideradas:

| Opção | Prós | Contras |
|---|---|---|
| **`minikube` via `os/exec`, flag `--shell none` (escolhida)** | Zero dependências novas; `--shell none` é a própria flag do Minikube para saída script-friendly, sem necessidade de heurística para remover `export`/aspas de um shell específico; mesmo binário `minikube` já exigido no host (`docs/MINIKUBE_SETUP.md`) | Requer `minikube` no PATH do host que roda o binário |
| Ler `~/.minikube/machines/<profile>/config.json` ou variáveis do certificado diretamente | Sem invocar subprocesso | Reimplementa lógica interna do Minikube (versão de API, formatos de certificado) sujeita a mudar entre releases; exatamente o tipo de acoplamento que `docker-env --shell none` já existe para evitar |

Segue a mesma filosofia do ADR 0001: reaproveitar a CLI já assumida no
ambiente do MVP (`docs/ARCHITECTURE.md` §7.2 — binário único rodando fora do
cluster, com `git`/`docker`/`kubectl`/`minikube` disponíveis no host) em vez
de reimplementar em Go o que a própria ferramenta já expõe de forma estável.

### 2. `docker build` via `os/exec` chamando o binário `docker`, com env do subprocesso sobrescrita pelo `docker-env`

`internal/build.DockerBuilder` monta `docker build -t <tag> -f
<DockerfilePath> <diretório do Dockerfile>` e define `cmd.Env` como
`os.Environ()` com as chaves resolvidas por `DockerEnvResolver`
sobrepostas (mesma chave, `exec.Cmd` usa a última ocorrência — dispensa
filtrar duplicatas manualmente).

Alternativas consideradas:

| Opção | Prós | Contras |
|---|---|---|
| **`docker` via `os/exec` (escolhida)** | Zero dependências novas no `go.mod`; comportamento idêntico ao uso manual da CLI, incluindo cache de camadas e mensagens de erro já conhecidas; consistente com a decisão do ADR 0001 para `git` | Requer `docker` no PATH do host |
| SDK Go do Docker (`github.com/docker/docker/client`) | API tipada, sem parsing de texto | Dependência nova pesada só para replicar `docker build -t`; exigiria decidir entre a API de build legada e BuildKit, superfície desnecessária para o único comando usado no MVP |

`DockerEnvResolver` é uma interface (não um detalhe interno de
`DockerBuilder`), no mesmo espírito de `CredentialsResolver` no ADR 0001:
permite trocar a implementação (ex.: uma baseada em registry/Kaniko para
EKS, seção 7.3.1 da arquitetura) sem alterar `DockerBuilder` nem o contrato
de `BuildSpec`.

### 3. Estratégia de tag: só `commit-sha`, 12 caracteres, com erro explícito para as demais

`internal/build.BuildImageTag(repository, strategy, commitSHA)` implementa
apenas `commit-sha` (default quando `strategy` é vazio), gerando
`<repository>:<12 primeiros caracteres do commitSHA>`. O `commitSHA` já é
resolvido pelo `Cloner` (`git rev-parse HEAD`, ver ADR 0001), então não há
nova chamada a `git`.

`timestamp` e `semver` (citadas em `docs/ARCHITECTURE.md:113-114`) retornam
`ErrUnsupportedImageTagStrategy` — mesmo padrão de `ErrUnsupportedRefType`
em `clone.go`: falhar de forma explícita em vez de tentar adivinhar um
comportamento razoável para uma estratégia ainda não implementada, mantendo
o `imageTagStrategy` como ponto de extensão único (o *contrato* já suporta
os três valores; a *implementação* das outras duas fica para quando surgir
necessidade real).

Doze caracteres de SHA (em vez de 7, mais comum em `git describe --short`,
ou do SHA completo de 40) equilibra tag legível/curta com baixo risco de
colisão, sem exigir lógica de abreviação incremental do `git`.

### 4. Log do build: retornado em `BuildResult.Log`, persistido pelo chamador via `ExecutionRepository`

`DockerBuilder.Build` captura stdout+stderr combinados do `docker build` em
um único buffer, devolvido em `BuildResult.Log` — inclusive quando o build
falha (o log parcial não é descartado). `internal/build` **não** depende de
`internal/store`: quem orquestra a chamada decide como e onde persistir o
log, mesma separação de responsabilidades que já existia entre `GitCloner` e
o restante do sistema.

A persistência em si vive em `internal/store`: uma nova tabela `Execution`
ganha as colunas `image_tag` e `build_log` (migration
`0002_add_build_log_and_image_tag_to_executions`), e
`ExecutionRepository.UpdateBuildLog(ctx, id, imageTag, buildLog)` associa o
log à execution correspondente — o requisito literal do critério de aceite
3.

Alternativas consideradas para armazenar o log:

| Opção | Prós | Contras |
|---|---|---|
| **Coluna `TEXT` na tabela `executions` (escolhida)** | Sem infraestrutura nova; consistente com a decisão de usar SQLite como Metadata Store (`docs/ARCHITECTURE.md` §7.2); log e metadados da execution lidos/escritos atomicamente | Log de builds muito grandes infla o arquivo SQLite — aceitável no perfil MVP de uso pessoal, sem volume de build alto |
| Arquivo em disco (`~/.kubeforge/logs/<execution-id>.log`), com só o caminho salvo no banco | Não infla o SQLite | Mais um artefato para gerenciar/limpar (rotação, `cleanup --all` teria que saber sobre ele); nenhuma necessidade real identificada no MVP para justificar a complexidade extra |

`ExecutionRepository.UpdateBuildLog` não altera `Phase`: a máquina de
estados completa (`Pending → Building → Succeeded/Failed`,
`status.buildImageDigest`) é escopo de E3.S3 — misturar as duas
responsabilidades no mesmo método acoplaria uma história à outra sem
necessidade.

### 5. Testes: binários fake em `PATH`, sem introduzir uma abstração de `CommandRunner`

Assim como `GitCloner` não ganhou uma interface de subprocesso genérica no
ADR 0001, `MinikubeDockerEnv` também chama `os/exec` diretamente. Para
testar sem exigir `minikube`/`docker` instalados em CI:

- `MinikubeDockerEnv.Resolve` é testado contra um binário `minikube` fake
  (script shell escrito em `t.TempDir()`, prependado ao `PATH` via
  `t.Setenv`) — verifica os argumentos recebidos e o parsing da saída,
  incluindo o caso de falha (`minikube` não iniciado).
- `DockerBuilder.Build` já recebe `DockerEnvResolver` como interface (uma
  necessidade própria do desenho, não só de teste), então o teste da
  integração com o `docker-env` usa um resolver stub em Go puro. Já a
  chamada ao `docker build` em si é exercitada contra um binário `docker`
  fake, que grava os argumentos e as env vars recebidas em arquivos —
  confirmando que a tag, o Dockerfile, o contexto de build e as env vars
  injetadas chegam corretamente ao subprocesso real.

Alternativa descartada: introduzir uma interface `CommandRunner`/`Executor`
para mockar todo `exec.Command` do pacote. Rejeitada pelo mesmo motivo do
ADR 0001 — nenhuma necessidade de produção para essa abstração hoje, só
adicionaria indireção; os binários fake já testam o comportamento real do
subprocesso (env, args, exit code) sem exigir `docker`/`minikube` instalados.

## Consequências

- `internal/build` ganha mais duas dependências de ambiente (`docker` e
  `minikube` no PATH), somadas à de `git` já assumida no ADR 0001 — todas já
  esperadas pelo perfil "binário único fora do cluster" do MVP
  (`docs/ARCHITECTURE.md` §7.2).
- `internal/store` ganha o domínio `Execution`/`ExecutionRepository`, hoje
  limitado a `Create`/`Get`/`List`/`UpdateBuildLog`; a evolução para a
  máquina de estados completa de `Phase` (E3.S3) deve estender esse
  repositório, não recriá-lo.
- Nenhuma peça desta história está integrada a `cmd/kubeforge/main.go` ou a
  uma API HTTP — ambos continuam inexistentes/placeholder neste ponto do
  projeto. A orquestração ponta-a-ponta (Clone → Build → persistência) fica
  para quando a API existir para acioná-la.
- Migrar a Opção A para a Opção B (Kaniko, `docs/ARCHITECTURE.md` §7.3.1)
  no futuro significa trocar a implementação de `Builder`
  (`internal/build.Builder`) mantendo o mesmo contrato `BuildSpec`/
  `BuildResult` — o mesmo ponto de extensão que `Cloner` já oferece para
  fontes de código diferentes.
