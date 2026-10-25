package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yrashk/arm/internal/model"
)

func TestSnapshotReservationPreflightOriginalClosesWithoutPayload(t *testing.T) {
	p, e := Open(filepath.Join(t.TempDir(), "provider"), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	name := "11111111111111111111111111111111"
	id, e := p.Create(name)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(p.Path(name), "payload"), []byte("unreadable payload"), 0000); e != nil {
		t.Fatal(e)
	}
	a := model.Artifact{Name: name, Path: p.Path(name), Identity: &id, Phase: "owned"}
	out, e := p.SnapshotReservationPreflight(t.Context(), a)
	if e != nil {
		t.Fatal(e)
	}
	if out.Source != id || out.Provider != p.Identity() || !out.Native.Closed || out.Native.Unknown {
		t.Fatal(out)
	}
	for _, r := range out.Native.Descriptors {
		if r.Role != "provider" && r.Role != "reservation-source-root" {
			t.Fatal("preflight traversed source tree", r)
		}
	}
}
func TestSnapshotReservationPreflightUnknownIsSticky(t *testing.T) {
	p, e := Open(filepath.Join(t.TempDir(), "provider"), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	name := "22222222222222222222222222222222"
	id, e := p.Create(name)
	if e != nil {
		t.Fatal(e)
	}
	owner := newSnapshotFDSet()
	calls := 0
	owner.closeFile = func(f *os.File) error {
		calls++
		e := f.Close()
		return errors.Join(e, errors.New("reported failure AFTER actual original close"))
	}
	out, e := p.snapshotReservationPreflight(context.Background(), model.Artifact{Name: name, Path: p.Path(name), Identity: &id, Phase: "owned"}, owner)
	if e == nil || !out.Native.Unknown || out.Native.Closed {
		t.Fatal(out, e)
	}
	before := calls
	if owner.closeAll() == nil || calls != before {
		t.Fatal("unknown original close was retried", calls, before)
	}
}
