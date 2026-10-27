package server

import (
	"encoding/json"
	"getarm.org/internal/mesh"
)

// provision_runtime is deliberately native-only. Browser and direct-peer
// allowlists do not expose it, even to observe-all or run-capable peers.
func (s *Server) provisionRuntime(raw json.RawMessage) (any, error) {
	var q struct{}
	if e := mesh.Decode(raw, &q); e != nil {
		return nil, e
	}
	proof, e := s.runtimeExecutable.Proof()
	if e != nil {
		return nil, e
	}
	status, e := s.Manager.Status()
	if e != nil {
		return nil, e
	}
	proof.Host = status.Host
	proof.UID = status.Owner
	return proof, nil
}
