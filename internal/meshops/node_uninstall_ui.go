package meshops

import (
	"context"
	"fmt"
	"time"

	"getarm.org/internal/buildinfo"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
)

type UninstallCandidate struct {
	ProvisionKey string `json:"provision_key"`
	Home         string `json:"home"`
	Binary       string `json:"binary"`
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size_bytes"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
}
type UninstallContext struct {
	Host       string               `json:"host"`
	Candidates []UninstallCandidate `json:"candidates"`
	Reason     string               `json:"reason"`
}

// UninstallCandidates exposes exact recorded provenance, never browser-selected
// executable paths. Destination ownership and grants are rechecked in Prepare.
func UninstallCandidates(home, host string) (UninstallContext, error) {
	out := UninstallContext{Host: host, Candidates: []UninstallCandidate{}, Reason: "No completed, exclusive ARM-managed installation is recorded for this exact node. Reused, legacy and unsettled installations are preserved."}
	if e := (StopDraft{Host: host, Reason: "inspect managed installation"}).Validate(); e != nil {
		return out, e
	}
	own, e := mesh.InspectLifecycle(home, buildinfo.Version)
	if e != nil {
		return out, e
	}
	if own.Config == nil {
		return out, nil
	}
	peer, e := mesh.ResolveConfig(*own.Config, host)
	if e != nil {
		return out, e
	}
	if peer.Identity.Host != host || peer.State != "active" || !peer.Proven {
		return out, nil
	}
	c := &Coordinator{Home: home}
	connection, e := c.stopConnection(peer)
	if e != nil {
		return out, e
	}
	if connection == nil {
		return out, nil
	}
	all, e := mesh.ListCoordinations(home)
	if e != nil {
		return out, e
	}
	for _, op := range all {
		if op.Kind != "provision" {
			continue
		}
		q, e := uninstallProvisionRequest(op, op.Key)
		if e != nil {
			continue
		}
		var result ProvisionResult
		if mesh.Decode(op.Result, &result) != nil || !provisionStarted(q, result.Receipt) || result.Receipt.Ownership == nil || result.Receipt.ReusedBinary {
			continue
		}
		if q.Destination != connection.SSHLocator || q.SSH.Account != connection.SSHAccount || q.SSH.HostKey != connection.SSHHostKey || q.Home != connection.RemoteHome || mesh.UninstallInstalledPath(q.Transfer) != connection.Binary || result.Receipt.StartedHost != host {
			continue
		}
		out.Candidates = append(out.Candidates, UninstallCandidate{op.Key, q.Home, connection.Binary, q.Transfer.SHA256, q.Transfer.Size, q.Transfer.OS, q.Transfer.Arch})
	}
	if len(out.Candidates) > 0 {
		out.Reason = "Select the exact completed source receipt. Destination ownership, current grants and the original backend generation are checked again before any effect."
	}
	return out, nil
}

// LookupUninstall owns one bounded native interval and returns the updated source
// receipt. Finish binds the current source revision and immutable original hash;
// it never prepares or applies another Stop.
func (s *Service) LookupUninstall(key string, revision int64, finish bool, hash string) (Operation, error) {
	release := func() {}
	if s.Register != nil {
		var e error
		release, e = s.Register()
		if e != nil {
			return Operation{}, e
		}
	}
	defer release()
	s.mu.Lock()
	if s.closing || s.Context.Err() != nil {
		s.mu.Unlock()
		return Operation{}, model.Err("backend_stopping", "operator coordinator is closing")
	}
	if len(s.workers) >= 2 || s.workers[key] != nil {
		s.mu.Unlock()
		return Operation{}, model.Err("worker_active", "original operation has an active owner")
	}
	op, ok, e := mesh.ReadCoordination(s.Home, key)
	if e != nil {
		s.mu.Unlock()
		return Operation{}, e
	}
	if !ok || op.Kind != "uninstall" || op.Revision != revision || !op.Approved || op.Worker != "" && time.Now().Before(op.WorkerExpires) {
		s.mu.Unlock()
		return Operation{}, model.Err("review_changed", "exact inactive approved uninstall receipt required")
	}
	var request UninstallDraft
	if e = mesh.Decode(op.Request, &request); e != nil {
		s.mu.Unlock()
		return Operation{}, e
	}
	var plan mesh.UninstallPlan
	if e = mesh.Decode(op.Review, &plan); e != nil {
		s.mu.Unlock()
		return Operation{}, e
	}
	digest, e := plan.Digest()
	if e != nil || digest != op.ReviewHash || plan.Key != key || request.Host != plan.Stop.Plan.Target.Host {
		s.mu.Unlock()
		return Operation{}, model.Err("review_changed", "exact original uninstall review required")
	}
	if finish && hash != digest {
		s.mu.Unlock()
		return Operation{}, model.Err("review_changed", "explicit finish must name the reviewed original fingerprint")
	}
	ctx, cancel := context.WithTimeout(s.Context, 60*time.Second)
	c := s.New(ctx)
	if c == nil {
		s.mu.Unlock()
		cancel()
		return Operation{}, fmt.Errorf("native uninstall inspector unavailable")
	}
	c.Print = func(any) error { return nil }
	c.Confirm = func(value any, yes, dry bool) error {
		p, ok := value.(mesh.UninstallPlan)
		if !finish || !yes || dry || !ok {
			return model.Err("confirmation_required", "explicit original uninstall finish review required")
		}
		h, e := p.Digest()
		if e != nil || h != digest {
			return model.Err("review_changed", "finish scope changed")
		}
		return nil
	}
	if s.workers == nil {
		s.workers = map[string]context.CancelFunc{}
	}
	s.workers[key] = cancel
	s.wg.Add(1)
	s.mu.Unlock()
	defer func() { cancel(); s.mu.Lock(); delete(s.workers, key); s.mu.Unlock(); s.wg.Done() }()
	_, callErr := c.UninstallLookupAt(key, request.Host, revision, finish, true)
	updated, getErr := s.Get(key)
	if getErr != nil {
		return updated, getErr
	}
	return updated, callErr
}
