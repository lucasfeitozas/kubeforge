package controller

import (
	"encoding/json"
	"testing"

	"github.com/lucasfeitozas/kubeforge/internal/store"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func newComponent(t *testing.T, runtime, resources string) *store.Component {
	t.Helper()
	return &store.Component{
		ID:               "componente-teste",
		BuildImageDigest: "kubeforge/componente-teste:abc123",
		Runtime:          json.RawMessage(runtime),
		Resources:        json.RawMessage(resources),
	}
}

func TestBuildJob_CasoCompleto(t *testing.T) {
	runtime := `{
		"workloadKind": "Job",
		"command": ["/app/start.sh"],
		"args": ["--mode=stress"],
		"env": [
			{"name": "LOG_LEVEL", "value": "debug"},
			{"name": "EXTRA_SECRET", "valueFrom": {"secretKeyRef": {"name": "teste-secret", "key": "token"}}}
		],
		"restartPolicy": "OnFailure",
		"backoffLimit": 2
	}`
	resources := `{
		"requests": {"cpu": "250m", "memory": "256Mi"},
		"limits": {"cpu": "1", "memory": "512Mi"}
	}`
	component := newComponent(t, runtime, resources)

	job, err := BuildJob(component)
	if err != nil {
		t.Fatalf("BuildJob retornou erro inesperado: %v", err)
	}

	if job.Name != component.ID {
		t.Errorf("job.Name = %q, esperava %q", job.Name, component.ID)
	}

	container := job.Spec.Template.Spec.Containers[0]
	if container.Image != component.BuildImageDigest {
		t.Errorf("container.Image = %q, esperava %q", container.Image, component.BuildImageDigest)
	}
	if container.ImagePullPolicy != corev1.PullNever {
		t.Errorf("container.ImagePullPolicy = %q, esperava %q", container.ImagePullPolicy, corev1.PullNever)
	}
	if len(container.Command) != 1 || container.Command[0] != "/app/start.sh" {
		t.Errorf("container.Command = %v, esperava [/app/start.sh]", container.Command)
	}
	if len(container.Args) != 1 || container.Args[0] != "--mode=stress" {
		t.Errorf("container.Args = %v, esperava [--mode=stress]", container.Args)
	}

	if len(container.Env) != 2 {
		t.Fatalf("container.Env tem %d itens, esperava 2", len(container.Env))
	}
	if container.Env[0].Name != "LOG_LEVEL" || container.Env[0].Value != "debug" {
		t.Errorf("container.Env[0] = %+v, esperava LOG_LEVEL=debug", container.Env[0])
	}
	secretEnv := container.Env[1]
	if secretEnv.Name != "EXTRA_SECRET" {
		t.Errorf("container.Env[1].Name = %q, esperava EXTRA_SECRET", secretEnv.Name)
	}
	if secretEnv.ValueFrom == nil || secretEnv.ValueFrom.SecretKeyRef == nil {
		t.Fatal("container.Env[1].ValueFrom.SecretKeyRef ausente")
	}
	if secretEnv.ValueFrom.SecretKeyRef.Name != "teste-secret" || secretEnv.ValueFrom.SecretKeyRef.Key != "token" {
		t.Errorf("secretKeyRef = %+v, esperava teste-secret/token", secretEnv.ValueFrom.SecretKeyRef)
	}

	wantRequests := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("250m"),
		corev1.ResourceMemory: resource.MustParse("256Mi"),
	}
	if !container.Resources.Requests.Cpu().Equal(*wantRequests.Cpu()) || !container.Resources.Requests.Memory().Equal(*wantRequests.Memory()) {
		t.Errorf("container.Resources.Requests = %v, esperava %v", container.Resources.Requests, wantRequests)
	}
	wantLimits := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("1"),
		corev1.ResourceMemory: resource.MustParse("512Mi"),
	}
	if !container.Resources.Limits.Cpu().Equal(*wantLimits.Cpu()) || !container.Resources.Limits.Memory().Equal(*wantLimits.Memory()) {
		t.Errorf("container.Resources.Limits = %v, esperava %v", container.Resources.Limits, wantLimits)
	}

	if job.Spec.Template.Spec.RestartPolicy != corev1.RestartPolicyOnFailure {
		t.Errorf("RestartPolicy = %q, esperava %q", job.Spec.Template.Spec.RestartPolicy, corev1.RestartPolicyOnFailure)
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 2 {
		t.Errorf("BackoffLimit = %v, esperava 2", job.Spec.BackoffLimit)
	}
}

func TestBuildJob_RestartPolicyPadraoNever(t *testing.T) {
	component := newComponent(t, `{"workloadKind": "Job"}`, `{}`)

	job, err := BuildJob(component)
	if err != nil {
		t.Fatalf("BuildJob retornou erro inesperado: %v", err)
	}
	if job.Spec.Template.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("RestartPolicy = %q, esperava %q (default)", job.Spec.Template.Spec.RestartPolicy, corev1.RestartPolicyNever)
	}
}

func TestBuildJob_BackoffLimitPadraoZero(t *testing.T) {
	component := newComponent(t, `{"workloadKind": "Job"}`, `{}`)

	job, err := BuildJob(component)
	if err != nil {
		t.Fatalf("BuildJob retornou erro inesperado: %v", err)
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 0 {
		t.Errorf("BackoffLimit = %v, esperava 0 (default)", job.Spec.BackoffLimit)
	}
}

func TestBuildJob_QuantidadeDeRecursoInvalida(t *testing.T) {
	component := newComponent(t, `{"workloadKind": "Job"}`, `{"requests": {"cpu": "abc"}}`)

	_, err := BuildJob(component)
	if err == nil {
		t.Fatal("BuildJob deveria retornar erro para quantidade de recurso inválida")
	}
}

func TestBuildJob_RuntimeJSONInvalido(t *testing.T) {
	component := newComponent(t, `{invalido`, `{}`)

	_, err := BuildJob(component)
	if err == nil {
		t.Fatal("BuildJob deveria retornar erro para runtime com JSON inválido")
	}
}

func TestBuildJob_ResourcesJSONInvalido(t *testing.T) {
	component := newComponent(t, `{"workloadKind": "Job"}`, `{invalido`)

	_, err := BuildJob(component)
	if err == nil {
		t.Fatal("BuildJob deveria retornar erro para resources com JSON inválido")
	}
}
