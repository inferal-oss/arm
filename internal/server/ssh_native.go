package server

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/sshservice"
)

func (s *Server) sshNative(ctx context.Context, operation string, raw json.RawMessage) (any, error) {
	switch operation {
	case "ssh_account":
		var q struct{}
		if e := decodeSSHFields(raw, &q); e != nil {
			return nil, e
		}
		b, home, e := s.Manager.SSHAccount(ctx)
		if e != nil {
			return nil, e
		}
		stateHome, e := filepath.EvalSymlinks(s.home)
		if e != nil {
			return nil, e
		}
		return struct {
			Binding   sshservice.Binding `json:"binding"`
			Home      string             `json:"home"`
			StateHome string             `json:"state_home"`
		}{b, home, stateHome}, nil
	case "ssh_close":
		var q struct {
			Revision uint64 `json:"revision"`
		}
		if e := decodeSSHFields(raw, &q, "revision"); e != nil {
			return nil, e
		}
		b, _, e := s.Manager.SSHAccount(ctx)
		if e != nil {
			return nil, e
		}
		home, e := filepath.EvalSymlinks(s.home)
		if e != nil {
			return nil, e
		}
		state, e := sshservice.ReadState(ctx, home, b)
		if e != nil {
			return nil, e
		}
		if state.Service == nil || state.Service.Enabled || state.Service.Revision != q.Revision {
			return nil, model.Err("ssh_context_changed", "exact disabled service revision required")
		}
		s.meshMu.Lock()
		service := s.mesh
		s.meshMu.Unlock()
		if service != nil && service.ssh != nil {
			service.ssh.ClosePeer("")
		}
		return struct {
			Revision uint64 `json:"revision"`
		}{q.Revision}, nil
	case "ssh_key_status":
		var q struct {
			Source string `json:"source"`
			Key    string `json:"key"`
		}
		if e := decodeSSHFields(raw, &q, "source", "key"); e != nil {
			return nil, e
		}
		status, e := s.Manager.LookupSSHKey(ctx, q.Source, q.Key)
		if e != nil {
			return nil, e
		}
		return compactSSHKeyStatus(status)
	case "ssh_key_seal":
		var q struct {
			Control model.SSHControlCommitment `json:"control"`
			Reason  string                     `json:"reason"`
		}
		if e := decodeSSHFields(raw, &q, "control", "reason"); e != nil {
			return nil, e
		}
		var fields map[string]json.RawMessage
		if e := json.Unmarshal(raw, &fields); e != nil {
			return nil, e
		}
		canonical, _ := json.Marshal(q.Control)
		if !bytes.Equal(canonical, fields["control"]) {
			return nil, model.Err("invalid_request", "exact canonical SSH control required")
		}
		status, e := s.Manager.SealSSHKeyAsOperator(ctx, q.Control, q.Reason)
		if e != nil {
			return nil, e
		}
		return compactSSHKeyStatus(status)
	}
	return nil, model.Err("forbidden", "unsupported native SSH operation")
}
