package manager

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sshservice"
	"github.com/yrashk/arm/internal/syncreconcile"
	"github.com/yrashk/arm/internal/syncservice"
	"golang.org/x/crypto/ssh"
)

type frameReviewChannel struct{ reads atomic.Int32 }

func (c *frameReviewChannel) Read([]byte) (int, error)                     { c.reads.Add(1); return 0, io.EOF }
func (*frameReviewChannel) Write(p []byte) (int, error)                    { return len(p), nil }
func (*frameReviewChannel) Close() error                                   { return nil }
func (*frameReviewChannel) CloseWrite() error                              { return nil }
func (*frameReviewChannel) SendRequest(string, bool, []byte) (bool, error) { return false, nil }
func (c *frameReviewChannel) Stderr() io.ReadWriter                        { return c }

func TestSyncFrameReviewRequiresOriginalTransportBeforePreparation(t *testing.T) {
	m, root, parent := syncManagerFixture(t)
	c := fenceControl(m, "frame-review-12345678")
	c.CommandSHA256, c.CWD = sshDigest([]byte(syncWorkerCommand)), root.Path()
	c.Resources = model.Resources{CPU: .25, Memory: 2 << 20, Disk: 2 << 20}
	op, _, _ := fenceOperation(m, c)
	ch := &frameReviewChannel{}
	_, hold, err := m.AdmitSyncWorkerFromFrame(t.Context(), root, parent, op.Request, sshservice.Execution{Kind: "exec", Command: syncWorkerCommand}, ch, model.Resources{CPU: 1, Memory: 1 << 30, Disk: 4 << 20}, time.Minute, time.Now().Add(time.Minute), c)
	if err == nil || hold != nil || ch.reads.Load() != 0 {
		t.Fatal("unowned channel decoded before exact original transport binding", hold, err, ch.reads.Load())
	}
	var count int
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM leases WHERE json_extract(data,'$.request.kind')='sync-preparation'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("unowned transport consumed preparation", count, err)
	}
}

func TestSyncFrameReviewPreparationRequiresFullOriginalLease(t *testing.T) {
	for _, change := range []string{"owner", "host", "expires"} {
		t.Run(change, func(t *testing.T) {
			m, _, _ := syncManagerFixture(t)
			p, err := m.beginSyncPreparation(t.Context(), "decode-owner-12345678", syncreconcile.Local, time.Minute, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer p.finish()
			var original string
			if err := m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, p.owner.id).Scan(&original); err != nil {
				t.Fatal(err)
			}
			defer m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, original, p.owner.id)
			m.mu.Lock()
			l, err := m.get(p.owner.id)
			if err == nil {
				switch change {
				case "owner":
					l.Owner = "different-owner"
				case "host":
					l.Host = "different-host"
				case "expires":
					expired := time.Now().Add(-time.Minute)
					l.Expires = &expired
				}
				err = m.put(l, "review.preparation.replacement")
			}
			m.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if err := p.CheckActive(context.Background()); err == nil {
				t.Fatal("changed full preparation lease retained decode authority", change)
			}
		})
	}
}

var _ ssh.Channel = (*frameReviewChannel)(nil)

type framePolicyBudget int64

func (framePolicyBudget) CheckActive(ctx context.Context) error { return ctx.Err() }
func (b framePolicyBudget) MemoryBytes() int64                  { return int64(b) }

func TestSyncFrameReviewSourceHandoffRetainsStructuralMemory(t *testing.T) {
	m, root, parent := syncManagerFixture(t)
	i, rows, control := sourceSyncIntent(t, m, root)
	record, err := json.Marshal(i)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	// Satisfy the former four-canonical-byte floor, but not the actual
	// conservative structural allowance established under preparation.
	i.LocalResources.Memory = 4*int64(len(record)+len(manifest)) + (128 << 10) + 1024
	required, err := syncservice.PreparationFootprint(t.Context(), syncservice.Preparation{Version: syncservice.Schema, Intent: i, Manifest: rows}, framePolicyBudget(768<<20))
	if err != nil || required <= i.LocalResources.Memory {
		t.Fatal("fixture does not cross structural handoff bound", required, i.LocalResources.Memory, err)
	}
	if _, err := m.ReserveSyncSource(t.Context(), i, rows, root, parent, control); err == nil || !strings.Contains(err.Error(), "final sync memory promise") {
		t.Fatal("source handoff lost preparation structural charge", err)
	}
	var finals int
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM leases WHERE json_extract(data,'$.request.kind')!='sync-preparation'`).Scan(&finals); err != nil || finals != 0 {
		t.Fatal("underfunded source final owner admitted", finals, err)
	}
}
