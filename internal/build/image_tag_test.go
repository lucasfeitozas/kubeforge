package build

import (
	"errors"
	"testing"
)

func TestBuildImageTag(t *testing.T) {
	const commitSHA = "abcdef1234567890abcdef1234567890abcdef12"

	tests := []struct {
		name       string
		repository string
		strategy   string
		commitSHA  string
		wantTag    string
		wantErr    bool
		wantErrIs  error
	}{
		{
			name:       "estratégia commit-sha explícita",
			repository: "kubeforge/carga-cpu",
			strategy:   ImageTagStrategyCommitSHA,
			commitSHA:  commitSHA,
			wantTag:    "kubeforge/carga-cpu:abcdef123456",
		},
		{
			name:       "estratégia vazia usa commit-sha como default",
			repository: "kubeforge/carga-cpu",
			strategy:   "",
			commitSHA:  commitSHA,
			wantTag:    "kubeforge/carga-cpu:abcdef123456",
		},
		{
			name:       "estratégia não suportada",
			repository: "kubeforge/carga-cpu",
			strategy:   "timestamp",
			commitSHA:  commitSHA,
			wantErr:    true,
			wantErrIs:  ErrUnsupportedImageTagStrategy,
		},
		{
			name:       "commitSHA curto demais",
			repository: "kubeforge/carga-cpu",
			strategy:   ImageTagStrategyCommitSHA,
			commitSHA:  "abc123",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildImageTag(tt.repository, tt.strategy, tt.commitSHA)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("BuildImageTag() error = nil, want erro")
				}
				if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
					t.Fatalf("BuildImageTag() error = %v, want errors.Is %v", err, tt.wantErrIs)
				}
				return
			}
			if err != nil {
				t.Fatalf("BuildImageTag() unexpected error = %v", err)
			}
			if got != tt.wantTag {
				t.Fatalf("BuildImageTag() = %q, want %q", got, tt.wantTag)
			}
		})
	}
}
