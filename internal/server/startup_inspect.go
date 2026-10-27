package server

import (
	"getarm.org/internal/model"
	"getarm.org/internal/storage"
)

func (s *Server) startupInspection() (model.BackendAuthority, error) {
	if s.nodeStopDirectory == nil || s.generation == "" {
		return model.BackendAuthority{}, model.Err("node_generation_changed", "actual backend lifetime is unavailable")
	}
	if err := s.nodeStopDirectory.Validate(); err != nil {
		return model.BackendAuthority{}, err
	}
	id, err := storage.DirectoryIdentity(int(s.nodeStopDirectory.FD.Fd()))
	if err != nil {
		return model.BackendAuthority{}, err
	}
	status, err := s.Manager.Status()
	if err != nil {
		return model.BackendAuthority{}, err
	}
	return model.BackendAuthority{Host: status.Host, Owner: status.Owner, Home: s.home, Directory: id, Generation: s.generation}, nil
}
