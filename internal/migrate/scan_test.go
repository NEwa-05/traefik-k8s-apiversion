package migrate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

func newObject(apiVersion, kind, namespace, name string, spec map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]any{"name": name, "namespace": namespace},
		"spec":       spec,
	}}
}

func resourceList(group string, kinds map[string]string) *metav1.APIResourceList {
	list := &metav1.APIResourceList{GroupVersion: group + "/v1alpha1"}
	for name, kind := range kinds {
		list.APIResources = append(list.APIResources,
			metav1.APIResource{Name: name, Kind: kind, Namespaced: true, Verbs: metav1.Verbs{"get", "list"}},
			metav1.APIResource{Name: name + "/status", Kind: kind, Namespaced: true, Verbs: metav1.Verbs{"get"}},
		)
	}
	return list
}

func newScanner(t *testing.T, namespace string, objects ...runtime.Object) *Scanner {
	t.Helper()

	disc := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{Resources: []*metav1.APIResourceList{
		resourceList(OldGroup, map[string]string{"ingressroutes": "IngressRoute", "middlewares": "Middleware", "tlsoptions": "TLSOption"}),
		// The traefik.io TLSOption CRD is deliberately not installed.
		resourceList(NewGroup, map[string]string{"ingressroutes": "IngressRoute", "middlewares": "Middleware"}),
	}}}

	listKinds := make(map[schema.GroupVersionResource]string)
	for _, group := range []string{OldGroup, NewGroup} {
		for name, kind := range map[string]string{"ingressroutes": "IngressRoute", "middlewares": "Middleware", "tlsoptions": "TLSOption"} {
			listKinds[schema.GroupVersionResource{Group: group, Version: "v1alpha1", Resource: name}] = kind + "List"
		}
	}

	return &Scanner{
		Discovery: disc,
		Dynamic:   dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds, objects...),
		Namespace: namespace,
	}
}

func TestScan(t *testing.T) {
	const oldAPI, newAPI = "traefik.containo.us/v1alpha1", "traefik.io/v1alpha1"
	spec := map[string]any{"entryPoints": []any{"web"}}
	otherSpec := map[string]any{"entryPoints": []any{"websecure"}}

	scanner := newScanner(t, "",
		newObject(oldAPI, "IngressRoute", "default", "missing", spec),
		newObject(oldAPI, "IngressRoute", "default", "synced", spec),
		newObject(newAPI, "IngressRoute", "default", "synced", spec),
		newObject(oldAPI, "Middleware", "other", "changed", spec),
		newObject(newAPI, "Middleware", "other", "changed", otherSpec),
		newObject(oldAPI, "TLSOption", "default", "nocrd", spec),
		// Only exists under the new group: not part of the migration.
		newObject(newAPI, "Middleware", "default", "new-only", spec),
	)

	items, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	got := make(map[string]Status)
	for _, item := range items {
		got[item.Resource.Kind+" "+objectKey(item.Source)] = item.Status
		if item.Converted.GetAPIVersion() != newAPI {
			t.Errorf("%s: converted apiVersion = %q, want %q", item.Source.GetName(), item.Converted.GetAPIVersion(), newAPI)
		}
	}
	want := map[string]Status{
		"IngressRoute default/missing": StatusMissing,
		"IngressRoute default/synced":  StatusUpToDate,
		"Middleware other/changed":     StatusOutOfSync,
		"TLSOption default/nocrd":      StatusNoCRD,
	}
	if len(got) != len(want) {
		t.Errorf("Scan() found %v, want %v", got, want)
	}
	for key, status := range want {
		if got[key] != status {
			t.Errorf("Scan() status of %s = %q, want %q", key, got[key], status)
		}
	}
}

func TestScanNamespace(t *testing.T) {
	const oldAPI = "traefik.containo.us/v1alpha1"
	scanner := newScanner(t, "default",
		newObject(oldAPI, "IngressRoute", "default", "in", nil),
		newObject(oldAPI, "IngressRoute", "other", "out", nil),
	)

	items, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(items) != 1 || items[0].Source.GetName() != "in" {
		t.Errorf("Scan() returned %d items, want only default/in", len(items))
	}
}

func TestScanWithoutOldGroup(t *testing.T) {
	scanner := &Scanner{
		Discovery: &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}},
		Dynamic:   dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
	}

	items, err := scanner.Scan(context.Background())
	if err != nil || len(items) != 0 {
		t.Errorf("Scan() = %v, %v, want no item and no error", items, err)
	}
}

func TestWriteManifest(t *testing.T) {
	dir := t.TempDir()
	obj := newObject("traefik.io/v1alpha1", "IngressRoute", "default", "whoami", map[string]any{"entryPoints": []any{"web"}})

	path, warnings, err := WriteManifest(dir, obj)
	if err != nil {
		t.Fatalf("WriteManifest() error = %v", err)
	}
	if want := filepath.Join(dir, "default", "ingressroute-whoami.yaml"); path != want {
		t.Errorf("WriteManifest() path = %q, want %q", path, want)
	}
	if len(warnings) != 0 {
		t.Errorf("WriteManifest() warnings = %q, want none", warnings)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "apiVersion: traefik.io/v1alpha1\n") {
		t.Errorf("manifest does not contain the traefik.io apiVersion:\n%s", data)
	}
}

func TestWriteManifestLargeObject(t *testing.T) {
	obj := newObject("traefik.io/v1alpha1", "TLSStore", "default", "big", map[string]any{
		"payload": strings.Repeat("x", annotationSizeLimit),
	})

	_, warnings, err := WriteManifest(t.TempDir(), obj)
	if err != nil {
		t.Fatalf("WriteManifest() error = %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "--server-side") {
		t.Errorf("WriteManifest() warnings = %q, want a server-side apply hint", warnings)
	}
}
