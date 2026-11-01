package residentbirth

import (
	"crypto/sha256"
	"encoding/hex"
	"getarm.org/internal/residentrefs"
)

func (h *WorkerFreshFencePending) BootstrapOriginalData(prepared *WorkerPreparedFence) (WorkerFenceData, error) {
	if h == nil || h.owner == nil {
		return WorkerFenceData{}, errOriginalWorkerFence
	}
	o := h.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.self != h || prepared == nil || prepared != o.prepared || prepared.owner != o || o.active != nil || o.firstError != nil || !o.bound || !o.intentCommitted || len(o.intent) == 0 || len(o.intent) > maxWorkerIntentBytes {
		return WorkerFenceData{}, errOriginalWorkerFence
	}
	ids := residentrefs.FenceIdentities{Launch: fenceSharedID(o.state.launch.id), Intent: fenceSharedID(o.state.journal.id), Home: fenceSharedID(o.state.home.id), Daemon: fenceSharedID(o.state.daemon.id), Directory: fenceSharedID(o.state.directory.id), Ledger: fenceSharedID(o.state.ledger.id)}
	hash := sha256.Sum256(o.intent)
	return WorkerFenceData{Schema: 2, Domain: "arm-resident-worker-fence-v1", Home: o.path, Identities: ids, IntentBytes: len(o.intent), IntentSHA256: hex.EncodeToString(hash[:])}, nil
}
