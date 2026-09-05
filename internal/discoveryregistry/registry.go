// internal/discoveryregistry/registry.go

// Package discoveryregistry is the single place that knows about every
// concrete discovery.Source implementation, mirroring internal/noderegistry's
// relationship to internal/workflow — kept separate from internal/discovery
// itself so internal/discovery/sources/* can import internal/discovery
// (for its types) without creating an import cycle back through a registry
// living inside internal/discovery.
package discoveryregistry

import (
	"sort"

	"github.com/monoes/mono-agent/internal/discovery"
	"github.com/monoes/mono-agent/internal/discovery/sources/arbeitnow"
	"github.com/monoes/mono-agent/internal/discovery/sources/jobicy"
	"github.com/monoes/mono-agent/internal/discovery/sources/linkedin"
)

var sources = map[string]discovery.Source{
	"linkedin":  linkedin.New(),
	"arbeitnow": arbeitnow.New(),
	"jobicy":    jobicy.New(),
}

// Get returns the registered Source for name, or ok=false if unknown.
func Get(name string) (discovery.Source, bool) {
	s, ok := sources[name]
	return s, ok
}

// Names returns every registered source name, sorted.
func Names() []string {
	names := make([]string, 0, len(sources))
	for n := range sources {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
