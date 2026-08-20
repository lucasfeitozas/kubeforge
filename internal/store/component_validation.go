package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// FieldError descreve uma violação de validação em um campo específico do Componente.
type FieldError struct {
	Field   string
	Message string
}

func (e FieldError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// ValidationError agrega uma ou mais violações de validação encontradas em um
// Componente, permitindo reportar todos os erros de campo de uma só vez em vez
// de interromper na primeira falha (fail-fast).
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	msgs := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		msgs[i] = f.Error()
	}
	return "componente inválido: " + strings.Join(msgs, "; ")
}

func (e *ValidationError) add(field, message string) {
	e.Fields = append(e.Fields, FieldError{Field: field, Message: message})
}

func (e *ValidationError) hasErrors() bool {
	return len(e.Fields) > 0
}

type sourceBlock struct {
	RepoURL string    `json:"repoUrl"`
	Ref     *refBlock `json:"ref"`
}

type refBlock struct {
	Type string `json:"type"`
}

type buildBlock struct {
	Strategy string `json:"strategy"`
}

type resourcesBlock struct {
	Requests json.RawMessage `json:"requests"`
	Storage  *storageBlock   `json:"storage"`
}

type storageBlock struct {
	Type string `json:"type"`
}

type runtimeBlock struct {
	WorkloadKind string `json:"workloadKind"`
}

type targetContextBlock struct {
	Cluster string `json:"cluster"`
}

type lifecycleBlock struct {
	CleanupPolicy string `json:"cleanupPolicy"`
}

var (
	validRefTypes        = map[string]bool{"branch": true, "tag": true, "commit": true}
	validWorkloadKinds   = map[string]bool{"Pod": true, "Job": true, "Deployment": true}
	validStorageTypes    = map[string]bool{"ephemeral": true, "pvc": true}
	validClusters        = map[string]bool{"minikube": true, "eks": true}
	validBuildStrategies = map[string]bool{"kaniko": true, "buildpacks": true, "doib": true}
	validCleanupPolicies = map[string]bool{
		"onSuccess": true, "onFailure": true, "onSuccessAndFailure": true, "manual": true,
	}
)

// Validate verifica se o Componente satisfaz os campos obrigatórios e os
// enums definidos no schema (docs/ARCHITECTURE.md §2.2), agregando todas as
// violações encontradas em um único erro.
func (c *Component) Validate() error {
	ve := &ValidationError{}

	if strings.TrimSpace(c.Nome) == "" {
		ve.add("nome", "campo obrigatório")
	}

	validateSource(c.Source, ve)
	validateResources(c.Resources, ve)
	validateRuntime(c.Runtime, ve)
	validateTargetContext(c.TargetContext, ve)
	validateBuild(c.Build, ve)
	validateLifecycle(c.Lifecycle, ve)

	if !ve.hasErrors() {
		return nil
	}
	return ve
}

func validateSource(raw json.RawMessage, ve *ValidationError) {
	if len(raw) == 0 {
		ve.add("source", "campo obrigatório")
		return
	}
	var s sourceBlock
	if err := json.Unmarshal(raw, &s); err != nil {
		ve.add("source", "JSON inválido")
		return
	}

	if strings.TrimSpace(s.RepoURL) == "" {
		ve.add("source.repoUrl", "campo obrigatório")
	} else if u, err := url.Parse(s.RepoURL); err != nil || u.Scheme == "" || u.Host == "" {
		ve.add("source.repoUrl", "deve ser uma URI absoluta válida (com scheme e host)")
	}

	if s.Ref == nil {
		ve.add("source.ref", "campo obrigatório")
	} else if s.Ref.Type != "" && !validRefTypes[s.Ref.Type] {
		ve.add("source.ref.type", "deve ser um dos valores: branch, tag, commit")
	}
}

func validateResources(raw json.RawMessage, ve *ValidationError) {
	if len(raw) == 0 {
		ve.add("resources", "campo obrigatório")
		return
	}
	var r resourcesBlock
	if err := json.Unmarshal(raw, &r); err != nil {
		ve.add("resources", "JSON inválido")
		return
	}

	validateResourcesRequests(r.Requests, ve)

	if r.Storage != nil && r.Storage.Type != "" && !validStorageTypes[r.Storage.Type] {
		ve.add("resources.storage.type", "deve ser um dos valores: ephemeral, pvc")
	}
}

func validateResourcesRequests(raw json.RawMessage, ve *ValidationError) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		ve.add("resources.requests", "campo obrigatório")
		return
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		ve.add("resources.requests", "deve ser um objeto JSON")
		return
	}
	if len(m) == 0 {
		ve.add("resources.requests", "não pode ser vazio")
	}
}

func validateRuntime(raw json.RawMessage, ve *ValidationError) {
	if len(raw) == 0 {
		ve.add("runtime", "campo obrigatório")
		return
	}
	var r runtimeBlock
	if err := json.Unmarshal(raw, &r); err != nil {
		ve.add("runtime", "JSON inválido")
		return
	}
	if strings.TrimSpace(r.WorkloadKind) == "" {
		ve.add("runtime.workloadKind", "campo obrigatório")
	} else if !validWorkloadKinds[r.WorkloadKind] {
		ve.add("runtime.workloadKind", "deve ser um dos valores: Pod, Job, Deployment")
	}
}

func validateTargetContext(raw json.RawMessage, ve *ValidationError) {
	if len(raw) == 0 {
		ve.add("targetContext", "campo obrigatório")
		return
	}
	var t targetContextBlock
	if err := json.Unmarshal(raw, &t); err != nil {
		ve.add("targetContext", "JSON inválido")
		return
	}
	if strings.TrimSpace(t.Cluster) == "" {
		ve.add("targetContext.cluster", "campo obrigatório")
	} else if !validClusters[t.Cluster] {
		ve.add("targetContext.cluster", "deve ser um dos valores: minikube, eks")
	}
}

func validateBuild(raw json.RawMessage, ve *ValidationError) {
	if len(raw) == 0 {
		return // bloco opcional
	}
	var b buildBlock
	if err := json.Unmarshal(raw, &b); err != nil {
		ve.add("build", "JSON inválido")
		return
	}
	if b.Strategy != "" && !validBuildStrategies[b.Strategy] {
		ve.add("build.strategy", "deve ser um dos valores: kaniko, buildpacks, doib")
	}
}

func validateLifecycle(raw json.RawMessage, ve *ValidationError) {
	if len(raw) == 0 {
		return // bloco opcional
	}
	var l lifecycleBlock
	if err := json.Unmarshal(raw, &l); err != nil {
		ve.add("lifecycle", "JSON inválido")
		return
	}
	if l.CleanupPolicy != "" && !validCleanupPolicies[l.CleanupPolicy] {
		ve.add("lifecycle.cleanupPolicy", "deve ser um dos valores: onSuccess, onFailure, onSuccessAndFailure, manual")
	}
}
