# KubeForge

> Ferramenta pessoal para cadastrar, construir (build) e executar (run) componentes de software parametrizados, a fim de testar/simular comportamentos de um cluster Kubernetes.

Projeto de experimentação individual. Escopo do MVP: **100% local, custo zero**, rodando sobre Minikube.

## Status

🚧 Em desenvolvimento — ver [docs/EPICOS_E_HISTORIAS.md](docs/EPICOS_E_HISTORIAS.md) para o backlog e [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) para a arquitetura completa.

## Visão geral

O KubeForge permite:

1. Cadastrar um **Componente** (repo GitHub, recursos de CPU/memória/armazenamento, envvars, args, hooks pré/pós execução).
2. **Buildar** a imagem a partir do código-fonte usando o Docker daemon do próprio Minikube (`minikube docker-env`) — sem registry externo.
3. **Executar** o componente como um `Job` do Kubernetes, com TTL automático.
4. **Limpar** os recursos automaticamente (TTL nativo) ou sob demanda (`kubeforge cleanup --all`).

## Stack (perfil MVP local)

| Camada | Tecnologia |
|---|---|
| Linguagem | Go |
| Cluster-alvo | Minikube |
| Build Engine | `docker build` via `minikube docker-env` (Opção A) |
| Metadata Store | SQLite |
| Console Web | Estático, embutido no binário (`embed.FS`) |
| K8s Client | `client-go` |

Ver detalhamento completo em [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md), seção 7 (Adendo — Perfil MVP Pessoal).

## Como rodar (quando implementado)

```bash
# pré-requisitos
minikube start
eval $(minikube docker-env)

# rodar o binário (API + Controller + Build Broker + Console Web, tudo em um processo)
go run ./cmd/kubeforge

# console web
open http://localhost:8080
```

## Estrutura do repositório

```
.
├── cmd/kubeforge/        # entrypoint do binário único
├── internal/
│   ├── api/               # handlers HTTP/REST (CRUD de Componente)
│   ├── build/             # Build Broker (Opção A: docker build local)
│   ├── controller/        # reconciler: Componente -> manifesto K8s
│   ├── k8s/                # ClusterProvider, client-go wrappers
│   └── store/              # SQLite (metadata store)
├── web/
│   ├── static/              # assets do console web
│   └── templates/            # templates (se optar por HTMX)
├── deployments/minikube/    # manifestos auxiliares (CRD, RBAC, namespace)
├── docs/                     # arquitetura e backlog
└── scripts/                   # scripts de bootstrap (inclui GitHub)
```

## Licença

Uso pessoal / experimental.
