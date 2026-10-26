package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/yrashk/arm/internal/companions"
	"github.com/yrashk/arm/internal/mesh"
)

func verifiedEmbeddedCatalogDigest(catalog *companions.Catalog) (string, error) {
	if catalog == nil {
		return "", errors.New("ARM carrier catalog is absent")
	}
	manifest := catalog.Manifest()
	_, digest, err := companions.Canonical(manifest)
	if err != nil {
		return "", err
	}
	for _, payload := range manifest.Artifacts {
		stream, _, err := catalog.Open(payload.OS, payload.Arch)
		if err != nil {
			return "", err
		}
		size, readErr := io.Copy(io.Discard, stream)
		closeErr := stream.Close()
		if readErr != nil || closeErr != nil || size != payload.Size {
			return "", errors.New("ARM carrier catalog payload verification failed")
		}
	}
	return digest, nil
}

// The shipped executable answers this fixed local proof without opening ARM
// state, starting a backend or accepting operator-controlled file paths.
func kubeCatalogProofMain(args []string, out io.Writer) int {
	if len(args) != 0 {
		return 125
	}
	catalog, err := companions.Embedded(Version, mesh.LifecycleProtocol)
	if err != nil {
		return 1
	}
	digest, err := verifiedEmbeddedCatalogDigest(catalog)
	if err != nil {
		return 1
	}
	if _, err := fmt.Fprintf(out, "ARM_CATALOG_VERIFIED %s\n", digest); err != nil {
		return 1
	}
	return 0
}
