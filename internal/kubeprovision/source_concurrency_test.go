package kubeprovision

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func sourcePayloadFixture(t *testing.T) (Provider, Plan) {
	t.Helper()
	p, plan, client := fixture(t)
	plan.Review.BinaryBytes = int64(len("payload"))
	plan.Review.BinarySHA256 = hash("payload")
	var err error
	plan, err = NewPlan(plan.Review)
	if err != nil {
		t.Fatal(err)
	}
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
	return p, plan
}

type sourceStageResult struct {
	record Record
	err    error
}

func heldSourceLookup(t *testing.T, p Provider, key string) (func(), <-chan sourceStageResult) {
	t.Helper()
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan sourceStageResult, 1)
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	t.Cleanup(finish)
	go func() {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		r, err := p.LookupBinary(ctx, key, sourceExecFunc(func(ctx context.Context, _ Pod, _ []string, _ io.Reader, out, _ io.Writer) error {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			_, err := io.WriteString(out, "ARM_BINARY_VERIFIED\n")
			return err
		}))
		done <- sourceStageResult{r, err}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("original lookup did not enter")
	}
	return finish, done
}
func sourceStageJoined(t *testing.T, p Provider, key string, finish func(), done <-chan sourceStageResult, revision uint64) {
	t.Helper()
	finish()
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("original lookup did not join")
	}
	r, ok, err := p.Journal.Read(t.Context(), key)
	if err != nil || !ok || r.SourceCleanupUnknown || r.Revision != revision {
		t.Fatal("refused stage poisoned original positive closure", r, err)
	}
}

type sourceInputFunc func([]byte) (int, error)

func (f sourceInputFunc) Read(b []byte) (int, error) { return f(b) }

func TestSourceOwnerRefusesUnshippedStageBeforePayloadRead(t *testing.T) {
	p, plan := sourcePayloadFixture(t)
	r, _, _ := p.Journal.Read(t.Context(), plan.Review.Key)
	finish, done := heldSourceLookup(t, p, plan.Review.Key)
	var reads, calls atomic.Int32
	result, err := p.ShipBinary(t.Context(), plan.Review.Key, sourceInputFunc(func([]byte) (int, error) { reads.Add(1); return 0, errors.New("payload must not be read") }), sourceExecFunc(func(context.Context, Pod, []string, io.Reader, io.Writer, io.Writer) error { calls.Add(1); return nil }))
	if !errors.Is(err, ErrSourceOwnerUnavailable) || !result.SourceCleanupUnknown || reads.Load() != 0 || calls.Load() != 0 {
		t.Fatal(result, err, reads.Load(), calls.Load())
	}
	sourceStageJoined(t, p, plan.Review.Key, finish, done, r.Revision)
	after, _, _ := p.Journal.Read(t.Context(), plan.Review.Key)
	if after.BinaryPhase != "" {
		t.Fatal("refused shipment consumed target attempt")
	}
	transport := &fakeBinaryExec{catalogDigest: plan.Review.CatalogSHA256}
	if result, err = p.ShipBinary(t.Context(), plan.Review.Key, strings.NewReader("payload"), transport); err != nil || result.BinaryPhase != "binary_verified" || transport.ships != 1 {
		t.Fatal("ordinary original-key continuation failed", result, err)
	}
}

func TestSourceOwnerAtomicShipmentCheckAfterEarlyRead(t *testing.T) {
	p, plan := sourcePayloadFixture(t)
	r, _, _ := p.Journal.Read(t.Context(), plan.Review.Key)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	payload := strings.NewReader("payload")
	var inputOnce sync.Once
	var calls atomic.Int32
	shipDone := make(chan sourceStageResult, 1)
	go func() {
		result, err := p.ShipBinary(t.Context(), plan.Review.Key, sourceInputFunc(func(b []byte) (int, error) {
			inputOnce.Do(func() { close(entered) })
			<-release
			return payload.Read(b)
		}), sourceExecFunc(func(context.Context, Pod, []string, io.Reader, io.Writer, io.Writer) error { calls.Add(1); return nil }))
		shipDone <- sourceStageResult{result, err}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("shipment did not pass early read")
	}
	finish, lookupDone := heldSourceLookup(t, p, plan.Review.Key)
	once.Do(func() { close(release) })
	select {
	case result := <-shipDone:
		if !errors.Is(result.err, ErrSourceOwnerUnavailable) || !result.record.SourceCleanupUnknown || calls.Load() != 0 {
			t.Fatal(result, calls.Load())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("atomic stage refusal stalled")
	}
	sourceStageJoined(t, p, plan.Review.Key, finish, lookupDone, r.Revision)
	after, _, _ := p.Journal.Read(t.Context(), plan.Review.Key)
	if after.BinaryPhase != "" {
		t.Fatal("atomic stage refusal advanced target phase")
	}
}

// Embedding keeps the exact native owner seam while arranging a new original
// lookup between the provider's returned proof and its stage CAS.
type sourceStageJournal struct {
	FileJournal
	beforeUpdate func()
}

func (j sourceStageJournal) Update(ctx context.Context, key string, rev uint64, change func(*Record) error) (Record, error) {
	if j.beforeUpdate != nil {
		j.beforeUpdate()
	}
	return j.FileJournal.Update(ctx, key, rev, change)
}

func TestSourceOwnerAtomicCommitAfterCatalogAndReconciliationProof(t *testing.T) {
	for _, stage := range []string{"start-after-catalog", "binary-reconcile", "marker-reconcile"} {
		t.Run(stage, func(t *testing.T) {
			p, plan := sourcePayloadFixture(t)
			r, _, _ := p.Journal.Read(t.Context(), plan.Review.Key)
			r, err := p.Journal.Update(t.Context(), plan.Review.Key, r.Revision, func(r *Record) error {
				r.BinaryPhase = "binary_verified"
				if stage == "binary-reconcile" {
					r.BinaryPhase = "ship_attempted"
				}
				if stage == "marker-reconcile" {
					r.StartPhase = "start_attempted"
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			base := p
			var finish func()
			var done <-chan sourceStageResult
			var once sync.Once
			p.Journal = sourceStageJournal{FileJournal: p.Journal.(FileJournal), beforeUpdate: func() { once.Do(func() { finish, done = heldSourceLookup(t, base, plan.Review.Key) }) }}
			var starts atomic.Int32
			transport := sourceExecFunc(func(_ context.Context, _ Pod, args []string, _ io.Reader, out, _ io.Writer) error {
				switch args[3] {
				case "arm-catalog-proof":
					_, err := io.WriteString(out, "ARM_CATALOG_VERIFIED "+plan.Review.CatalogSHA256+"\n")
					return err
				case "arm-lookup":
					_, err := io.WriteString(out, "ARM_BINARY_VERIFIED\n")
					return err
				case "arm-start-lookup":
					_, err := io.WriteString(out, "ARM_START_MARKER_VERIFIED\n")
					return err
				default:
					starts.Add(1)
					return errors.New("unexpected effect")
				}
			})
			var result Record
			switch stage {
			case "start-after-catalog":
				result, err = p.RequestStart(t.Context(), plan.Review.Key, transport)
			case "binary-reconcile":
				result, err = p.ReconcileBinary(t.Context(), plan.Review.Key, transport)
			case "marker-reconcile":
				result, err = p.ReconcileStartMarker(t.Context(), plan.Review.Key, transport)
			}
			if !errors.Is(err, ErrSourceOwnerUnavailable) || !result.SourceCleanupUnknown || starts.Load() != 0 || finish == nil {
				t.Fatal("stage CAS did not refuse concurrent owner", result, err)
			}
			sourceStageJoined(t, base, plan.Review.Key, finish, done, r.Revision)
			after, _, _ := base.Journal.Read(t.Context(), plan.Review.Key)
			if after.BinaryPhase != r.BinaryPhase || after.StartPhase != r.StartPhase {
				t.Fatal("refused stage changed target evidence", after)
			}
		})
	}
}

func TestSourceOwnerRequestStartPreservesFreshCatalogCleanupUnknown(t *testing.T) {
	p, plan := sourcePayloadFixture(t)
	r, _, _ := p.Journal.Read(t.Context(), plan.Review.Key)
	if _, err := p.Journal.Update(t.Context(), plan.Review.Key, r.Revision, func(r *Record) error { r.BinaryPhase = "binary_verified"; return nil }); err != nil {
		t.Fatal(err)
	}
	calls := 0
	result, err := p.RequestStart(t.Context(), plan.Review.Key, sourceExecFunc(func(_ context.Context, _ Pod, args []string, _ io.Reader, _, _ io.Writer) error {
		calls++
		if args[3] != "arm-catalog-proof" {
			t.Error("start effect launched")
		}
		return ErrHelperCleanupUnknown
	}))
	if !errors.Is(err, ErrHelperCleanupUnknown) || !result.SourceCleanupUnknown || result.StartPhase != "" || calls != 1 {
		t.Fatal("catalog cleanup uncertainty lost from returned record", result, err)
	}
	persisted, _, err := p.Journal.Read(t.Context(), plan.Review.Key)
	if err != nil || !persisted.SourceCleanupUnknown {
		t.Fatal(persisted, err)
	}
}

type sourceFailingRequestCloser struct {
	file  *os.File
	calls int
}

func (c *sourceFailingRequestCloser) Close() error {
	c.calls++
	_ = c.file.Close()
	return fmt.Errorf("private injected close detail: %w", syscall.EIO)
}

func TestSourceOwnerEarlyRequestFileFailureRetainsCleanupClass(t *testing.T) {
	t.Run("injected-close-failure", func(t *testing.T) {
		p, plan := sourcePayloadFixture(t)
		var closer *sourceFailingRequestCloser
		result, err := p.LookupBinary(t.Context(), plan.Review.Key, sourceExecFunc(func(context.Context, Pod, []string, io.Reader, io.Writer, io.Writer) error {
			file, err := os.CreateTemp(t.TempDir(), "request-")
			if err != nil {
				return err
			}
			closer = &sourceFailingRequestCloser{file: file}
			_, err = failHeldOperationRequest(closer, errors.New("private injected construction detail"))
			return err
		}))
		if !errors.Is(err, ErrHelperCleanupUnknown) || !result.SourceCleanupUnknown || closer == nil || closer.calls != 1 || strings.Contains(err.Error(), "private") {
			t.Fatal(result, err)
		}
		after, _, err := p.Journal.Read(t.Context(), plan.Review.Key)
		if err != nil || !after.SourceCleanupUnknown {
			t.Fatal("early request close failure settled original source", after, err)
		}
	})
	file, err := os.CreateTemp(t.TempDir(), "joined-request-")
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("ordinary construction failure")
	if _, err = failHeldOperationRequest(file, cause); err != cause {
		t.Fatal("positive original close changed ordinary construction outcome", err)
	}
}
