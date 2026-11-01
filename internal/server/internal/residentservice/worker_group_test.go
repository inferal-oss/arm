package residentservice

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"getarm.org/internal/residentprotocol"
)

func TestWorkerGroupRequiresOriginalCommittedDebitBeforeConstructionData(t *testing.T) {
	p := workerParentRefusalData(t)
	command := &exec.Cmd{ExtraFiles: make([]*os.File, 13)}
	for _, debit := range []*WorkerOriginalDebit{nil, {}, {owner: p.custody, record: &WorkerDebitRecord{}}} {
		if group, refs, err := NewWorkerServiceGroupWithParentReferences(p, debit, command); err == nil || group != nil || refs != nil || p.group != nil {
			t.Fatal("uncommitted debit constructed group")
		}
	}
	if _, err := p.LaunchInputsOriginalData(); err == nil {
		t.Fatal("unopened parent returned inputs")
	}
	assertWorkerNoNativeData(t, p.custody)
}

func TestWorkerGroupUnissuedSurfacesRefuseBeforeNativeData(t *testing.T) {
	p := workerParentRefusalData(t)
	g := &WorkerServiceGroup{parent: p}
	g.self = g
	copy := *g
	for _, group := range []*WorkerServiceGroup{nil, {}, &copy, g} {
		ctx := context.Background()
		if group.ValidateFundingOriginal(ctx) == nil || group.PrepareOriginalChannel(ctx) == nil || group.BindOriginal(ctx) == nil || group.WithdrawOriginalWriter(ctx) == nil || group.ValidateOriginalBeforeStart(ctx, &exec.Cmd{}) == nil || group.ShortSealOriginal(ctx, &exec.Cmd{}) == nil {
			t.Fatal("unissued operation accepted")
		}
		if _, err := group.SelectorsOriginal(); err == nil {
			t.Fatal("unissued selectors accepted")
		}
		if _, err := group.BootstrapOriginalData(); err == nil {
			t.Fatal("unissued bootstrap accepted")
		}
		if group.CheckOriginalCommand(&exec.Cmd{}) == nil {
			t.Fatal("foreign command accepted")
		}
		if pin, refs, err := group.NewOriginalWorkerImagePinWithParentReferences(); err == nil || pin != nil || refs != nil {
			t.Fatal("unissued pin accepted")
		}
	}
	assertWorkerNoNativeData(t, p.custody)
}

func TestWorkerParentReadAndAuthenticationCannotBeReconstructedData(t *testing.T) {
	g := &WorkerServiceGroup{}
	read := &WorkerParentRead{group: g}
	read.self = read
	copy := *read
	for _, r := range []*WorkerParentRead{nil, {}, &copy, read} {
		if _, err := r.BoundAckPayloadOriginalData(); err == nil {
			t.Fatal("unissued ACK read accepted")
		}
		if _, err := r.CurrentProofPayloadOriginalData(); err == nil {
			t.Fatal("unissued proof read accepted")
		}
	}
	if _, err := json.Marshal(read); err == nil {
		t.Fatal("original read serialized")
	}
	if json.Unmarshal([]byte(`{}`), read) == nil {
		t.Fatal("original read reconstructed")
	}
	var ack *WorkerParentAckAuthentication
	if g.AuthenticateWorkerBoundAckOriginal(&ack, context.Background(), read, residentprotocol.WorkerContextData{}) == nil || ack != nil {
		t.Fatal("DATA issued ACK authority")
	}
	var challenge *WorkerParentChallenge
	if _, err := g.SignWorkerCurrentChallengeOriginal(&challenge, context.Background(), &WorkerParentAckAuthentication{}); err == nil || challenge != nil {
		t.Fatal("DATA issued challenge")
	}
	var proof *WorkerParentProofAuthentication
	if g.AuthenticateWorkerCurrentProofOriginal(&proof, context.Background(), read, &WorkerParentChallenge{}) == nil || proof != nil {
		t.Fatal("DATA issued proof")
	}
}

func TestWorkerParentPhasePrefixDataRefusesSkippingReplayAndLegacy(t *testing.T) {
	for i, next := range workerParentControlKinds {
		if !workerParentControlPrefixData(workerParentControlKinds[:i], next) {
			t.Fatal("valid DATA prefix refused")
		}
	}
	for _, prior := range [][]string{nil, {workerParentControlKinds[1]}, workerParentControlKinds[:1], workerParentControlKinds[:]} {
		if workerParentControlPrefixData(prior, "p1-transfer") {
			t.Fatal("legacy phase accepted")
		}
	}
	if workerParentControlPrefixData(nil, workerParentControlKinds[1]) || workerParentControlPrefixData(workerParentControlKinds[:1], workerParentControlKinds[0]) {
		t.Fatal("skip/replay accepted")
	}
}

func TestWorkerParentReferenceOwnerRequiresOriginalHandoffBeforeEffectsData(t *testing.T) {
	p := workerParentRefusalData(t)
	g := &WorkerServiceGroup{parent: p}
	g.self = g
	p.group = g
	r := &WorkerServiceParentReferenceOwner{group: g}
	r.self = r
	g.references = r
	copy := *r
	for _, candidate := range []*WorkerServiceParentReferenceOwner{nil, {}, &copy, r} {
		if candidate.RetireServiceOriginal(context.Background(), &exec.Cmd{}) == nil || candidate.RetireControlOriginal(context.Background(), &exec.Cmd{}) == nil {
			t.Fatal("unhanded references closed")
		}
	}
	if g.retirement != nil || p.custody.raw.parentReferences != nil || p.custody.raw.parentRetirement != nil {
		t.Fatal("unissued closure changed owners")
	}
	assertWorkerNoNativeData(t, p.custody)
}
