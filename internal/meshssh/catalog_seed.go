package meshssh

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/companions"
)

type CatalogSeed struct {
	Schema   int                 `json:"schema"`
	Digest   string              `json:"manifest_sha256"`
	Manifest companions.Manifest `json:"manifest"`
}

func (s CatalogSeed) Validate(t Transfer) error {
	if s.Schema != 1 {
		return fmt.Errorf("unsupported companion seed schema")
	}
	if e := s.Manifest.Validate(t.Version, t.Protocol); e != nil {
		return e
	}
	canonical, hash, e := companions.Canonical(s.Manifest)
	if e != nil || len(canonical) > 16384 || hash != s.Digest {
		return fmt.Errorf("seed manifest digest differs")
	}
	for _, p := range s.Manifest.Artifacts {
		if p.OS == t.OS && p.Arch == t.Arch {
			if p.SHA256 != t.SHA256 || p.Size != t.Size {
				return fmt.Errorf("selected executable differs from reviewed companion catalog")
			}
			return nil
		}
	}
	return fmt.Errorf("selected executable not in seed catalog")
}

type CatalogSeedRequest struct {
	Transfer Transfer    `json:"transfer"`
	Home     string      `json:"home"`
	Host     string      `json:"started_host"`
	Catalog  CatalogSeed `json:"catalog"`
}

func (q CatalogSeedRequest) Validate() error {
	if e := q.Transfer.Validate(); e != nil {
		return e
	}
	if !ValidStateHome(q.Home) || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(q.Host) {
		return fmt.Errorf("exact started authority/home required for catalog seed")
	}
	return q.Catalog.Validate(q.Transfer)
}

// SeedCatalog uses only the fixed native helper and reviewed, length-delimited
// four-payload stream. No shell extraction or additional mesh endpoint exists.
func SeedCatalog(ctx context.Context, destination, binary string, q CatalogSeedRequest, frames io.Reader, expected Identity, result any) (Identity, error) {
	if e := q.Validate(); e != nil {
		return expected, e
	}
	if !validDestination(destination) || !regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`).MatchString(binary) {
		return expected, fmt.Errorf("fixed absolute installed helper required")
	}
	raw, e := json.Marshal(q)
	if e != nil || len(raw) > 32768 {
		return expected, fmt.Errorf("seed descriptor exceeds bound")
	}
	descriptor := base64.RawStdEncoding.EncodeToString(raw)
	observed, out, e := exchange(ctx, destination, []string{binary, "mesh", "provision-seed", descriptor}, io.MultiReader(strings.NewReader("ARM_REVIEWED_CATALOG_SEED_V1\n"), frames), expected, 2*time.Minute)
	if e != nil {
		return observed, e
	}
	if e = json.Unmarshal(out, result); e != nil {
		return observed, e
	}
	return observed, nil
}
