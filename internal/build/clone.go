package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// dockerfileName é o nome de arquivo padrão validado após o clone. A escolha
// de um caminho customizado (spec.build.dockerfilePath) fica fora do escopo
// desta história (E3.S1), que só precisa garantir que o código clonado é
// buildável.
const dockerfileName = "Dockerfile"

// RefSpec identifica o ponto do repositório a ser obtido, espelhando
// source.ref no schema do Componente (docs/ARCHITECTURE.md §2.2).
type RefSpec struct {
	// Type é "branch", "tag" ou "commit".
	Type string
	// Value é o nome da branch/tag ou o SHA do commit.
	Value string
}

// CloneSpec descreve a origem do código-fonte de um Componente, espelhando o
// bloco source do schema (docs/ARCHITECTURE.md §2.2).
type CloneSpec struct {
	// RepoURL é a URL do repositório Git (HTTPS).
	RepoURL string
	// Ref indica branch, tag ou commit a obter.
	Ref RefSpec
	// SubPath é o diretório dentro do repositório onde o código do
	// Componente vive (monorepo). Vazio ou "/" significa a raiz.
	SubPath string
	// CredentialsSecretRef referencia, por nome lógico, o token de acesso
	// necessário para repositórios privados. Vazio significa repositório
	// público, sem autenticação.
	CredentialsSecretRef string
}

// CloneResult descreve o resultado de um clone bem-sucedido.
type CloneResult struct {
	// Dir é o diretório local contendo o checkout do repositório.
	Dir string
	// CommitSHA é o commit efetivamente extraído (resolvido via `git
	// rev-parse HEAD` após o checkout).
	CommitSHA string
	// DockerfilePath é o caminho absoluto do Dockerfile validado dentro de
	// Dir/SubPath.
	DockerfilePath string
}

var (
	// ErrUnsupportedRefType é retornado quando spec.Ref.Type não é um dos
	// valores suportados pelo schema (branch, tag, commit).
	ErrUnsupportedRefType = errors.New("tipo de ref não suportado")
	// ErrDockerfileNotFound é retornado quando o repositório clonado (ou o
	// subPath informado) não contém um Dockerfile.
	ErrDockerfileNotFound = errors.New("Dockerfile não encontrado no repositório")
)

// Cloner obtém o código-fonte de um Componente em um diretório local,
// respeitando source.ref e, quando aplicável, source.credentialsSecretRef.
type Cloner interface {
	Clone(ctx context.Context, spec CloneSpec, destDir string) (*CloneResult, error)
}

// resolveSourceDir junta destDir com spec.SubPath, validando que o resultado
// não escapa de destDir (proteção contra subPath malicioso como "../../etc").
func resolveSourceDir(destDir, subPath string) (string, error) {
	if subPath == "" || subPath == "/" {
		return destDir, nil
	}

	joined := filepath.Join(destDir, subPath)
	rel, err := filepath.Rel(destDir, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("subPath %q é inválido: escapa do diretório do repositório", subPath)
	}
	return joined, nil
}

// validateDockerfile confere se sourceDir contém um Dockerfile, retornando
// seu caminho absoluto.
func validateDockerfile(sourceDir string) (string, error) {
	path := filepath.Join(sourceDir, dockerfileName)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w em %q", ErrDockerfileNotFound, sourceDir)
		}
		return "", fmt.Errorf("erro ao verificar Dockerfile em %q: %w", path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%w: %q é um diretório, não um arquivo", ErrDockerfileNotFound, path)
	}
	return path, nil
}
