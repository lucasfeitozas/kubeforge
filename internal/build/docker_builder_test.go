package build

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// stubDockerEnvResolver é um DockerEnvResolver fake controlado diretamente
// pelo teste, evitando depender de um binário `minikube` real só para
// exercitar a integração DockerBuilder -> docker: a resolução do
// minikube docker-env em si já é coberta por TestMinikubeDockerEnvResolve.
type stubDockerEnvResolver struct {
	env map[string]string
	err error
}

func (s stubDockerEnvResolver) Resolve(ctx context.Context) (map[string]string, error) {
	return s.env, s.err
}

// fakeImageDigest é o Image ID devolvido por `docker image inspect` no
// binário `docker` fake, usado para asserções em BuildResult.Digest.
const fakeImageDigest = "sha256:fakeimageid000000000000000000000000000000000000000000000000"

// writeFakeDocker cria um binário `docker` fake em PATH que grava os
// argumentos recebidos em argsFile e o valor da env var FAKE_DOCKER_MARKER
// em envFile (usada para confirmar que env vars injetadas pelo
// DockerEnvResolver chegam ao processo filho), imprime "log de build fake"
// em stdout/stderr e sai com exitCode. Responde a `docker image inspect ...`
// (usado pela captura de digest após um build bem-sucedido) imprimindo
// fakeImageDigest e saindo com 0, sem tocar em argsFile/envFile.
func writeFakeDocker(t *testing.T, argsFile, envFile string, exitCode int) {
	writeFakeDockerWithInspect(t, argsFile, envFile, exitCode, 0)
}

// writeFakeDockerWithInspect é como writeFakeDocker, mas permite controlar o
// código de saída de `docker image inspect` separadamente do de
// `docker build`, para testar o tratamento de falha na captura de digest.
func writeFakeDockerWithInspect(t *testing.T, argsFile, envFile string, buildExitCode, inspectExitCode int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fixture de binário fake via shell script não suportada no Windows")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "docker")
	script := `#!/bin/sh
if [ "$1" = "image" ] && [ "$2" = "inspect" ]; then
  echo "` + fakeImageDigest + `"
  exit ` + strconv.Itoa(inspectExitCode) + `
fi
echo "$@" > ` + argsFile + `
echo "FAKE_DOCKER_MARKER=$FAKE_DOCKER_MARKER" > ` + envFile + `
echo "log de build fake (stdout)"
echo "log de build fake (stderr)" >&2
exit ` + strconv.Itoa(buildExitCode) + `
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("escrevendo docker fake: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func newTestCloneResult(t *testing.T) CloneResult {
	t.Helper()
	dir := t.TempDir()
	dockerfilePath := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte("FROM scratch"), 0o644); err != nil {
		t.Fatalf("escrevendo Dockerfile de teste: %v", err)
	}
	return CloneResult{
		Dir:            dir,
		CommitSHA:      "abcdef1234567890abcdef1234567890abcdef12",
		DockerfilePath: dockerfilePath,
	}
}

func TestDockerBuilderBuild(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	envFile := filepath.Join(t.TempDir(), "env")
	writeFakeDocker(t, argsFile, envFile, 0)
	t.Setenv("FAKE_DOCKER_MARKER", "")

	cloneResult := newTestCloneResult(t)
	builder := &DockerBuilder{
		DockerEnv: stubDockerEnvResolver{env: map[string]string{
			"DOCKER_HOST":        "tcp://192.168.49.2:2376",
			"FAKE_DOCKER_MARKER": "injetado-pelo-docker-env",
		}},
	}

	result, err := builder.Build(context.Background(), BuildSpec{
		CloneResult:     cloneResult,
		ImageRepository: "kubeforge/carga-cpu",
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	wantTag := "kubeforge/carga-cpu:abcdef123456"
	if result.ImageTag != wantTag {
		t.Fatalf("Build() ImageTag = %q, want %q", result.ImageTag, wantTag)
	}
	if result.CommitSHA != cloneResult.CommitSHA {
		t.Fatalf("Build() CommitSHA = %q, want %q", result.CommitSHA, cloneResult.CommitSHA)
	}
	if result.Digest != fakeImageDigest {
		t.Fatalf("Build() Digest = %q, want %q", result.Digest, fakeImageDigest)
	}
	if !strings.Contains(result.Log, "log de build fake (stdout)") ||
		!strings.Contains(result.Log, "log de build fake (stderr)") {
		t.Fatalf("Build() Log = %q, want conter saída de stdout e stderr", result.Log)
	}

	gotArgs, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("lendo args capturados: %v", err)
	}
	args := strings.Fields(string(gotArgs))
	wantArgs := []string{"build", "-t", wantTag, "-f", cloneResult.DockerfilePath, cloneResult.Dir}
	if strings.Join(args, " ") != strings.Join(wantArgs, " ") {
		t.Fatalf("args recebidos pelo docker fake = %v, want %v", args, wantArgs)
	}

	gotEnv, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("lendo env capturada: %v", err)
	}
	if strings.TrimSpace(string(gotEnv)) != "FAKE_DOCKER_MARKER=injetado-pelo-docker-env" {
		t.Fatalf("env recebida pelo docker fake = %q, want marker injetado pelo DockerEnvResolver", string(gotEnv))
	}
}

func TestDockerBuilderBuildFalhaPreservaLog(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	envFile := filepath.Join(t.TempDir(), "env")
	writeFakeDocker(t, argsFile, envFile, 1)

	cloneResult := newTestCloneResult(t)
	builder := &DockerBuilder{DockerEnv: stubDockerEnvResolver{env: map[string]string{}}}

	result, err := builder.Build(context.Background(), BuildSpec{
		CloneResult:     cloneResult,
		ImageRepository: "kubeforge/carga-cpu",
	})
	if err == nil {
		t.Fatal("Build() error = nil, want erro (docker build fake sai com código 1)")
	}
	if result == nil || !strings.Contains(result.Log, "log de build fake") {
		t.Fatalf("Build() deveria devolver o log mesmo em caso de falha, got %#v", result)
	}
}

func TestDockerBuilderBuildInspectFalha(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	envFile := filepath.Join(t.TempDir(), "env")
	writeFakeDockerWithInspect(t, argsFile, envFile, 0, 1)

	cloneResult := newTestCloneResult(t)
	builder := &DockerBuilder{DockerEnv: stubDockerEnvResolver{env: map[string]string{}}}

	result, err := builder.Build(context.Background(), BuildSpec{
		CloneResult:     cloneResult,
		ImageRepository: "kubeforge/carga-cpu",
	})
	if err == nil {
		t.Fatal("Build() error = nil, want erro (docker image inspect fake sai com código 1)")
	}
	if result == nil {
		t.Fatal("Build() result = nil, want ImageTag/Log preservados mesmo com falha no inspect")
	}
	if result.ImageTag == "" {
		t.Errorf("Build() ImageTag = %q, want tag preenchida mesmo com falha no inspect", result.ImageTag)
	}
	if !strings.Contains(result.Log, "log de build fake") {
		t.Errorf("Build() Log = %q, want log do docker build preservado", result.Log)
	}
	if result.Digest != "" {
		t.Errorf("Build() Digest = %q, want vazio quando o inspect falha", result.Digest)
	}
}

func TestDockerBuilderBuildImageRepositoryObrigatorio(t *testing.T) {
	builder := &DockerBuilder{DockerEnv: stubDockerEnvResolver{env: map[string]string{}}}
	_, err := builder.Build(context.Background(), BuildSpec{CloneResult: newTestCloneResult(t)})
	if err == nil {
		t.Fatal("Build() error = nil, want erro por ImageRepository ausente")
	}
}

func TestDockerBuilderBuildPropagaErroDoDockerEnv(t *testing.T) {
	wantErr := os.ErrNotExist
	builder := &DockerBuilder{DockerEnv: stubDockerEnvResolver{err: wantErr}}
	_, err := builder.Build(context.Background(), BuildSpec{
		CloneResult:     newTestCloneResult(t),
		ImageRepository: "kubeforge/carga-cpu",
	})
	if err == nil {
		t.Fatal("Build() error = nil, want erro propagado do DockerEnvResolver")
	}
}
