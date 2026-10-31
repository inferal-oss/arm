package server

import "getarm.org/internal/server/internal/residentservice"

// This offline operator API has no Parent/Child/ManagerCharge/native issuer
// aliases. Calling the command is distinct from ordinary start or installation.
type ResidentOperatorPending = residentservice.OperatorPending

func NewResidentOperatorPending(home, profilePath, evidencePath, key, reason string) (*ResidentOperatorPending, error) {
	return residentservice.NewOperatorPending(home, profilePath, evidencePath, key, reason)
}
