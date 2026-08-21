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

	backoffLimit := int32(0)
	if rt.BackoffLimit != nil {
		backoffLimit = *rt.BackoffLimit
	}

	podSpec := corev1.PodSpec{
		RestartPolicy: toRestartPolicy(rt.RestartPolicy),
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

// toRestartPolicy converte runtime.restartPolicy para o tipo do PodSpec.
// Vazio usa Never como padrão (mesmo default do exemplo em
// docs/ARCHITECTURE.md §2.1): Always é inválido para o Pod de um Job.
func toRestartPolicy(raw string) corev1.RestartPolicy {
	if raw == "" {
		return corev1.RestartPolicyNever
	}
	return corev1.RestartPolicy(raw)
}
