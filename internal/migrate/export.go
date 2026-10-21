package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

const (
	// annotationSizeLimit is the maximum total size of an object's annotations.
	// A client-side "kubectl apply" stores the whole object in the
	// last-applied-configuration annotation, so larger objects cannot be applied
	// that way.
	annotationSizeLimit = 256 * 1024
	// etcdRequestLimit is the default maximum size of a request accepted by etcd.
	etcdRequestLimit = 1536 * 1024

	clusterScopeDir = "_cluster"
)

// WriteManifest writes obj as YAML in its own file below dir, in a
// sub-directory named after its namespace, and returns the file path.
// Keeping one object per file means each object is applied with its own
// request, whatever the number of objects. Returned warnings flag objects too
// large to be applied in the usual way.
func WriteManifest(dir string, obj *unstructured.Unstructured) (path string, warnings []string, err error) {
	data, err := yaml.Marshal(obj.Object)
	if err != nil {
		return "", nil, fmt.Errorf("encoding %s %s: %w", obj.GetKind(), objectKey(obj), err)
	}

	namespace := obj.GetNamespace()
	if namespace == "" {
		namespace = clusterScopeDir
	}
	nsDir := filepath.Join(dir, namespace)
	if err := os.MkdirAll(nsDir, 0o750); err != nil {
		return "", nil, err
	}

	path = filepath.Join(nsDir, strings.ToLower(obj.GetKind())+"-"+obj.GetName()+".yaml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", nil, err
	}

	switch size := len(data); {
	case size > etcdRequestLimit:
		warnings = append(warnings, fmt.Sprintf("manifest is %d KiB, above the default etcd request limit (%d KiB): the API server may reject it", size/1024, etcdRequestLimit/1024))
	case size > annotationSizeLimit:
		warnings = append(warnings, fmt.Sprintf("manifest is %d KiB, too large for a client-side kubectl apply (annotations are limited to %d KiB): use kubectl apply --server-side or kubectl create", size/1024, annotationSizeLimit/1024))
	}

	return path, warnings, nil
}
