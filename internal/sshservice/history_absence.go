package sshservice

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

// Catalog ref absence alone is not key absence: a damaged/retargeted ref can
// retain the same row count while the immutable pack still owns the old key.
// Full bounded closure is deliberately off source locks. Its native version
// witness is checked again under those locks before publishing a new intent.
type verifiedSourceAbsence struct {
	Marker  *SourceHistoryMarker
	Version sourceCatalogVersion
}

func verifySourceCatalogAbsence(ctx context.Context, c *historyCatalog, target, key string) (sourceCatalogVersion, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	version, e := captureSourceCatalogVersion(ctx, c)
	if e != nil {
		return version, e
	}
	if e = c.validateFull(ctx); e != nil {
		return version, fmt.Errorf("source key absence lacks complete immutable history closure: %w", e)
	}
	if _, e = c.ref(ctx, target, key); !errors.Is(e, sql.ErrNoRows) {
		return version, fmt.Errorf("source key absence changed during inspection")
	}
	if e = c.guard(); e != nil {
		return version, e
	}
	return version, version.check(c.path)
}
func preverifySourceAbsence(ctx context.Context, home string, b Binding, target, key string) (verifiedSourceAbsence, error) {
	var out verifiedSourceAbsence
	s, e := ReadState(ctx, home, b)
	if e != nil {
		return out, e
	}
	if s.historyMarker == nil {
		return out, nil
	}
	marker := *s.historyMarker
	out.Marker = &marker
	c, e := openHistoryCatalog(ctx, home, b, marker, false, false, nil)
	if e != nil {
		return out, e
	}
	defer c.close()
	if positive, e := sourceFenceMetadata(ctx, c, target, key); e != nil {
		return out, e
	} else if positive != nil {
		return out, ErrIntentExists
	}
	out.Version, e = verifySourceCatalogAbsence(ctx, c, target, key)
	return out, e
}
func (v verifiedSourceAbsence) check(s *State) error {
	ctx := s.mutationContext
	if ctx == nil {
		ctx = context.Background()
	}
	current, e := historyCanonicalMarker(ctx, s.home, s.Binding)
	if e != nil {
		return e
	}
	if v.Marker == nil {
		if s.historyMarker != nil || current != nil {
			return fmt.Errorf("source archive appeared after key absence inspection; inspect again")
		}
		return nil
	}
	if s.historyMarker == nil || *s.historyMarker != *v.Marker || current == nil || *current != *v.Marker {
		return fmt.Errorf("source history authority changed after key absence inspection")
	}
	path := filepath.Join(s.home, SourceHistoryName)
	directory, e := privateDirectory(path)
	if e != nil {
		return e
	}
	defer directory.Close()
	native := &historyCatalog{ctx: ctx, home: s.home, path: path, marker: *v.Marker, directory: directory}
	if e = native.guard(); e != nil {
		return e
	}
	if e = checkHistorySidecars(directory, false); e != nil {
		return e
	}
	return v.Version.check(path)
}

// atomicStateRecord calls s.guard both before creating its born staging file and
// after its fsync, immediately before publication. Retain the absence witness
// through both checks rather than checking only before entering that writer.
func bindSourceAbsenceGuard(s *State, v verifiedSourceAbsence) {
	previous := s.guard
	s.guard = func() error {
		if previous != nil {
			if e := previous(); e != nil {
				return e
			}
		}
		return v.check(s)
	}
}
