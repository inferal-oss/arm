package kubejournal

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"time"

	"getarm.org/internal/kubesource"
	"getarm.org/internal/model"
)

func stageRoster(kind string) []string {
	if kind == "kubernetes_install" {
		return stageNames[:3]
	}
	if kind == "kubernetes_connect" {
		return stageNames[3:]
	}
	return nil
}
func profile(stage string) byte {
	switch stage {
	case "create", "ship":
		return kubesource.Ship
	case "start":
		return kubesource.Start
	case "prove":
		return kubesource.Forward
	default:
		return kubesource.Bootstrap
	}
}
func (r Record) InputHash(stage string) string {
	return Hash(struct {
		Schema, Key, Kind, Stage, Plan string
		Connection                     *ConnectionDraft
	}{Schema, r.Key, r.Kind, stage, r.Plan.SHA256, r.Connection})
}
func (r Record) Validate() error {
	roster := stageRoster(r.Kind)
	if r.Schema != Schema || roster == nil || !digestPattern.MatchString(r.Key) || !idPattern.MatchString(r.SourceHost) || !uidPattern.MatchString(r.Owner) || r.Owner != r.Plan.Review.Owner || r.Revision < 1 || r.Created.IsZero() || r.Updated.Before(r.Created) || r.Plan.Validate() != nil || r.Kind == "kubernetes_install" && r.Key != r.Plan.Review.Key || r.Stages == nil || len(r.Stages) != len(roster) || r.Sources == nil || len(r.Sources) > 128 || !slices.Contains([]string{"pending", "review", "executing", "unknown", "declined", "cancelled", "completed", "disconnected", "revoked"}, r.Phase) {
		return fmt.Errorf("invalid closed Kubernetes workflow provenance")
	}
	if uid, err := strconv.ParseUint(r.Owner, 10, 32); err != nil || strconv.FormatUint(uid, 10) != r.Owner {
		return fmt.Errorf("noncanonical Kubernetes owner")
	}
	if r.Kind == "kubernetes_connect" {
		c := r.Connection
		if c == nil || c.SourceIdentity.Host != r.SourceHost || !digestPattern.MatchString(c.SourceIdentity.Fingerprint) || c.Forward.Validate() != nil || !c.Reverse.Empty() || c.Runtime.PodUID == "" || !idPattern.MatchString(c.Runtime.Host) || !idPattern.MatchString(c.Runtime.Generation) || c.SSHHostKey == "" || !uidPattern.MatchString(c.DestinationUID) {
			return fmt.Errorf("invalid immutable connection draft")
		}
	} else if r.Connection != nil || r.Connector != nil {
		return fmt.Errorf("installation carries connector authority")
	}
	prior := ""
	incomplete := false
	for n, s := range r.Stages {
		if s.Name != roster[n] || s.Key != StageKey(r.Key, s.Name) || s.InputSHA256 != r.InputHash(s.Name) || s.ExpectedPrior != prior || !slices.Contains([]string{"pending", "review", "approved", "declined", "attempted", "confirmed", "unknown"}, s.State) {
			return fmt.Errorf("invalid stage identity/order")
		}
		if incomplete && s.State != "pending" {
			return fmt.Errorf("stage advanced past an unconfirmed predecessor")
		}
		if s.State != "confirmed" {
			incomplete = true
		}
		if err := r.validateStage(s); err != nil {
			return err
		}
		prior = s.Name
	}
	if r.Phase == "completed" && incomplete {
		return fmt.Errorf("completed workflow lacks every confirmed stage")
	}
	attempts := map[string]bool{}
	ids := map[string]bool{}
	unresolved := false
	for _, a := range r.Sources {
		scope, err := a.Scope.Native()
		if err != nil || scope.Operation != r.Plan.Review.Key || scope.B != r.Plan.Review.BinaryBytes || !KnownStage(a.Stage) || !slices.Contains(roster, a.Stage) || scope.Stage != profile(a.Stage) || a.Intent.Before(r.Created) || a.Intent.After(r.Updated) || attempts[scope.Attempt] || unresolved {
			return fmt.Errorf("invalid or overlapping source intent")
		}
		attempts[scope.Attempt] = true
		stage := r.Stages[slices.Index(roster, a.Stage)]
		if !stage.Approved || stage.Review == nil || a.Intent.Before(stage.Review.Created) || !a.Intent.Before(stage.Review.Expires) {
			return fmt.Errorf("source intent lacks exact stage approval")
		}
		if a.Binding != nil {
			b := a.Binding
			if b.Operation != scope.Operation || b.Attempt != scope.Attempt || b.Stage != string(scope.Stage) || b.BinaryBytes != scope.B || !idPattern.MatchString(b.SourceLeaseID) || !idPattern.MatchString(b.StorageLeaseID) || (scope.Stage == kubesource.Forward) != (b.PortLeaseID != "") {
				return fmt.Errorf("source binding differs from original intent")
			}
			for _, id := range []string{b.SourceLeaseID, b.StorageLeaseID, b.PortLeaseID} {
				if id == "" {
					continue
				}
				if !idPattern.MatchString(id) || ids[id] {
					return fmt.Errorf("duplicate or noncanonical original binding")
				}
				ids[id] = true
			}
		}
		if a.Result != nil {
			b, v := a.Binding, a.Result
			if scope.Stage == kubesource.Forward {
				if b == nil || v.PortLeaseID != b.PortLeaseID || v.PortSettled && (!v.PortClosed || !v.SourceClosed) || !slices.Contains([]string{"active", "ending", "needs_attention", "terminal"}, v.PortState) || v.PortSettled != (v.PortState == "terminal") {
					return fmt.Errorf("forward port closure/settlement is independent and required")
				}
			} else if v.PortLeaseID != "" || v.PortClosed || v.PortSettled || v.PortState != "" {
				return fmt.Errorf("non-forward source carries port closure evidence")
			}
			if b == nil || v.SourceLeaseID != b.SourceLeaseID || v.StorageLeaseID != b.StorageLeaseID || v.PromisedDisk != scope.DiskBytes() || v.SourceSettled && !v.SourceClosed || !slices.Contains([]string{"active", "ending", "needs_attention", "terminal"}, v.StorageState) || v.StorageRetained != (v.StorageState != "terminal") || a.Finished.Before(a.Intent) || a.Finished.After(r.Updated) {
				return fmt.Errorf("source result does not bind the admitted originals")
			}
		} else if !a.Finished.IsZero() {
			return fmt.Errorf("source finish lacks original closure observations")
		}
		unresolved = a.Result == nil || !a.Result.SourceClosed || !a.Result.SourceSettled || a.Scope.Stage == "f" && (!a.Result.PortClosed || !a.Result.PortSettled)
	}
	if r.Connector != nil {
		c := r.Connector
		if r.Kind != "kubernetes_connect" || c.ConnectionKey != r.Key || c.InstallationKey != r.Plan.Review.Key || !digestPattern.MatchString(c.ScopeSHA256) || !slices.Contains([]string{"bootstrap_only", "enrollment_proof", "active", "disconnected", "unknown_or_revoked"}, c.Phase) || c.PodUID != r.Connection.Runtime.PodUID || c.BackendHost != r.Connection.Runtime.Host || c.BackendGeneration != r.Connection.Runtime.Generation || c.SSHHostKey != r.Connection.SSHHostKey || c.DestinationUID != r.Connection.DestinationUID || !same(c.SourceIdentity, r.Connection.SourceIdentity) || !same(c.Forward, r.Connection.Forward) || c.PodUID == "" || !idPattern.MatchString(c.BackendHost) || !idPattern.MatchString(c.BackendGeneration) || !uidPattern.MatchString(c.DestinationUID) || c.MeshIdentity.Host != c.BackendHost || !digestPattern.MatchString(c.MeshIdentity.SPKI) || !digestPattern.MatchString(c.MeshIdentity.Fingerprint) || c.MeshIdentity.Certificate == "" || c.SSHHostKey == "" || c.SourceIdentity.Host != r.SourceHost || c.Forward.Validate() != nil || !c.Reverse.Empty() {
			return fmt.Errorf("invalid pinned Kubernetes connector")
		}
		if c.Phase != "bootstrap_only" && (c.Pair == "" || c.LocalRevision < 1 || c.RemoteRevision < 1 || c.ProofDeadline.IsZero()) {
			return fmt.Errorf("connector lacks exact pending pair proof scope")
		}
		if c.Phase == "active" && (incomplete || r.Phase != "completed" || c.Revoked || c.ListenerDisabled) {
			return fmt.Errorf("active connector lacks confirmed activation")
		}
		if (c.Revoked || c.ListenerDisabled) && c.Phase != "unknown_or_revoked" {
			return fmt.Errorf("revoked connector retains admission phase")
		}
	}
	return nil
}
func (r Record) validateStage(s Stage) error {
	if s.Review != nil {
		v := s.Review
		scope := kubesource.Scope{Operation: r.Plan.Review.Key, Attempt: "00000000000000000000000000000000", Stage: profile(s.Name), B: r.Plan.Review.BinaryBytes}
		if r.Connection != nil && v.ExpectedGeneration != r.Connection.Runtime.Generation || v.ExpectedGeneration != s.ExpectedGeneration || v.ExpectedPrior != s.ExpectedPrior || v.PriorSHA256 != r.priorHash(s.Name) || v.Stage != s.Name || v.StageKey != s.Key || v.InputSHA256 != s.InputSHA256 || v.Revision < 1 || v.Revision > r.Revision || v.SHA256 == "" || v.SHA256 != reviewHash(*v) || v.Created.Before(r.Created) || v.Created.After(r.Updated) || !v.Expires.After(v.Created) || v.Expires.Sub(v.Created) > 2*time.Minute || v.SourceProfile != string(scope.Stage) || v.SourceResources != (model.Resources{CPU: kubesource.SourceCPU, Memory: kubesource.SourceMemory}) || v.SourceTTL != kubesource.SourceTTL || v.StorageBytes != scope.DiskBytes() || v.StorageTTL != kubesource.ParentTTL || !same(v.Effects, effects(s.Name)) {
			return fmt.Errorf("stage review scope/budgets differ")
		}
	}
	if s.Approved {
		if s.Review == nil || s.ApprovedRevision != s.Review.Revision || s.ApprovedSHA256 != s.Review.SHA256 || !slices.Contains([]string{"approved", "attempted", "confirmed", "unknown"}, s.State) {
			return fmt.Errorf("stage lacks its immutable exact approval")
		}
	} else if s.ApprovedRevision != 0 || s.ApprovedSHA256 != "" || !s.Attempted.IsZero() || !s.Confirmed.IsZero() || s.Result != nil {
		return fmt.Errorf("unapproved stage carries effect evidence")
	}
	if s.State == "approved" && !s.Approved || s.State == "pending" && (s.Review != nil || s.Approved || s.ExpectedGeneration != "") || s.State == "review" && s.Review == nil || s.State == "declined" && (s.Approved || s.Review == nil) {
		return fmt.Errorf("stage review state differs")
	}
	if slices.Contains([]string{"attempted", "confirmed", "unknown"}, s.State) {
		if !s.Approved || s.Attempted.Before(s.Review.Created) || !s.Attempted.Before(s.Review.Expires) || s.Attempted.After(r.Updated) {
			return fmt.Errorf("stage effect lacks live approved attempt")
		}
	} else if !s.Attempted.IsZero() {
		return fmt.Errorf("unattempted stage carries attempt time")
	}
	if s.State == "confirmed" {
		if s.Result == nil || s.Result.Unknown || s.Confirmed.Before(s.Attempted) || s.Confirmed.After(r.Updated) {
			return fmt.Errorf("unconfirmed stage outcome")
		}
		if err := validateEffect(s.Name, *s.Result); err != nil {
			return err
		}
	} else if !s.Confirmed.IsZero() || s.State == "unknown" && (s.Result == nil || !s.Result.Unknown) || s.State != "unknown" && s.Result != nil {
		return fmt.Errorf("stage outcome/state differs")
	}
	return nil
}
func validateEffect(stage string, v EffectResult) error {
	// Each stage has one closed outcome family. A success flag from a different
	// effect cannot confirm it; the caller still verifies the actual native proof.
	count := 0
	for _, x := range []bool{v.Pod != nil, v.Runtime != nil, v.Prepared != nil, v.Identity != nil, v.Peer != nil, v.Proven, v.Activated} {
		if x {
			count++
		}
	}
	if count != 1 || v.Unknown {
		return fmt.Errorf("effect result is mixed or missing")
	}
	switch stage {
	case "create", "ship":
		if v.Pod == nil || v.Pod.UID == "" {
			return fmt.Errorf("Pod result required")
		}
	case "start":
		if v.Runtime == nil || v.Runtime.PodUID == "" || !idPattern.MatchString(v.Runtime.Host) || !idPattern.MatchString(v.Runtime.Generation) {
			return fmt.Errorf("runtime proof required")
		}
	case "local-listener-prepare", "pod-listener-prepare":
		if v.Prepared == nil || v.Prepared.Key == "" || v.Prepared.Token == "" || v.Prepared.Identity.Host == "" || v.Prepared.Expires.IsZero() {
			return fmt.Errorf("original listener preparation required")
		}
	case "local-listener-apply", "pod-listener-apply":
		if v.Identity == nil || v.Identity.Host == "" {
			return fmt.Errorf("applied listener identity required")
		}
	case "local-pair", "pod-pair":
		if v.Peer == nil || v.Peer.Pair == "" || v.Peer.Revision < 1 || v.Peer.State != "pending" {
			return fmt.Errorf("exact pending pair required")
		}
	case "prove":
		if !v.Proven || !v.OwnerClosed {
			return fmt.Errorf("proof requires positive original forward retirement")
		}
	case "local-activate", "pod-activate":
		if !v.Activated {
			return fmt.Errorf("activation result required")
		}
	default:
		return fmt.Errorf("unknown Kubernetes effect")
	}
	return nil
}
func (r Record) SourceUnresolved() bool {
	for _, a := range r.Sources {
		if a.Result == nil || !a.Result.SourceClosed || !a.Result.SourceSettled || a.Scope.Stage == "f" && (!a.Result.PortClosed || !a.Result.PortSettled) {
			return true
		}
	}
	return false
}
func same(a, b any) bool { return reflect.DeepEqual(a, b) }
