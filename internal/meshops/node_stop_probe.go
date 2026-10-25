package meshops

import (
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshssh"
	"github.com/yrashk/arm/internal/nodestop"
)

const StopObserverProtocol = "arm.node-stop.observer/v1"

type StopProbeRequest struct {
	Action string          `json:"action"`
	Home   string          `json:"home"`
	Target nodestop.Target `json:"target"`
}
type StopObserverReady struct {
	Protocol  string            `json:"protocol"`
	Supported bool              `json:"supported"`
	Target    nodestop.Target   `json:"target"`
	Runtime   mesh.RuntimeProof `json:"helper_runtime"`
}

func (q StopObserverReady) Validate(target nodestop.Target) error {
	hash, err := hex.DecodeString(q.Runtime.SHA256)
	if err != nil || len(hash) != 32 || q.Protocol != StopObserverProtocol || !q.Supported || q.Target != target || q.Runtime.Host != target.Host || q.Runtime.UID != target.Owner || q.Runtime.Size < 1 || q.Runtime.Size > mesh.MaxArtifactBytes || q.Runtime.Version == "" || q.Runtime.Protocol != mesh.LifecycleProtocol || q.Runtime.Captured.IsZero() || (q.Runtime.Method != "kernel-self-executable" && q.Runtime.Method != "startup-open-file-snapshot") {
		return fmt.Errorf("fixed native observer did not prove exact supported account/home/generation context")
	}
	return nil
}
func (a *Coordinator) probeStopObserver(connection *mesh.LifecycleOperation, target nodestop.Target) StopCompletionCheck {
	check := StopCompletionCheck{Detail: "No verified native observer is available. Transport loss or a remembered helper path cannot prove completion."}
	if connection == nil || connection.RemoteHome != target.Home {
		return check
	}
	var raw json.RawMessage
	_, err := a.ssh(connection.SSHLocator, connection.Binary, StopProbeRequest{Action: "node_stop_probe", Home: connection.RemoteHome, Target: target}, meshssh.Identity{Account: connection.SSHAccount, HostKey: connection.SSHHostKey}, &raw)
	if err != nil {
		return check
	}
	var ready StopObserverReady
	if err = nodestop.Decode(raw, &ready); err != nil {
		return check
	}
	if err = ready.Validate(target); err != nil {
		return check
	}
	check.Available = true
	check.Detail = "Fresh read-only SSH probe verified the fixed observer for this exact native account, home, identity and backend generation. Completion rechecks native evidence; later changes or disconnection remain unknown. Helper SHA-256: " + ready.Runtime.SHA256
	return check
}
