package kubeprovision

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"os"
	"strings"
	"testing"
)

type sourceExecFunc func(context.Context, Pod, []string, io.Reader, io.Writer, io.Writer) error

func (f sourceExecFunc) Exec(c context.Context, p Pod, a []string, i io.Reader, o, e io.Writer) error {
	return f(c, p, a, i, o, e)
}

func TestSourceJournalOriginalClosureAndDurableLostOwner(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "joined", true: "unknown"}[unknown], func(t *testing.T) {
			p, plan, _ := fixture(t)
			r, err := p.Apply(t.Context(), plan)
			if err != nil {
				t.Fatal(err)
			}
			j := p.Journal.(FileJournal)
			calls := 0
			transport := sourceExecFunc(func(context.Context, Pod, []string, io.Reader, io.Writer, io.Writer) error {
				calls++
				raw, err := os.ReadFile(j.Root + "/" + plan.Review.Key + ".json")
				if err != nil {
					t.Fatal(err)
				}
				var legacy Record
				if err = json.Unmarshal(raw, &legacy); err != nil {
					t.Fatal(err)
				}
				// The exact legacy phase predicate refuses this value even though its
				// json.Unmarshal ignores the new envelope fields.
				if legacy.Phase != sourceExecPhase || validRecord(legacy, plan.Review.Key) {
					t.Fatal("helper preceded durable closed-phase fence")
				}
				live, ok, err := j.Read(t.Context(), plan.Review.Key)
				if err != nil || !ok || !live.SourceCleanupUnknown || live.Phase != PhaseCreated {
					t.Fatal(live, err)
				}
				projected, _ := json.Marshal(live)
				if strings.Contains(string(projected), "source_owner") || strings.Contains(string(projected), "source_target_phase") {
					t.Fatal("private source nonce leaked")
				}
				if unknown {
					return ErrHelperCleanupUnknown
				}
				return nil
			})
			err = p.execWithSource(t.Context(), plan.Review.Key, transport, *r.Pod, nil, nil, io.Discard, io.Discard)
			if unknown != errors.Is(err, ErrHelperCleanupUnknown) {
				t.Fatal(err)
			}
			reopened := FileJournal{Root: j.Root}
			after, ok, err := reopened.Read(t.Context(), plan.Review.Key)
			if err != nil || !ok || after.SourceCleanupUnknown != unknown || after.Revision != r.Revision {
				t.Fatal(after, err)
			}
			if unknown {
				if _, err = reopened.Update(t.Context(), plan.Review.Key, after.Revision, func(r *Record) error { r.SourceCleanupUnknown = false; return nil }); err == nil {
					t.Fatal("public update cleared lost original owner")
				}
				p.Journal = reopened
				if err = p.execWithSource(t.Context(), plan.Review.Key, transport, *r.Pod, nil, nil, io.Discard, io.Discard); !errors.Is(err, ErrSourceOwnerUnavailable) {
					t.Fatal(err)
				}
				if calls != 1 {
					t.Fatal("lost source owner was adopted")
				}
				after, err = reopened.Update(t.Context(), plan.Review.Key, after.Revision, func(r *Record) error { r.BinaryPhase = "ship_attempted"; return nil })
				if err != nil || !after.SourceCleanupUnknown {
					t.Fatal(after, err)
				}
				raw, _ := os.ReadFile(j.Root + "/" + plan.Review.Key + ".json")
				var disk Record
				_ = json.Unmarshal(raw, &disk)
				if disk.Phase != sourceExecPhase {
					t.Fatal("target metadata removed source fence")
				}
			} else {
				raw, _ := os.ReadFile(j.Root + "/" + plan.Review.Key + ".json")
				var legacy Record
				_ = json.Unmarshal(raw, &legacy)
				if !validRecord(legacy, plan.Review.Key) || strings.Contains(string(raw), "source_owner") {
					t.Fatal("original positive join failed to settle envelope")
				}
			}
		})
	}
}

func TestSourceJournalCleanupFailureAndPanicRetainOwner(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(map[bool]string{false: "artifact-close", true: "panic"}[panics], func(t *testing.T) {
			p, plan, _ := fixture(t)
			r, err := p.Apply(t.Context(), plan)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer func() {
					if panics && recover() == nil {
						t.Error("panic disappeared")
					}
				}()
				err = p.execWithSourceCleanup(t.Context(), plan.Review.Key, sourceExecFunc(func(context.Context, Pod, []string, io.Reader, io.Writer, io.Writer) error {
					if panics {
						panic("fixture")
					}
					return nil
				}), *r.Pod, nil, nil, io.Discard, io.Discard, func() error { return errors.New("fixture close unknown") })
				if !panics && !errors.Is(err, ErrHelperCleanupUnknown) {
					t.Fatal(err)
				}
			}()
			after, _, err := p.Journal.Read(t.Context(), plan.Review.Key)
			if err != nil || !after.SourceCleanupUnknown {
				t.Fatal(after, err)
			}
		})
	}
}

func TestSourceJournalStaleOriginalGuardCannotSettle(t *testing.T) {
	p, plan, _ := fixture(t)
	r, err := p.Apply(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	j := p.Journal.(FileJournal)
	g, err := j.beginSourceExec(t.Context(), plan.Review.Key, plan.SHA256, r.Pod.UID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = j.Update(t.Context(), plan.Review.Key, r.Revision, func(r *Record) error { r.BinaryPhase = "ship_attempted"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = g.finish(false); !errors.Is(err, ErrHelperCleanupUnknown) {
		t.Fatal(err)
	}
	after, _, err := j.Read(t.Context(), plan.Review.Key)
	if err != nil || !after.SourceCleanupUnknown {
		t.Fatal(after, err)
	}
}

func TestSourceJournalPublicProviderPathsCannotAdoptLostOwner(t *testing.T) {
	p, plan, client := fixture(t)
	r, err := p.Apply(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	pod, err := client.CoreV1().Pods("default").Get(t.Context(), r.Pod.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pod.Status.Phase = corev1.PodRunning
	pod.Spec.NodeName = plan.Review.Node.Name
	if _, err = client.CoreV1().Pods("default").Update(t.Context(), pod, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	j := p.Journal.(FileJournal)
	g, err := j.beginSourceExec(t.Context(), plan.Review.Key, plan.SHA256, r.Pod.UID)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(g.finish(true), ErrHelperCleanupUnknown) {
		t.Fatal("fixture lost unknown source")
	}
	// Existing target evidence may remain visible; it grants no source adoption.
	_, err = j.Update(t.Context(), plan.Review.Key, r.Revision, func(r *Record) error { r.BinaryPhase = "binary_verified"; r.StartPhase = "marker_verified"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	p.Journal = FileJournal{Root: j.Root}
	calls := 0
	e := sourceExecFunc(func(context.Context, Pod, []string, io.Reader, io.Writer, io.Writer) error { calls++; return nil })
	if observed, err := p.LookupBinary(t.Context(), plan.Review.Key, e); !errors.Is(err, ErrSourceOwnerUnavailable) || !observed.SourceCleanupUnknown {
		t.Fatal("returned target evidence omitted unresolved source ownership", observed, err)
	}
	for name, call := range map[string]func() error{
		"binary lookup":  func() error { _, err := p.LookupBinary(t.Context(), plan.Review.Key, e); return err },
		"catalog lookup": func() error { _, err := p.LookupCatalog(t.Context(), plan.Review.Key, e); return err },
		"runtime lookup": func() error { _, err := p.LookupRuntime(t.Context(), plan.Review.Key, e); return err },
		"ship": func() error {
			_, err := p.ShipBinary(t.Context(), plan.Review.Key, strings.NewReader("unused"), e)
			return err
		},
		"start":         func() error { _, err := p.RequestStart(t.Context(), plan.Review.Key, e); return err },
		"marker lookup": func() error { _, err := p.LookupStartMarker(t.Context(), plan.Review.Key, e); return err },
		"forward":       func() error { _, err := p.ForwardOwned(t.Context(), plan.Review.Key); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, ErrSourceOwnerUnavailable) {
				t.Fatal(err)
			}
		})
	}
	if calls != 0 {
		t.Fatal("unresolved original owner launched new exec")
	}
	if result, err := p.Lookup(t.Context(), plan.Review.Key); err != nil || !result.SourceCleanupUnknown {
		t.Fatal("target observation lost original source status", result, err)
	}
}
