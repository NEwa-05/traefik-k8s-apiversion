package migrate

import (
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const lastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// serverMetadataFields are populated by the API server and must not be sent
// when creating the object under the new group.
var serverMetadataFields = []string{
	"resourceVersion",
	"uid",
	"creationTimestamp",
	"generation",
	"managedFields",
	"selfLink",
}

// Convert returns a copy of obj ready to be created under the traefik.io group.
// Every apiVersion referring to traefik.containo.us is rewritten, and the
// server-populated metadata, the status and the kubectl last-applied
// annotation are removed. The returned warnings describe lossy changes.
func Convert(obj *unstructured.Unstructured) (converted *unstructured.Unstructured, warnings []string) {
	converted = obj.DeepCopy()

	// An owner reference holds the UID of the traefik.containo.us owner. Once
	// that owner is removed by the migration, the garbage collector would
	// delete the new object, so the reference is dropped instead of rewritten.
	if refs := converted.GetOwnerReferences(); len(refs) > 0 {
		var kept []metav1.OwnerReference
		for _, ref := range refs {
			if isOldAPIVersion(ref.APIVersion) {
				warnings = append(warnings, fmt.Sprintf("dropped ownerReference to %s %q: it points to the %s object", ref.Kind, ref.Name, OldGroup))
				continue
			}
			kept = append(kept, ref)
		}
		converted.SetOwnerReferences(kept)
	}

	rewriteAPIVersions(converted.Object)

	for _, field := range serverMetadataFields {
		unstructured.RemoveNestedField(converted.Object, "metadata", field)
	}
	unstructured.RemoveNestedField(converted.Object, "status")

	if annotations := converted.GetAnnotations(); annotations != nil {
		delete(annotations, lastAppliedAnnotation)
		if len(annotations) == 0 {
			annotations = nil
		}
		converted.SetAnnotations(annotations)
	}

	return converted, warnings
}

// rewriteAPIVersions walks v and rewrites every apiVersion field referring to
// the old group.
func rewriteAPIVersions(v any) {
	switch t := v.(type) {
	case map[string]any:
		for key, val := range t {
			if s, ok := val.(string); ok && key == "apiVersion" {
				t[key] = newAPIVersion(s)
				continue
			}
			rewriteAPIVersions(val)
		}
	case []any:
		for _, e := range t {
			rewriteAPIVersions(e)
		}
	}
}

func isOldAPIVersion(apiVersion string) bool {
	return strings.HasPrefix(apiVersion, OldGroup+"/")
}

func newAPIVersion(apiVersion string) string {
	if version, ok := strings.CutPrefix(apiVersion, OldGroup+"/"); ok {
		return NewGroup + "/" + version
	}
	return apiVersion
}
