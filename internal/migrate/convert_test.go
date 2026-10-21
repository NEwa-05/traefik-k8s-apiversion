package migrate

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestConvert(t *testing.T) {
	source := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "traefik.containo.us/v1alpha1",
		"kind":       "IngressRoute",
		"metadata": map[string]any{
			"name":              "whoami",
			"namespace":         "default",
			"uid":               "1234",
			"resourceVersion":   "42",
			"generation":        int64(3),
			"creationTimestamp": "2024-01-01T00:00:00Z",
			"managedFields":     []any{map[string]any{"manager": "kubectl"}},
			"labels":            map[string]any{"app": "whoami"},
			"annotations": map[string]any{
				lastAppliedAnnotation: "{}",
				"team":                "web",
			},
			"ownerReferences": []any{
				map[string]any{"apiVersion": "traefik.containo.us/v1alpha1", "kind": "Middleware", "name": "owner", "uid": "abcd"},
				map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "app", "uid": "efgh"},
			},
		},
		"spec": map[string]any{
			"routes": []any{
				map[string]any{
					"match":       "Host(`whoami.example.com`)",
					"middlewares": []any{map[string]any{"name": "auth", "apiVersion": "traefik.containo.us/v1alpha1"}},
				},
			},
		},
		"status": map[string]any{"ready": true},
	}}

	got, warnings := Convert(source)

	want := map[string]any{
		"apiVersion": "traefik.io/v1alpha1",
		"kind":       "IngressRoute",
		"metadata": map[string]any{
			"name":        "whoami",
			"namespace":   "default",
			"labels":      map[string]any{"app": "whoami"},
			"annotations": map[string]any{"team": "web"},
			"ownerReferences": []any{
				map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "app", "uid": "efgh"},
			},
		},
		"spec": map[string]any{
			"routes": []any{
				map[string]any{
					"match":       "Host(`whoami.example.com`)",
					"middlewares": []any{map[string]any{"name": "auth", "apiVersion": "traefik.io/v1alpha1"}},
				},
			},
		},
	}
	if !reflect.DeepEqual(got.Object, want) {
		t.Errorf("Convert() =\n%#v\nwant\n%#v", got.Object, want)
	}
	if len(warnings) != 1 {
		t.Errorf("Convert() warnings = %q, want 1 warning about the dropped ownerReference", warnings)
	}
	if source.GetAPIVersion() != "traefik.containo.us/v1alpha1" || source.GetUID() != "1234" {
		t.Error("Convert() modified its input")
	}
}

func TestConvertRemovesEmptyMetadataMaps(t *testing.T) {
	source := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "traefik.containo.us/v1alpha1",
		"kind":       "Middleware",
		"metadata": map[string]any{
			"name":            "auth",
			"namespace":       "default",
			"annotations":     map[string]any{lastAppliedAnnotation: "{}"},
			"ownerReferences": []any{map[string]any{"apiVersion": "traefik.containo.us/v1alpha1", "kind": "Middleware", "name": "owner", "uid": "abcd"}},
		},
	}}

	got, _ := Convert(source)

	metadata := got.Object["metadata"].(map[string]any)
	for _, field := range []string{"annotations", "ownerReferences"} {
		if _, ok := metadata[field]; ok {
			t.Errorf("metadata.%s should be removed when empty, got %v", field, metadata[field])
		}
	}
}
