package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"getarm.org/internal/local"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
)

type preparedMeshSetup struct {
	request mesh.PrepareRequest
	view    mesh.PreparedSetup
	socket  *mesh.PreparedListener
	store   *mesh.Store
	timer   *time.Timer
	release func()
}

func (p *preparedMeshSetup) close() {
	if p.release != nil {
		p.release()
		p.release = nil
	}
	if p.timer != nil {
		p.timer.Stop()
	}
	if p.socket != nil {
		p.socket.Close()
	}
	if p.store != nil {
		p.store.Close()
	}
}

func meshNodeName(host string) string {
	name, _ := os.Hostname()
	name = strings.TrimSpace(name)
	if name == "" || name == "localhost" {
		name = "node"
	}
	for _, r := range name {
		if r <= 32 || r == 127 {
			name = "node"
			break
		}
	}
	if len(name) > 96 {
		name = "node"
	}
	return name + "-" + host[:8]
}

func (s *Server) meshSetupOperation(ctx context.Context, operation string, raw json.RawMessage) (any, error) {
	if operation == "mesh_setup_prepare" {
		var request mesh.PrepareRequest
		if err := mesh.Decode(raw, &request); err != nil {
			return nil, err
		}
		return s.prepareMeshSetup(request)
	}
	if operation == "mesh_setup_abort" {
		var request struct{ Key, Token string }
		if err := mesh.Decode(raw, &request); err != nil {
			return nil, err
		}
		s.meshMu.Lock()
		defer s.meshMu.Unlock()
		if pending := s.meshPending; pending != nil && pending.view.Key == request.Key && pending.view.Token == request.Token {
			pending.close()
			s.meshPending = nil
		}
		return map[string]any{"aborted": request.Key}, nil
	}
	if operation == "mesh_setup_apply" {
		var request mesh.ApplySetup
		if err := mesh.Decode(raw, &request); err != nil {
			return nil, err
		}
		return s.applyMeshSetup(ctx, request)
	}
	if operation == "mesh_setup_revoke" {
		var request struct {
			Key, Host, Pair, Fingerprint, Reason string
			Revision                             int
		}
		if err := mesh.Decode(raw, &request); err != nil {
			return nil, err
		}
		s.meshMu.Lock()
		service := s.mesh
		s.meshMu.Unlock()
		var store *mesh.Store
		if service != nil {
			store = service.store
		} else {
			var err error
			store, err = mesh.OpenRecovery(s.home)
			if err != nil {
				return nil, err
			}
			defer store.Close()
		}
		if err := store.RevokeExact(request.Key, request.Host, request.Pair, request.Fingerprint, request.Revision, request.Reason); err != nil {
			return nil, err
		}
		disabled, err := store.DisableUnsharedListener()
		if err != nil {
			return nil, err
		}
		if disabled {
			s.closeMesh()
		}
		return map[string]any{"revoked": request.Host, "pair": request.Pair, "listener_disabled": disabled}, nil
	}
	return nil, model.Err("forbidden", "unknown mesh lifecycle administration operation")
}

func (s *Server) prepareMeshSetup(request mesh.PrepareRequest) (mesh.PreparedSetup, error) {
	var none mesh.PreparedSetup
	if !mesh.ValidKey(request.Key) {
		return none, model.Err("invalid_request", "mesh setup requires an exact operation key")
	}
	if err := s.meshAdmission(); err != nil {
		return none, err
	}
	status, err := s.Manager.Status()
	if err != nil {
		return none, err
	}
	if request.ExpectedHost != "" && request.ExpectedHost != status.Host {
		return none, model.Err("wrong_host", "host ledger changed since inspection")
	}
	s.meshMu.Lock()
	if service := s.mesh; service != nil {
		config := service.store.Config()
		s.meshMu.Unlock()
		if request.ExpectedFingerprint != "" && config.Identity.Fingerprint != request.ExpectedFingerprint {
			return none, model.Err("setup_changed", "active node pin changed since inspection")
		}
		return mesh.PreparedSetup{Key: request.Key, Token: "existing", Identity: config.Identity, Listen: config.Listen, Existing: true}, nil
	}
	if pending := s.meshPending; pending != nil {
		if time.Now().Before(pending.view.Expires) {
			if !reflect.DeepEqual(pending.request, request) {
				s.meshMu.Unlock()
				return none, model.Err("setup_busy", "another setup or changed exact-key request owns the pending socket")
			}
			view := pending.view
			s.meshMu.Unlock()
			return view, nil
		}
		pending.close()
		s.meshPending = nil
	}
	s.meshMu.Unlock()
	if err := mesh.AutomaticIP(request.IP, request.AllowPublic); err != nil {
		return none, err
	}
	socket, err := mesh.PrepareListener(request.IP)
	if err != nil {
		return none, err
	}
	owned := false
	defer func() {
		if !owned {
			socket.Close()
		}
	}()
	store, err := mesh.Open(s.home)
	if os.IsNotExist(err) {
		if request.ExpectedFingerprint != "" {
			return none, model.Err("setup_changed", "inspected node identity disappeared; no replacement key was generated")
		}
		name := request.Name
		if name == "" {
			name = meshNodeName(status.Host)
		}
		store, err = mesh.InitPrepared(s.home, status.Host, name, socket.Address(), socket.Address())
	} else if err == nil {
		prior := store.Config()
		if prior.Identity.Host != status.Host || prior.Identity.Owner != status.Owner || (request.ExpectedFingerprint != "" && prior.Identity.Fingerprint != request.ExpectedFingerprint) {
			store.Close()
			return none, model.Err("setup_changed", "existing identity differs from inspected host/owner/pin; endpoint was not changed")
		}
		if store.Config().ListenerEnabled() {
			store.Close()
			return none, model.Err("mesh_unavailable", "existing configured listener is unavailable; its address was preserved rather than rebound")
		}
		if err = store.PrepareEndpoint(socket.Address()); err != nil {
			store.Close()
			return none, err
		}
	}
	if err != nil {
		return none, err
	}
	config := store.Config()
	if config.Identity.Host != status.Host || config.Identity.Owner != status.Owner || (request.ExpectedFingerprint != "" && request.ExpectedFingerprint != config.Identity.Fingerprint) {
		store.Close()
		return none, model.Err("setup_changed", "prepared identity differs from inspected host/owner/pin")
	}
	if err = s.Manager.TrackPreparedMeshInfrastructure(socket.Address()); err != nil {
		store.Close()
		return none, err
	}
	pending := &preparedMeshSetup{release: s.Manager.RemovePreparedMeshInfrastructure, request: request, view: mesh.PreparedSetup{Key: request.Key, Token: local.ID(), Identity: config.Identity, Listen: config.Listen, Expires: time.Now().UTC().Add(2 * time.Minute)}, socket: socket, store: store}
	s.meshMu.Lock()
	if s.mesh != nil || s.meshPending != nil {
		s.meshMu.Unlock()
		pending.close()
		return none, model.Err("setup_busy", "mesh lifecycle changed during preparation")
	}
	s.lifecycleMu.Lock()
	if s.lifecycleClosing || (s.lifecycleContext != nil && s.lifecycleContext.Err() != nil) {
		s.lifecycleMu.Unlock()
		s.meshMu.Unlock()
		pending.close()
		return none, model.Err("backend_stopping", "mesh preparation cancelled during shutdown")
	}
	latest, latestErr := s.Manager.Status()
	if latestErr != nil || latest.Draining {
		s.lifecycleMu.Unlock()
		s.meshMu.Unlock()
		pending.close()
		return none, model.Err("backend_stopping", "mesh preparation unavailable during drain")
	}
	s.meshPending = pending
	s.lifecycleMu.Unlock()
	pending.timer = time.AfterFunc(2*time.Minute, func() {
		s.meshMu.Lock()
		defer s.meshMu.Unlock()
		if s.meshPending == pending {
			pending.close()
			s.meshPending = nil
		}
	})
	s.meshMu.Unlock()
	owned = true
	return pending.view, nil
}

func (s *Server) applyMeshSetup(ctx context.Context, request mesh.ApplySetup) (mesh.Identity, error) {
	var none mesh.Identity
	if err := ctx.Err(); err != nil {
		return none, err
	}
	if err := s.meshAdmission(); err != nil {
		return none, err
	}
	s.meshMu.Lock()
	if service := s.mesh; service != nil {
		config := service.store.Config()
		s.meshMu.Unlock()
		if !mesh.ValidKey(request.Key) || (request.Token != "existing" && !config.AppliedListener(request.Key, request.Token)) || config.Identity.Fingerprint != request.Fingerprint || config.Listen != request.Listen {
			return none, model.Err("setup_changed", "existing listener differs from reviewed endpoint/pin")
		}
		return config.Identity, nil
	}
	pending := s.meshPending
	if pending == nil || pending.view.Key != request.Key || pending.view.Token != request.Token || pending.view.Identity.Fingerprint != request.Fingerprint || pending.view.Listen != request.Listen || !time.Now().Before(pending.view.Expires) {
		s.meshMu.Unlock()
		return none, model.Err("setup_expired", "pending listener changed or expired; prepare and review its exact endpoint again")
	}
	s.meshPending = nil
	pending.timer.Stop()
	s.meshMu.Unlock()
	transferred := false
	defer func() {
		if !transferred {
			pending.close()
		}
	}()
	if err := pending.store.ApproveListener(request.Key, request.Token, request.Fingerprint, request.Listen); err != nil {
		return none, err
	}
	if err := ctx.Err(); err != nil {
		if rollback := pending.store.SetListenerEnabled(false, request.Fingerprint, request.Listen); rollback != nil {
			return none, model.Err("setup_outcome_unknown", "cancelled listener preparation could not persist disabled state; inspect configured listener")
		}
		return none, err
	}
	if err := s.startMeshListener(pending.store, pending.socket); err != nil {
		rollbackErr := pending.store.SetListenerEnabled(false, request.Fingerprint, request.Listen)
		if rollbackErr != nil {
			return none, model.Err("setup_outcome_unknown", fmt.Sprintf("listener failed to activate (%v); desired-state rollback failed (%v); inspect before retry", err, rollbackErr))
		}
		return none, fmt.Errorf("reviewed listener did not activate: %w", err)
	}
	if pending.release != nil {
		pending.release()
		pending.release = nil
	}
	transferred = true
	return pending.view.Identity, nil
}
