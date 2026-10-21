package migrate

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
)

// Status tells whether a traefik.containo.us object already has an
// equivalent under the traefik.io group.
type Status string

const (
	// StatusMissing means no traefik.io object with the same name exists.
	StatusMissing Status = "missing"
	// StatusOutOfSync means a traefik.io object exists with a different spec.
	StatusOutOfSync Status = "out-of-sync"
	// StatusUpToDate means a traefik.io object exists with the same spec.
	StatusUpToDate Status = "up-to-date"
	// StatusNoCRD means the traefik.io CRD for this kind is not installed.
	StatusNoCRD Status = "crd-missing"
)

// DefaultPageSize is the number of objects requested per list call. Listing
// in pages keeps each response small, which matters when objects are large.
const DefaultPageSize = 100

// Item is a traefik.containo.us object found in the cluster.
type Item struct {
	Resource Resource
	Source   *unstructured.Unstructured
	// Converted is Source converted to the traefik.io group.
	Converted *unstructured.Unstructured
	Warnings  []string
	Status    Status
}

// Scanner lists the traefik.containo.us objects of a cluster.
type Scanner struct {
	Discovery discovery.DiscoveryInterface
	Dynamic   dynamic.Interface
	// Namespace restricts the scan to one namespace. Empty means all namespaces.
	Namespace string
	PageSize  int64
}

// Scan returns every traefik.containo.us object, converted to traefik.io and
// compared with the traefik.io objects already present in the cluster.
// It returns no item and no error when the old group is not served.
func (s *Scanner) Scan(ctx context.Context) ([]Item, error) {
	oldResources, err := discoverGroup(s.Discovery, OldGroup)
	if err != nil {
		return nil, err
	}
	newResources, err := discoverGroup(s.Discovery, NewGroup)
	if err != nil {
		return nil, err
	}
	newByKind := make(map[string]Resource, len(newResources))
	for _, r := range newResources {
		newByKind[r.Kind] = r
	}

	var items []Item
	for _, res := range oldResources {
		// Cluster-scoped objects are out of scope when a namespace is requested.
		if !res.Namespaced && s.Namespace != "" {
			continue
		}

		objects, err := s.list(ctx, res.GVR(OldGroup), res.Namespaced)
		if err != nil {
			return nil, err
		}
		if len(objects) == 0 {
			continue
		}

		newRes, hasNewCRD := newByKind[res.Kind]
		var existing map[string]*unstructured.Unstructured
		if hasNewCRD {
			newObjects, err := s.list(ctx, newRes.GVR(NewGroup), newRes.Namespaced)
			if err != nil {
				return nil, err
			}
			existing = make(map[string]*unstructured.Unstructured, len(newObjects))
			for i := range newObjects {
				existing[objectKey(&newObjects[i])] = &newObjects[i]
			}
		}

		for i := range objects {
			source := &objects[i]
			converted, warnings := Convert(source)
			item := Item{
				Resource:  res,
				Source:    source,
				Converted: converted,
				Warnings:  warnings,
			}

			switch current, found := existing[objectKey(source)]; {
			case !hasNewCRD:
				item.Status = StatusNoCRD
			case !found:
				item.Status = StatusMissing
			case equality.Semantic.DeepEqual(converted.Object["spec"], current.Object["spec"]):
				item.Status = StatusUpToDate
			default:
				item.Status = StatusOutOfSync
			}

			items = append(items, item)
		}
	}

	return items, nil
}

// list returns all objects of gvr, fetched page by page.
func (s *Scanner) list(ctx context.Context, gvr schema.GroupVersionResource, namespaced bool) ([]unstructured.Unstructured, error) {
	var client dynamic.ResourceInterface = s.Dynamic.Resource(gvr)
	if namespaced && s.Namespace != "" {
		client = s.Dynamic.Resource(gvr).Namespace(s.Namespace)
	}

	pageSize := s.PageSize
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}

	var objects []unstructured.Unstructured
	opts := metav1.ListOptions{Limit: pageSize}
	for {
		page, err := client.List(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", gvr.GroupResource(), err)
		}
		objects = append(objects, page.Items...)

		if page.GetContinue() == "" {
			return objects, nil
		}
		opts.Continue = page.GetContinue()
	}
}

func objectKey(obj *unstructured.Unstructured) string {
	return obj.GetNamespace() + "/" + obj.GetName()
}
