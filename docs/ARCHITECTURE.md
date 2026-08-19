# KubeForge — Documento de Arquitetura e Escopo Técnico

**Versão:** 1.0
**Status:** Proposta para revisão
**Autor:** Arquitetura de Plataforma
**Data:** Agosto/2026

---

## 0. Nome do Projeto

**Nome sugerido: `KubeForge`**
Transmite a ideia de "forjar" workloads (build + execução) dentro do ecossistema Kubernetes. Curto, memorável e sem colisão óbvia com produtos conhecidos do ecossistema CNCF.

**Alternativas:**
1. **`PodForge`** — mais específico ao objeto final de execução (Pod/Job), reforça o caráter de "criação sob demanda".
2. **`ClusterBench`** — remete a "bancada de testes" (bench) para o cluster, adequado se o foco de marketing interno for experimentação/benchmark, não apenas build.

Seguirei usando **KubeForge** no restante do documento.

---

## 1. Visão Geral da Arquitetura

### 1.1 Contexto (C4 Nível 1 — resumo)

O KubeForge é uma plataforma interna, acessada por engenheiros/SREs, que orquestra o ciclo **cadastro → build → execução → observação → teardown** de componentes de teste dentro de um cluster Kubernetes (Minikube local ou EKS em nuvem), usando o próprio cluster como motor de build e execução — sem depender de infraestrutura externa de CI.

### 1.2 Diagrama de Contêineres (C4 Nível 2)

```mermaid
C4Container
    title KubeForge - Diagrama de Contêineres (C4 N2)

    Person(user, "Engenheiro/SRE", "Cadastra e executa componentes de teste")

    System_Boundary(kubeforge, "KubeForge Platform") {
        Container(api, "API/Backend", "Go ou Python", "REST/gRPC. Orquestra ciclo de vida dos componentes, expõe CRUD e ações build/run/teardown")
        Container(ui, "Console Web", "React/Next.js", "UI para cadastro, acompanhamento de builds e execuções, logs em tempo real")
        Container(controller, "KubeForge Controller/Operator", "Go (client-go / controller-runtime)", "Reconcilia CRDs KubeForgeComponent, cria Jobs/Pods/Deployments")
        ContainerDb(db, "Metadata Store", "PostgreSQL", "Componentes, histórico de execuções, parâmetros, status")
        Container(queue, "Fila de Eventos", "NATS/Redis Streams", "Desacopla API de ações assíncronas (build/run/cleanup)")
        Container(registry_client, "Build Broker", "Go", "Prepara contexto de build, dispara Job de build (Kaniko/Buildpacks)")
    }

    System_Boundary(k8s, "Cluster Kubernetes (Minikube | EKS)") {
        Container(build_job, "Build Job", "Kaniko Pod", "Constrói imagem a partir do código-fonte, sem privilégio Docker")
        Container(exec_workload, "Workload de Execução", "Pod/Job/Deployment", "Componente sob teste, com recursos e envs configurados")
        ContainerDb(pvc, "PersistentVolumeClaim", "EBS (EKS) / hostPath (Minikube)", "Armazenamento efêmero/persistente do componente")
    }

    System_Ext(git, "GitHub", "Repositório de código-fonte")
    System_Ext(ecr, "Registro de Imagens", "ECR (EKS) / Registro local (Minikube)")

    Rel(user, ui, "Usa", "HTTPS")
    Rel(ui, api, "Chama", "REST/gRPC")
    Rel(api, db, "Lê/Grava", "SQL")
    Rel(api, queue, "Publica eventos", "AMQP/NATS")
    Rel(queue, registry_client, "Consome evento de build")
    Rel(queue, controller, "Consome evento de run/teardown")
    Rel(registry_client, git, "Clona código", "HTTPS/SSH")
    Rel(registry_client, build_job, "Cria Job (K8s API)")
    Rel(build_job, ecr, "Push da imagem")
    Rel(controller, k8s, "Cria/observa recursos", "K8s API (client-go)")
    Rel(controller, exec_workload, "Gerencia ciclo de vida")
    Rel(exec_workload, pvc, "Monta volume")
    Rel(exec_workload, ecr, "Pull da imagem")
```

### 1.3 Fluxo de Dados (ponta a ponta)

1. **Cadastro** — usuário registra um `Componente` via UI/API (repo GitHub, branch/tag/commit, recursos, envvars, args, hooks).
2. **Solicitação de Build** — API persiste o componente e publica evento `build.requested` na fila.
3. **Build Broker** consome o evento, clona o snippet de contexto (repo + ref), gera um `BuildJob` (Kaniko) no cluster, escreve a imagem no registry (ECR ou registry local do Minikube).
4. **Callback de Build** — o Job de build atualiza status via webhook/observação do Controller; API grava `status=BUILT` e a digest da imagem.
5. **Solicitação de Execução** — usuário aciona "Run"; API publica `run.requested`.
6. **Controller/Operator** reconcilia o CRD `KubeForgeComponent`, traduz a especificação em manifesto nativo (Pod, Job ou Deployment) e aplica via `client-go`, no contexto (kubeconfig) apropriado — Minikube ou EKS.
7. **Observação** — Controller acompanha eventos do Pod (Running, CrashLoop, Completed) e replica status/logs para o Metadata Store; UI consome via streaming (SSE/WebSocket) ou polling.
8. **Hooks pré/pós** — Init Containers (pré) e Jobs de verificação (pós) executam scripts definidos pelo usuário.
9. **Teardown** — ao término (TTL, sucesso, falha ou ação manual), o Controller aciona a rotina de limpeza (ver seção 5).

---

## 2. Modelo de Dados / Entidades

A entidade central é o **Componente**. Abaixo, o esquema representado tanto como **Custom Resource Definition (CRD) do Kubernetes** (fonte de verdade operacional) quanto como **schema JSON** (usado pela API/Metadata Store).

### 2.1 Especificação como CRD (YAML)

```yaml
apiVersion: kubeforge.io/v1alpha1
kind: KubeForgeComponent
metadata:
  name: exemplo-carga-cpu
  namespace: kubeforge-workloads
  labels:
    kubeforge.io/owner: "time-plataforma"
    kubeforge.io/environment: "eks"
spec:
  source:
    repoUrl: "https://github.com/org/carga-cpu-teste"
    ref:
      type: branch          # branch | tag | commit
      value: "main"
    subPath: "/"             # opcional, monorepo
    credentialsSecretRef: "github-readonly-token"  # opcional, repos privados

  build:
    strategy: kaniko          # kaniko | buildpacks | doib
    dockerfilePath: "Dockerfile"
    cacheEnabled: true
    imageRepository: "123456789.dkr.ecr.us-east-1.amazonaws.com/kubeforge/carga-cpu"
    imageTagStrategy: "commit-sha"   # commit-sha | timestamp | semver

  resources:
    requests:
      cpu: "250m"
      memory: "256Mi"
    limits:
      cpu: "1"
      memory: "512Mi"
    storage:
      type: ephemeral         # ephemeral | pvc
      sizeLimit: "1Gi"
      pvc:
        storageClassName: "gp3"     # ignorado se type=ephemeral
        accessModes: ["ReadWriteOnce"]
        size: "5Gi"

  runtime:
    workloadKind: Job          # Pod | Job | Deployment
    replicas: 1                 # aplicável a Deployment
    command: ["/app/start.sh"]
    args: ["--mode=stress"]
    env:
      - name: LOG_LEVEL
        value: "debug"
      - name: DURATION_SECONDS
        value: "300"
      - name: EXTRA_SECRET
        valueFrom:
          secretKeyRef:
            name: teste-secret
            key: token
    restartPolicy: Never        # aplicável a Pod/Job

  hooks:
    preRun:
      - name: warmup-check
        image: "curlimages/curl:8.9.0"
        command: ["sh", "-c", "curl -f http://dependencia:8080/health"]
    postRun:
      - name: verificacao-resultado
        image: "org/verificador:latest"
        command: ["/verify.sh"]
        continueOnError: false

  targetContext:
    cluster: "eks"              # minikube | eks
    kubeconfigSecretRef: "kubeforge-eks-kubeconfig"  # obrigatório se cluster=eks
    namespace: "kubeforge-workloads"

  lifecycle:
    ttlSecondsAfterFinished: 3600
    activeDeadlineSeconds: 1800
    cleanupPolicy: onSuccessAndFailure   # onSuccess | onFailure | onSuccessAndFailure | manual

status:
  phase: "Running"              # Pending | Building | Built | Running | Succeeded | Failed | CleanedUp
  buildImageDigest: "sha256:abc123..."
  startTime: "2026-08-18T14:00:00Z"
  completionTime: null
  conditions:
    - type: BuildComplete
      status: "True"
      lastTransitionTime: "2026-08-18T13:58:12Z"
```

### 2.2 Schema JSON (contrato de API — resumido)

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "Componente",
  "type": "object",
  "required": ["nome", "source", "resources", "runtime", "targetContext"],
  "properties": {
    "id": { "type": "string", "format": "uuid" },
    "nome": { "type": "string" },
    "descricao": { "type": "string" },
    "source": {
      "type": "object",
      "required": ["repoUrl", "ref"],
      "properties": {
        "repoUrl": { "type": "string", "format": "uri" },
        "ref": {
          "type": "object",
          "properties": {
            "type": { "enum": ["branch", "tag", "commit"] },
            "value": { "type": "string" }
          }
        },
        "credentialsSecretRef": { "type": "string" }
      }
    },
    "build": {
      "type": "object",
      "properties": {
        "strategy": { "enum": ["kaniko", "buildpacks", "doib"] },
        "dockerfilePath": { "type": "string" },
        "cacheEnabled": { "type": "boolean" }
      }
    },
    "resources": {
      "type": "object",
      "properties": {
        "requests": { "type": "object" },
        "limits": { "type": "object" },
        "storage": {
          "type": "object",
          "properties": {
            "type": { "enum": ["ephemeral", "pvc"] },
            "sizeLimit": { "type": "string" }
          }
        }
      }
    },
    "runtime": {
      "type": "object",
      "properties": {
        "workloadKind": { "enum": ["Pod", "Job", "Deployment"] },
        "env": { "type": "array" },
        "args": { "type": "array" },
        "command": { "type": "array" },
        "logLevel": { "type": "string" }
      }
    },
    "hooks": {
      "type": "object",
      "properties": {
        "preRun": { "type": "array" },
        "postRun": { "type": "array" }
      }
    },
    "targetContext": {
      "type": "object",
      "required": ["cluster"],
      "properties": {
        "cluster": { "enum": ["minikube", "eks"] },
        "namespace": { "type": "string" }
      }
    },
    "lifecycle": {
      "type": "object",
      "properties": {
        "ttlSecondsAfterFinished": { "type": "integer" },
        "cleanupPolicy": { "enum": ["onSuccess", "onFailure", "onSuccessAndFailure", "manual"] }
      }
    }
  }
}
```

> **Decisão de design:** o CRD é a fonte de verdade **operacional** (o que está de fato rodando no cluster); o Metadata Store (PostgreSQL) é a fonte de verdade **histórica/analítica** (auditoria, métricas de uso, versionamento de configurações). O Controller sincroniza status do CRD de volta para o banco.

---

## 3. Estratégia de Integração com o Kubernetes

### 3.1 K8s Client SDK

- **Linguagem do Controller:** Go, usando `client-go` + `controller-runtime` (padrão operator-sdk/kubebuilder). É a escolha natural por ser a mesma stack do ecossistema Kubernetes, com suporte maduro a informers, work queues e reconciliation loops.
- Caso o backend principal seja em outra linguagem (ex.: Python/FastAPI para a API REST), o **Controller permanece isolado em Go** como um microserviço separado (operator pattern), comunicando-se com a API via fila de eventos ou API interna — evitando reimplementar client-go em outra linguagem.

### 3.2 Controle de Contexto / Kubeconfig

O maior desafio de portabilidade Minikube ↔ EKS é abstrair **onde** os recursos serão criados. Estratégia:

```yaml
targetContext:
  cluster: "eks"                              # chave lógica
  kubeconfigSecretRef: "kubeforge-eks-kubeconfig"
```

- **Minikube:** o Controller roda **dentro** do próprio cluster Minikube (in-cluster config via `rest.InClusterConfig()`), sem necessidade de kubeconfig externo — simplifica o fluxo local de desenvolvimento.
- **EKS:** o Controller mantém um **mapa de clientsets** (`map[string]*kubernetes.Clientset`), um por cluster-alvo registrado. Os kubeconfigs (ou preferencialmente credenciais via **IRSA — IAM Roles for Service Accounts**) ficam armazenados como Kubernetes Secrets, nunca em texto plano no banco.
- Uma **camada de abstração `ClusterProvider`** encapsula a obtenção do clientset correto:

```go
type ClusterProvider interface {
    GetClientset(ctx context.Context, clusterKey string) (*kubernetes.Clientset, error)
}
```

Isso permite adicionar novos clusters-alvo (ex.: um segundo EKS de staging) sem alterar o Controller.

- **Recomendação para EKS:** priorizar autenticação via **IRSA** ou **EKS Pod Identity** em vez de kubeconfig estático — elimina rotação manual de credenciais e reduz superfície de exposição de secrets de longa duração.

### 3.3 RBAC Necessário

O Controller deve operar com o **menor privilégio possível**, restrito a um namespace dedicado (`kubeforge-workloads`) sempre que viável:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: kubeforge-controller-role
  namespace: kubeforge-workloads
rules:
  - apiGroups: [""]
    resources: ["pods", "pods/log", "persistentvolumeclaims", "configmaps", "secrets", "events"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: ["batch"]
    resources: ["jobs"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: ["apps"]
    resources: ["deployments"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: ["kubeforge.io"]
    resources: ["kubeforgecomponents", "kubeforgecomponents/status"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kubeforge-crd-manager
rules:
  - apiGroups: ["apiextensions.k8s.io"]
    resources: ["customresourcedefinitions"]
    verbs: ["get", "list", "watch"]
```

- **Secrets sensíveis** (tokens do GitHub, credenciais de registry) devem usar **RBAC restrito por verbo** (`get` apenas onde necessário) e, idealmente, integração com um cofre externo (AWS Secrets Manager + External Secrets Operator) em vez de Secrets nativos puros.
- Aplicar **NetworkPolicy** para isolar workloads de teste entre si e do restante do cluster.
- Aplicar **Resource Quotas** e **LimitRanges** por namespace, evitando que testes descontrolados consumam capacidade do cluster (crítico especialmente em EKS, por custo).

---

## 4. Arquitetura dos Motores de Build e Execution

### 4.1 Motor de Build (Build Engine)

**Estratégia recomendada: Kaniko como padrão, com Cloud Native Buildpacks como alternativa opcional.**

| Critério | Kaniko | Buildpacks (pack/lifecycle) | Docker-in-Docker (DinD) |
|---|---|---|---|
| Requer Docker socket / privilégio | Não | Não | Sim (privileged) |
| Segurança em multi-tenant | Alta | Alta | Baixa (risco de escape) |
| Requer Dockerfile | Sim | Não (detecção automática) | Sim |
| Complexidade de adoção | Baixa | Média | Baixa, mas insegura |
| Cache de camadas | Sim (registry cache) | Sim | Sim |

**Decisão:** Kaniko é escolhido como padrão pois **não exige privilégios elevados nem Docker socket**, rodando como um Pod comum no cluster — essencial em ambientes EKS compartilhados com políticas de segurança rígidas (PodSecurity `restricted`). Buildpacks fica disponível como estratégia alternativa (`spec.build.strategy: buildpacks`) para times que preferem não manter Dockerfile.

**Fluxo de build isolado:**

1. O Build Broker cria um **Job efêmero** de build em um namespace isolado (`kubeforge-builds`), diferente do namespace de execução dos workloads de teste.
2. O Job monta:
   - Um **Secret** com credenciais do GitHub (se repo privado).
   - Um **Secret** com credenciais do registry de destino (ECR via IRSA, ou registry local do Minikube sem autenticação).
3. Kaniko executa: `git clone` (init container) → build da imagem → push direto para o registry, **sem daemon Docker**.
4. `securityContext` do Job de build: `runAsNonRoot: true`, `readOnlyRootFilesystem` quando possível, `allowPrivilegeEscalation: false`.
5. **Isolamento de rede:** NetworkPolicy restringe o Job de build a acessar apenas GitHub e o registry — nada mais no cluster.
6. TTL agressivo (`ttlSecondsAfterFinished: 300`) garante que o Job de build não acumule.

### 4.2 Motor de Execução (Execution Engine)

O Controller traduz `spec.runtime` + `spec.resources` em um manifesto nativo, escolhendo o tipo conforme `workloadKind`:

- **Pod:** testes pontuais, sem retry automático — útil para depuração interativa.
- **Job:** caso de uso predominante para "testar comportamento" (carga, chaos, validação) — possui `restartPolicy`, `backoffLimit` e `ttlSecondsAfterFinished` nativos, alinhados à natureza efêmera da ferramenta.
- **Deployment:** quando o componente precisa simular uma carga contínua/serviço de longa duração (ex.: testar HPA, rolling updates).

**Mapeamento simplificado (pseudocódigo do Controller):**

```go
func BuildManifest(c KubeForgeComponent) client.Object {
    podSpec := corev1.PodSpec{
        Containers: []corev1.Container{{
            Name:      c.Name,
            Image:     c.Status.BuildImageDigest,
            Command:   c.Spec.Runtime.Command,
            Args:      c.Spec.Runtime.Args,
            Env:       toEnvVars(c.Spec.Runtime.Env),
            Resources: toResourceRequirements(c.Spec.Resources),
        }},
        InitContainers: toInitContainers(c.Spec.Hooks.PreRun),
        RestartPolicy:  c.Spec.Runtime.RestartPolicy,
    }

    switch c.Spec.Runtime.WorkloadKind {
    case "Job":
        return &batchv1.Job{Spec: batchv1.JobSpec{
            Template: corev1.PodTemplateSpec{Spec: podSpec},
            TTLSecondsAfterFinished: &c.Spec.Lifecycle.TTLSecondsAfterFinished,
            ActiveDeadlineSeconds:   &c.Spec.Lifecycle.ActiveDeadlineSeconds,
        }}
    case "Deployment":
        return &appsv1.Deployment{Spec: appsv1.DeploymentSpec{
            Replicas: &c.Spec.Runtime.Replicas,
            Template: corev1.PodTemplateSpec{Spec: podSpec},
        }}
    default:
        return &corev1.Pod{Spec: podSpec}
    }
}
```

- **Hooks pós-execução** (`postRun`) são implementados como Jobs separados, disparados pelo Controller ao detectar `phase: Succeeded/Failed` do workload principal — evitando acoplar lógica de verificação ao Pod original.
- **Abstração Minikube ↔ EKS:** o mesmo manifesto é aplicado via `ClusterProvider.GetClientset(targetContext.cluster)`; diferenças de ambiente (StorageClass, nó com GPU, etc.) ficam isoladas em um **mapa de perfis de cluster** (`clusterProfiles.yaml`), nunca hardcoded no Controller:

```yaml
clusterProfiles:
  minikube:
    defaultStorageClass: "standard"
    imagePullPolicy: "IfNotPresent"
  eks:
    defaultStorageClass: "gp3"
    imagePullPolicy: "Always"
```

---

## 5. Estratégia de Teardown / Cleanup

Objetivo: garantir que nenhum recurso de teste sobreviva além do necessário, protegendo custo (EKS) e capacidade (Minikube).

### 5.1 Camadas de limpeza (defesa em profundidade)

1. **TTL nativo do Kubernetes (`ttlSecondsAfterFinished`)** — primeira linha de defesa para Jobs; o próprio `kube-controller-manager` remove o Job e seus Pods após o TTL.
2. **`activeDeadlineSeconds`** — evita testes "travados" rodando indefinidamente; força término e status `Failed` após o prazo.
3. **Reconciliation Loop do Controller** — o Controller observa periodicamente (`RequeueAfter`) todos os `KubeForgeComponent` e, conforme `lifecycle.cleanupPolicy`, decide se remove os recursos associados (Pod/Job/Deployment/PVC) imediatamente após conclusão, mantendo apenas os metadados/logs no PostgreSQL.
4. **CronJob de Garbage Collection (rede de segurança)** — um CronJob de auditoria roda a cada N minutos, varrendo o namespace `kubeforge-workloads` em busca de recursos órfãos (label `kubeforge.io/managed=true` sem CRD correspondente, ou mais antigos que `X` horas) e os remove. Cobre falhas do Controller (crash, race condition).
5. **Namespace por execução (opcional, para isolamento forte)** — para testes de maior risco (ex.: chaos engineering), o Controller pode provisionar um **namespace efêmero** dedicado por execução, com **ResourceQuota** e deletar o namespace inteiro no teardown — limpeza atômica e garantida.
6. **Limpeza de PVCs** — PVCs com `reclaimPolicy: Delete` na StorageClass garantem que o volume subjacente (EBS na AWS) seja removido junto com o PVC, evitando custo de armazenamento órfão. Auditoria periódica adicional para volumes `Retain` esquecidos.
7. **Limpeza de imagens de build** — política de ciclo de vida no ECR (lifecycle policy) removendo imagens de teste após N dias ou mantendo apenas as últimas M tags por componente; no Minikube, `docker image prune` agendado.

### 5.2 Ações manuais e limites de segurança

- Endpoint/UI de **"Stop & Cleanup"** manual, permitindo interromper testes de longa duração fora do horário comercial.
- **Budget Guardrails (EKS):** ResourceQuota por time/namespace + alerta (via métricas expostas ao Prometheus) quando a soma de `requests.cpu`/`requests.memory` ativos ultrapassar um limiar configurado.
- Todas as remoções são registradas no Metadata Store como eventos de auditoria (`cleanup_reason`, `triggered_by`, `timestamp`).

---

## 6. Tecnologias Recomendadas

| Camada | Tecnologia | Justificativa |
|---|---|---|
| **Linguagem do Controller/Operator** | Go 1.23+ | Padrão de facto para operators K8s; `client-go`/`controller-runtime` maduros |
| **Framework do Operator** | `kubebuilder` (SDK sobre `controller-runtime`) | Gera boilerplate de CRD, reconciler, webhooks de validação |
| **Linguagem da API/Backend de negócio** | Go (mono-stack) **ou** Python/FastAPI | Go simplifica reaproveitar client-go direto; Python/FastAPI acelera desenvolvimento de CRUD/UI se o time já domina a stack |
| **Metadata Store** | PostgreSQL | Relacional, suporta auditoria, histórico versionado e consultas analíticas |
| **Fila de eventos** | NATS ou Redis Streams | Baixa latência, simples de operar; RabbitMQ como alternativa se já houver expertise no time |
| **Build Engine** | Kaniko (padrão), Cloud Native Buildpacks (alternativa) | Build sem privilégio de daemon Docker, seguro em multi-tenant |
| **Frontend/Console** | React + Next.js, ou simplesmente um dashboard leve (Backstage plugin, se já houver Backstage na empresa) | Reaproveita padrões de Internal Developer Platform já existentes |
| **Client K8s** | `client-go` (Go) | SDK oficial, suporte total a informers/watch |
| **Gestão de credenciais AWS** | IRSA / EKS Pod Identity + External Secrets Operator | Elimina credenciais estáticas de longa duração |
| **Observabilidade** | Prometheus + Grafana, Loki (logs) | Padrão CNCF; permite dashboards de uso de recursos por componente/time |
| **CI/CD do próprio KubeForge** | GitHub Actions + Argo CD (GitOps) | Deploy do Controller/API segue o mesmo padrão declarativo que a ferramenta promove |
| **Empacotamento** | Helm Chart | Distribuição padronizada entre Minikube (dev) e EKS (produção interna) |
| **Testes de integração** | `envtest` (controller-runtime) + Kind | Testa reconciliation loops sem depender de cluster real |
| **Registro de imagens** | Amazon ECR (EKS) / registry local `registry:2` (Minikube) | Nativo por ambiente, sem necessidade de configuração adicional |

---

## 7. Adendo — Perfil de Implantação MVP Pessoal (Custo Zero / 100% Local)

> Contexto: projeto estritamente pessoal, para experimentação individual. Objetivo é **simular** o comportamento descrito nas seções 1–6, mas eliminando qualquer dependência paga (AWS/EKS/ECR) e qualquer complexidade operacional de multi-tenant (RBAC fino, NetworkPolicy, IRSA). O abstrato `ClusterProvider` (seção 3.2) é mantido no código para não fechar a porta ao EKS no futuro — ele simplesmente terá um único provider registrado: `minikube`.

### 7.1 PodSecurity — nível simplificado

Como não há multi-tenant nem dados de terceiros envolvidos, dispensa-se o padrão `restricted` da seção 3.3.

- **Recomendação:** deixar o namespace **sem label de PodSecurity Admission** (Minikube, por padrão, não aplica nenhum perfil automaticamente) — equivalente a `privileged`.
- Se quiser manter um mínimo de higiene sem esforço extra, aplique apenas o nível **`baseline`**, que já vem pronto no Kubernetes e bloqueia só os vetores mais óbvios (hostNetwork, hostPID, containers privilegiados fora de casos justificados):

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: kubeforge-workloads
  labels:
    pod-security.kubernetes.io/enforce: baseline
```

- **RBAC:** mantenha ainda assim um `ServiceAccount` dedicado com uma `Role` (não `ClusterRole`) escopada ao namespace `kubeforge-workloads` — é o mesmo YAML da seção 3.3, só que sem o `ClusterRole` de CRD manager (você mesmo aplica a CRD manualmente uma vez com seu usuário admin do Minikube). Isso custa zero em complexidade e evita que um bug no Controller mexa em outros namespaces do seu Minikube.
- **NetworkPolicy, IRSA, External Secrets Operator:** descartados nesta fase — não há isolamento de rede nem credenciais cloud a proteger.

### 7.2 Onde cada peça roda (custo zero garantido)

| Componente | Decisão MVP | Por quê |
|---|---|---|
| Cluster | **Somente Minikube** (`targetContext.cluster: minikube` fixo) | Elimina qualquer custo de EKS/EC2/EBS/ECR. O código de abstração de contexto continua existindo, só não é exercitado ainda. |
| Metadata Store | **SQLite** (arquivo local) em vez de PostgreSQL | Zero infraestrutura para subir; suficiente para volume de uso pessoal; migração para Postgres é só trocar o driver do ORM se um dia crescer. |
| Fila de eventos | **Removida** — chamadas assíncronas viram goroutines/canal em memória dentro do mesmo processo | NATS/Redis são overhead desnecessário para um único usuário rodando um binário. |
| API + Controller + Build Broker | **Um único binário Go**, rodando **fora** do cluster (na sua máquina), apontando para o Minikube via kubeconfig padrão (`~/.kube/config`) | Menos peças móveis para subir/derrubar; você roda com `go run .` ou um binário compilado, sem precisar empacotar/deployar o Controller dentro do cluster. |
| Console Web | **Servido pelo mesmo binário** (arquivos estáticos embutidos via `embed.FS` do Go), acessível em `localhost:PORT` | Não precisa de Nginx, Ingress, nem hospedagem separada. |

### 7.3 Build Engine — decisão definitiva para o MVP: Opção A

**Decisão fechada: Opção A — Docker daemon do próprio Minikube.** A Opção B (Kaniko) fica documentada apenas como ponto de evolução futura (seção 7.3.1), não faz parte do escopo do MVP.

**Opção A — Docker daemon do próprio Minikube**
```bash
eval $(minikube docker-env)
docker build -t kubeforge/carga-cpu:local .
```
- O Build Broker (rodando localmente) apenas clona o repo e executa `docker build` usando o daemon Docker interno do Minikube (`minikube docker-env`).
- A imagem gerada já fica disponível para o cluster **sem precisar de push/pull de registry** — basta usar `imagePullPolicy: Never` no manifesto do Pod/Job.
- **Vantagem:** zero peças extras (sem Kaniko Job, sem registry local, sem ECR).
- **Trade-off:** não é portável direto para EKS (lá não existe "docker daemon do cluster" acessível assim) — quando/se você quiser testar em EKS futuramente, troca-se a implementação do Build Broker para a Opção B, mantendo o mesmo contrato `spec.build.strategy`.

#### 7.3.1 Evolução futura (fora do escopo do MVP) — Opção B: Kaniko
- Ative o **registry addon** do Minikube (`minikube addons enable registry`) — sobe um registry local sem custo.
- Kaniko builda e faz push para `localhost:5000/...` (ou o endereço do addon).
- Use essa opção se, mesmo sendo pessoal, você quiser já validar o fluxo idêntico ao que rodaria em EKS.

> Ambas continuam respeitando o schema `spec.build.strategy` definido na seção 2 — só muda a implementação por trás, sem impacto no modelo de dados.

### 7.4 Teardown — o que muda

- Continua valendo `ttlSecondsAfterFinished` e `activeDeadlineSeconds` (seção 5) — são gratuitos e nativos do K8s, não há razão para removê-los mesmo em cenário pessoal.
- **Removido:** ResourceQuota corporativa, alertas de budget e CronJob de auditoria multi-time — não fazem sentido para um único usuário.
- **Mantido, mas simplificado:** um comando/endpoint único `kubeforge cleanup --all` que lista e remove qualquer recurso com label `kubeforge.io/managed=true` no namespace — útil sobretudo para não deixar o **seu laptop** com CPU/memória do Minikube presa por testes esquecidos (aqui o "custo" é a bateria/ventoinha, não a fatura da AWS).

### 7.5 Tabela de tecnologias — revisão para o perfil MVP local

| Camada | Decisão original (seção 6) | Decisão MVP pessoal | Custo |
|---|---|---|---|
| Cluster-alvo | Minikube + EKS | Minikube apenas | Zero |
| Controller/API | Serviços separados, deployados no cluster | Um binário Go único, rodado localmente fora do cluster | Zero |
| Metadata Store | PostgreSQL | SQLite (arquivo local) | Zero |
| Fila de eventos | NATS/Redis Streams | Canal/goroutine em memória (mesmo processo) | Zero |
| Build Engine | Kaniko (padrão) | Docker daemon do Minikube (`minikube docker-env`) | Zero |
| Registro de imagens | ECR / registry local | Nenhum (imagem fica no daemon do Minikube, `imagePullPolicy: Never`) | Zero |
| Credenciais cloud | IRSA + External Secrets | N/A (sem AWS) | Zero |
| Console Web | React/Next.js hospedado | Estático embutido no binário Go (`embed.FS`), servido em `localhost` — pode ser um front simples em HTMX+Go templates para evitar até o build step do React | Zero |
| Observabilidade | Prometheus + Grafana + Loki | Opcional/dispensável no MVP — logs via `kubectl logs` e um endpoint `/status` no próprio binário | Zero |

### 7.6 Próximos Passos (revisados para o escopo pessoal)

1. Subir o Minikube local (`minikube start`) e aplicar manualmente a CRD `KubeForgeComponent` (seção 2.1) com seu usuário admin — não precisa de pipeline de instalação ainda.
2. Implementar o binário único (API + Controller + Build Broker) restrito a `workloadKind: Job` e `build.strategy: local-docker` (Opção A da seção 7.3).
3. Validar o ciclo completo — cadastro → build via `minikube docker-env` → execução como Job → teardown por TTL — antes de qualquer preocupação com Kaniko, registry ou EKS.
4. Construir o Console Web como front estático simples embutido no binário (Go `embed.FS` + HTMX, ou um SPA leve), focado só nas telas de cadastro/acompanhamento/logs.
5. Deixar registrado no código (mas não implementado) o ponto de extensão `ClusterProvider` para "eks" e a Opção B de build com Kaniko — assim, se um dia quiser testar em EKS de fato, o esforço é incremental, não uma reescrita.
