package controller

import (
	"encoding/json"
	"fmt"

	"github.com/lucasfeitozas/kubeforge/internal/store"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// jobRuntimeBlock espelha os campos do bloco runtime (docs/ARCHITECTURE.md
// §2.2, deployments/minikube/crd.yaml) necessários para montar um Job,
// no mesmo estilo dos blocos privados de internal/build/broker.go.
type jobRuntimeBlock struct {
	Command       []string      `json:"command"`
	Args          []string      `json:"args"`
	Env           []jobEnvBlock `json:"env"`
	RestartPolicy string        `json:"restartPolicy"`
	BackoffLimit  *int32        `json:"backoffLimit"`
}

type jobEnvBlock struct {
	Name      string                `json:"name"`
	Value     string                `json:"value"`
	ValueFrom *jobEnvVarSourceBlock `json:"valueFrom"`
}

type jobEnvVarSourceBlock struct {
	SecretKeyRef *jobSecretKeySelectorBlock `json:"secretKeyRef"`
}

type jobSecretKeySelectorBlock struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// jobResourcesBlock espelha os campos do bloco resources necessários para
// montar os requests/limits do container. Os valores chegam como strings de
// quantidade Kubernetes (ex.: "250m", "512Mi"), igual ao schema do CRD.
type jobResourcesBlock struct {
	Requests map[string]string `json:"requests"`
	Limits   map[string]string `json:"limits"`
}

// jobHooksBlock espelha o bloco hooks (preRun/postRun). preRun vira
// InitContainers do Job principal (E4.S2, via BuildJob); postRun vira um
// Job de verificação separado (E4.S3, via BuildPostRunJob) —
// docs/ARCHITECTURE.md §4.2.
type jobHooksBlock struct {
	PreRun  []jobPreRunItemBlock  `json:"preRun"`
	PostRun []jobPostRunItemBlock `json:"postRun"`
}

type jobPreRunItemBlock struct {
	Name    string   `json:"name"`
	Image   string   `json:"image"`
	Command []string `json:"command"`
}

// jobPostRunItemBlock espelha um item de hooks.postRun (docs/ARCHITECTURE.md
// §2.1). continueOnError não existe em preRun: falha de InitContainer
// sempre bloqueia nativamente (ADR 0006); postRun roda como Job separado
// depois que o principal já terminou, então o Controller precisa decidir
// explicitamente se a falha da verificação deve ou não marcar a execução
// como Failed (E4.S3, AC2).
type jobPostRunItemBlock struct {
	Name            string   `json:"name"`
	Image           string   `json:"image"`
	Command         []string `json:"command"`
	ContinueOnError bool     `json:"continueOnError"`
}

// BuildJob gera o manifesto batch/v1 Job de um Componente a partir de
// spec.runtime + spec.resources (E4.S1), para executá-lo como workload de
// teste. BuildJob não aplica o Job no cluster nem decide seu namespace: essas
// responsabilidades pertencem a quem consome o manifesto, usando
// targetContext + k8s.ClusterProvider (fora do escopo desta história).
//
// imagePullPolicy é sempre Never: a imagem buildada pelo Build Broker (ver
// internal/build.Broker) só existe no daemon Docker do Minikube (ADR 0002),
// nunca em um registry acessível via pull normal.
func BuildJob(component *store.Component) (*batchv1.Job, error) {
	var rt jobRuntimeBlock
	if err := json.Unmarshal(component.Runtime, &rt); err != nil {
		return nil, fmt.Errorf("interpretando runtime do componente %q: %w", component.ID, err)
	}

	var res jobResourcesBlock
	if len(component.Resources) > 0 {
		if err := json.Unmarshal(component.Resources, &res); err != nil {
			return nil, fmt.Errorf("interpretando resources do componente %q: %w", component.ID, err)
		}
	}

	envVars := toEnvVars(rt.Env)

	requests, err := toResourceList(res.Requests)
	if err != nil {
		return nil, fmt.Errorf("interpretando resources.requests do componente %q: %w", component.ID, err)
	}
	limits, err := toResourceList(res.Limits)
	if err != nil {
		return nil, fmt.Errorf("interpretando resources.limits do componente %q: %w", component.ID, err)
	}

	var hooks jobHooksBlock
	if len(component.Hooks) > 0 {
		if err := json.Unmarshal(component.Hooks, &hooks); err != nil {
			return nil, fmt.Errorf("interpretando hooks do componente %q: %w", component.ID, err)
		}
	}

	backoffLimit := int32(0)
	if rt.BackoffLimit != nil {
		backoffLimit = *rt.BackoffLimit
	}

	podSpec := corev1.PodSpec{
		RestartPolicy:  toRestartPolicy(rt.RestartPolicy),
		InitContainers: toInitContainers(hooks.PreRun),
		Containers: []corev1.Container{{
			Name:            "main",
			Image:           component.BuildImageDigest,
			ImagePullPolicy: corev1.PullNever,
			Command:         rt.Command,
			Args:            rt.Args,
			Env:             envVars,
			Resources: corev1.ResourceRequirements{
				Requests: requests,
				Limits:   limits,
			},
		}},
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: component.ID,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				Spec: podSpec,
			},
		},
	}
	return job, nil
}

// PostRunPlan é o resultado de interpretar hooks.postRun: o Job de
// verificação a aplicar (Job == nil se hooks.postRun estiver vazio ou
// ausente — não há nada a criar) e a política de continueOnError agregada
// dos itens, usada pelo Runner (internal/controller/runner.go) para decidir
// se a falha do Job de verificação deve interromper o fluxo (E4.S3, AC2).
type PostRunPlan struct {
	Job             *batchv1.Job
	ContinueOnError bool
}

// postRunJobSuffix separa o nome do Job de verificação do nome do Job
// principal (que usa component.ID puro, ver BuildJob), evitando colisão de
// nomes no mesmo namespace.
const postRunJobSuffix = "-postrun"

// BuildPostRunJob gera o manifesto do Job de verificação pós-execução de um
// Componente, a partir de hooks.postRun (E4.S3). Cada item do array vira um
// Container comum do mesmo Pod, não um InitContainer: diferente do Job
// principal, o Job de verificação não tem um container "main" para servir
// de gate, então os itens rodam em paralelo (docs/ARCHITECTURE.md §4.2).
//
// Como BuildJob, é uma função pura: não recebe k8s.ClusterProvider nem
// decide Namespace do Job — isso é responsabilidade de quem aplica o
// manifesto no cluster (ver Runner).
func BuildPostRunJob(component *store.Component) (*PostRunPlan, error) {
	var hooks jobHooksBlock
	if len(component.Hooks) > 0 {
		if err := json.Unmarshal(component.Hooks, &hooks); err != nil {
			return nil, fmt.Errorf("interpretando hooks do componente %q: %w", component.ID, err)
		}
	}
	if len(hooks.PostRun) == 0 {
		return &PostRunPlan{}, nil
	}

	backoffLimit := int32(0)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: component.ID + postRunJobSuffix,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers:    toPostRunContainers(hooks.PostRun),
				},
			},
		},
	}

	return &PostRunPlan{
		Job:             job,
		ContinueOnError: postRunContinueOnError(hooks.PostRun),
	}, nil
}

func toPostRunContainers(items []jobPostRunItemBlock) []corev1.Container {
	containers := make([]corev1.Container, 0, len(items))
	for _, item := range items {
		containers = append(containers, corev1.Container{
			Name:    item.Name,
			Image:   item.Image,
			Command: item.Command,
		})
	}
	return containers
}

// postRunContinueOnError agrega o continueOnError de todos os itens de
// postRun num único booleano: true somente se TODOS os itens declararem
// continueOnError:true. Qualquer item omitindo o campo (default false do
// JSON) ou explicitando false derruba a política inteira para false —
// comportamento fail-safe, consistente com o critério de aceite E4.S3/AC2
// ("continueOnError: false interrompe o fluxo").
func postRunContinueOnError(items []jobPostRunItemBlock) bool {
	for _, item := range items {
		if !item.ContinueOnError {
			return false
		}
	}
	return true
}

func toEnvVars(blocks []jobEnvBlock) []corev1.EnvVar {
	if len(blocks) == 0 {
		return nil
	}
	envVars := make([]corev1.EnvVar, 0, len(blocks))
	for _, b := range blocks {
		envVar := corev1.EnvVar{Name: b.Name, Value: b.Value}
		if b.ValueFrom != nil && b.ValueFrom.SecretKeyRef != nil {
			envVar.ValueFrom = &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: b.ValueFrom.SecretKeyRef.Name,
					},
					Key: b.ValueFrom.SecretKeyRef.Key,
				},
			}
		}
		envVars = append(envVars, envVar)
	}
	return envVars
}

func toResourceList(quantities map[string]string) (corev1.ResourceList, error) {
	if len(quantities) == 0 {
		return nil, nil
	}
	list := make(corev1.ResourceList, len(quantities))
	for name, value := range quantities {
		qty, err := resource.ParseQuantity(value)
		if err != nil {
			return nil, fmt.Errorf("quantidade inválida para %q (%q): %w", name, value, err)
		}
		list[corev1.ResourceName(name)] = qty
	}
	return list, nil
}

// toInitContainers traduz hooks.preRun em InitContainers, preservando a
// ordem declarada no JSON (E4.S2). O Kubernetes já garante nativamente que
// o container principal só inicia depois que todos os Init Containers
// terminarem com sucesso, e que a falha de um interrompe o Pod — nenhuma
// lógica extra de sequenciamento é necessária aqui.
func toInitContainers(items []jobPreRunItemBlock) []corev1.Container {
	if len(items) == 0 {
		return nil
	}
	containers := make([]corev1.Container, 0, len(items))
	for _, item := range items {
		containers = append(containers, corev1.Container{
			Name:    item.Name,
			Image:   item.Image,
			Command: item.Command,
		})
	}
	return containers
}

// toRestartPolicy converte runtime.restartPolicy para o tipo do PodSpec.
// Vazio usa Never como padrão (mesmo default do exemplo em
// docs/ARCHITECTURE.md §2.1): Always é inválido para o Pod de um Job.
func toRestartPolicy(raw string) corev1.RestartPolicy {
	if raw == "" {
		return corev1.RestartPolicyNever
	}
	return corev1.RestartPolicy(raw)
}
