package syncworker

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/syncreconcile"
	"github.com/inferal-oss/arm/internal/syncservice"
)

type allocationCutReviewHold struct {
	*testHold
	ledger           *syncservice.Ledger
	cut              string
	cancel           context.CancelFunc
	fired            bool
	marker, retained string
}

func (h *allocationCutReviewHold) CheckActive(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := h.testHold.CheckActive(ctx); err != nil {
		return err
	}
	if h.fired {
		return nil
	}
	var effects, workspaces int
	if err := h.ledger.DB.QueryRowContext(ctx, `SELECT count(*) FROM sync_effects WHERE outcome IS NOT NULL`).Scan(&effects); err != nil {
		return err
	}
	if err := h.ledger.DB.QueryRowContext(ctx, `SELECT count(*) FROM sync_workspaces`).Scan(&workspaces); err != nil {
		return err
	}
	trigger := h.cut == "before-cancel" && workspaces > 0 || h.cut != "before-cancel" && effects > 0
	if !trigger {
		return nil
	}
	h.fired = true
	if h.cut == "after-unknown" {
		var path string
		if err := h.ledger.DB.QueryRowContext(ctx, `SELECT path FROM sync_workspaces WHERE role='receiver'`).Scan(&path); err != nil {
			return err
		}
		h.marker = filepath.Join(path, ".arm-sync-owner.json")
		h.retained = h.marker + ".retained"
		return os.Rename(h.marker, h.retained)
	}
	h.cancel()
	return ctx.Err()
}

func TestWorkerAllocationCheckpointRetainsEvidenceAcrossPublicationCuts(t *testing.T) {
	for _, cut := range []string{"before-cancel", "after-cancel", "after-unknown"} {
		t.Run(cut, func(t *testing.T) {
			p := setupPair(t)
			write(t, p.local, "file", "publish reviewed content")
			write(t, p.remote, "keep", "retained original")
			req := requestPair(t, p, syncreconcile.Push, syncreconcile.Additive, nil, nil)
			plan, err := syncreconcile.Reconcile(req)
			if err != nil {
				t.Fatal(err)
			}
			res := model.Resources{CPU: 1, Memory: 64 << 20, Disk: 64 << 20}
			local, rows, err := syncservice.NewIntent("allocation-cut-original-key", FixedCommandSHA256(), hash("reviewed SSH control"), "allocation cut fixture", syncreconcile.Local, req, plan, res, res, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			remote, other, err := syncservice.NewIntent(local.Key, local.WorkerCommandSHA256, local.SSHControlSHA256, local.Reason, syncreconcile.Remote, req, plan, res, res, local.Created)
			if err != nil {
				t.Fatal(err)
			}
			reserveTest(t, p.localDB, local, rows)
			reserveTest(t, p.remoteDB, remote, other)
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			a, b := net.Pipe()
			defer a.Close()
			defer b.Close()
			localJoins, remoteJoins := 0, 0
			ls, err := newSingleStreamSession(a, func() error { localJoins++; return nil })
			if err != nil {
				t.Fatal(err)
			}
			rs, err := newSingleStreamSession(b, func() error { remoteJoins++; return nil })
			if err != nil {
				t.Fatal(err)
			}
			ll := &syncservice.Ledger{DB: p.localDB, Key: local.Key, Side: syncreconcile.Local}
			rl := &syncservice.Ledger{DB: p.remoteDB, Key: remote.Key, Side: syncreconcile.Remote}
			lh := &testHold{intent: local, root: req.Binding.Local.RootID, disk: res.Disk}
			rh := &allocationCutReviewHold{testHold: &testHold{intent: remote, root: req.Binding.Remote.RootID, disk: res.Disk}, ledger: rl, cut: cut, cancel: cancel}
			le := &endpoint{Side: syncreconcile.Local, Root: p.local, WorkParent: p.localWork, Names: p.names, Metadata: p.metadata, Ledger: ll, Hold: lh, Session: ls}
			re := &endpoint{Side: syncreconcile.Remote, Root: p.remote, WorkParent: p.remoteWork, Names: p.names, Metadata: p.metadata, Ledger: rl, Hold: rh, Session: rs}
			ld, rd := make(chan error, 1), make(chan error, 1)
			go func() { ld <- runEndpoint(ctx, le) }()
			go func() { rd <- runEndpoint(ctx, re) }()
			var lerr, rerr error
			select {
			case rerr = <-rd:
			case <-time.After(4 * time.Second):
				cancel()
				_ = a.Close()
				_ = b.Close()
				rerr = <-rd
				lerr = <-ld
				t.Fatal("original pair failed to terminate", lerr, rerr)
			}
			select {
			case lerr = <-ld:
			case <-time.After(2 * time.Second):
				cancel()
				_ = a.Close()
				lerr = <-ld
				t.Fatal("original peer failed to join", lerr)
			}
			if !rh.fired || rerr == nil || localJoins != 1 || remoteJoins != 1 || !ls.joined || !rs.joined || !lh.charged || !rh.charged {
				t.Fatal("cut lost failure/hold/join witnesses", lerr, rerr, localJoins, remoteJoins)
			}
			if contents(t, p.remote, "keep") != "retained original" || contents(t, p.local, "file") != "publish reviewed content" {
				t.Fatal("cut changed original selected input")
			}
			result, err := rl.Lookup(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if result.LocalReceipt != nil || result.RemoteReceipt != nil || re.Receipt != nil {
				t.Fatal("failed cut created receiver completion")
			}
			if cut == "before-cancel" {
				if _, err := os.Stat(filepath.Join(p.remote.Path(), "file")); !os.IsNotExist(err) {
					t.Fatal("prepublication cancellation published", err)
				}
				if len(result.Effects) != 0 {
					t.Fatal("prepublication cancellation created effects")
				}
			} else {
				if contents(t, p.remote, "file") != "publish reviewed content" || len(result.Effects) == 0 || result.State != "applying" {
					t.Fatal("postpublication unknown lost applied evidence", result.State)
				}
			}
			for _, ledger := range []*syncservice.Ledger{ll, rl} {
				locators, err := ledger.Workspaces(t.Context())
				if err != nil || len(locators) == 0 {
					t.Fatal("cut lost durable workspace obligations", err)
				}
				for _, row := range locators {
					if _, err := os.Stat(row.Path); err != nil {
						t.Fatal("cut cleaned retained workspace", err)
					}
				}
				if _, err := syncservice.LoadBaseline(t.Context(), ledger.DB, req.Binding, ledger.Side); err == nil {
					t.Fatal("failed cut confirmed baseline")
				}
			}
			if cut == "after-unknown" {
				if _, err := os.Stat(rh.retained); err != nil {
					t.Fatal("unknown marker evidence lost", err)
				}
				if errors.Is(rerr, context.Canceled) || ctx.Err() != nil {
					t.Fatal("unknown survey required parent cancellation", rerr)
				}
			}
		})
	}
}
