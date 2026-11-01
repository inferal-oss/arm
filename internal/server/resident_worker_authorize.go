package server

import "getarm.org/internal/server/internal/residentservice"

// Worker authorization is distinct from the old schema2 resident operator.
// Its original owner receives this exact composed source census before Plan.
// No alias here returns a parent/child/charge/native issuer or runtime permit.
type ResidentWorkerOperatorPending = residentservice.WorkerOperatorPending

func NewResidentWorkerOperatorPending(home, profilePath, evidencePath, key, reason string) (*ResidentWorkerOperatorPending, error) {
	source, err := compiledWorkerPendingSourceData()
	if err != nil {
		return nil, err
	}
	pending, err := residentservice.NewWorkerOperatorPending(home, profilePath, evidencePath, key, reason)
	if err != nil {
		return nil, err
	}
	if err = pending.BindCompiledPendingSourceData(source); err != nil {
		return nil, err
	}
	return pending, nil
}
