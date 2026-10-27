package server

import (
	"context"
	"encoding/json"
	"testing"

	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
)

func TestSSHNativeControlsNeverBrowserAllowed(t *testing.T) {
	s := &Server{}
	for _, op := range []string{"ssh_account", "ssh_close", "ssh_key_status", "ssh_key_seal"} {
		if browserAllowedOperation(op) {
			t.Fatal("native SSH operator control browser allowed", op)
		}
		if _, e := s.dispatch(context.WithValue(t.Context(), browserContextKey{}, true), model.WireRequest{Operation: op, Params: json.RawMessage(`{}`)}); e == nil {
			t.Fatal("browser reached native SSH dispatch", op)
		}
	}
}
func TestSSHServiceDoesNotGrantFileOrForwardingRPCs(t *testing.T) {
	f := newPublicSSHFixture(t)
	for _, op := range []string{"ssh_account", "ssh_close", "ssh_exec", "ssh_files", "ssh_share", "ssh_forward", "sftp", "database_maintenance", "provision_runtime"} {
		if _, e := f.left.Call(t.Context(), f.identity.Target, mesh.Request{Operation: op, Params: json.RawMessage(`{}`)}); e == nil {
			t.Fatal("unexpected peer SSH/native capability", op)
		}
	}
}
