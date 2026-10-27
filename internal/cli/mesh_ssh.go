package cli

import (
	"context"
	"github.com/inferal-oss/arm/internal/meshssh"
)

type meshSSHIdentity = meshssh.Identity

func meshSSHFlags() []string                    { return meshssh.Flags() }
func meshSSHEnvironment() []string              { return meshssh.Environment() }
func validateSSHConfiguration(raw string) error { return meshssh.ValidateConfiguration(raw) }
func meshSSHCanonicalHost(ctx context.Context, destination string) (string, error) {
	return meshssh.CanonicalHost(ctx, destination)
}
func (a *app) meshSSH(destination, binary string, payload any, expected meshSSHIdentity, result any) (meshSSHIdentity, error) {
	return meshssh.Call(a.ctx, destination, binary, payload, expected, result)
}
