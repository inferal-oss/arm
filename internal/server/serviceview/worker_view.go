package serviceview

import "getarm.org/internal/server/internal/residentservice"

// No conversion to legacy standing authority, parent/child issuer or birth.
type WorkerManagerCharge = residentservice.WorkerManagerCharge
type WorkerChargeObservation = residentservice.WorkerChargeObservation

// Exact opaque worker challenge view, never a legacy conversion or DATA issuer.
type WorkerCurrentChallengeView = residentservice.WorkerCurrentChallengeView
