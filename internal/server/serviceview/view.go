// Package serviceview exposes only the actual original nonretiring charge view.
package serviceview

import "getarm.org/internal/server/internal/residentservice"

type ManagerCharge = residentservice.ManagerCharge
type ChargeObservation = residentservice.ChargeObservation

// No parent/child issuer, native descriptor, debit, handoff or release alias.
