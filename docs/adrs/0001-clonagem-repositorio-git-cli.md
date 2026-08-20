# ADR 0001 — Clonagem do repositório via binário `git` do host, com credenciais por env var local

**Status:** Aceita
**Data:** 2026-08-19
**Contexto da história:** E3.S1 — Clonagem do repositório (branch/tag/commit)

## Contexto

O Build Broker precisa obter o código-fonte de um Componente antes de buildar
sua imagem, a partir de `source.repoUrl` e `source.ref` (`docs/ARCHITECTURE.md`
§2.2). A história E3.S1 exige:

- Clone raso (`--depth=1`) quando `ref.type` é `branch` ou `tag`.
- Checkout de um commit específico quando `ref.type` é `commit`.
- Suporte a repositório privado via `source.credentialsSecretRef`.
- Validação de que o repositório (ou o `subPath`, em caso de monorepo) contém
  um `Dockerfile`, pré-requisito do Build Engine (Opção A — seção 7.3 da
  arquitetura).

O MVP roda como binário único, fora do cluster, sem fila de eventos nem
acesso a um cofre de segredos (seção 7.2 da arquitetura). Isso reduz as
opções realistas de implementação e de resolução de credenciais.

## Decisão

### 1. Implementação do clone via `os/exec` chamando o binário `git` do host

`internal/build.GitCloner` invoca o binário `git` já exigido no ambiente de
desenvolvimento (ver `docs/MINIKUBE_SETUP.md`), em vez de adicionar uma
biblioteca Git pura em Go (ex.: `go-git`).

Alternativas consideradas:

| Opção | Prós | Contras |
|---|---|---|
| **`git` via `os/exec` (escolhida)** | Zero dependências novas no `go.mod`; comportamento idêntico ao `git` usado manualmente (mesmas flags, mesmo suporte a protocolos/autenticação); mensagens de erro do próprio git, já conhecidas por quem opera a ferramenta | Requer `git` instalado no PATH do host que roda o binário |
| `go-git` (biblioteca pura Go) | Sem dependência de binário externo | Suporte a shallow clone historicamente mais limitado/instável que o `git` oficial; dependência nova só para reimplementar o que o `git` já faz melhor; superfície de bugs de compatibilidade de protocolo |

Como o MVP já assume `git`/`docker`/`kubectl` no host (seção 7.2 —
"binário único... rodando fora do cluster"), adicionar essa dependência de
ambiente não introduz complexidade nova.

### 2. Estratégia de clone por tipo de ref

- `branch`/`tag`: `git clone --depth=1 --branch <value> <repoUrl> <destDir>`.
  Um único comando cobre os dois casos, já que `--branch` aceita tanto nomes
  de branch quanto de tag.
- `commit`: `git clone <repoUrl> <destDir>` (histórico completo) seguido de
  `git checkout <value>`. Um clone raso não pode ser usado aqui: um clone com
  `--depth=1` só traz o histórico alcançável a partir da ref pedida no
  próprio clone, e não há como saber de antemão qual branch contém um SHA
  arbitrário. A história não exige clone raso para `ref.type=commit` — só
  para `branch`/`tag`.

### 3. Credenciais de repositório privado: token via env var local, injetado na URL HTTPS

`source.credentialsSecretRef` é resolvido por `EnvCredentialsResolver`, que
lê a env var `KUBEFORGE_GIT_TOKEN_<SECRETREF_EM_MAIUSCULAS>` (mesma
convenção `KUBEFORGE_*` já usada em `cmd/kubeforge/main.go`). O token é
embutido na URL como usuário HTTP básico
(`https://x-access-token:<token>@host/...`), convenção suportada por
GitHub/GitLab/Bitbucket.

Alternativas consideradas:

- **Kubernetes Secret real** (o que `credentialsSecretRef` sugere no nome):
  descartado porque o Build Broker roda **fora** do cluster no MVP (seção
  7.2), então não haveria um Secret do namespace de trabalho para ler sem
  antes implementar um caminho de leitura via `client-go` só para isso — fora
  do escopo desta história, e sem necessidade real com um único usuário
  local.
- **Arquivo de credenciais dedicado** (ex.: `~/.kubeforge/credentials.json`):
  mais um formato de configuração para gerenciar; env var já é o padrão do
  projeto para segredos locais (`.env` via `KUBEFORGE_*`).

`CredentialsResolver` é uma interface (não um detalhe interno do
`GitCloner`), justamente para permitir trocar essa implementação por uma
baseada em Kubernetes Secret/cofre externo no futuro, sem alterar o
`GitCloner` nem o contrato de `CloneSpec`.

**Limitação assumida:** só HTTPS é suportado para repositórios privados
(erro explícito se `repoUrl` usa outro scheme com `credentialsSecretRef`
definido). Autenticação via SSH (chave privada) fica fora do escopo do MVP.

### 4. Validação de Dockerfile após o clone

`GitCloner.Clone` valida a presença de `Dockerfile` em `destDir` (ou
`destDir/subPath`, se `source.subPath` estiver definido) antes de retornar
sucesso, retornando `ErrDockerfileNotFound` caso contrário. Isso falha o
clone cedo, antes de a execução chegar ao Build Engine (E3.S2), que assume
`build.strategy: local-docker` com um `Dockerfile` na raiz do contexto de
build (seção 7.3).

## Consequências

- `internal/build` ganha uma dependência de ambiente (`git` no PATH), sem
  dependências novas no `go.mod`.
- Repositórios privados só funcionam via HTTPS + token; suporte a SSH, se
  necessário no futuro, exigirá uma nova decisão (provavelmente um
  `known_hosts`/chave gerenciados via outro `CredentialsResolver`).
- A resolução de credenciais por env var local é adequada ao perfil MVP
  pessoal (seção 7 da arquitetura), mas não escala para multi-tenant; a
  interface `CredentialsResolver` existe para tornar essa evolução um ponto
  de extensão, não uma reescrita.
