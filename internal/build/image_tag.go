package build

import (
	"errors"
	"fmt"
)

// ImageTagStrategyCommitSHA é a única estratégia de tag suportada no MVP,
// conforme docs/EPICOS_E_HISTORIAS.md:101 ("commit-sha como padrão do MVP").
// As estratégias timestamp/semver citadas em docs/ARCHITECTURE.md:113-114
// ficam fora de escopo desta história.
const ImageTagStrategyCommitSHA = "commit-sha"

// commitSHATagLength é o número de caracteres do commit SHA usados na tag,
// convenção comum de tags de imagem OCI/CI (ex.: `git rev-parse --short`
// tende a 7-12 caracteres; 12 reduz ainda mais o risco de colisão mantendo a
// tag curta).
const commitSHATagLength = 12

// ErrUnsupportedImageTagStrategy é retornado quando build.imageTagStrategy
// não é um dos valores suportados.
var ErrUnsupportedImageTagStrategy = errors.New("estratégia de tag de imagem não suportada")

// BuildImageTag monta a tag completa (repository:tag) de uma imagem a partir
// de repository, da estratégia informada (vazio equivale a
// ImageTagStrategyCommitSHA) e do commitSHA completo resolvido pelo Cloner
// (CloneResult.CommitSHA).
func BuildImageTag(repository, strategy, commitSHA string) (string, error) {
	if strategy == "" {
		strategy = ImageTagStrategyCommitSHA
	}

	switch strategy {
	case ImageTagStrategyCommitSHA:
		if len(commitSHA) < commitSHATagLength {
			return "", fmt.Errorf("commitSHA %q é curto demais para a estratégia %q", commitSHA, ImageTagStrategyCommitSHA)
		}
		return fmt.Sprintf("%s:%s", repository, commitSHA[:commitSHATagLength]), nil
	default:
		return "", fmt.Errorf("%w: %q (esperado: %s)", ErrUnsupportedImageTagStrategy, strategy, ImageTagStrategyCommitSHA)
	}
}
