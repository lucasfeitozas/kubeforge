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
	return newComponentComHooks(t, runtime, resources, "")
}

func newComponentComHooks(t *testing.T, runtime, resources, hooks string) *store.Component {
	t.Helper()
	component := &store.Component{
		ID:               "componente-teste",
		BuildImageDigest: "kubeforge/componente-teste:abc123",
		Runtime:          json.RawMessage(runtime),
		Resources:        json.RawMessage(resources),
	}
	if hooks != "" {
		component.Hooks = json.RawMessage(hooks)
	}
	return component
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

func TestBuildJob_StorageEphemeralUsaEmptyDirComSizeLimit(t *testing.T) {
	resources := `{"storage": {"type": "ephemeral", "sizeLimit": "1Gi"}}`
	component := newComponent(t, `{"workloadKind": "Job"}`, resources)

	job, err := BuildJob(component)
	if err != nil {
		t.Fatalf("BuildJob retornou erro inesperado: %v", err)
	}

	volumes := job.Spec.Template.Spec.Volumes
	if len(volumes) != 1 {
		t.Fatalf("Volumes tem %d itens, esperava 1", len(volumes))
	}
	if volumes[0].EmptyDir == nil {
		t.Fatal("Volumes[0].EmptyDir = nil, esperava EmptyDir")
	}
	wantSizeLimit := resource.MustParse("1Gi")
	if volumes[0].EmptyDir.SizeLimit == nil || !volumes[0].EmptyDir.SizeLimit.Equal(wantSizeLimit) {
		t.Errorf("Volumes[0].EmptyDir.SizeLimit = %v, esperava %v", volumes[0].EmptyDir.SizeLimit, wantSizeLimit)
	}

	container := job.Spec.Template.Spec.Containers[0]
	if len(container.VolumeMounts) != 1 {
		t.Fatalf("VolumeMounts tem %d itens, esperava 1", len(container.VolumeMounts))
	}
	if container.VolumeMounts[0].Name != "storage" || container.VolumeMounts[0].MountPath != "/data" {
		t.Errorf("VolumeMounts[0] = %+v, esperava name=storage mountPath=/data", container.VolumeMounts[0])
	}
}

func TestBuildJob_StorageEphemeralSemSizeLimit(t *testing.T) {
	resources := `{"storage": {"type": "ephemeral"}}`
	component := newComponent(t, `{"workloadKind": "Job"}`, resources)

	job, err := BuildJob(component)
	if err != nil {
		t.Fatalf("BuildJob retornou erro inesperado: %v", err)
	}

	volumes := job.Spec.Template.Spec.Volumes
	if len(volumes) != 1 || volumes[0].EmptyDir == nil {
		t.Fatalf("Volumes = %+v, esperava 1 item com EmptyDir", volumes)
	}
	if volumes[0].EmptyDir.SizeLimit != nil {
		t.Errorf("Volumes[0].EmptyDir.SizeLimit = %v, esperava nil sem sizeLimit", volumes[0].EmptyDir.SizeLimit)
	}
}

func TestBuildJob_StoragePVCGeraVolumeEVolumeMount(t *testing.T) {
	resources := `{"storage": {"type": "pvc", "pvc": {"size": "5Gi"}}}`
	component := newComponent(t, `{"workloadKind": "Job"}`, resources)

	job, err := BuildJob(component)
	if err != nil {
		t.Fatalf("BuildJob retornou erro inesperado: %v", err)
	}

	volumes := job.Spec.Template.Spec.Volumes
	if len(volumes) != 1 {
		t.Fatalf("Volumes tem %d itens, esperava 1", len(volumes))
	}
	if volumes[0].PersistentVolumeClaim == nil {
		t.Fatal("Volumes[0].PersistentVolumeClaim = nil, esperava referência ao PVC")
	}
	wantClaimName := component.ID + "-data"
	if volumes[0].PersistentVolumeClaim.ClaimName != wantClaimName {
		t.Errorf("ClaimName = %q, esperava %q", volumes[0].PersistentVolumeClaim.ClaimName, wantClaimName)
	}

	container := job.Spec.Template.Spec.Containers[0]
	if len(container.VolumeMounts) != 1 || container.VolumeMounts[0].Name != "storage" {
		t.Errorf("VolumeMounts = %+v, esperava 1 item com name=storage", container.VolumeMounts)
	}
}

func TestBuildJob_SemStorageNaoGeraVolumes(t *testing.T) {
	component := newComponent(t, `{"workloadKind": "Job"}`, `{}`)

	job, err := BuildJob(component)
	if err != nil {
		t.Fatalf("BuildJob retornou erro inesperado: %v", err)
	}
	if job.Spec.Template.Spec.Volumes != nil {
		t.Errorf("Volumes = %v, esperava nil sem resources.storage", job.Spec.Template.Spec.Volumes)
	}
	if job.Spec.Template.Spec.Containers[0].VolumeMounts != nil {
		t.Errorf("VolumeMounts = %v, esperava nil sem resources.storage", job.Spec.Template.Spec.Containers[0].VolumeMounts)
	}
}

func TestBuildJob_StorageSizeLimitInvalido(t *testing.T) {
	resources := `{"storage": {"type": "ephemeral", "sizeLimit": "abc"}}`
	component := newComponent(t, `{"workloadKind": "Job"}`, resources)

	_, err := BuildJob(component)
	if err == nil {
		t.Fatal("BuildJob deveria retornar erro para sizeLimit inválido")
	}
}

func TestBuildJob_StoragePVCSizeInvalido(t *testing.T) {
	resources := `{"storage": {"type": "pvc", "pvc": {"size": "abc"}}}`
	component := newComponent(t, `{"workloadKind": "Job"}`, resources)

	_, err := BuildJob(component)
	if err == nil {
		t.Fatal("BuildJob deveria retornar erro para pvc.size inválido")
	}
}

func TestBuildStoragePVC_TypePvcUsaStorageClassPadrao(t *testing.T) {
	resources := `{"storage": {"type": "pvc", "pvc": {"size": "5Gi"}}}`
	component := newComponent(t, `{"workloadKind": "Job"}`, resources)

	pvc, err := BuildStoragePVC(component)
	if err != nil {
		t.Fatalf("BuildStoragePVC retornou erro inesperado: %v", err)
	}
	if pvc == nil {
		t.Fatal("pvc = nil, esperava PersistentVolumeClaim")
	}
	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "standard" {
		t.Errorf("StorageClassName = %v, esperava %q (default Minikube)", pvc.Spec.StorageClassName, "standard")
	}
}

func TestBuildStoragePVC_StorageClassNameCustomizado(t *testing.T) {
	resources := `{"storage": {"type": "pvc", "pvc": {"size": "5Gi", "storageClassName": "gp3"}}}`
	component := newComponent(t, `{"workloadKind": "Job"}`, resources)

	pvc, err := BuildStoragePVC(component)
	if err != nil {
		t.Fatalf("BuildStoragePVC retornou erro inesperado: %v", err)
	}
	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "gp3" {
		t.Errorf("StorageClassName = %v, esperava %q", pvc.Spec.StorageClassName, "gp3")
	}
}

func TestBuildStoragePVC_AccessModesPadraoReadWriteOnce(t *testing.T) {
	resources := `{"storage": {"type": "pvc", "pvc": {"size": "5Gi"}}}`
	component := newComponent(t, `{"workloadKind": "Job"}`, resources)

	pvc, err := BuildStoragePVC(component)
	if err != nil {
		t.Fatalf("BuildStoragePVC retornou erro inesperado: %v", err)
	}
	if len(pvc.Spec.AccessModes) != 1 || pvc.Spec.AccessModes[0] != corev1.ReadWriteOnce {
		t.Errorf("AccessModes = %v, esperava [ReadWriteOnce]", pvc.Spec.AccessModes)
	}

	wantSize := resource.MustParse("5Gi")
	got := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	if !got.Equal(wantSize) {
		t.Errorf("Resources.Requests[storage] = %v, esperava %v", got, wantSize)
	}
}

func TestBuildStoragePVC_SizeAusenteRetornaErro(t *testing.T) {
	resources := `{"storage": {"type": "pvc"}}`
	component := newComponent(t, `{"workloadKind": "Job"}`, resources)

	_, err := BuildStoragePVC(component)
	if err == nil {
		t.Fatal("BuildStoragePVC deveria retornar erro quando pvc.size está ausente")
	}
}

func TestBuildStoragePVC_TypeEphemeralRetornaNil(t *testing.T) {
	component := newComponent(t, `{"workloadKind": "Job"}`, `{"storage": {"type": "ephemeral"}}`)

	pvc, err := BuildStoragePVC(component)
	if err != nil {
		t.Fatalf("BuildStoragePVC retornou erro inesperado: %v", err)
	}
	if pvc != nil {
		t.Errorf("pvc = %+v, esperava nil para type=ephemeral", pvc)
	}
}

func TestBuildStoragePVC_SemStorageRetornaNil(t *testing.T) {
	component := newComponent(t, `{"workloadKind": "Job"}`, `{}`)

	pvc, err := BuildStoragePVC(component)
	if err != nil {
		t.Fatalf("BuildStoragePVC retornou erro inesperado: %v", err)
	}
	if pvc != nil {
		t.Errorf("pvc = %+v, esperava nil sem resources.storage", pvc)
	}
}

func TestBuildJob_PreRunGeraInitContainersNaOrdem(t *testing.T) {
	hooks := `{
		"preRun": [
			{"name": "warmup-check", "image": "curlimages/curl:8.9.0", "command": ["sh", "-c", "curl -f http://dependencia:8080/health"]},
			{"name": "migra-schema", "image": "org/migrador:latest", "command": ["/migrate.sh"]}
		]
	}`
	component := newComponentComHooks(t, `{"workloadKind": "Job"}`, `{}`, hooks)

	job, err := BuildJob(component)
	if err != nil {
		t.Fatalf("BuildJob retornou erro inesperado: %v", err)
	}

	initContainers := job.Spec.Template.Spec.InitContainers
	if len(initContainers) != 2 {
		t.Fatalf("InitContainers tem %d itens, esperava 2", len(initContainers))
	}

	primeiro := initContainers[0]
	if primeiro.Name != "warmup-check" || primeiro.Image != "curlimages/curl:8.9.0" {
		t.Errorf("InitContainers[0] = %+v, esperava name=warmup-check image=curlimages/curl:8.9.0", primeiro)
	}
	if len(primeiro.Command) != 3 || primeiro.Command[2] != "curl -f http://dependencia:8080/health" {
		t.Errorf("InitContainers[0].Command = %v, esperava comando de curl", primeiro.Command)
	}

	segundo := initContainers[1]
	if segundo.Name != "migra-schema" || segundo.Image != "org/migrador:latest" {
		t.Errorf("InitContainers[1] = %+v, esperava name=migra-schema image=org/migrador:latest", segundo)
	}
}

func TestBuildJob_SemPreRunNaoGeraInitContainers(t *testing.T) {
	component := newComponent(t, `{"workloadKind": "Job"}`, `{}`)

	job, err := BuildJob(component)
	if err != nil {
		t.Fatalf("BuildJob retornou erro inesperado: %v", err)
	}
	if job.Spec.Template.Spec.InitContainers != nil {
		t.Errorf("InitContainers = %v, esperava nil sem hooks.preRun", job.Spec.Template.Spec.InitContainers)
	}
}

func TestBuildJob_HooksJSONInvalido(t *testing.T) {
	component := newComponentComHooks(t, `{"workloadKind": "Job"}`, `{}`, `{invalido`)

	_, err := BuildJob(component)
	if err == nil {
		t.Fatal("BuildJob deveria retornar erro para hooks com JSON inválido")
	}
}

func TestBuildPostRunJob_CasoCompleto(t *testing.T) {
	hooks := `{
		"postRun": [
			{"name": "verifica-http", "image": "curlimages/curl:8.9.0", "command": ["sh", "-c", "curl -f http://localhost:8080/health"], "continueOnError": true},
			{"name": "verifica-resultado", "image": "org/verificador:latest", "command": ["/verify.sh"], "continueOnError": false}
		]
	}`
	component := newComponentComHooks(t, `{"workloadKind": "Job"}`, `{}`, hooks)

	plan, err := BuildPostRunJob(component)
	if err != nil {
		t.Fatalf("BuildPostRunJob retornou erro inesperado: %v", err)
	}
	if plan.Job == nil {
		t.Fatal("plan.Job = nil, esperava Job de verificação")
	}

	wantName := component.ID + "-postrun"
	if plan.Job.Name != wantName {
		t.Errorf("plan.Job.Name = %q, esperava %q", plan.Job.Name, wantName)
	}
	if plan.Job.Spec.BackoffLimit == nil || *plan.Job.Spec.BackoffLimit != 0 {
		t.Errorf("BackoffLimit = %v, esperava 0", plan.Job.Spec.BackoffLimit)
	}
	if plan.Job.Spec.Template.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("RestartPolicy = %q, esperava %q", plan.Job.Spec.Template.Spec.RestartPolicy, corev1.RestartPolicyNever)
	}

	containers := plan.Job.Spec.Template.Spec.Containers
	if len(containers) != 2 {
		t.Fatalf("Containers tem %d itens, esperava 2", len(containers))
	}
	primeiro := containers[0]
	if primeiro.Name != "verifica-http" || primeiro.Image != "curlimages/curl:8.9.0" {
		t.Errorf("Containers[0] = %+v, esperava name=verifica-http image=curlimages/curl:8.9.0", primeiro)
	}
	segundo := containers[1]
	if segundo.Name != "verifica-resultado" || segundo.Image != "org/verificador:latest" {
		t.Errorf("Containers[1] = %+v, esperava name=verifica-resultado image=org/verificador:latest", segundo)
	}

	if plan.ContinueOnError {
		t.Error("plan.ContinueOnError = true, esperava false (um item tem continueOnError:false)")
	}
}

func TestBuildPostRunJob_SemPostRun(t *testing.T) {
	component := newComponentComHooks(t, `{"workloadKind": "Job"}`, `{}`, `{"preRun": [{"name": "warmup", "image": "curlimages/curl:8.9.0", "command": ["true"]}]}`)

	plan, err := BuildPostRunJob(component)
	if err != nil {
		t.Fatalf("BuildPostRunJob retornou erro inesperado: %v", err)
	}
	if plan.Job != nil {
		t.Errorf("plan.Job = %+v, esperava nil sem hooks.postRun", plan.Job)
	}
}

func TestBuildPostRunJob_SemHooks(t *testing.T) {
	component := newComponent(t, `{"workloadKind": "Job"}`, `{}`)

	plan, err := BuildPostRunJob(component)
	if err != nil {
		t.Fatalf("BuildPostRunJob retornou erro inesperado: %v", err)
	}
	if plan.Job != nil {
		t.Errorf("plan.Job = %+v, esperava nil sem hooks", plan.Job)
	}
}

func TestBuildPostRunJob_HooksJSONInvalido(t *testing.T) {
	component := newComponentComHooks(t, `{"workloadKind": "Job"}`, `{}`, `{invalido`)

	_, err := BuildPostRunJob(component)
	if err == nil {
		t.Fatal("BuildPostRunJob deveria retornar erro para hooks com JSON inválido")
	}
}

func TestPostRunContinueOnError(t *testing.T) {
	tests := []struct {
		name  string
		items []jobPostRunItemBlock
		want  bool
	}{
		{
			name: "todos true",
			items: []jobPostRunItemBlock{
				{Name: "a", ContinueOnError: true},
				{Name: "b", ContinueOnError: true},
			},
			want: true,
		},
		{
			name: "um item omite o campo (default false)",
			items: []jobPostRunItemBlock{
				{Name: "a", ContinueOnError: true},
				{Name: "b"},
			},
			want: false,
		},
		{
			name: "um item explicita false",
			items: []jobPostRunItemBlock{
				{Name: "a", ContinueOnError: true},
				{Name: "b", ContinueOnError: false},
			},
			want: false,
		},
		{
			name:  "item único true",
			items: []jobPostRunItemBlock{{Name: "a", ContinueOnError: true}},
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := postRunContinueOnError(tt.items); got != tt.want {
				t.Errorf("postRunContinueOnError(%+v) = %v, esperava %v", tt.items, got, tt.want)
			}
		})
	}
}
