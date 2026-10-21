// Package migrate finds Traefik custom resources served under the deprecated
// traefik.containo.us API group and converts them to the traefik.io group
// used by Traefik v3.
package migrate

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

const (
	// OldGroup is the API group used by Traefik v2 CRDs, removed in Traefik v3.
	OldGroup = "traefik.containo.us"
	// NewGroup is the API group introduced in Traefik v2.10 and required by Traefik v3.
	NewGroup = "traefik.io"
)

// Resource is a listable resource served by the API server.
type Resource struct {
	Name       string // plural resource name, e.g. ingressroutes
	Kind       string
	Version    string
	Namespaced bool
}

// GVR returns the GroupVersionResource of r in the given group.
func (r Resource) GVR(group string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: group, Version: r.Version, Resource: r.Name}
}

// discoverGroup returns the listable resources served under the preferred
// version of group, sorted by kind. It returns nil when the group is not served.
func discoverGroup(disc discovery.DiscoveryInterface, group string) ([]Resource, error) {
	groups, err := disc.ServerGroups()
	if err != nil {
		return nil, fmt.Errorf("discovering API groups: %w", err)
	}

	var groupVersion string
	for i := range groups.Groups {
		g := &groups.Groups[i]
		if g.Name == group {
			groupVersion = g.PreferredVersion.GroupVersion
			break
		}
	}
	if groupVersion == "" {
		return nil, nil
	}

	list, err := disc.ServerResourcesForGroupVersion(groupVersion)
	if err != nil {
		return nil, fmt.Errorf("discovering resources of %s: %w", groupVersion, err)
	}

	_, version, _ := strings.Cut(groupVersion, "/")
	var resources []Resource
	for i := range list.APIResources {
		r := &list.APIResources[i]
		// Skip subresources such as ingressroutes/status.
		if strings.Contains(r.Name, "/") || !slices.Contains(r.Verbs, "list") {
			continue
		}
		resources = append(resources, Resource{
			Name:       r.Name,
			Kind:       r.Kind,
			Version:    version,
			Namespaced: r.Namespaced,
		})
	}
	sort.Slice(resources, func(i, j int) bool { return resources[i].Kind < resources[j].Kind })

	return resources, nil
}
