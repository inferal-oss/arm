package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"

	"getarm.org/internal/kubesource"
	"getarm.org/internal/model"
)

func invocationCaller(t *testing.T) (chan struct{}, func()) {
	t.Helper()
	joined := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(joined) }) }
	t.Cleanup(finish)
	return joined, finish
}
func invocationJoined(t *testing.T, i *KubernetesSourceInvocation) {
	t.Helper()
	if i.preparation != nil {
		sourceCoreWait(t, i.preparation.done)
		sourceCoreWait(t, i.preparation.transit.workerDone)
	}
}
func invocationUnknown(t *testing.T, i *KubernetesSourceInvocation) {
	t.Helper()
	out, err := i.OutcomeAfterJoin(t.Context())
	var typed *model.Error
	if !errors.As(err, &typed) || typed.Code != "source_outcome_unknown" || out.Disposition != "unknown" {
		t.Fatalf("uncertain original became positive: %+v %v", out, err)
	}
}
func TestKubernetesInvocationSeparatesNoAdmissionAndPartialRefusal(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial=%v", partial), func(t *testing.T) {
			m := sourceCoreManager(t)
			if partial {
				m.capacity.Disk = 1
			} else {
				m.capacity.Memory = 1
			}
			joined, finish := invocationCaller(t)
			i, err := m.PrepareKubernetesSourceInvocation(t.Context(), sourceCoreScope(kubesource.Ship), joined)
			if err == nil || i == nil {
				t.Fatal("refusal lost original invocation", err)
			}
			if p, e := i.Preparation(); p != nil || e == nil {
				t.Fatal("failed constructor exposed preparation")
			}
			if out, e := i.OutcomeAfterJoin(t.Context()); e == nil || out.Disposition != "unknown" {
				t.Fatal("unjoined caller produced outcome")
			}
			finish()
			invocationJoined(t, i)
			out, err := i.OutcomeAfterJoin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			want := "no_admission"
			admitted := 0
			if partial {
				want = "closed_originals"
				admitted = 1
			}
			actual := 0
			for _, row := range out.Rows {
				if row.Admitted {
					actual++
					if row.State != "terminal" {
						t.Fatal(row)
					}
				}
			}
			if out.Disposition != want || actual != admitted || len(out.Rows) != 2 {
				t.Fatalf("%+v", out)
			}
			if partial && (!out.Result.SourceClosed || !out.Result.SourceSettled || i.preparation == nil) {
				t.Fatal("partial original cleanup not retained")
			}
		})
	}
}
func TestKubernetesInvocationCapturesIdentityBeforeAdmissionSQL(t *testing.T) {
	m := sourceCoreManager(t)
	m.capacity.Memory = 1
	joined, finish := invocationCaller(t)
	i, err := m.newKubernetesInvocation(t.Context(), sourceCoreScope(kubesource.Inspect), joined)
	if err != nil {
		t.Fatal(err)
	}
	// Force the constructor's first ledger read to fail. Original proposed IDs,
	// keys and fixed requests were captured before this SQL, not learned from it.
	if _, err = m.db.Exec(`ALTER TABLE leases RENAME TO withheld_leases`); err != nil {
		t.Fatal(err)
	}
	if err = i.construct(); err == nil {
		t.Fatal("failed SQL reported success")
	}
	finish()
	invocationUnknown(t, i)
	for _, c := range i.candidates {
		if len(c.id) != 32 || c.key != c.request.Key || c.attempted || len(c.initial) != 0 {
			t.Fatal("missing or invented capture", c.role)
		}
	}
	if _, err = m.db.Exec(`ALTER TABLE withheld_leases RENAME TO leases`); err != nil {
		t.Fatal(err)
	}
	out, err := i.OutcomeAfterJoin(t.Context())
	if err != nil || out.Disposition != "no_admission" {
		t.Fatal(out, err)
	}
}
func TestKubernetesInvocationUnacknowledgedCommitNeverBecomesNoAdmission(t *testing.T) {
	for _, role := range []kubesource.Role{kubesource.SourceRole, kubesource.StorageRole, kubesource.PortRole} {
		t.Run(string(role), func(t *testing.T) {
			m := sourceCoreManager(t)
			scope := sourceCoreScope(kubesource.Ship)
			if role == kubesource.PortRole {
				scope.Stage = kubesource.Forward
			}
			joined, finish := invocationCaller(t)
			i, err := m.newKubernetesInvocation(t.Context(), scope, joined)
			if err != nil {
				t.Fatal(err)
			}
			c := i.candidate(role != kubesource.SourceRole, role == kubesource.PortRole)
			c.lostAdmissionACK = true
			if err = i.construct(); err == nil {
				t.Fatal("lost ACK returned success")
			}
			finish()
			invocationJoined(t, i)
			invocationUnknown(t, i)
			var data string
			if err = m.db.QueryRow(`SELECT data FROM leases WHERE id=? AND request_key=?`, c.id, c.key).Scan(&data); err != nil {
				t.Fatal("committed original row absent", err)
			}
			var l model.Lease
			if json.Unmarshal([]byte(data), &l) != nil || l.ID != c.id || !consuming(l.State) {
				t.Fatal("unacknowledged original claim was released", l.State)
			}
			if !c.commitAttempted || c.acknowledged || len(c.initial) == 0 {
				t.Fatal("unknown commit capture lost")
			}
			if role == kubesource.PortRole {
				source, readErr := m.get(i.preparation.transit.id)
				if readErr != nil || source.State != "needs_attention" || !consuming(l.State) || i.preparation.transit.recoveryReason == "" {
					t.Fatal("lost port ACK erased original source or port uncertainty", source.State, l.State, readErr)
				}
			}
		})
	}
}
func TestKubernetesInvocationRolledBackInsertHasExactAbsence(t *testing.T) {
	m := sourceCoreManager(t)
	if _, err := m.db.Exec(`CREATE TRIGGER refuse_source BEFORE INSERT ON leases BEGIN SELECT RAISE(FAIL,'fixture insert refusal'); END`); err != nil {
		t.Fatal(err)
	}
	joined, finish := invocationCaller(t)
	i, err := m.PrepareKubernetesSourceInvocation(t.Context(), sourceCoreScope(kubesource.Ship), joined)
	if err == nil || i == nil {
		t.Fatal(err)
	}
	finish()
	c := i.candidate(false, false)
	if !c.attempted || c.commitAttempted || !c.rolledBack || len(c.initial) == 0 {
		t.Fatal("rollback was not observed")
	}
	out, err := i.OutcomeAfterJoin(t.Context())
	if err != nil || out.Disposition != "no_admission" {
		t.Fatal(out, err)
	}
}
func invocationFinishedOwner(t *testing.T, forward bool) (*Manager, *KubernetesSourceInvocation) {
	t.Helper()
	m := sourceCoreManager(t)
	scope := sourceCoreScope(kubesource.Ship)
	joined, finish := invocationCaller(t)
	var i *KubernetesSourceInvocation
	var err error
	if forward {
		scope.Stage = kubesource.Forward
		i, err = m.PrepareKubernetesForwardSourceInvocation(t.Context(), scope, joined)
	} else {
		i, err = m.PrepareKubernetesSourceInvocation(t.Context(), scope, joined)
	}
	if err != nil {
		t.Fatal(err)
	}
	k, err := i.Preparation()
	if err != nil || k == nil {
		t.Fatal(err)
	}
	// Keep ordinary storage cleanup pending, without pretending retained disk is
	// reclaimed. The real constructor still owns its directory/port originals.
	m.mu.Lock()
	m.storageJobs[k.parent.ID] = true
	m.mu.Unlock()
	t.Cleanup(func() { m.mu.Lock(); delete(m.storageJobs, k.parent.ID); m.mu.Unlock() })
	finish()
	_, err = k.FinishAfterJoin()
	if err != nil {
		t.Fatal(err)
	}
	invocationJoined(t, i)
	return m, i
}
func TestKubernetesInvocationClosedOriginalsRetainStorageDebt(t *testing.T) {
	for _, forward := range []bool{false, true} {
		t.Run(fmt.Sprintf("forward=%v", forward), func(t *testing.T) {
			m, i := invocationFinishedOwner(t, forward)
			out, err := i.OutcomeAfterJoin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if out.Disposition != "closed_originals_storage_retained" || !out.Result.SourceClosed || !out.Result.SourceSettled || out.Binding.SourceLeaseID == "" || out.Binding.StorageLeaseID == "" {
				t.Fatal(out)
			}
			if forward && (!out.Result.PortClosed || !out.Result.PortSettled || out.Binding.PortLeaseID == "") {
				t.Fatal(out)
			}
			l, err := m.get(out.Binding.StorageLeaseID)
			if err != nil || l.State != "ending" || l.Ended != nil || l.Request.Resources.Disk != i.scope.DiskBytes() {
				t.Fatal("census changed retained charge", l, err)
			}
			if i.preparation.Files() != nil {
				t.Fatal("completed owner exposed factory")
			}
		})
	}
}
func TestKubernetesInvocationCensusRejectsCorruptPhysicalAndBodyRows(t *testing.T) {
	m, i := invocationFinishedOwner(t, false)
	c := i.candidate(false, false)
	var original string
	if err := m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, c.id).Scan(&original); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"blob-id", "blob-key", "key-move", "run-empty", "run-blob", "body-id", "body-request", "clock", "unknown-field", "oversize", "blob-data", "alias"} {
		t.Run(kind, func(t *testing.T) {
			var err error
			switch kind {
			case "blob-id":
				_, err = m.db.Exec(`UPDATE leases SET id=CAST(id AS BLOB) WHERE id=?`, c.id)
			case "blob-key":
				_, err = m.db.Exec(`UPDATE leases SET request_key=CAST(request_key AS BLOB) WHERE id=?`, c.id)
			case "key-move":
				_, err = m.db.Exec(`UPDATE leases SET request_key='foreign-key' WHERE id=?`, c.id)
			case "run-empty":
				_, err = m.db.Exec(`UPDATE leases SET run='' WHERE id=?`, c.id)
			case "run-blob":
				_, err = m.db.Exec(`UPDATE leases SET run=X'' WHERE id=?`, c.id)
			case "body-id":
				var l model.Lease
				_ = json.Unmarshal([]byte(original), &l)
				l.ID = strings.Repeat("f", 32)
				b, _ := json.Marshal(l)
				_, err = m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(b), c.id)
			case "body-request":
				var l model.Lease
				_ = json.Unmarshal([]byte(original), &l)
				l.Request.Resources.CPU *= 2
				b, _ := json.Marshal(l)
				_, err = m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(b), c.id)
			case "clock":
				var l model.Lease
				_ = json.Unmarshal([]byte(original), &l)
				deadline := *l.Clock
				deadline.End++
				l.Clock = &deadline
				b, _ := json.Marshal(l)
				_, err = m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(b), c.id)
			case "unknown-field":
				_, err = m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, original[:len(original)-1]+`,"unknown":true}`, c.id)
			case "oversize":
				_, err = m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, strings.Repeat(" ", kubernetesInvocationRowBytes+1), c.id)
			case "blob-data":
				_, err = m.db.Exec(`UPDATE leases SET data=CAST(data AS BLOB) WHERE id=?`, c.id)
			case "alias":
				_, err = m.db.Exec(`INSERT INTO leases(id,request_key,data,run) VALUES(?,CAST(? AS BLOB),?,NULL)`, strings.Repeat("f", 32), c.key, original)
			}
			if err != nil {
				t.Fatal(err)
			}
			invocationUnknown(t, i)
			if _, err = m.db.Exec(`DELETE FROM leases WHERE id=?`, strings.Repeat("f", 32)); err != nil {
				t.Fatal(err)
			}
			if _, err = m.db.Exec(`UPDATE leases SET id=?,request_key=?,data=?,run=NULL WHERE CAST(id AS TEXT)=?`, c.id, c.key, original, c.id); err != nil {
				t.Fatal(err)
			}
			out, err := i.OutcomeAfterJoin(t.Context())
			if err != nil || out.Disposition != "closed_originals_storage_retained" {
				t.Fatal("exact restoration failed", out, err)
			}
		})
	}
}
func TestKubernetesInvocationNativeCloseFailureStaysUnknown(t *testing.T) {
	m := sourceCoreManager(t)
	joined, finish := invocationCaller(t)
	i, err := m.PrepareKubernetesSourceInvocation(t.Context(), sourceCoreScope(kubesource.Ship), joined)
	if err != nil {
		t.Fatal(err)
	}
	k, _ := i.Preparation()
	payload, err := k.Files().NewPayload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_ = payload // held by the original factory until its joined cleanup
	calls := 0
	k.files.closeFile = func(f *os.File) error { calls++; return errors.Join(f.Close(), syscall.EIO) }
	finish()
	result, err := k.FinishAfterJoin()
	if err == nil || result.SourceSettled {
		t.Fatal(result, err)
	}
	invocationJoined(t, i)
	invocationUnknown(t, i)
	_, _ = k.FinishAfterJoin()
	invocationUnknown(t, i)
	if calls != 1 {
		t.Fatal("native first close was retried", calls)
	}
	status, err := m.Status()
	if err != nil || status.Reserved.CPU != kubesource.SourceCPU || status.Reserved.Disk != i.scope.DiskBytes() {
		t.Fatal("cleanup uncertainty erased charge", status.Reserved, err)
	}
}

func TestKubernetesInvocationCannotBeReconstructedOrReadDuringClose(t *testing.T) {
	m, i := invocationFinishedOwner(t, false)
	copy := *i
	invocationUnknown(t, &copy)
	invocationUnknown(t, &KubernetesSourceInvocation{})
	saved := i.candidates[0].initial
	i.candidates[0].initial = nil
	invocationUnknown(t, i)
	i.candidates[0].initial = saved
	m.mu.Lock()
	out, err := i.OutcomeAfterJoin(t.Context())
	m.mu.Unlock()
	if err == nil || out.Disposition != "unknown" {
		t.Fatal("busy lock granted census")
	}
	m.mu.Lock()
	m.closing = true
	m.mu.Unlock()
	invocationUnknown(t, i)
	m.mu.Lock()
	m.closing = false
	m.mu.Unlock()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if out, e := i.OutcomeAfterJoin(canceled); e == nil || out.Disposition != "unknown" {
		t.Fatal("canceled census became positive")
	}
	if _, err = i.OutcomeAfterJoin(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// A pre-admission refusal has no acknowledged row, so accidentally omitting a
// later malformed physical match would falsely turn this census into absence.
func TestKubernetesInvocationCensusFindsMalformedPhysicalMatchesBeforeAbsence(t *testing.T) {
	m := sourceCoreManager(t)
	m.capacity.Memory = 1
	joined, finish := invocationCaller(t)
	i, err := m.PrepareKubernetesSourceInvocation(t.Context(), sourceCoreScope(kubesource.Ship), joined)
	if err == nil || i == nil {
		t.Fatal("expected pre-admission refusal", err)
	}
	finish()
	assertAbsent := func(t *testing.T) {
		t.Helper()
		out, e := i.OutcomeAfterJoin(t.Context())
		if e != nil || out.Disposition != "no_admission" {
			t.Fatal("exact empty census lost", out, e)
		}
	}
	assertAbsent(t)
	c := i.candidate(false, false)
	for _, kind := range []string{"oversize-text-id", "oversize-blob-id", "oversize-text-key", "oversize-blob-key", "blob-id-only", "blob-key-only"} {
		t.Run(kind, func(t *testing.T) {
			var id, key any = c.id, c.key
			switch kind {
			case "oversize-text-id":
				id = strings.Repeat("a", kubernetesInvocationRowBytes+1)
			case "oversize-blob-id":
				id = []byte(strings.Repeat("a", kubernetesInvocationRowBytes+1))
			case "oversize-text-key":
				key = strings.Repeat("b", kubernetesInvocationRowBytes+1)
			case "oversize-blob-key":
				key = []byte(strings.Repeat("b", kubernetesInvocationRowBytes+1))
			case "blob-id-only":
				id = []byte(c.id)
				key = "foreign-request"
			case "blob-key-only":
				id = strings.Repeat("f", 32)
				key = []byte(c.key)
			}
			result, e := m.db.Exec(`INSERT INTO leases(id,request_key,data,run) VALUES(?,?,?,NULL)`, id, key, `{}`)
			if e != nil {
				t.Fatal(e)
			}
			rowID, e := result.LastInsertId()
			if e != nil {
				t.Fatal(e)
			}
			invocationUnknown(t, i)
			if _, e = m.db.Exec(`DELETE FROM leases WHERE rowid=?`, rowID); e != nil {
				t.Fatal(e)
			}
			assertAbsent(t)
		})
	}
}

func TestKubernetesSourceConstructorsRefuseInterposedOriginalKeys(t *testing.T) {
	for _, recorded := range []bool{false, true} {
		t.Run(fmt.Sprintf("recorded=%v", recorded), func(t *testing.T) {
			for _, role := range []kubesource.Role{kubesource.SourceRole, kubesource.StorageRole, kubesource.PortRole} {
				t.Run(string(role), func(t *testing.T) {
					m := sourceCoreManager(t)
					scope := sourceCoreScope(kubesource.Ship)
					if role == kubesource.PortRole {
						scope.Stage = kubesource.Forward
					}
					joined, finish := invocationCaller(t)
					foreignJoined, finishForeign := invocationCaller(t)
					var foreign model.Lease
					var foreignRaw string
					var foreignTransit *RelayTransit
					var interposedErr error
					t.Cleanup(func() {
						finishForeign()
						if foreignTransit != nil {
							sourceCoreWait(t, foreignTransit.workerDone)
						}
					})
					m.sourceBeforeAdmission = func() {
						budget := m.canonicalBudgetSelector("")
						if role == kubesource.SourceRole {
							foreignTransit, interposedErr = m.acquireKubernetesSourceTransit(t.Context(), scope, budget, foreignJoined)
							if interposedErr == nil {
								foreign = foreignTransit.result
							}
						} else {
							request := scope.Request(m.host, budget, true)
							if role == kubesource.PortRole {
								request = scope.PortRequest(m.host, budget, m.portNamespace)
							}
							m.mu.Lock()
							foreign, interposedErr = m.acquireWithSourcePermit(request, nil, false, nil, &kubernetesSourcePermit{manager: m, scope: scope, parent: true, port: role == kubesource.PortRole, request: request}, nil)
							m.mu.Unlock()
						}
						if interposedErr == nil {
							interposedErr = m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, foreign.ID).Scan(&foreignRaw)
						}
					}
					var preparation *KubernetesSourcePreparation
					var invocation *KubernetesSourceInvocation
					var err error
					t.Cleanup(func() {
						finish()
						if preparation != nil {
							_, _ = preparation.FinishAfterJoin()
						}
					})
					if recorded {
						if role == kubesource.PortRole {
							invocation, err = m.PrepareKubernetesForwardSourceInvocation(t.Context(), scope, joined)
						} else {
							invocation, err = m.PrepareKubernetesSourceInvocation(t.Context(), scope, joined)
						}
						if invocation != nil {
							preparation, _ = invocation.Preparation()
						}
					} else if role == kubesource.PortRole {
						preparation, err = m.PrepareKubernetesForwardSource(t.Context(), scope, joined)
					} else {
						preparation, err = m.PrepareKubernetesSource(t.Context(), scope, joined)
					}
					if interposedErr != nil {
						t.Fatal("foreign original admission failed", interposedErr)
					}
					if err == nil || preparation != nil {
						t.Fatal("existing physical row became a source preparation", err)
					}
					if recorded {
						if invocation == nil {
							t.Fatal("failed constructor lost invocation")
						}
						c := invocation.candidate(role != kubesource.SourceRole, role == kubesource.PortRole)
						if c.id == foreign.ID || c.attempted || c.acknowledged {
							t.Fatal("foreign physical row became captured admission")
						}
						finish()
						invocationJoined(t, invocation)
						invocationUnknown(t, invocation)
						if k := invocation.preparation; k != nil && (k.files != nil || k.socket != nil) {
							t.Fatal("refused parent key created a file factory or native socket")
						}
					}
					var after string
					if e := m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, foreign.ID).Scan(&after); e != nil || after != foreignRaw {
						t.Fatal("foreign original row was changed", e)
					}
					m.mu.Lock()
					registered := m.kubernetesSources[foreign.ID]
					sameTransit := foreignTransit == nil || m.relayTransits[foreign.ID] == foreignTransit
					m.mu.Unlock()
					if registered != nil || !sameTransit {
						t.Fatal("foreign original registration replaced")
					}
					if foreignTransit != nil && foreignTransit.Context().Err() != nil {
						t.Fatal("foreign original was canceled")
					}
					rows, e := m.list()
					if e != nil {
						t.Fatal(e)
					}
					for _, l := range rows {
						if l.Request.Key == scope.Key(false) && l.ID != foreign.ID && (l.State != "terminal" || l.Request.StorageLease != "" || l.Request.PortLease != "") {
							t.Fatal("failed constructor left an attached or live source", l.State)
						}
					}
					// The interposed storage/port fixture really admitted only that fixed role.
					// No native source factory/helper/socket is constructed from its saved row.
					if role == kubesource.SourceRole && m.storage != nil {
						t.Fatal("refused source key created storage")
					}
				})
			}
		})
	}
}

// The real provider's acknowledged capability transition is distinct from its
// pre-INSERT admission snapshot. Neither a rewritten row nor a matching rewrite
// of the cached original can authorize a different capability list.
func TestKubernetesInvocationCapabilitiesRequireOriginalProvisioning(t *testing.T) {
	m, i := invocationFinishedOwner(t, true)
	k := i.preparation
	parent := i.candidate(true, false)
	var admitted model.Lease
	if err := json.Unmarshal(parent.initial, &admitted); err != nil {
		t.Fatal(err)
	}
	expected := append(append([]string{}, admitted.Capabilities...), "managed-storage", "explicit-storage-consumers")
	if k.parent.Artifact == nil || k.parent.Artifact.Identity == nil || !reflect.DeepEqual(k.parent.Capabilities, expected) || reflect.DeepEqual(admitted.Capabilities, expected) {
		t.Fatal("real original provider did not acknowledge its capability transition")
	}
	if out, err := i.OutcomeAfterJoin(t.Context()); err != nil || out.Disposition != "closed_originals_storage_retained" {
		t.Fatal("original provisioned capabilities refused", out, err)
	}
	for _, change := range []string{"missing-provider-pair", "extra-storage-capability", "reordered-provider-pair", "cached-and-row-capability", "missing-original-identity", "source-capability", "port-capability"} {
		t.Run(change, func(t *testing.T) {
			c := parent
			if change == "source-capability" {
				c = i.candidate(false, false)
			} else if change == "port-capability" {
				c = i.candidate(true, true)
			}
			var raw string
			if err := m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, c.id).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			cached := k.parent
			defer func() {
				k.parent = cached
				if _, err := m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, raw, c.id); err != nil {
					t.Error("restore original capability record", err)
				}
			}()
			var changed model.Lease
			if err := json.Unmarshal([]byte(raw), &changed); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "missing-provider-pair":
				changed.Capabilities = append([]string{}, admitted.Capabilities...)
			case "reordered-provider-pair":
				n := len(changed.Capabilities)
				changed.Capabilities[n-2], changed.Capabilities[n-1] = changed.Capabilities[n-1], changed.Capabilities[n-2]
			case "cached-and-row-capability":
				changed.Capabilities = append(changed.Capabilities, "foreign-capability")
				k.parent.Capabilities = append([]string{}, changed.Capabilities...)
			case "missing-original-identity":
				artifact := *k.parent.Artifact
				artifact.Identity = nil
				k.parent.Artifact = &artifact
			default:
				changed.Capabilities = append(changed.Capabilities, "foreign-capability")
			}
			encoded, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(encoded), c.id); err != nil {
				t.Fatal(err)
			}
			invocationUnknown(t, i)
		})
	}
	if out, err := i.OutcomeAfterJoin(t.Context()); err != nil || out.Disposition != "closed_originals_storage_retained" {
		t.Fatal("restored original provisioned capabilities refused", out, err)
	}
}

func TestKubernetesInvocationNoPortAttemptPreservesOriginalDebts(t *testing.T) {
	m := sourceCoreManager(t)
	scope := sourceCoreScope(kubesource.Forward)
	joined, finish := invocationCaller(t)
	i, err := m.newKubernetesInvocation(t.Context(), scope, joined)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the admitted original storage debt pending independently from CPU
	// closure, as in the ordinary closed-original fixture. No port is opened.
	parent := i.candidate(true, false)
	m.mu.Lock()
	m.storageJobs[parent.id] = true
	m.mu.Unlock()
	t.Cleanup(func() { m.mu.Lock(); delete(m.storageJobs, parent.id); m.mu.Unlock() })
	var foreign model.Lease
	var foreignRaw string
	var interposedErr error
	m.sourceBeforeAdmission = func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		request := scope.PortRequest(m.host, m.canonicalBudgetSelector(""), m.portNamespace)
		foreign, interposedErr = m.acquireWithSourcePermit(request, nil, false, nil, &kubernetesSourcePermit{manager: m, scope: scope, parent: true, port: true, request: request}, nil)
		if interposedErr == nil {
			interposedErr = m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, foreign.ID).Scan(&foreignRaw)
		}
	}
	if err = i.construct(); err == nil || interposedErr != nil {
		t.Fatal("port-key interposition did not refuse construction", err, interposedErr)
	}
	finish()
	invocationJoined(t, i)
	if preparation, err := i.Preparation(); preparation != nil || err == nil {
		t.Fatal("refused original exposed preparation", err)
	}
	k := i.preparation
	port := i.candidate(true, true)
	if k == nil || k.files != nil || k.socket != nil || k.portID != "" || port.attempted || port.commitAttempted || port.acknowledged || len(port.initial) != 0 {
		t.Fatal("key refusal adopted a port or performed an original port attempt")
	}
	out, err := i.OutcomeAfterJoin(t.Context())
	if err == nil || out.Disposition != "unknown" {
		t.Fatal("foreign physical-key match became original outcome evidence", out, err)
	}
	source, sourceErr := m.get(k.transit.id)
	storage, storageErr := m.get(parent.id)
	if sourceErr != nil || storageErr != nil || source.State != "terminal" || !k.transit.settled || k.transit.recoveryReason != "" || !k.result.SourceClosed || !k.result.SourceSettled || source.Request.StorageLease != "" || source.Request.PortLease != "" {
		t.Fatal("proven no-port-attempt did not close only its original source", source, sourceErr, storageErr)
	}
	if storage.State != "ending" || storage.Ended != nil || storage.Request.Resources.Disk != scope.DiskBytes() || !k.result.StorageRetained {
		t.Fatal("source retirement erased retained original storage debt", storage.State, k.result)
	}
	var after string
	if err = m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, foreign.ID).Scan(&after); err != nil || after != foreignRaw || !consuming(foreign.State) || foreign.ID == port.id {
		t.Fatal("key refusal changed the foreign admitted port obligation", err)
	}
}
