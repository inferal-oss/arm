//go:build arm_devtrace

package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/client"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/nodestop"
)

// Real Serve/Stop/marker/native lock. Publication+helper-entry are deliberately
// separate premises: fixture bytes are never executed, and this is not SSH or
// a second claim of the live VM helper route. No fabricated stopped observation.
func TestUninstallActualDevTrace(t *testing.T) {
	for _, name := range []string{"removed-source-cas", "lost-finish-ack", "copy-unknown-new-key", "grant-read-after-revoke", "replaced-name-refusal"} {
		t.Run(name, func(t *testing.T) {
			source, _, sourceStatus := stopPrivateBackend(t)
			home, e := filepath.EvalSymlinks(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			if e = os.Chmod(home, 0700); e != nil {
				t.Fatal(e)
			}
			destination, _, status := startOwnedStopServe(t, home)
			left := stopEnablePrivateMesh(t, source, sourceStatus.Host, "uninstall-trace-source")
			right := stopEnablePrivateMesh(t, home, status.Host, "uninstall-trace-destination")
			peer := stopPairPrivateGrant(t, source, home, left, right, strings.Repeat("d", 64), mesh.Grant{Permissions: []string{"node-stop", "node-uninstall", "observe-own"}})
			key := "uninstall-actual-" + name
			plan, e := stopPlanPrivate(t, source, right, peer, mesh.UninstallStopKey(key))
			if e != nil {
				t.Fatal(e)
			}
			hash, e := plan.Digest()
			if e != nil {
				t.Fatal(e)
			}
			root, e := filepath.EvalSymlinks(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			if e = os.Chmod(root, 0700); e != nil {
				t.Fatal(e)
			}
			finish, e := mesh.DevObserveUninstall(root)
			if e != nil {
				t.Fatal(e)
			}
			done := false
			defer func() {
				if !done {
					_, _ = finish()
				}
			}()
			q, e := mesh.DevUninstallPublicationInput(root, nodestop.Prepared{Plan: plan, SHA256: hash})
			if e != nil {
				t.Fatal(e)
			}
			p, e := mesh.PrepareUninstall(q, key, plan.Reason, nodestop.Prepared{Plan: plan, SHA256: hash})
			if e != nil {
				t.Fatal(e)
			}
			if e = mesh.DevBindUninstall(p); e != nil {
				t.Fatal(e)
			}

			request := struct {
				Host         string                `json:"host"`
				ProvisionKey string                `json:"provision_key"`
				Reason       string                `json:"reason"`
				Policy       model.ShutdownRequest `json:"policy"`
			}{right.Host, "original-publication-premise", p.Reason, p.Stop.Plan.Policy}
			requestHash, _ := mesh.ReviewDigest(request)
			guard, e := mesh.ClaimCoordination(source, key, "uninstall", requestHash, "host:"+right.Host)
			if e != nil {
				t.Fatal(e)
			}
			requestBytes, _ := json.Marshal(request)
			reviewBytes, _ := json.Marshal(p)
			reviewHash, _ := p.Digest()
			pending := mesh.UninstallResult{Receipt: mesh.UninstallReceipt{Schema: mesh.UninstallSchema, Plan: p, SHA256: reviewHash, Phase: "unknown"}, Detail: "Original source approval; outcome not known"}
			pendingBytes, _ := json.Marshal(pending)
			if e = guard.Change(func(op *mesh.Coordination) error {
				op.Request = requestBytes
				op.Review = reviewBytes
				op.ReviewHash = reviewHash
				op.Approved = true
				op.Phase = "incomplete"
				op.Result = pendingBytes
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			if e = mesh.DevUninstallSourceObserved("source-initial", p, source); e != nil {
				t.Fatal(e)
			}
			mesh.DevUninstallDriver("driver-initial", p, nil)
			if e = mesh.AuthorizeUninstallNative(p); e != nil {
				t.Fatal(e)
			}

			copyCtx, cancelCopy := context.WithCancel(t.Context())
			defer cancelCopy()
			if name == "copy-unknown-new-key" {
				if e = mesh.DevUninstallCutHook(root, func(f mesh.UninstallDevFact) {
					if f.Action == "copy-output-created" {
						cancelCopy()
					}
				}); e != nil {
					t.Fatal(e)
				}
			}
			admitted, e := mesh.BeginUninstall(copyCtx, p)
			if name == "copy-unknown-new-key" {
				if e == nil || admitted.Phase != "copy-unknown" {
					t.Fatal("real cancelled copy was not retained", admitted, e)
				}
				if e = mesh.DevUninstallCutHook(root, nil); e != nil {
					t.Fatal(e)
				}
				next, e := stopPlanPrivate(t, source, right, peer, mesh.UninstallStopKey("other-key"))
				if e != nil {
					t.Fatal(e)
				}
				nextHash, _ := next.Digest()
				_, e = mesh.PrepareUninstall(q, "other-key", next.Reason, nodestop.Prepared{Plan: next, SHA256: nextHash})
				mesh.DevUninstallOtherKey(p, "other-key", e)
				if e == nil || !strings.Contains(e.Error(), "pending uninstall") {
					t.Fatal("new key escaped original copy duty", e)
				}
				if _, e = os.Lstat(mesh.UninstallRecoveryPath(q, "other-key")); !os.IsNotExist(e) {
					t.Fatal("new recovery allocation appeared", e)
				}
			} else {
				if e != nil || admitted.Phase != "accepted" {
					t.Fatal(admitted, e)
				}
				raw, _ := json.Marshal(nodestop.Apply{Key: plan.Key, Plan: plan, ReviewSHA256: hash})
				stopErr := client.Call(t.Context(), source, "mesh_dispatch", struct {
					Host    string
					Request mesh.Request
				}{right.Host, mesh.Request{Operation: "node_stop_apply", Key: plan.Key, Expected: &mesh.ExpectedEnrollment{Pair: peer.Pair, Revision: peer.Revision}, Params: raw}}, nil)
				if _, e = mesh.MarkUninstallStop(p, stopErr == nil); e != nil {
					t.Fatal(e)
				}
				destination.joined(t)
				if stopErr != nil {
					t.Log("original Stop ACK unavailable; actual joined marker/lock determines inspection", stopErr)
				}
				if name == "grant-read-after-revoke" {
					store, e := mesh.Open(home)
					if e != nil {
						t.Fatal(e)
					}
					config := store.Config()
					if e = store.Close(); e != nil {
						t.Fatal(e)
					}
					for i := range config.Peers {
						if config.Peers[i].Identity.Host == left.Host {
							config.Peers[i].Revision++
							config.Peers[i].Grant = mesh.Grant{Permissions: []string{"observe-own"}}
						}
					}
					data, _ := json.Marshal(config)
					if e = os.WriteFile(filepath.Join(home, "mesh", "config.json"), data, 0600); e != nil {
						t.Fatal(e)
					}
					if e = mesh.AuthorizeUninstallRead(p); e != nil {
						t.Fatal("own inspection denied", e)
					}
				}
				if name == "replaced-name-refusal" {
					artifact := filepath.Dir(mesh.UninstallInstalledPath(q))
					if e = os.Rename(artifact, artifact+"-original"); e != nil {
						t.Fatal(e)
					}
					if e = os.Mkdir(artifact, 0700); e != nil {
						t.Fatal(e)
					}
					if e = os.WriteFile(filepath.Join(artifact, "arm"), []byte("unowned replacement sentinel"), 0700); e != nil {
						t.Fatal(e)
					}
				}
				nativeCtx, cancelNative := context.WithCancel(t.Context())
				defer cancelNative()
				if name == "lost-finish-ack" {
					if e = mesh.DevUninstallCutHook(root, func(f mesh.UninstallDevFact) {
						if f.Action == "receipt-write-return" && f.Attempt != nil && f.Attempt.Phase == "removed" && f.Error == "" {
							cancelNative()
						}
					}); e != nil {
						t.Fatal(e)
					}
				}
				var result mesh.UninstallResult
				// Explicit test-supplied review premise, not a UI acceptance claim.
				mesh.DevUninstallDriver("finish-reviewed", p, nil)
				_, nativeErr := WithInspectedNodeStop(nativeCtx, home, plan.Target, plan.Key, func(observation nodestop.Observation) error {
					var e error
					result, e = mesh.RemoveUninstallArtifact(p, observation)
					return e
				})
				if e = mesh.DevUninstallCutHook(root, nil); e != nil {
					t.Fatal(e)
				}
				mesh.DevUninstallResultObserved("finish-call-return", p, result, nativeErr)
				switch name {
				case "grant-read-after-revoke", "replaced-name-refusal":
					if nativeErr == nil {
						t.Fatal("changed authority/name allowed removal", result)
					}
					if _, e = os.Stat(mesh.UninstallInstalledPath(q)); e != nil {
						t.Fatal("refusal removed the named binary", e)
					}
				case "lost-finish-ack":
					if nativeErr == nil || !errors.Is(nativeErr, context.Canceled) {
						t.Fatal("actual post-removal reply loss absent", nativeErr)
					}
					// Fresh exact-key inspection, no Stop/copy/unlink retry.
					observation, e := InspectNodeStop(t.Context(), home, plan.Target, plan.Key)
					if e != nil {
						t.Fatal(e)
					}
					receipt, e := mesh.LookupUninstall(q, key)
					if e != nil {
						t.Fatal(e)
					}
					result, e = mesh.ObserveUninstall(receipt, observation)
					if e != nil {
						t.Fatal(e)
					}
					mesh.DevUninstallResultObserved("lookup-call-return", p, result, nil)
					fallthrough
				case "removed-source-cas":
					if name == "removed-source-cas" && nativeErr != nil {
						t.Fatal(nativeErr)
					}
					if result.Receipt.Phase != "removed" || !result.StopObserved || !result.ArtifactAbsent {
						t.Fatal("original actual removal not positively observed", result)
					}
					// The original synchronous caller has returned; its creator settles only
					// that source owner. Expiry alone never clears an unknown worker.
					if e = guard.Change(func(op *mesh.Coordination) error { op.Worker = ""; op.WorkerExpires = time.Time{}; return nil }); e != nil {
						t.Fatal(e)
					}
					if e = mesh.DevUninstallSourceObserved("source-owner-returned", p, source); e != nil {
						t.Fatal(e)
					}
					before, _, e := mesh.ReadCoordination(source, key)
					if e != nil {
						t.Fatal(e)
					}
					body, _ := json.Marshal(result)
					if _, e = mesh.RecordUninstallLookup(source, key, before.Revision-1, body, nil); e == nil {
						t.Fatal("stale source CAS accepted")
					}
					saved, e := mesh.RecordUninstallLookup(source, key, before.Revision, body, nil)
					if e != nil || saved.Phase != "completed" {
						t.Fatal(saved, e)
					}
					if _, e = mesh.RecordUninstallLookup(source, key, saved.Revision, pendingBytes, errors.New("later inspection unavailable")); e != nil {
						t.Fatal(e)
					}
					if e = mesh.DevUninstallSourceObserved("source-monotone-observed", p, source); e != nil {
						t.Fatal(e)
					}
				}
			}
			mesh.DevUninstallDriver("driver-final", p, nil)
			facts, e := finish()
			done = true
			if e != nil {
				t.Fatal(e)
			}
			for _, fact := range facts {
				raw, e := json.Marshal(fact)
				if e != nil {
					t.Fatal(e)
				}
				t.Log("UNINSTALL_ACTUAL " + string(raw))
			}
		})
	}
}
