package residentservice

import "getarm.org/internal/residentcost"

// WorkerPendingProspectiveFloorData reserves the complete fixed v1 transport/
// lifetime contract even while this stage refuses actual worker activation.
// CheckOriginal separately compares the same source ledger with the actual
// bound contract's LogicalFloorData; these constants cannot weaken that gate.
func WorkerPendingProspectiveFloorData() residentcost.BoundsData {
	const retained = 128*266240 + 32*workerResponseWireBytes + 2*4096 + 2*4*2*workerRequestWireBytes + 16*workerControlWireBytes + 128*2*workerLogTailBytes + 2*workerManifestBytes
	return residentcost.BoundsData{Tasks: 5, Descriptors: 8, Children: 2, Sockets: 2, NativePending: 2, IOBytes: retained + 128*workerLogInputBytes, RetainedReceiptBytes: retained}
}
