package residentservice

import "os/exec"

// Pure exact-original command binding check. No command/File/Close/Start is lent.
// A success is only a local selector check, never a native or service grant.
func (p *P1ServiceGroup) CheckOriginalCommand(command *exec.Cmd) error {
	if p == nil || p.p1ServiceState == nil || p.self != p || p.parent == nil || p.parent.owner == nil {
		return ErrInvalid
	}
	o := p.parent.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.p1 != p || o.active != nil || o.firstError != nil || p.command != command || command == nil || command.Process != nil || command.ProcessState != nil || len(command.ExtraFiles) != 13 || len(p.extra) != 13 || &command.ExtraFiles[0] != &p.extra[0] || !p.fundingChecked || p.debit.validateOriginalLocked(o) != nil {
		return ErrInvalid
	}
	return nil
}
