package cli

import (
	"encoding/base64"
	"fmt"
	"getarm.org/internal/client"
	"getarm.org/internal/local"
	"os"
	"path/filepath"

	"getarm.org/internal/mesh"
	"getarm.org/internal/meshssh"
)

// Native-only commands: no browser/peer RPC route exposes installation/start.
func (a *app) meshProvisionHelper(action string, args []string) (int, error) {
	if len(args) != 1 || len(args[0]) > 32768 {
		return 2, fmt.Errorf("one bounded native provision descriptor required")
	}
	raw, e := base64.RawStdEncoding.DecodeString(args[0])
	if e != nil {
		return 2, e
	}
	var q meshssh.Transfer
	if e = mesh.Decode(raw, &q); e != nil {
		return 2, e
	}
	var result mesh.ProvisionReceipt
	if action == "provision-finalize" {
		result, e = mesh.FinalizeProvision(q)
	} else {
		result, e = mesh.LookupProvision(q)
	}
	if e != nil {
		return 1, e
	}
	return 0, a.print(result)
}

type meshProvisionStart struct {
	Action   string             `json:"Action"`
	Transfer meshssh.Transfer   `json:"Transfer"`
	Home     string             `json:"Home"`
	Startup  mesh.StartupPolicy `json:"Startup"`
}

func (a *app) meshProvisionStartHelper(raw []byte) (int, error) {
	var q meshProvisionStart
	if e := mesh.Decode(raw, &q); e != nil {
		return 2, e
	}
	if q.Action != "provision_start" || !filepath.IsAbs(q.Home) {
		return 2, fmt.Errorf("exact native provision start descriptor required")
	}
	a.home = q.Home
	current, e := a.inspectMeshLifecycle()
	if e != nil {
		return 1, e
	}
	// Bind recorded settings before durable intent; preserve existing authority,
	// including its stable host/leases/budgets and any operator drain fence.
	fresh := current.StartupSource == "current defaults; no prior launch policy exists" && !current.Running && current.Host == "" && q.Startup.RecordedAt.IsZero()
	actualBefore := current.Startup
	actualBefore.RecordedAt = q.Startup.RecordedAt
	if actualBefore != q.Startup && !fresh {
		return 1, fmt.Errorf("startup settings changed since install/start review; no backend start authorized")
	}
	if fresh {
		if entries, e := os.ReadDir(q.Home); e == nil && len(entries) != 0 {
			return 1, fmt.Errorf("existing ARM home has no saved startup provenance; preserve it and review separately")
		} else if e != nil && !os.IsNotExist(e) {
			return 1, e
		}
	}

	if current.Running {
		var existing mesh.RuntimeProof
		if e = client.Call(a.ctx, a.home, "provision_runtime", nil, &existing); e != nil || existing.Host != current.Host || existing.UID != q.Transfer.ExpectedUID || existing.SHA256 != q.Transfer.SHA256 || existing.Size != q.Transfer.Size || existing.Version != q.Transfer.Version || existing.Protocol != q.Transfer.Protocol {
			return 1, fmt.Errorf("existing backend runtime differs or cannot be proven; preserved without upgrade/start")
		}
	}
	if _, e = mesh.BeginProvisionStart(q.Transfer, q.Home, q.Startup); e != nil {
		return 1, e
	}
	expected := current.Startup
	if fresh {
		lock, e := local.Lock(q.Home, "daemon.lock")
		if e != nil {
			return 1, e
		}
		entries, e := os.ReadDir(q.Home)
		if e != nil {
			lock.Close()
			return 1, e
		}
		for _, entry := range entries {
			if entry.Name() != "daemon.lock" {
				lock.Close()
				return 1, fmt.Errorf("fresh ARM home changed; no settings overwritten")
			}
		}
		e = mesh.SaveStartupPolicy(q.Home, q.Startup)
		lock.Close()
		if e != nil {
			return 1, e
		}
		var exists bool
		expected, exists, e = mesh.ReadStartupPolicy(q.Home)
		if e != nil || !exists {
			return 1, fmt.Errorf("reviewed fresh startup policy not durably readable")
		}
	}
	if !current.Running {
		current, e = a.ensureMeshBackend(expected)
		if e != nil {
			return 1, e
		}
	}
	actual := current.Startup
	actual.RecordedAt = q.Startup.RecordedAt
	if actual != q.Startup {
		return 1, fmt.Errorf("actual backend settings differ; provision start remains unresolved")
	}
	var proof mesh.RuntimeProof
	if e = client.Call(a.ctx, a.home, "provision_runtime", nil, &proof); e != nil {
		return 1, e
	}
	result, e := mesh.FinishProvisionStart(q.Transfer, proof, q.Home, q.Startup)
	if e != nil {
		return 1, e
	}
	return 0, a.print(result)
}

func (a *app) meshProvisionRuntimeHelper(raw []byte) (int, error) {
	var q struct {
		Action string
		Home   string
	}
	if e := mesh.Decode(raw, &q); e != nil {
		return 2, e
	}
	if q.Action != "provision_runtime" || !filepath.IsAbs(q.Home) {
		return 2, fmt.Errorf("exact native runtime proof lookup home required")
	}
	a.home = q.Home
	var proof mesh.RuntimeProof
	if e := a.call("provision_runtime", nil, &proof); e != nil {
		return 1, e
	}
	return 0, a.print(proof)
}
