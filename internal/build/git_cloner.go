package build

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
)

// GitCloner é o Cloner padrão do Build Broker: invoca o binário `git`
// instalado no host (mesma abordagem descrita em docs/ARCHITECTURE.md §7.3
// para o Build Engine — sem dependências extras, reaproveitando a
// ferramenta já necessária no ambiente de desenvolvimento). Ver
// docs/adrs para o racional completo desta escolha.
type GitCloner struct {
	// Credentials resolve source.credentialsSecretRef em um token de
	// acesso. Se nil, usa EnvCredentialsResolver.
	Credentials CredentialsResolver
}

// NewGitCloner cria um GitCloner com o CredentialsResolver padrão.
func NewGitCloner() *GitCloner {
	return &GitCloner{Credentials: EnvCredentialsResolver{}}
}

// Clone implementa Cloner. destDir precisa existir e estar vazio; o próprio
// `git clone` popula seu conteúdo.
func (c *GitCloner) Clone(ctx context.Context, spec CloneSpec, destDir string) (*CloneResult, error) {
	repoURL := spec.RepoURL
	if spec.CredentialsSecretRef != "" {
		resolver := c.Credentials
		if resolver == nil {
			resolver = EnvCredentialsResolver{}
		}
		token, err := resolver.Resolve(spec.CredentialsSecretRef)
		if err != nil {
			return nil, err
		}
		authenticated, err := withTokenAuth(repoURL, token)
		if err != nil {
			return nil, err
		}
		repoURL = authenticated
	}

	switch spec.Ref.Type {
	case "branch", "tag":
		if err := c.runGit(ctx, "", "clone", "--depth=1", "--branch", spec.Ref.Value, repoURL, destDir); err != nil {
			return nil, fmt.Errorf("git clone raso (branch/tag %q) falhou: %w", spec.Ref.Value, err)
		}
	case "commit":
		if err := c.runGit(ctx, "", "clone", repoURL, destDir); err != nil {
			return nil, fmt.Errorf("git clone falhou: %w", err)
		}
		if err := c.runGit(ctx, destDir, "checkout", spec.Ref.Value); err != nil {
			return nil, fmt.Errorf("git checkout do commit %q falhou: %w", spec.Ref.Value, err)
		}
	default:
		return nil, fmt.Errorf("%w: %q (esperado branch, tag ou commit)", ErrUnsupportedRefType, spec.Ref.Type)
	}

	commitSHA, err := c.revParseHead(ctx, destDir)
	if err != nil {
		return nil, err
	}

	sourceDir, err := resolveSourceDir(destDir, spec.SubPath)
	if err != nil {
		return nil, err
	}
	dockerfilePath, err := validateDockerfile(sourceDir)
	if err != nil {
		return nil, err
	}

	return &CloneResult{
		Dir:            destDir,
		CommitSHA:      commitSHA,
		DockerfilePath: dockerfilePath,
	}, nil
}

func (c *GitCloner) revParseHead(ctx context.Context, repoDir string) (string, error) {
	var stdout bytes.Buffer
	if err := c.runGitCapture(ctx, repoDir, &stdout, "rev-parse", "HEAD"); err != nil {
		return "", fmt.Errorf("git rev-parse HEAD falhou: %w", err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// runGit executa `git <args...>` com dir como working directory (vazio =
// diretório corrente), descartando stdout e reportando stderr em caso de
// erro.
func (c *GitCloner) runGit(ctx context.Context, dir string, args ...string) error {
	return c.runGitCapture(ctx, dir, nil, args...)
}

func (c *GitCloner) runGitCapture(ctx context.Context, dir string, stdout *bytes.Buffer, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	if stdout != nil {
		cmd.Stdout = stdout
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, redactSecrets(msg))
	}
	return nil
}

// withTokenAuth embute o token como usuário HTTP básico na URL do
// repositório (https://x-access-token:<token>@host/path), convenção
// suportada por GitHub/GitLab/Bitbucket para autenticação via HTTPS com
// token pessoal/de deploy. Só se aplica a repoURL com scheme http(s); outros
// esquemas (ex.: ssh) retornam erro, já que a autenticação por token neste
// formato não se aplica a eles.
func withTokenAuth(repoURL, token string) (string, error) {
	u, err := url.Parse(repoURL)
	if err != nil {
		return "", fmt.Errorf("repoUrl %q inválida: %w", repoURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("credentialsSecretRef só é suportado para repoUrl http(s), scheme informado: %q", u.Scheme)
	}
	u.User = url.UserPassword("x-access-token", token)
	return u.String(), nil
}

// userinfoPattern casa o trecho "usuário:senha@" (ou só "usuário@") de uma
// URL, sem cruzar espaços — delimitador seguro já que URLs não têm espaço.
var userinfoPattern = regexp.MustCompile(`://[^\s@]+@`)

// redactSecrets remove qualquer token embutido (via withTokenAuth) de
// mensagens de erro do git antes de propagá-las (ex.: para logs), evitando
// vazar credenciais. Defesa em profundidade: o git normalmente já omite a
// senha ao ecoar URLs, mas mensagens de transporte podem incluir a URL
// original completa.
func redactSecrets(msg string) string {
	return userinfoPattern.ReplaceAllString(msg, "://***@")
}
