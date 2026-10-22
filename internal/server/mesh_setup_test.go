package server

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/mesh"
)

func TestPreparedMeshSocketCannotListenBeforeExactReview(t *testing.T) {
	s := meshFixture(t)
	t.Cleanup(s.closeMesh)
	status, _ := s.Manager.Status()
	prepared, err := s.prepareMeshSetup(mesh.PrepareRequest{Key: "setup", IP: "127.0.0.1", ExpectedHost: status.Host})
	if err != nil {
		t.Fatal(err)
	}
	if c, err := net.DialTimeout("tcp", prepared.Listen, 50*time.Millisecond); err == nil {
		c.Close()
		t.Fatal("unapproved listener acceptedTCP")
	}
	if listener, err := net.Listen("tcp", prepared.Listen); err == nil {
		listener.Close()
		t.Fatal("prepared endpoint not reserved")
	}
	request := mesh.ApplySetup{Key: "setup", Token: prepared.Token, Fingerprint: "changed", Listen: prepared.Listen}
	if _, err = s.applyMeshSetup(t.Context(), request); err == nil {
		t.Fatal("unreviewed pin enabled listener")
	}
	request.Fingerprint = prepared.Identity.Fingerprint
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = s.applyMeshSetup(ctx, request); err == nil {
		t.Fatal("cancelled request enabled listener")
	}
	if _, err = s.applyMeshSetup(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if c, err := net.DialTimeout("tcp", prepared.Listen, time.Second); err != nil {
		t.Fatal("approved endpoint not listening", err)
	} else {
		c.Close()
	}
	if _, err = s.applyMeshSetup(t.Context(), request); err != nil {
		t.Fatal("lost acknowledgement exact apply retry", err)
	}
	wrong := request
	wrong.Token = "unreviewed"
	if _, err = s.applyMeshSetup(t.Context(), wrong); err == nil {
		t.Fatal("unreviewed existing setup token accepted")
	}
	if !s.mesh.store.Config().ListenerEnabled() {
		t.Fatal("desired endpoint not persisted")
	}
	s.closeMesh()
	if err = s.loadMesh(); err != nil {
		t.Fatal("restart", err)
	}
	if s.mesh.listener.Addr().String() != prepared.Listen {
		t.Fatal("restart changed reviewed port")
	}
}
func TestPendingSetupDoesNotSurviveShutdownOrExpiry(t *testing.T) {
	for _, which := range []string{"shutdown", "expiry"} {
		t.Run(which, func(t *testing.T) {
			s := meshFixture(t)
			t.Cleanup(s.closeMesh)
			prepared, err := s.prepareMeshSetup(mesh.PrepareRequest{Key: "setup", IP: "127.0.0.1"})
			if err != nil {
				t.Fatal(err)
			}
			if which == "shutdown" {
				s.lifecycleMu.Lock()
				s.lifecycleClosing = true
				s.lifecycleMu.Unlock()
				s.closeMesh()
			} else {
				s.meshMu.Lock()
				s.meshPending.view.Expires = time.Now().Add(-time.Second)
				s.meshMu.Unlock()
			}
			if _, err = s.applyMeshSetup(t.Context(), mesh.ApplySetup{Key: "setup", Token: prepared.Token, Fingerprint: prepared.Identity.Fingerprint, Listen: prepared.Listen}); err == nil {
				t.Fatal("late callback enabled listener")
			}
			if c, err := net.DialTimeout("tcp", prepared.Listen, 50*time.Millisecond); err == nil {
				c.Close()
				t.Fatal("late setup listening")
			}
		})
	}
}
func TestUnavailableRestoredMeshPortDoesNotRebindOrDisableLocalAuthority(t *testing.T) {
	s := meshFixture(t)
	t.Cleanup(s.closeMesh)
	p, err := s.prepareMeshSetup(mesh.PrepareRequest{Key: "setup", IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.applyMeshSetup(t.Context(), mesh.ApplySetup{Key: p.Key, Token: p.Token, Fingerprint: p.Identity.Fingerprint, Listen: p.Listen}); err != nil {
		t.Fatal(err)
	}
	s.closeMesh()
	occupied, err := net.Listen("tcp", p.Listen)
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	if err = s.loadMesh(); err == nil {
		t.Fatal("occupied restored endpoint silently rebound")
	}
	if s.mesh != nil {
		t.Fatal("failed mesh marked running")
	}
	status, err := s.Manager.Status()
	if err != nil {
		t.Fatal("local authority unavailable", err)
	}
	for _, entry := range status.Infrastructure {
		if entry.Name == "arm-mesh" {
			t.Fatal("failed listener still charged")
		}
	}
	occupied.Close()
	if err = s.loadMesh(); err != nil {
		t.Fatal(err)
	}
}
func TestUnavailableRestoredInterfacePreservesLocalAuthority(t *testing.T) {
	s := meshFixture(t)
	t.Cleanup(s.closeMesh)
	status, _ := s.Manager.Status()
	store, err := mesh.InitPrepared(s.home, status.Host, "unavailable", "192.0.2.123:41001", "192.0.2.123:41001")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ApproveListener("reviewed", "token", store.Config().Identity.Fingerprint, store.Config().Listen); err != nil {
		t.Fatal(err)
	}
	store.Close()
	if err = s.loadMesh(); err == nil {
		t.Fatal("unassigned restored interface rebound")
	}
	if _, err = s.Manager.Status(); err != nil {
		t.Fatal("local authority failed", err)
	}
}
func TestSetupAdministrationNeverEntersPeerAllowlist(t *testing.T) {
	s := meshFixture(t)
	service := meshService{owner: s}
	for _, operation := range []string{"mesh_setup_prepare", "mesh_setup_apply", "mesh_setup_revoke", "mesh_setup_abort", "lifecycle_ensure", "lifecycle_inspect"} {
		if _, err := service.dispatch(t.Context(), mesh.Peer{Grant: mesh.Grant{Permissions: []string{"observe-host", "observe-own", "observe-all", "lease-own", "run", "message", "contact-read"}}}, mesh.Request{Operation: operation}); err == nil {
			t.Fatal("peer invoked local lifecycle administration", operation)
		}
	}
}

func TestPrepareRejectsCopiedOrMissingPinBeforeChangingIdentity(t *testing.T) {
	t.Run("copied", func(t *testing.T) {
		s := meshFixture(t)
		t.Cleanup(s.closeMesh)
		store, err := mesh.InitPrepared(s.home, strings.Repeat("a", 32), "copied", "127.0.0.1:41002", "127.0.0.1:41002")
		if err != nil {
			t.Fatal(err)
		}
		store.Close()
		path := filepath.Join(s.home, "mesh", "config.json")
		before, _ := os.ReadFile(path)
		if _, err = s.prepareMeshSetup(mesh.PrepareRequest{Key: "setup", IP: "127.0.0.1"}); err == nil {
			t.Fatal("copied identity accepted")
		}
		after, _ := os.ReadFile(path)
		if string(before) != string(after) {
			t.Fatal("copied identity modified before rejection")
		}
	})
	t.Run("missing", func(t *testing.T) {
		s := meshFixture(t)
		t.Cleanup(s.closeMesh)
		if _, err := s.prepareMeshSetup(mesh.PrepareRequest{Key: "setup", IP: "127.0.0.1", ExpectedFingerprint: strings.Repeat("a", 64)}); err == nil {
			t.Fatal("missing expected identity replaced")
		}
		if _, err := os.Stat(filepath.Join(s.home, "mesh")); !os.IsNotExist(err) {
			t.Fatal("unexpected replacement key state", err)
		}
	})
}
