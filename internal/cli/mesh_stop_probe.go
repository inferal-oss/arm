package cli

import (
	"os"
	"strconv"

	"github.com/inferal-oss/arm/internal/client"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/meshops"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/nodestop"
)

// SSH-only fixed readonly probe. No Ensure, startup, trust/grant mutation,
// arbitrary command, file removal or fallback to a saved PID is performed.
func (a *app) meshStopProbeHelper(raw []byte) (int, error) {
	var q meshops.StopProbeRequest
	if err := nodestop.Decode(raw, &q); err != nil {
		return 2, err
	}
	if q.Action != "node_stop_probe" || q.Home != q.Target.Home || q.Target.Owner != strconv.Itoa(os.Geteuid()) {
		return 2, model.Err("invalid_request", "exact native account/home observer target required")
	}
	var current nodestop.Target
	if err := client.Call(a.ctx, q.Home, "node_stop_observer_context", struct{}{}, &current); err != nil {
		return 1, err
	}
	if current != q.Target {
		return 1, model.Err("node_context_changed", "actual backend home/directory/generation/mesh identity differs from observer review")
	}
	image, err := mesh.CaptureRuntimeExecutable()
	if err != nil {
		return 1, err
	}
	proof, err := image.Proof()
	closeErr := image.Close()
	if err != nil {
		return 1, err
	}
	if closeErr != nil {
		return 1, closeErr
	}
	proof.Host = current.Host
	proof.UID = strconv.Itoa(os.Geteuid())
	return 0, a.print(meshops.StopObserverReady{Protocol: meshops.StopObserverProtocol, Supported: true, Target: current, Runtime: proof})
}
