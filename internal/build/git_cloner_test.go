package build

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runGitFixture executa git na criação dos repositórios de teste, falhando
// o teste imediatamente em caso de erro.
func runGitFixture(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=kubeforge-test",
		"GIT_AUTHOR_EMAIL=kubeforge-test@example.com",
		"GIT_COMMITTER_NAME=kubeforge-test",
		"GIT_COMMITTER_EMAIL=kubeforge-test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newFixtureRepo cria um repositório git local com um Dockerfile na branch
// "main", uma tag "v1.0.0" e devolve os SHAs de cada commit relevante, para
// exercitar os três tipos de ref suportados (branch, tag, commit).
type fixtureRepo struct {
	dir string
	// url é dir com o esquema file://, necessário para exercitar --depth=1:
	// o git ignora --depth em clones de um caminho local puro (otimização
	// de "local clone" via hardlink), mas o respeita com file://, que é o
	// caminho mais fiel ao comportamento real com https://.
	url              string
	mainCommit       string
	taggedCommit     string
	beforeDockerfile string
}

func newFixtureRepo(t *testing.T) fixtureRepo {
	t.Helper()
	dir := t.TempDir()

	runGitFixture(t, dir, "init", "--initial-branch=main")
	runGitFixture(t, dir, "config", "commit.gpgsign", "false")
	runGitFixture(t, dir, "config", "tag.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("fixture repo"), 0o644); err != nil {
		t.Fatalf("writing README.md: %v", err)
	}
	runGitFixture(t, dir, "add", "README.md")
	runGitFixture(t, dir, "commit", "-m", "commit sem Dockerfile")
	beforeDockerfile := runGitFixture(t, dir, "rev-parse", "HEAD")

	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch"), 0o644); err != nil {
		t.Fatalf("writing Dockerfile: %v", err)
	}
	runGitFixture(t, dir, "add", "Dockerfile")
	runGitFixture(t, dir, "commit", "-m", "adiciona Dockerfile")
	taggedCommit := runGitFixture(t, dir, "rev-parse", "HEAD")
	runGitFixture(t, dir, "tag", "v1.0.0")

	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatalf("writing main.go: %v", err)
	}
	runGitFixture(t, dir, "add", "main.go")
	runGitFixture(t, dir, "commit", "-m", "commit adicional em main")
	mainCommit := runGitFixture(t, dir, "rev-parse", "HEAD")

	return fixtureRepo{
		dir:              dir,
		url:              "file://" + dir,
		mainCommit:       mainCommit,
		taggedCommit:     taggedCommit,
		beforeDockerfile: beforeDockerfile,
	}
}

func TestGitClonerClonaBranchRaso(t *testing.T) {
	repo := newFixtureRepo(t)
	dest := filepath.Join(t.TempDir(), "checkout")

	cloner := NewGitCloner()
	result, err := cloner.Clone(context.Background(), CloneSpec{
		RepoURL: repo.url,
		Ref:     RefSpec{Type: "branch", Value: "main"},
	}, dest)
	if err != nil {
		t.Fatalf("Clone() error = %v", err)
	}

	if result.CommitSHA != repo.mainCommit {
		t.Errorf("CommitSHA = %q, want %q", result.CommitSHA, repo.mainCommit)
	}
	if result.DockerfilePath != filepath.Join(dest, "Dockerfile") {
		t.Errorf("DockerfilePath = %q, want %q", result.DockerfilePath, filepath.Join(dest, "Dockerfile"))
	}
	if _, err := os.Stat(filepath.Join(dest, ".git", "shallow")); err != nil {
		t.Errorf("esperava clone raso (--depth=1), arquivo .git/shallow não encontrado: %v", err)
	}
}

func TestGitClonerClonaTagRasa(t *testing.T) {
	repo := newFixtureRepo(t)
	dest := filepath.Join(t.TempDir(), "checkout")

	cloner := NewGitCloner()
	result, err := cloner.Clone(context.Background(), CloneSpec{
		RepoURL: repo.url,
		Ref:     RefSpec{Type: "tag", Value: "v1.0.0"},
	}, dest)
	if err != nil {
		t.Fatalf("Clone() error = %v", err)
	}

	if result.CommitSHA != repo.taggedCommit {
		t.Errorf("CommitSHA = %q, want %q", result.CommitSHA, repo.taggedCommit)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git", "shallow")); err != nil {
		t.Errorf("esperava clone raso (--depth=1) para tag, arquivo .git/shallow não encontrado: %v", err)
	}
}

func TestGitClonerCheckoutDeCommitEspecifico(t *testing.T) {
	repo := newFixtureRepo(t)
	dest := filepath.Join(t.TempDir(), "checkout")

	cloner := NewGitCloner()
	result, err := cloner.Clone(context.Background(), CloneSpec{
		RepoURL: repo.url,
		Ref:     RefSpec{Type: "commit", Value: repo.taggedCommit},
	}, dest)
	if err != nil {
		t.Fatalf("Clone() error = %v", err)
	}

	if result.CommitSHA != repo.taggedCommit {
		t.Errorf("CommitSHA = %q, want %q", result.CommitSHA, repo.taggedCommit)
	}
	// Um checkout por commit específico não deve ser raso: precisa de todo o
	// histórico disponível para alcançar o commit pedido.
	if _, err := os.Stat(filepath.Join(dest, ".git", "shallow")); err == nil {
		t.Errorf("checkout por commit não deveria ser raso, mas .git/shallow existe")
	}
}

func TestGitClonerFalhaSemDockerfile(t *testing.T) {
	repo := newFixtureRepo(t)
	dest := filepath.Join(t.TempDir(), "checkout")

	cloner := NewGitCloner()
	_, err := cloner.Clone(context.Background(), CloneSpec{
		RepoURL: repo.url,
		Ref:     RefSpec{Type: "commit", Value: repo.beforeDockerfile},
	}, dest)
	if err == nil {
		t.Fatal("esperava erro por ausência de Dockerfile, obteve nil")
	}
	if !strings.Contains(err.Error(), "Dockerfile") {
		t.Errorf("erro = %v, esperava menção a Dockerfile", err)
	}
}

func TestGitClonerRefTypeInvalido(t *testing.T) {
	repo := newFixtureRepo(t)
	dest := filepath.Join(t.TempDir(), "checkout")

	cloner := NewGitCloner()
	_, err := cloner.Clone(context.Background(), CloneSpec{
		RepoURL: repo.url,
		Ref:     RefSpec{Type: "release", Value: "v1"},
	}, dest)
	if err == nil {
		t.Fatal("esperava erro para ref.type inválido, obteve nil")
	}
}

func TestGitClonerRespeitaSubPath(t *testing.T) {
	repo := newFixtureRepo(t)

	sub := filepath.Join(repo.dir, "servico-a")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir subPath: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "Dockerfile"), []byte("FROM scratch"), 0o644); err != nil {
		t.Fatalf("writing Dockerfile em subPath: %v", err)
	}
	runGitFixture(t, repo.dir, "add", "servico-a/Dockerfile")
	runGitFixture(t, repo.dir, "commit", "-m", "adiciona subPath monorepo")

	dest := filepath.Join(t.TempDir(), "checkout")
	cloner := NewGitCloner()
	result, err := cloner.Clone(context.Background(), CloneSpec{
		RepoURL: repo.url,
		Ref:     RefSpec{Type: "branch", Value: "main"},
		SubPath: "servico-a",
	}, dest)
	if err != nil {
		t.Fatalf("Clone() error = %v", err)
	}
	if result.DockerfilePath != filepath.Join(dest, "servico-a", "Dockerfile") {
		t.Errorf("DockerfilePath = %q, want %q", result.DockerfilePath, filepath.Join(dest, "servico-a", "Dockerfile"))
	}
}

func TestGitClonerRejeitaSubPathEscapando(t *testing.T) {
	repo := newFixtureRepo(t)
	dest := filepath.Join(t.TempDir(), "checkout")

	cloner := NewGitCloner()
	_, err := cloner.Clone(context.Background(), CloneSpec{
		RepoURL: repo.url,
		Ref:     RefSpec{Type: "branch", Value: "main"},
		SubPath: "../../etc",
	}, dest)
	if err == nil {
		t.Fatal("esperava erro para subPath que escapa do diretório clonado, obteve nil")
	}
}

func TestGitClonerCredenciaisNaoEncontradas(t *testing.T) {
	repo := newFixtureRepo(t)
	dest := filepath.Join(t.TempDir(), "checkout")

	cloner := NewGitCloner()
	_, err := cloner.Clone(context.Background(), CloneSpec{
		RepoURL:              "https://example.invalid/" + repo.dir,
		Ref:                  RefSpec{Type: "branch", Value: "main"},
		CredentialsSecretRef: "repo-privado-inexistente",
	}, dest)
	if err == nil {
		t.Fatal("esperava erro por credenciais ausentes, obteve nil")
	}
	if !strings.Contains(err.Error(), "credentialsSecretRef") && !strings.Contains(err.Error(), "credenciais") {
		t.Errorf("erro = %v, esperava menção a credenciais ausentes", err)
	}
}

func TestGitClonerEmbuteTokenNaURLQuandoCredenciaisPresentes(t *testing.T) {
	const secretRef = "repo-privado-teste"
	const token = "ghp_teste123"
	t.Setenv(envKeyFor(secretRef), token)

	// O clone autenticado de um repositório real exigiria um servidor HTTP
	// git de teste; aqui validamos as duas peças que compõem esse caminho no
	// GitCloner: a resolução da credencial e a montagem da URL autenticada.
	authenticated, err := withTokenAuth("https://example.com/org/repo.git", token)
	if err != nil {
		t.Fatalf("withTokenAuth() error = %v", err)
	}
	if !strings.Contains(authenticated, "x-access-token:"+token+"@") {
		t.Errorf("URL autenticada = %q, esperava conter token embutido", authenticated)
	}

	resolver := EnvCredentialsResolver{}
	got, err := resolver.Resolve(secretRef)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got != token {
		t.Errorf("Resolve() = %q, want %q", got, token)
	}
}

func TestGitClonerRespeitaContextCancelado(t *testing.T) {
	repo := newFixtureRepo(t)
	dest := filepath.Join(t.TempDir(), "checkout")

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)

	cloner := NewGitCloner()
	_, err := cloner.Clone(ctx, CloneSpec{
		RepoURL: repo.url,
		Ref:     RefSpec{Type: "branch", Value: "main"},
	}, dest)
	if err == nil {
		t.Fatal("esperava erro com contexto já expirado, obteve nil")
	}
}
