package residentservice

import "encoding/hex"

// Private-channel DATA from the actual original debit/group. Its nonce/secret
// are never a public readiness receipt, resource grant or native close witness.
func (p *WorkerServiceGroup) BootstrapOriginalData() (WorkerBootstrapData, error) {
	if p == nil || p.self != p || p.parent == nil || p.parent.custody == nil || p.parent.custody.raw == nil {
		return WorkerBootstrapData{}, ErrInvalid
	}
	w := p.parent.custody
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if p.parent.group != p || w.raw.active != nil || w.raw.firstError != nil || !p.bound || !w.originalWorkerDebitLocked(p.debit) || p.controlPhases[0] != nil || w.raw.files.childChannel == nil || w.raw.files.childChannel.closeTried {
		return WorkerBootstrapData{}, ErrInvalid
	}
	d := p.debit.record
	data := WorkerBootstrapData{Schema: 2, Domain: workerBootstrapDomain, Role: WorkerCoreRole, ChannelID: w.raw.files.childChannel.id, Home: w.path, HomeID: w.grant.Intent.HomeID, Build: w.grant.Intent.Profile.Core.Build, DebitID: d.ID, LaunchKey: d.LaunchKey, ProfileSHA256: d.ProfileSHA256, ContractSHA256: d.ContractSHA256, InitialPackageSHA256: d.InitialPackageSHA256, InitialManifestSHA256: d.InitialManifestSHA256, InitialManifestBytes: w.grant.Intent.Profile.Contract.InitialManifestBytes, JournalBytes: w.journal.bytes, JournalSHA256: hex.EncodeToString(w.journal.last[:]), Secret: w.secret}
	if data.Validate() != nil {
		return WorkerBootstrapData{}, ErrInvalid
	}
	return data, nil
}
