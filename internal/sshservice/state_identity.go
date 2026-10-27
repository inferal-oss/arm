package sshservice

import "getarm.org/internal/mesh"

// MeshIdentity is the public local certificate identity from this exact verified
// read snapshot. It exposes no private key and never initializes configuration.
func (s *State) MeshIdentity() mesh.Identity { return s.config.Identity }
