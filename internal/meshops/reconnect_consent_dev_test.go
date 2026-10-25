//go:build arm_devtrace

package meshops

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/mesh"
)

// Confirmation input is captured at the delegated callback, independently of
// subsequent saved records. It is fixture input evidence, not human/auth proof.
func reconnectActiveConsent(op mesh.Coordination, artifact reconnectNativeArtifact, inputs map[string]reconnectConfirmationInput) (string, string, bool) {
	var object map[string]json.RawMessage
	if json.Unmarshal(artifact.Raw, &object) != nil {
		return "changed", "absent", false
	}
	approval := "absent"
	if bytes.Equal(object["approved"], []byte("true")) {
		approval = "approved"
	}
	if bytes.Equal(object["approved"], []byte("false")) {
		approval = "unapproved"
	}
	input, exists := inputs[op.Key]
	accepted := exists && input.Attempt == op.Key && input.Accepted
	if len(op.Review) == 0 || bytes.Equal(bytes.TrimSpace(op.Review), []byte("null")) {
		return "absent", approval, accepted
	}
	samePlan := func(left, right []byte) bool {
		decode := func(raw []byte) (any, error) {
			var value any
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			e := decoder.Decode(&value)
			return value, e
		}
		a, ea := decode(left)
		b, eb := decode(right)
		return ea == nil && eb == nil && reflect.DeepEqual(a, b)
	}
	sum := sha256.Sum256(input.Plan)
	exact := exists && input.Attempt == op.Key && input.ReviewRevision > 0 && input.ReviewRevision == op.ReviewRevision && op.ReviewRevision <= op.Revision && input.SHA256 == hex.EncodeToString(sum[:]) && op.ReviewHash == input.SHA256 && samePlan(op.Review, input.Plan)
	if exact {
		return "exact", approval, accepted
	}
	return "changed", approval, accepted
}

func reconnectControlReady(t *testing.T) *reconnectDriver {
	t.Helper()
	d := newReconnectDriver(t, nil, true)
	op, ok, e := mesh.ReadCoordination(d.f.home, "formal-old-disconnect")
	if e != nil || !ok {
		t.Fatal(op, e)
	}
	hash, e := mesh.CoordinationRecoveryHash(op)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = mesh.HandoffCoordination(d.f.home, op.Key, op.Revision, hash, "exact retained-obligation control handoff"); e != nil {
		t.Fatal(e)
	}
	if e = d.emit("handoff"); e != nil {
		t.Fatal(e)
	}
	r, e := ResolveReconnect(d.f.home, d.key, d.f.draft)
	if e != nil {
		t.Fatal(e)
	}
	d.resolved = &r
	if e = d.emit("resolve"); e != nil {
		t.Fatal(e)
	}
	return d
}

func reconnectActiveControl(t *testing.T, d *reconnectDriver, action string) reconnectObserved {
	t.Helper()
	for i := len(d.actual) - 1; i >= 0; i-- {
		if d.actual[i]["action"] == action {
			return d.actual[i]
		}
	}
	t.Fatalf("missing actual %s cut", action)
	return nil
}

func TestReconnectActualActiveChildConsentControls(t *testing.T) {
	evidence := map[string]any{}
	for _, scenario := range []string{"child-approved", "child-declined", "post-confirm-revision-refused"} {
		t.Run(scenario, func(t *testing.T) {
			d := reconnectControlReady(t)
			if scenario == "post-confirm-revision-refused" {
				d.control = "revision"
				if e := d.connect(false); e != nil {
					t.Fatal(e)
				}
				cut := reconnectActiveControl(t, d, "revision_refused")
				if cut["active_review"] != "exact" || cut["active_approval"] != "unapproved" || cut["active_accepted"] != true || d.f.pairCalls != 0 {
					t.Fatal(cut)
				}
			} else {
				d.control = "lostpair"
				if e := d.connect(false); e != nil {
					t.Fatal(e)
				}
				parent, ok, e := mesh.ReadCoordination(d.f.home, d.key)
				if e != nil || !ok || !parent.Approved {
					t.Fatal(parent, e)
				}
				oldCalls := d.f.pairCalls
				d.control = ""
				if scenario == "child-declined" {
					d.control = "decline"
				}
				if e = d.connect(true); e != nil {
					t.Fatal(e)
				}
				cut := reconnectActiveControl(t, d, "child_review")
				if cut["review"] != true || cut["approved"] != true || cut["active_review"] != "exact" || cut["active_approval"] != "unapproved" || cut["active_accepted"] != false || cut["pair_calls"] != oldCalls {
					t.Fatal(cut)
				}
				if scenario == "child-declined" {
					declined := reconnectActiveControl(t, d, "decline")
					if declined["active_approval"] != "unapproved" || declined["active_accepted"] != false || d.f.pairCalls != oldCalls {
						t.Fatal(declined)
					}
				} else {
					approved := reconnectActiveControl(t, d, "approve")
					if approved["active_review"] != "exact" || approved["active_approval"] != "approved" || approved["active_accepted"] != true || approved["pair_calls"] != oldCalls {
						t.Fatal(approved)
					}
				}
			}
			evidence[scenario] = map[string]any{"states": d.actual, "facts": d.facts}
		})
	}
	if output := os.Getenv("ARM_RECONNECT_CONSENT_EVIDENCE"); output != "" {
		cwd, e := os.Getwd()
		if e != nil {
			t.Fatal(e)
		}
		base := filepath.Join(filepath.Clean(filepath.Join(cwd, "../..")), ".tla")
		if e = reconnectWriteControlEvidence(output, base, evidence); e != nil {
			t.Fatal(e)
		}
	}
}

func reconnectMutateChildConsent(t *testing.T, d *reconnectDriver, name string) {
	t.Helper()
	key := d.current.guard.Key()
	path := reconnectCoordPath(d.f.home, key)
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var object map[string]json.RawMessage
	if e = json.Unmarshal(raw, &object); e != nil {
		t.Fatal(e)
	}
	switch name {
	case "missing-review":
		delete(object, "review")
	case "substituted-review":
		var plan map[string]json.RawMessage
		if e = json.Unmarshal(object["review"], &plan); e != nil {
			t.Fatal(e)
		}
		plan["warning"] = json.RawMessage(`"different unreviewed plan, coherent saved hash"`)
		changed, e := json.Marshal(plan)
		if e != nil {
			t.Fatal(e)
		}
		object["review"] = changed
		sum := sha256.Sum256(changed)
		object["review_sha256"], _ = json.Marshal(hex.EncodeToString(sum[:]))
	case "changed-review-revision":
		object["review_revision"] = json.RawMessage(`1`)
	case "missing-approval":
		delete(object, "approved")
	case "cleared-approval":
		object["approved"] = json.RawMessage(`false`)
	default:
		t.Fatal(name)
	}
	if e = local.AtomicJSON(path, object); e != nil {
		t.Fatal(e)
	}
}

func TestReconnectActualChildConsentCorruptionControls(t *testing.T) {
	evidence := map[string]any{}
	for _, name := range []string{"missing-review", "substituted-review", "changed-review-revision", "missing-approval", "cleared-approval"} {
		t.Run(name, func(t *testing.T) {
			d := reconnectControlReady(t)
			d.control = "lostpair"
			if e := d.connect(false); e != nil {
				t.Fatal(e)
			}
			d.control = ""
			if e := d.connect(true); e != nil {
				t.Fatal(e)
			}
			parentBefore, _, e := mesh.ReadCoordination(d.f.home, d.key)
			if e != nil {
				t.Fatal(e)
			}
			sourceBefore, destinationBefore := d.f.source.Config(), d.f.destination.Config()
			good, _, e := d.sample("lookup")
			if e != nil {
				t.Fatal(e)
			}
			if good["active_approval"] != "approved" || good["active_review"] != "exact" {
				t.Fatal(good)
			}
			reconnectMutateChildConsent(t, d, name)
			bad, fact, e := d.sample("lookup")
			if e != nil {
				t.Fatal(e)
			}
			parentAfter, _, e := mesh.ReadCoordination(d.f.home, d.key)
			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(parentBefore, parentAfter) || !reflect.DeepEqual(sourceBefore, d.f.source.Config()) || !reflect.DeepEqual(destinationBefore, d.f.destination.Config()) {
				t.Fatal("child control changed original receipt/native enrollment")
			}
			// This is exactly the old lossy projection. It must remain identical while
			// the successor exposes child corruption, rather than claiming runtime denial.
			oldGood, oldBad := map[string]any{}, map[string]any{}
			for k, v := range good {
				if k != "active_review" && k != "active_approval" && k != "active_accepted" {
					oldGood[k] = v
					oldBad[k] = bad[k]
				}
			}
			if !reflect.DeepEqual(oldGood, oldBad) {
				t.Fatal("control did not isolate old child-consent projection")
			}
			switch name {
			case "missing-review":
				if bad["active_review"] != "absent" {
					t.Fatal(bad)
				}
			case "substituted-review", "changed-review-revision":
				if bad["active_review"] != "changed" {
					t.Fatal(bad)
				}
			case "missing-approval":
				if bad["active_approval"] != "absent" {
					t.Fatal(bad)
				}
			case "cleared-approval":
				if bad["active_approval"] != "unapproved" {
					t.Fatal(bad)
				}
			}
			evidence[name] = map[string]any{"states": append(append([]reconnectObserved{}, d.actual...), bad), "facts": append(append([]reconnectRawFact{}, d.facts...), fact)}
		})
	}
	if output := os.Getenv("ARM_RECONNECT_CONSENT_EVIDENCE"); output != "" {
		cwd, e := os.Getwd()
		if e != nil {
			t.Fatal(e)
		}
		base := filepath.Join(filepath.Clean(filepath.Join(cwd, "../..")), ".tla")
		if e = reconnectWriteControlEvidence(output+".corruptions.json", base, evidence); e != nil {
			t.Fatal(e)
		}
	}
}

func TestReconnectConfirmationInputAttemptAndDetachment(t *testing.T) {
	d := reconnectControlReady(t)
	if e := d.connect(false); e != nil {
		t.Fatal(e)
	}
	_, before, e := d.sample("lookup")
	if e != nil {
		t.Fatal(e)
	}
	input := d.confirmationInputs[d.key]
	input.Attempt = "different-attempt"
	input.Plan = append(json.RawMessage{}, input.Plan...)
	input.Plan[0] = '['
	d.confirmationInputs[d.key] = input
	observed, after, e := d.sample("lookup")
	if e != nil {
		t.Fatal(e)
	}
	if observed["active_review"] != "changed" || observed["active_accepted"] != false {
		t.Fatal(observed)
	}
	if bytes.Equal(before.ConfirmationInputs[d.key].Plan, after.ConfirmationInputs[d.key].Plan) || before.ConfirmationInputs[d.key].Attempt != d.key {
		t.Fatal("historical callback input alias or wrong attempt accepted")
	}
}

// Each actual history/raw-fact pair retains the same individual4MiB boundary.
// A closed bounded manifest replaces aggregate objects that exceed that cap.
type reconnectEvidenceCase struct {
	Name         string `json:"name"`
	States       string `json:"states"`
	Facts        string `json:"facts"`
	StatesSHA256 string `json:"states_sha256"`
	FactsSHA256  string `json:"facts_sha256"`
}
type reconnectEvidenceManifest struct {
	Schema string                  `json:"schema"`
	Cases  []reconnectEvidenceCase `json:"cases"`
}

func reconnectWriteCases(output, base string, states map[string][]reconnectObserved, facts map[string][]reconnectRawFact) error {
	if len(states) < 1 || len(states) > 16 || len(states) != len(facts) {
		return fmt.Errorf("bounded complete case roster required")
	}
	names := make([]string, 0, len(states))
	for name := range states {
		names = append(names, name)
	}
	sort.Strings(names)
	manifest := reconnectEvidenceManifest{Schema: "arm.mesh-reconnect-evidence/v1"}
	for _, name := range names {
		if len(name) < 1 || len(name) > 128 || strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
			return fmt.Errorf("closed case name required")
		}
		if _, ok := facts[name]; !ok {
			return fmt.Errorf("missing case facts")
		}
		row := reconnectEvidenceCase{Name: name, States: output + "." + name + ".states.json", Facts: output + "." + name + ".facts.json"}
		if e := reconnectFormalWrite(row.States, base, states[name]); e != nil {
			return e
		}
		if e := reconnectFormalWrite(row.Facts, base, facts[name]); e != nil {
			return e
		}
		for _, item := range []struct {
			path string
			hash *string
		}{{row.States, &row.StatesSHA256}, {row.Facts, &row.FactsSHA256}} {
			file, e := os.Open(item.path)
			if e != nil {
				return e
			}
			info, e := file.Stat()
			if e != nil || !info.Mode().IsRegular() || info.Size() > 4<<20 {
				file.Close()
				return fmt.Errorf("bounded emitted case file required")
			}
			hash := sha256.New()
			n, e := io.Copy(hash, io.LimitReader(file, (4<<20)+1))
			file.Close()
			if e != nil || n > 4<<20 || n != info.Size() {
				return fmt.Errorf("bounded case hashing failed: %v", e)
			}
			*item.hash = hex.EncodeToString(hash.Sum(nil))
		}
		manifest.Cases = append(manifest.Cases, row)
	}
	return reconnectFormalWrite(output, base, manifest)
}
func reconnectWriteControlEvidence(output, base string, evidence map[string]any) error {
	states := map[string][]reconnectObserved{}
	facts := map[string][]reconnectRawFact{}
	for name, value := range evidence {
		row := value.(map[string]any)
		states[name] = row["states"].([]reconnectObserved)
		facts[name] = row["facts"].([]reconnectRawFact)
	}
	return reconnectWriteCases(output, base, states, facts)
}
