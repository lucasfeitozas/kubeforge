package build

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestParseDockerEnv(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   map[string]string
	}{
		{
			name: "saída típica do --shell none",
			output: "DOCKER_TLS_VERIFY=1\n" +
				"DOCKER_HOST=tcp://192.168.49.2:2376\n" +
				"DOCKER_CERT_PATH=/home/user/.minikube/certs\n" +
				"MINIKUBE_ACTIVE_DOCKERD=minikube\n",
			want: map[string]string{
				"DOCKER_TLS_VERIFY":       "1",
				"DOCKER_HOST":             "tcp://192.168.49.2:2376",
				"DOCKER_CERT_PATH":        "/home/user/.minikube/certs",
				"MINIKUBE_ACTIVE_DOCKERD": "minikube",
			},
		},
		{
			name:   "ignora linhas em branco e comentários",
			output: "\n# comentário\nDOCKER_HOST=tcp://x\n\n",
			want:   map[string]string{"DOCKER_HOST": "tcp://x"},
		},
		{
			name:   "remove aspas ao redor do valor",
			output: `DOCKER_HOST="tcp://x"` + "\n",
			want:   map[string]string{"DOCKER_HOST": "tcp://x"},
		},
		{
			name:   "saída vazia",
			output: "",
			want:   map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDockerEnv(tt.output)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseDockerEnv() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

// writeFakeMinikube cria um binário `minikube` fake em dir e o prepende ao
// PATH do processo de teste, mesma abordagem de fixture usada em
// git_cloner_test.go, mas para um binário externo (não temos como depender
// de um Minikube real em CI).
func writeFakeMinikube(t *testing.T, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fixture de binário fake via shell script não suportada no Windows")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "minikube")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("escrevendo minikube fake: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestMinikubeDockerEnvResolve(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	writeFakeMinikube(t, `echo "$@" > `+argsFile+`
echo "DOCKER_HOST=tcp://192.168.49.2:2376"
echo "DOCKER_TLS_VERIFY=1"
`)

	resolver := MinikubeDockerEnv{Profile: "meu-profile"}
	env, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	want := map[string]string{
		"DOCKER_HOST":       "tcp://192.168.49.2:2376",
		"DOCKER_TLS_VERIFY": "1",
	}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("Resolve() = %#v, want %#v", env, want)
	}

	gotArgs, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("lendo args capturados: %v", err)
	}
	wantArgs := "-p meu-profile docker-env --shell none\n"
	if string(gotArgs) != wantArgs {
		t.Fatalf("args recebidos pelo minikube fake = %q, want %q", string(gotArgs), wantArgs)
	}
}

func TestMinikubeDockerEnvResolveDefaultProfile(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	writeFakeMinikube(t, `echo "$@" > `+argsFile+`
`)

	resolver := MinikubeDockerEnv{}
	if _, err := resolver.Resolve(context.Background()); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	gotArgs, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("lendo args capturados: %v", err)
	}
	wantArgs := "-p minikube docker-env --shell none\n"
	if string(gotArgs) != wantArgs {
		t.Fatalf("args recebidos pelo minikube fake = %q, want %q", string(gotArgs), wantArgs)
	}
}

func TestMinikubeDockerEnvResolveFalha(t *testing.T) {
	writeFakeMinikube(t, `echo "minikube não está rodando" >&2
exit 1
`)

	resolver := MinikubeDockerEnv{}
	_, err := resolver.Resolve(context.Background())
	if err == nil {
		t.Fatal("Resolve() error = nil, want erro")
	}
}
