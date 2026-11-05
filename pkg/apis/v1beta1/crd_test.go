package v1beta1

import (
	"os"
	"regexp"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

// The CRD in the chart is maintained by hand, so these checks keep it in
// line with the types for what the API server enforces.

func loadCRDSchema(t *testing.T) (apiextensionsv1.CustomResourceDefinitionVersion, apiextensionsv1.JSONSchemaProps) {
	t.Helper()
	data, err := os.ReadFile("../../../charts/kup6s-pages/crds/staticsites.pages.kup6s.com.yaml")
	if err != nil {
		t.Fatalf("read CRD: %v", err)
	}
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.Unmarshal(data, crd); err != nil {
		t.Fatalf("parse CRD: %v", err)
	}
	if len(crd.Spec.Versions) != 1 || crd.Spec.Versions[0].Name != "v1beta1" {
		t.Fatalf("CRD versions = %v, want only v1beta1", crd.Spec.Versions)
	}
	version := crd.Spec.Versions[0]
	return version, version.Schema.OpenAPIV3Schema.Properties["spec"]
}

func TestCRD_Visibility(t *testing.T) {
	_, spec := loadCRDSchema(t)

	visibility, ok := spec.Properties["visibility"]
	if !ok {
		t.Fatal("spec.visibility missing in the CRD")
	}
	var enum []string
	for _, v := range visibility.Enum {
		enum = append(enum, string(v.Raw))
	}
	if len(enum) != 2 || enum[0] != `"Public"` || enum[1] != `"Private"` {
		t.Errorf("visibility enum = %v, want Public, Private", enum)
	}
	if visibility.Default == nil || string(visibility.Default.Raw) != `"Public"` {
		t.Errorf("visibility default = %v, want Public", visibility.Default)
	}
}

func TestCRD_PrivateValidationMatchesTypes(t *testing.T) {
	_, spec := loadCRDSchema(t)

	source, err := os.ReadFile("types.go")
	if err != nil {
		t.Fatalf("read types.go: %v", err)
	}
	marker := regexp.MustCompile(`XValidation:rule="([^"]+)",message="([^"]+)"`).FindSubmatch(source)
	if marker == nil {
		t.Fatal("no XValidation marker in types.go")
	}

	if len(spec.XValidations) != 1 {
		t.Fatalf("spec x-kubernetes-validations = %v, want one rule", spec.XValidations)
	}
	if got := spec.XValidations[0].Rule; got != string(marker[1]) {
		t.Errorf("CRD rule = %q, types.go marker = %q", got, marker[1])
	}
	if got := spec.XValidations[0].Message; got != string(marker[2]) {
		t.Errorf("CRD message = %q, types.go marker = %q", got, marker[2])
	}
}

func TestCRD_InternalURLAndVisibilityColumn(t *testing.T) {
	version, _ := loadCRDSchema(t)

	status := version.Schema.OpenAPIV3Schema.Properties["status"]
	if _, ok := status.Properties["internalURL"]; !ok {
		t.Error("status.internalURL missing in the CRD")
	}

	found := false
	for _, col := range version.AdditionalPrinterColumns {
		if col.Name == "Visibility" && col.JSONPath == ".spec.visibility" {
			found = true
		}
	}
	if !found {
		t.Error("Visibility printer column missing in the CRD")
	}
}
