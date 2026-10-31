package manager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"getarm.org/internal/model"
)

func forwardUnixTestChangePlacement(t *testing.T, m *Manager, id string, binding model.StoragePoolBinding) model.Lease {
	t.Helper()
	m.mu.Lock()
	original, err := m.get(id)
	m.mu.Unlock()
	if err != nil || original.Request.StoragePool != "" || original.StorageBinding != nil {
		t.Fatalf("default-root original unavailable: %v", err)
	}
	changed := original
	changed.StorageBinding = &binding
	raw, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(raw), id); err != nil {
		t.Fatalf("same-revision placement mutation did not reach durable row: %v", err)
	}
	m.mu.Lock()
	current, err := m.get(id)
	m.mu.Unlock()
	if err != nil || current.Revision != original.Revision || current.StorageBinding == nil || current.Request.StoragePool != "" {
		t.Fatalf("same-revision placement mutation unavailable: %v", err)
	}
	return current
}

func forwardUnixTestReadPairLease(t *testing.T, m *Manager, id string) error {
	t.Helper()
	tx, err := m.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, _, _, err = readForwardUnixPairLease(context.Background(), tx, id)
	return err
}

func forwardUnixTestChargesAndReferences(t *testing.T, m *Manager, h *ForwardListener, c *ForwardUnixChild, p *ForwardUnixPair) {
	t.Helper()
	for _, id := range []string{p.source, p.storage} {
		lease, err := m.Inspect(id)
		if err != nil || lease.State != "active" {
			t.Fatalf("original forwarding obligation no longer charged: %v", err)
		}
	}
	m.mu.Lock()
	attached := h.unixChild == c && c.pair == p && m.forwardListeners[h.id] == h
	m.mu.Unlock()
	if !attached {
		t.Fatal("original forwarding references were released")
	}
}

func TestForwardUnixDefaultRootPlacementRefusedBeforeNativeBind(t *testing.T) {
	for _, role := range []string{"source", "storage"} {
		for _, binding := range []struct {
			name  string
			value model.StoragePoolBinding
		}{
			{"empty", model.StoragePoolBinding{}},
			{"named", model.StoragePoolBinding{Name: "alternate", Volume: "volume", Backing: "backing", SharesDefault: true}},
		} {
			t.Run(role+"/"+binding.name, func(t *testing.T) {
				m, h, c, _, _ := newShortForwardUnixPairFixture(t)
				p, err := c.IssueStoragePair(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if _, err = h.Remaining(context.Background()); err != nil || forwardUnixTestReadPairLease(t, m, p.source) != nil || forwardUnixTestReadPairLease(t, m, p.storage) != nil {
					t.Fatal("default-root positive baseline was refused", err)
				}
				id := p.source
				if role == "storage" {
					id = p.storage
				}
				forwardUnixTestChangePlacement(t, m, id, binding.value)
				if role == "source" {
					if _, err = h.Remaining(context.Background()); err == nil {
						t.Fatal("changed source placement retained listener authority")
					}
				}
				if err = forwardUnixTestReadPairLease(t, m, id); err == nil {
					t.Fatal("changed placement survived shared pair lease reader")
				}
				storage, err := m.Inspect(p.storage)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(storage.ManagedDir, p.data.SocketName)
				workersJoined := make(chan struct{})
				t.Cleanup(func() { close(workersJoined) })
				nativeAttempt := false
				p.beforeNativeBindTest = func() { nativeAttempt = true }
				bound, err := p.BindOriginal(context.Background(), workersJoined)
				if err == nil || bound != nil || nativeAttempt || p.native != nil || p.data.Phase != "planned" {
					t.Fatalf("changed placement reached native bind: phase=%q attempt=%t err=%v", p.data.Phase, nativeAttempt, err)
				}
				if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("changed placement left native socket: %v", err)
				}
				if err = p.AbortBeforeBind(context.Background()); err == nil {
					t.Fatal("contradictory placement reached no-bind terminal reader")
				}
				forwardUnixTestChargesAndReferences(t, m, h, c, p)
			})
		}
	}
}

func TestForwardUnixDefaultRootPlacementRefusedBeforeApplicationBytes(t *testing.T) {
	for _, role := range []string{"source", "storage"} {
		t.Run(role, func(t *testing.T) {
			m, h, c, _, _ := newShortForwardUnixPairFixture(t)
			p, err := c.IssueStoragePair(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			workersJoined := make(chan struct{})
			var bound *ForwardUnixBound
			t.Cleanup(func() {
				close(workersJoined)
				if bound != nil {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					_ = bound.CloseAfterJoin(ctx)
				}
			})
			bound, err = p.BindOriginal(context.Background(), workersJoined)
			if err != nil {
				t.Fatal(err)
			}
			if err := bound.CheckBeforeApplicationBytes(context.Background()); err != nil {
				t.Fatal("default-root positive application gate refused", err)
			}
			id := p.source
			if role == "storage" {
				id = p.storage
			}
			forwardUnixTestChangePlacement(t, m, id, model.StoragePoolBinding{})
			if err := bound.CheckBeforeApplicationBytes(context.Background()); err == nil {
				t.Fatal("same-revision placement reached application-byte gate")
			}
			if err := p.readyForNativePhase(context.Background(), bound, m.storage, workersJoined, "bound", false); err == nil {
				t.Fatal("same-revision placement retained native phase authority")
			}
			forwardUnixTestChargesAndReferences(t, m, h, c, p)
		})
	}
}

func TestForwardUnixDefaultRootTerminalReaderRefusesChangedPlacement(t *testing.T) {
	for _, role := range []string{"source", "storage"} {
		t.Run(role, func(t *testing.T) {
			m, h, c, _, _ := newForwardUnixPairFixture(t)
			p, err := c.IssueStoragePair(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err := p.AbortBeforeBind(context.Background()); err != nil {
				t.Fatal("positive no-bind terminal unavailable", err)
			}
			id := p.source
			if role == "storage" {
				id = p.storage
			}
			forwardUnixTestChangePlacement(t, m, id, model.StoragePoolBinding{})
			tx, err := m.db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if err := validateForwardUnixPairStartup(context.Background(), tx, false); err == nil {
				t.Fatal("contradictory placement survived terminal pair reader")
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			source, err := m.Inspect(h.id)
			if err != nil || source.State != "active" {
				t.Fatal("terminal reader refusal erased original source", err)
			}
		})
	}
}
