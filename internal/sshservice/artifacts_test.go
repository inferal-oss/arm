package sshservice

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestArtifactsRequireDurableIntentAndExactCleanup(t *testing.T) {
	home, b := stateFixture(t)
	pin := statePin(t, home, b)
	r := sourceFixture(t, home, b, pin, "artifacts")
	if _, e := PrepareArtifacts(t.Context(), home, b, r); e == nil {
		t.Fatal("unreserved artifacts created")
	}
	entries, _ := os.ReadDir(r.Directory.Path)
	if len(entries) != 0 {
		t.Fatal("unreserved directory changed")
	}
	if e := ReserveSource(t.Context(), home, b, r); e != nil {
		t.Fatal(e)
	}
	a, e := PrepareArtifacts(t.Context(), home, b, r)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		a.Close()
		for _, n := range append(artifactNames, ArtifactReceiptName) {
			os.Remove(filepath.Join(r.Directory.Path, n))
		}
		os.Remove(r.Directory.Path)
	})
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		c, e := a.Accept(ctx, "proxy.sock")
		if c != nil {
			c.Close()
		}
		done <- e
	}()
	conn, e := net.Dial("unix", a.Path("proxy.sock"))
	if e != nil {
		t.Fatal(e)
	}
	conn.Close()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if e = a.Cleanup(t.Context()); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Lstat(r.Directory.Path); !os.IsNotExist(e) {
		t.Fatal("exact directory retained", e)
	}
}
func TestArtifactsChangedRosterNeverUnlinksGuessedChildren(t *testing.T) {
	for _, change := range []string{"extra", "content", "replacement", "partial"} {
		t.Run(change, func(t *testing.T) {
			home, b := stateFixture(t)
			pin := statePin(t, home, b)
			r := sourceFixture(t, home, b, pin, "artifact-refusal")
			if e := ReserveSource(t.Context(), home, b, r); e != nil {
				t.Fatal(e)
			}
			a, e := PrepareArtifacts(t.Context(), home, b, r)
			if e != nil {
				t.Fatal(e)
			}
			a.Close()
			t.Cleanup(func() {
				for _, n := range append(append([]string{}, artifactNames...), ArtifactReceiptName, "extra") {
					os.Remove(filepath.Join(r.Directory.Path, n))
				}
				os.Remove(r.Directory.Path)
			})
			switch change {
			case "extra":
				os.WriteFile(filepath.Join(r.Directory.Path, "extra"), []byte("preserve"), 0600)
			case "content":
				os.WriteFile(a.Path("known_hosts"), []byte("changed"), 0600)
			case "replacement":
				os.Remove(a.Path("agent.sock"))
				os.WriteFile(a.Path("agent.sock"), []byte("replacement"), 0600)
			case "partial":
				os.Remove(a.Path(ArtifactReceiptName))
			}
			if e = a.Cleanup(t.Context()); e == nil {
				t.Fatal("unsafe cleanup accepted")
			}
			if _, e = os.Lstat(a.Path("identity.pub")); e != nil {
				t.Fatal("cleanup modified children before complete validation", e)
			}
		})
	}
}

func TestDiscardBornEmptyDirectoryNeverErasesArtifactsOrReplacement(t *testing.T) {
	home, b := stateFixture(t)
	dir, e := CreateInvocationDirectory(t.Context(), home, b)
	if e != nil {
		t.Fatal(e)
	}
	if e = DiscardEmptyInvocationDirectory(t.Context(), dir); e != nil {
		t.Fatal(e)
	}
	dir, e = CreateInvocationDirectory(t.Context(), home, b)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir.Path, "preserve")
	if e = os.WriteFile(path, []byte("must survive"), 0600); e != nil {
		t.Fatal(e)
	}
	defer func() { os.Remove(path); os.Remove(dir.Path) }()
	if e = DiscardEmptyInvocationDirectory(t.Context(), dir); e == nil {
		t.Fatal("nonempty artifact directory discarded")
	}
	if _, e = os.Stat(path); e != nil {
		t.Fatal(e)
	}
	changed := dir
	changed.Inode++
	if e = DiscardEmptyInvocationDirectory(t.Context(), changed); e == nil {
		t.Fatal("different born inode accepted")
	}
}
