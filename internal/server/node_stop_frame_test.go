package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/client"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/meshops"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/nodestop"
)

// Real native Serve lifetimes, 64 actual leases, authenticated mesh prepare/apply,
// original Serve join, native closure/absence inspection and stored lookup. The
// fixture supplies explicit pairing/human approval; it never supplies join flags.
func TestActualMaximalStopCompletionFitsStoredLookup(t *testing.T) {
	source, _, sourceStatus := stopPrivateBackend(t)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	owner, _, status := startOwnedStopServe(t, home)
	left := stopEnablePrivateMesh(t, source, sourceStatus.Host, "large-source")
	right := stopEnablePrivateMesh(t, home, status.Host, "large-target")
	peer := stopPairPrivateGrant(t, source, home, left, right, strings.Repeat("e", 64), mesh.Grant{Permissions: []string{"node-stop", "observe-all"}})
	for n := 0; n < nodestop.MaxItems; n++ {
		var lease model.Lease
		err = client.Call(t.Context(), home, "acquire", model.Request{Key: fmt.Sprintf("max-frame-%02d", n), Reason: "owned maximum-frame CPU lease", Resources: model.Resources{CPU: .01}, TTL: time.Minute}, &lease)
		if err != nil {
			t.Fatal(n, err)
		}
	}
	draft := meshops.StopDraft{Host: right.Host, Reason: "a" + strings.Repeat("\x00", 4095), Policy: model.ShutdownRequest{Force: true}}
	key := "max-completion-frame"
	params, _ := json.Marshal(stopPlanRequest{Key: key, Reason: draft.Reason, Policy: draft.Policy})
	var prepared nodestop.Prepared
	if err = client.Call(t.Context(), source, "mesh_dispatch", struct {
		Host    string
		Request mesh.Request
	}{right.Host, mesh.Request{Operation: "node_stop_plan", Expected: &mesh.ExpectedEnrollment{Pair: peer.Pair, Revision: peer.Revision}, Params: params}}, &prepared); err != nil {
		t.Fatal(err)
	}
	p := prepared.Plan
	hash, err := p.Digest()
	if err != nil || hash != prepared.SHA256 || len(p.Impact.Obligations) != 64 || p.Impact.Outstanding != 64 {
		t.Fatal("complete maximal actual census missing", len(p.Impact.Obligations), err)
	}
	planRaw, _ := json.Marshal(p)
	if len(planRaw) <= nodestop.MaxFrameBytes/2 || len(planRaw) > nodestop.MaxPlanBytes {
		t.Fatal("control did not exercise a large valid plan", len(planRaw))
	}
	for _, item := range p.Impact.Obligations {
		if item.LeaseDetails != "permitted" || item.Lease == nil {
			t.Fatal("actual observe-all census did not include permitted preview")
		}
	}
	review := nodestop.SourceReview{Operation: "stop-arm-node", Key: key, Plan: p, SHA256: hash, OwnerUsername: prepared.OwnerUsername, HistoryCapacity: nodestop.MaxRecords, CompletionCheck: nodestop.CompletionCheck{Available: true, Detail: "This fixture uses the fixed native inspector after the original Serve owner joins."}}
	if err = review.Validate(); err != nil {
		t.Fatal(err)
	}
	digest, _ := mesh.ReviewDigest(draft)
	guard, err := mesh.ClaimCoordination(source, key, "stop", digest, "host:"+right.Host)
	if err != nil {
		t.Fatal(err)
	}
	reviewRaw, _ := json.Marshal(review)
	reviewHash, _ := mesh.ReviewDigest(review)
	requestRaw, _ := json.Marshal(meshops.StartRequest{Key: key, Kind: "stop", Stop: &draft})
	if err = guard.Change(func(op *mesh.Coordination) error {
		op.Request = requestRaw
		op.Review = reviewRaw
		op.ReviewHash = reviewHash
		op.ReviewRevision = op.Revision + 1
		op.ReviewExpires = p.Expires
		op.Approved = true
		op.Phase = "incomplete"
		op.Worker = ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	applyRaw, _ := json.Marshal(nodestop.Apply{Key: key, Plan: p, ReviewSHA256: hash})
	if err = client.Call(t.Context(), source, "mesh_dispatch", struct {
		Host    string
		Request mesh.Request
	}{right.Host, mesh.Request{Operation: "node_stop_apply", Key: key, Expected: &mesh.ExpectedEnrollment{Pair: peer.Pair, Revision: peer.Revision}, Params: applyRaw}}, nil); err != nil {
		t.Log("single original apply reply unconfirmed", err)
	}
	owner.joined(t)
	observation, err := InspectNodeStop(t.Context(), home, p.Target, key)
	if err != nil {
		t.Fatal(err)
	}
	result := nodestop.Result{Operation: "stop-arm-node", Key: key, Status: "stopped", Observation: &observation, Detail: "Actual original backend joined with matched native closure and original-generation absence."}
	if err = result.Validate(review); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > nodestop.MaxFrameBytes {
		t.Fatal("canonical completed frame does not fit", len(raw), err)
	}
	duplicate := result
	record := observation.Record
	duplicate.Record = &record
	oversized, _ := json.Marshal(duplicate)
	if len(oversized) <= nodestop.MaxFrameBytes || duplicate.Validate(review) == nil {
		t.Fatal("old duplicate-record overflow control not meaningful", len(oversized))
	}
	var decoded nodestop.Result
	if err = nodestop.Decode(raw, &decoded); err != nil || decoded.Validate(review) != nil {
		t.Fatal("canonical positive result cannot be decoded", err)
	}
	original, _, err := mesh.ReadCoordination(source, key)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := mesh.RecordStopLookup(source, key, original.Revision, raw, nil)
	if err != nil || saved.Phase != "completed" {
		t.Fatal("actual completed result cannot be stored", err)
	}

	canonicalResult := func(payload []byte) []byte {
		t.Helper()
		var value nodestop.Result
		if err := nodestop.Decode(payload, &value); err != nil {
			t.Fatal("stored positive result violates closed frame", err)
		}
		if err := value.Validate(review); err != nil {
			t.Fatal("stored positive result lost original typed evidence", err)
		}
		canonical, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return canonical
	}
	want := canonicalResult(raw)
	lifecyclePath, err := mesh.LifecycleOperationPath(source, key)
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(filepath.Dir(lifecyclePath), "coordinator-"+filepath.Base(lifecyclePath))
	beforeFile, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	actualStored, exists, err := mesh.ReadCoordination(source, key)
	if err != nil || !exists {
		t.Fatal("actual saved coordination unavailable", err)
	}
	expectedReview, err := nodestop.StoredReviewFrame(review)
	if err != nil {
		t.Fatal(err)
	}
	expectedResult, err := nodestop.StoredResultFrame(result)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actualStored.Review, expectedReview) || !bytes.Equal(actualStored.Result, expectedResult) {
		t.Fatal("pre-admission wrapper format differs from actual Coordination nested storage")
	}
	var storedReview nodestop.SourceReview
	if err = nodestop.Decode(actualStored.Review, &storedReview); err != nil || storedReview.Validate() != nil {
		t.Fatal("actual stored review exceeds/violates closed frame", len(actualStored.Review), err)
	}
	if !bytes.Equal(canonicalResult(actualStored.Result), want) {
		t.Fatal("actual stored result lost full canonical evidence")
	}
	beforeInfo, err := os.Lstat(receiptPath)
	if err != nil || !beforeInfo.Mode().IsRegular() || beforeInfo.Mode().Perm() != 0600 {
		t.Fatal("actual stored coordination must be a private regular file", err)
	}
	service := meshops.Service{Home: source, Context: t.Context()}
	looked, err := service.LookupStop(key, saved.Revision)
	if err != nil || looked.Phase != "completed" {
		t.Fatal("positive stored lookup failed", err)
	}
	got := canonicalResult(looked.Result)
	if !bytes.Equal(got, want) {
		t.Fatal("positive stored lookup changed full canonical typed result")
	}
	t.Logf("actual completed representation compact=%d stored=%d canonical_sha256=%x", len(raw), len(looked.Result), sha256.Sum256(got))
	service.Close()
	retained, _, err := mesh.ReadCoordination(source, key)
	if err != nil || retained.Revision != saved.Revision || !bytes.Equal(canonicalResult(retained.Result), want) {
		t.Fatal("passive positive lookup changed complete typed history", err)
	}
	afterFile, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Lstat(receiptPath)
	if err != nil || !os.SameFile(beforeInfo, afterInfo) || beforeInfo.Mode() != afterInfo.Mode() || beforeInfo.Size() != afterInfo.Size() || !beforeInfo.ModTime().Equal(afterInfo.ModTime()) || !bytes.Equal(beforeFile, afterFile) {
		t.Fatal("passive positive lookup mutated actual coordinator file identity/version/bytes", err)
	}
}
