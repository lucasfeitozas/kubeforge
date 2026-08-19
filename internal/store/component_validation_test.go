package store

import (
	"encoding/json"
	"errors"
	"testing"
)

func validComponent() *Component {
	return &Component{
		Nome:          "componente-valido",
		Source:        json.RawMessage(`{"repoUrl":"https://example.com/repo.git","ref":{"type":"branch","value":"main"}}`),
		Resources:     json.RawMessage(`{"requests":{"cpu":"100m"}}`),
		Runtime:       json.RawMessage(`{"workloadKind":"Pod"}`),
		TargetContext: json.RawMessage(`{"cluster":"minikube"}`),
	}
}

func TestComponent_Validate(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Component)
		wantField string // "" significa que nenhum erro é esperado
	}{
		{"componente válido passa", func(c *Component) {}, ""},
		{"nome ausente", func(c *Component) { c.Nome = "" }, "nome"},
		{"source ausente", func(c *Component) { c.Source = nil }, "source"},
		{"source.repoUrl vazia", func(c *Component) {
			c.Source = json.RawMessage(`{"repoUrl":"","ref":{"type":"branch"}}`)
		}, "source.repoUrl"},
		{"source.repoUrl sem scheme/host", func(c *Component) {
			c.Source = json.RawMessage(`{"repoUrl":"not-a-url","ref":{"type":"branch"}}`)
		}, "source.repoUrl"},
		{"source.ref ausente", func(c *Component) {
			c.Source = json.RawMessage(`{"repoUrl":"https://example.com/repo.git"}`)
		}, "source.ref"},
		{"source.ref.type fora do enum", func(c *Component) {
			c.Source = json.RawMessage(`{"repoUrl":"https://example.com/repo.git","ref":{"type":"invalido"}}`)
		}, "source.ref.type"},
		{"resources ausente", func(c *Component) { c.Resources = nil }, "resources"},
		{"resources.requests ausente", func(c *Component) {
			c.Resources = json.RawMessage(`{}`)
		}, "resources.requests"},
		{"resources.requests vazio", func(c *Component) {
			c.Resources = json.RawMessage(`{"requests":{}}`)
		}, "resources.requests"},
		{"resources.storage.type fora do enum", func(c *Component) {
			c.Resources = json.RawMessage(`{"requests":{"cpu":"100m"},"storage":{"type":"invalido"}}`)
		}, "resources.storage.type"},
		{"runtime ausente", func(c *Component) { c.Runtime = nil }, "runtime"},
		{"runtime.workloadKind ausente", func(c *Component) {
			c.Runtime = json.RawMessage(`{}`)
		}, "runtime.workloadKind"},
		{"runtime.workloadKind fora do enum", func(c *Component) {
			c.Runtime = json.RawMessage(`{"workloadKind":"CronJob"}`)
		}, "runtime.workloadKind"},
		{"targetContext ausente", func(c *Component) { c.TargetContext = nil }, "targetContext"},
		{"targetContext.cluster fora do enum", func(c *Component) {
			c.TargetContext = json.RawMessage(`{"cluster":"aws"}`)
		}, "targetContext.cluster"},
		{"build ausente não gera erro", func(c *Component) {}, ""},
		{"build.strategy fora do enum quando build presente", func(c *Component) {
			c.Build = json.RawMessage(`{"strategy":"invalido"}`)
		}, "build.strategy"},
		{"lifecycle.cleanupPolicy fora do enum quando lifecycle presente", func(c *Component) {
			c.Lifecycle = json.RawMessage(`{"cleanupPolicy":"invalido"}`)
		}, "lifecycle.cleanupPolicy"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validComponent()
			tt.mutate(c)
			err := c.Validate()

			if tt.wantField == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}

			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("Validate() error = %v, want *ValidationError", err)
			}
			found := false
			for _, f := range ve.Fields {
				if f.Field == tt.wantField {
					found = true
				}
			}
			if !found {
				t.Fatalf("Validate() fields = %+v, want field %q present", ve.Fields, tt.wantField)
			}
		})
	}
}

func TestComponent_Validate_AggregatesMultipleErrors(t *testing.T) {
	c := validComponent()
	c.Source = json.RawMessage(`{"repoUrl":"not-a-url"}`) // repoUrl inválida + ref ausente
	c.Runtime = json.RawMessage(`{"workloadKind":"CronJob"}`)
	c.Resources = json.RawMessage(`{}`) // requests ausente

	err := c.Validate()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("Validate() error = %v, want *ValidationError", err)
	}

	want := []string{"source.repoUrl", "source.ref", "runtime.workloadKind", "resources.requests"}
	for _, field := range want {
		found := false
		for _, f := range ve.Fields {
			if f.Field == field {
				found = true
			}
		}
		if !found {
			t.Errorf("Validate() fields = %+v, missing expected field %q", ve.Fields, field)
		}
	}
	if len(ve.Fields) < len(want) {
		t.Errorf("Validate() aggregated %d errors, want at least %d (proves not fail-fast)", len(ve.Fields), len(want))
	}
}

func TestComponent_Validate_MalformedJSON(t *testing.T) {
	c := validComponent()
	c.Source = json.RawMessage(`{not valid json`)

	err := c.Validate()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("Validate() error = %v, want *ValidationError", err)
	}
	if len(ve.Fields) != 1 || ve.Fields[0].Field != "source" {
		t.Fatalf("Validate() fields = %+v, want single field error on %q", ve.Fields, "source")
	}
}
