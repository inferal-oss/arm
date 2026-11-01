package residentservice

// Only bounded deep-copied DATA for whole-frame preflight before native debit,
// image or fence effects. It carries no service, child, closer or launch token.
type WorkerLaunchInputsData struct {
	Home                          string
	HomeID                        NativeID
	Build                         Build
	ProfileSHA256, ContractSHA256 string
	Environment                   []string
	Contract                      WorkerStandingContract
	Resources                     Resources
	Bounds                        Bounds
}

func (p *WorkerParentPending) LaunchInputsOriginalData() (WorkerLaunchInputsData, error) {
	if p == nil || p.self != p || p.custody == nil || p.custody.raw == nil {
		return WorkerLaunchInputsData{}, ErrInvalid
	}
	w := p.custody
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if w.parent != p || !w.typedOriginalLocked() || !w.legacyEmptyLocked() || w.raw.active != nil || w.raw.firstError != nil || !p.opened || p.debitTried || p.group != nil || w.grant == nil || w.debit != nil || len(w.journal.records) != 1 || w.journal.records[0].Grant != w.grant || w.grant.Intent.Profile.Validate() != nil {
		return WorkerLaunchInputsData{}, ErrInvalid
	}
	profile := w.grant.Intent.Profile
	profileSHA, err := profile.SHA256()
	if err != nil {
		return WorkerLaunchInputsData{}, err
	}
	contractSHA, err := profile.Contract.SHA256()
	if err != nil {
		return WorkerLaunchInputsData{}, err
	}
	return WorkerLaunchInputsData{Home: w.path, HomeID: w.grant.Intent.HomeID, Build: profile.Core.Build, ProfileSHA256: profileSHA, ContractSHA256: contractSHA, Environment: append([]string(nil), profile.Core.RuntimeEnvironment.Entries...), Contract: profile.Contract, Resources: profile.Core.Resources, Bounds: profile.Core.Bounds}, nil
}
