package controller

import (
	"os"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

// crdManifestPath aponta para o manifesto versionado que deve ser aplicado
// manualmente no cluster (ver docs/EPICOS_E_HISTORIAS.md, E1.S4).
const crdManifestPath = "../../deployments/minikube/crd.yaml"

func loadCRD(t *testing.T) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()

	raw, err := os.ReadFile(crdManifestPath)
	if err != nil {
		t.Fatalf("erro ao ler %q: %v", crdManifestPath, err)
	}

	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.UnmarshalStrict(raw, &crd); err != nil {
		t.Fatalf("erro ao decodificar CRD em %q: %v", crdManifestPath, err)
	}
	return &crd
}

func TestCRD_MetadadosBasicos(t *testing.T) {
	crd := loadCRD(t)

	if crd.APIVersion != "apiextensions.k8s.io/v1" {
		t.Errorf("apiVersion = %q, esperava %q", crd.APIVersion, "apiextensions.k8s.io/v1")
	}
	if crd.Kind != "CustomResourceDefinition" {
		t.Errorf("kind = %q, esperava %q", crd.Kind, "CustomResourceDefinition")
	}
	if crd.Name != "kubeforgecomponents.kubeforge.io" {
		t.Errorf("metadata.name = %q, esperava %q", crd.Name, "kubeforgecomponents.kubeforge.io")
	}
	if crd.Spec.Group != "kubeforge.io" {
		t.Errorf("spec.group = %q, esperava %q", crd.Spec.Group, "kubeforge.io")
	}
	if crd.Spec.Scope != apiextensionsv1.NamespaceScoped {
		t.Errorf("spec.scope = %q, esperava %q", crd.Spec.Scope, apiextensionsv1.NamespaceScoped)
	}
}

func TestCRD_Names(t *testing.T) {
	crd := loadCRD(t)
	names := crd.Spec.Names

	if names.Kind != "KubeForgeComponent" {
		t.Errorf("spec.names.kind = %q, esperava %q", names.Kind, "KubeForgeComponent")
	}
	if names.ListKind != "KubeForgeComponentList" {
		t.Errorf("spec.names.listKind = %q, esperava %q", names.ListKind, "KubeForgeComponentList")
	}
	if names.Plural != "kubeforgecomponents" {
		t.Errorf("spec.names.plural = %q, esperava %q", names.Plural, "kubeforgecomponents")
	}
	if names.Singular != "kubeforgecomponent" {
		t.Errorf("spec.names.singular = %q, esperava %q", names.Singular, "kubeforgecomponent")
	}
	if len(names.ShortNames) != 1 || names.ShortNames[0] != "kfc" {
		t.Errorf("spec.names.shortNames = %v, esperava [kfc]", names.ShortNames)
	}
}

func TestCRD_VersaoV1Alpha1(t *testing.T) {
	crd := loadCRD(t)

	if len(crd.Spec.Versions) != 1 {
		t.Fatalf("esperava exatamente 1 versão registrada, obteve %d", len(crd.Spec.Versions))
	}

	v := crd.Spec.Versions[0]
	if v.Name != "v1alpha1" {
		t.Errorf("versions[0].name = %q, esperava %q", v.Name, "v1alpha1")
	}
	if !v.Served {
		t.Error("versions[0].served deveria ser true")
	}
	if !v.Storage {
		t.Error("versions[0].storage deveria ser true")
	}
	if v.Subresources == nil || v.Subresources.Status == nil {
		t.Error("versions[0].subresources.status deveria estar habilitado")
	}
}

func TestCRD_SchemaSpecCamposObrigatorios(t *testing.T) {
	crd := loadCRD(t)

	schema := crd.Spec.Versions[0].Schema
	if schema == nil || schema.OpenAPIV3Schema == nil {
		t.Fatal("versions[0].schema.openAPIV3Schema ausente")
	}

	specProps, ok := schema.OpenAPIV3Schema.Properties["spec"]
	if !ok {
		t.Fatal("schema não define properties.spec")
	}

	wantRequired := []string{"source", "resources", "runtime", "targetContext"}
	gotRequired := map[string]bool{}
	for _, r := range specProps.Required {
		gotRequired[r] = true
	}
	for _, field := range wantRequired {
		if !gotRequired[field] {
			t.Errorf("spec.required não contém %q (obtido: %v)", field, specProps.Required)
		}
	}

	// Confere que todos os grupos de campos exigidos pela história E1.S4
	// (source, build, resources, runtime, hooks, targetContext, lifecycle, status)
	// estão de fato presentes no schema.
	wantSpecGroups := []string{"source", "build", "resources", "runtime", "hooks", "targetContext", "lifecycle"}
	for _, group := range wantSpecGroups {
		if _, ok := specProps.Properties[group]; !ok {
			t.Errorf("spec.properties não contém o grupo %q", group)
		}
	}

	if _, ok := schema.OpenAPIV3Schema.Properties["status"]; !ok {
		t.Error("schema não define properties.status")
	}
}

func TestCRD_SchemaEnumsCriticos(t *testing.T) {
	crd := loadCRD(t)
	specProps := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]

	targetContext := specProps.Properties["targetContext"]
	cluster := targetContext.Properties["cluster"]
	wantClusters := map[string]bool{"minikube": true, "eks": true}
	if len(cluster.Enum) != len(wantClusters) {
		t.Fatalf("targetContext.cluster.enum tem %d valores, esperava %d", len(cluster.Enum), len(wantClusters))
	}
	for _, e := range cluster.Enum {
		var v string
		if err := yaml.Unmarshal(e.Raw, &v); err != nil {
			t.Fatalf("erro ao decodificar valor de enum: %v", err)
		}
		if !wantClusters[v] {
			t.Errorf("targetContext.cluster.enum contém valor inesperado: %q", v)
		}
	}

	runtime := specProps.Properties["runtime"]
	if len(runtime.Required) == 0 {
		t.Error("runtime deveria ter ao menos um campo obrigatório (workloadKind)")
	}
}
