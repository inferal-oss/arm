package cli

import (
	"io"

	"getarm.org/internal/companions"
	"getarm.org/internal/mesh"
)

// Embedded first, then an exact private seeded catalog. Ordinary thin nodes
// with no catalog preserve the existing explicit artifact/sibling behavior.
func (a *app) meshCompanionSource() (*companions.Catalog, io.Closer, error) {
	embedded, e := companions.Embedded(Version, mesh.LifecycleProtocol)
	if e != nil {
		return nil, nil, e
	}
	if embedded != nil {
		return embedded, nil, nil
	}
	stored, e := mesh.OpenCompanionCatalog(a.home, Version, mesh.LifecycleProtocol)
	if e != nil {
		return nil, nil, e
	}
	if stored == nil {
		return nil, nil, nil
	}
	return stored.Catalog, stored, nil
}
