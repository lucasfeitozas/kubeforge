# Setup do Minikube — guia de configuração local

Guia validado nesta máquina de desenvolvimento. Complementa `docs/ARCHITECTURE.md` (§7.2 e §7.3), que definem o perfil MVP: o binário `kubeforge` roda **fora** do cluster, apontando para `~/.kube/config`, e o Build Engine usa o daemon Docker interno do Minikube (`minikube docker-env`).

## Ambiente em que este guia foi validado

| Item | Valor |
|---|---|
| SO | macOS 15.6.1 (Sequoia), Darwin 24.6.0 |
| Arquitetura | arm64 (Apple Silicon) |
| Terminal | iTerm2 |
| Shell de login | **fish** |
| CPU / RAM | 8 CPUs / 8 GB RAM |
| Gerenciador de pacotes | Homebrew |
| Container runtime | Docker Desktop (já instalado e rodando) |

> Se sua máquina usa **bash/zsh** em vez de fish, troque `| source` por `eval $(...)` nos comandos abaixo (é a forma como `docs/ARCHITECTURE.md` já documenta o `docker-env`).

## Pré-requisitos

- [Homebrew](https://brew.sh) instalado.
- [Docker Desktop](https://www.docker.com/products/docker-desktop/) instalado e rodando (`docker ps` deve funcionar sem erro). É o driver usado pelo Minikube nesta máquina — não há Hyperkit/QEMU instalados, e um eventual VirtualBox pode estar quebrado em Apple Silicon (veja Troubleshooting).

## Instalação

```fish
brew install minikube
```

Isso também instala `kubernetes-cli` (kubectl) como dependência, numa versão atual — em fish, `/opt/homebrew/bin` já vem primeiro no `$PATH`, então esse kubectl mais novo passa a ter prioridade automaticamente sobre qualquer kubectl mais antigo instalado em `/usr/local/bin`.

## Configuração e start

```fish
# Fixa o driver docker como default (evita passar --driver toda vez)
minikube config set driver docker

# Sobe o cluster com recursos conservadores — máquinas com 8GB de RAM total
# já têm parte da memória reservada pela VM do Docker Desktop
minikube start --cpus=2 --memory=2200mb --kubernetes-version=stable
```

Se sua máquina tiver mais RAM disponível (16GB+), pode aumentar `--memory` (ex.: `4000mb`) para mais folga.

O `minikube start` cria/atualiza um contexto chamado `minikube` em `~/.kube/config` e já o deixa como `current-context` — não é necessário nenhum passo manual de `kubectl config use-context`.

## Completions no fish

```fish
minikube completion fish > ~/.config/fish/completions/minikube.fish
```

O fish carrega automaticamente qualquer arquivo `.fish` desse diretório na próxima sessão.

## Usando o Docker interno do Minikube (Build Engine)

`docs/ARCHITECTURE.md` §7.3 descreve o build via `eval $(minikube docker-env)` — sintaxe de bash/zsh. Em fish, o equivalente é:

```fish
minikube docker-env --shell fish | source
docker build -t kubeforge/exemplo:local .
```

Isso faz o `docker build` local usar o daemon Docker **de dentro do cluster Minikube**, deixando a imagem disponível para os Pods sem precisar de registry — desde que o manifesto use `imagePullPolicy: Never`.

Para voltar a usar o Docker Desktop normal (fora do Minikube) na mesma sessão de terminal, abra uma nova aba/janela ou rode `eval (minikube docker-env --shell fish -u)`.

## Verificação

```fish
minikube status
kubectl config current-context   # deve retornar "minikube"
minikube kubectl -- get nodes
```

Saída esperada de `minikube status`:
```
minikube
type: Control Plane
host: Running
kubelet: Running
apiserver: Running
kubeconfig: Configured
```

Por fim, validar de ponta a ponta com o próprio binário do projeto (GH-3/E1.S3 — conexão validada no startup):

```fish
cd /Users/lucasfeitozas/Dev/workspace/kubeforge
go run ./cmd/kubeforge
```

Log esperado:
```
level=INFO msg="kubeforge iniciado" http_port=8080 kubeconfig=/Users/lucasfeitozas/.kube/config db_path=./kubeforge.db
level=INFO msg="conectado ao cluster Minikube" kubernetes_version=v1.35.1 platform=linux/arm64
```

## Comandos do dia a dia

```fish
minikube start    # sobe o cluster (se já existir, apenas religa)
minikube stop      # desliga o cluster sem apagar o estado — libera CPU/RAM
minikube delete     # apaga o cluster por completo
```

Em máquinas com RAM limitada (8GB), recomenda-se `minikube stop` quando não estiver desenvolvendo, para liberar memória para o restante do sistema.

## Troubleshooting

- **`VBoxManage` aponta para um app que não existe / VirtualBox quebrado**: se você tiver um resquício de instalação do VirtualBox com o binário `VBoxManage` em `/usr/local/bin` mas sem `VirtualBox.app` em `/Applications`, pode ignorar — o driver usado aqui é `docker`, não `virtualbox`. VirtualBox também tem suporte limitado/experimental a hosts Apple Silicon, então não vale a pena tentar consertar só para o Minikube.
- **kubectl global desatualizado**: se você tiver um `kubectl` antigo em outro caminho do `$PATH` competindo com o instalado pelo Homebrew, prefira `minikube kubectl -- <comando>` — é um binário sempre compatível com a versão do cluster ativo, sem precisar mexer no kubectl global (que pode estar em uso por outro contexto/projeto).
- **Erro de conexão ao rodar `go run ./cmd/kubeforge`**: confira `kubectl config current-context` — o binário exige que o contexto ativo seja exatamente `minikube`. Se estiver diferente, rode `kubectl config use-context minikube` ou simplesmente `minikube start` (que já reativa o contexto correto).
- **Pressão de memória / máquina lenta com o cluster rodando**: reduza `--memory` no `minikube start` (mínimo recomendado pelo Minikube é em torno de 1800mb) ou rode `minikube stop` quando não estiver usando.
