package lifetimeview

import "getarm.org/internal/server/internal/nativeowner"

// Distinct worker-original nonretiring borrowers only. No issuer, constructor,
// descriptor, closer or retirement capability is exposed here.
type WorkerManagerView = nativeowner.WorkerManagerView
type WorkerServerView = nativeowner.WorkerServerView
type WorkerManagerClaim = nativeowner.WorkerManagerClaim
type WorkerServerClaim = nativeowner.WorkerServerClaim
type WorkerSnapshot = nativeowner.WorkerSnapshot
