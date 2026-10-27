package manager

import (
	"context"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/sshservice"
)

// SSHAccount is read-only native account inventory. It creates no key, policy,
// service manifest, listener or operation namespace.
func (m *Manager) SSHAccount(ctx context.Context) (sshservice.Binding, string, error) {
	if ctx == nil {
		return sshservice.Binding{}, "", model.Err("invalid_request", "context required")
	}
	account, e := resolveSSHAccount(ctx)
	if e != nil {
		return sshservice.Binding{}, "", e
	}
	if account.Uid != m.owner || account.Username != m.ownerUsername {
		return sshservice.Binding{}, "", model.Err("account_unavailable", "current SSH native account differs from ledger owner")
	}
	return sshservice.Binding{Host: m.host, Owner: m.owner, Account: m.ownerUsername}, account.HomeDir, nil
}
