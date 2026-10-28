package kubeprovision

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// These two closed actions share e's single mesh_bootstrap request-file lane.
// They add no public builder, command selector or production provider adapter.
// The future provider adapter must capture a durable pre-Pod original intent
// before using this private constructor; no saved record can supply current.
const (
	execCreateOriginalPod reviewedExecKind = "create_original_pod"
	execLookupOriginalPod reviewedExecKind = "lookup_original_pod"
)

func apiSourceKind(kind reviewedExecKind) bool {
	return kind == execCreateOriginalPod || kind == execLookupOriginalPod
}

func (o *OwnedExec) newAPISourceCall(kind reviewedExecKind, current func(context.Context) error) (*NativeExecCall, error) {
	if o == nil || o.image == "" || current == nil || !apiSourceKind(kind) {
		return nil, errors.New("exact original API intent and closed action required")
	}
	raw, err := json.Marshal(o.Plan)
	if err != nil {
		return nil, err
	}
	var plan Plan
	if err := json.Unmarshal(raw, &plan); err != nil || plan.Validate() != nil {
		return nil, ErrIdentity
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	nonce := hex.EncodeToString(random[:])
	encoded, err := json.Marshal(struct {
		Nonce   string              `json:"nonce"`
		Request reviewedExecRequest `json:"request"`
	}{nonce, reviewedExecRequest{Plan: plan, Kind: kind}})
	if err != nil || len(encoded) > 65536 {
		return nil, errors.New("bounded original API request required")
	}
	return &NativeExecCall{original: &OwnedExec{Plan: plan, image: o.image}, kind: kind, nonce: nonce, encoded: encoded, done: make(chan struct{}), providerCurrent: current}, nil
}

func executeReviewedAPI(ctx context.Context, request reviewedExecRequest, payloadPresent bool) reviewedExecResult {
	failed := func() reviewedExecResult {
		// API and credential errors are untrusted, including short errors.
		return reviewedExecResult{Error: "Kubernetes API outcome unavailable; inspect the original key"}
	}
	if !apiSourceKind(request.Kind) || payloadPresent || request.Pod != (Pod{}) || request.Plan.Validate() != nil {
		return failed()
	}
	api, err := OpenReviewedContext(request.Plan)
	if err != nil {
		return failed()
	}
	provider := Provider{API: api}
	if provider.Check(ctx, request.Plan) != nil {
		return failed()
	}
	// The exact selector is read first for both actions. Create never retries a
	// POST and never silently turns a collision into a newly created original.
	found, err := api.ListByKey(ctx, request.Plan.Review.Namespace.Name, request.Plan.Review.Key)
	if err != nil || len(found) > 1 {
		return failed()
	}
	// List is remote I/O. Recheck the reviewed target after it rather than
	// spending a Node/namespace/identity observation from before that wait.
	if provider.Check(ctx, request.Plan) != nil {
		return failed()
	}
	var pod Pod
	if request.Kind == execCreateOriginalPod {
		if len(found) != 0 {
			return failed()
		}
		pod, err = api.CreatePod(ctx, request.Plan.Pod)
	} else {
		if len(found) != 1 || checkPod(request.Plan, found[0]) != nil {
			return failed()
		}
		pod, err = api.GetPod(ctx, found[0].Namespace, found[0].Name)
		if err == nil && pod.UID != found[0].UID {
			err = ErrIdentity
		}
	}
	if err != nil || checkPod(request.Plan, pod) != nil {
		return failed()
	}
	encoded, err := json.Marshal(pod)
	if err != nil || len(encoded) > 4096 {
		return failed()
	}
	return reviewedExecResult{Output: string(encoded)}
}
