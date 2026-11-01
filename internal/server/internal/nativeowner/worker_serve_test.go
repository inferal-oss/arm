package nativeowner

import (
	"context"
	"testing"

	"getarm.org/internal/residentrefs"
	"getarm.org/internal/server/internal/residentbirth"
	"getarm.org/internal/server/internal/residentservice"
)

func unissuedWorkerNativeData(t *testing.T) *WorkerServeIssuer {
	t.Helper()
	var p *WorkerServeIssuer
	if err := NewInheritedWorkerServePending(&p, &residentbirth.WorkerInheritedFencePending{}, &residentservice.WorkerServeBirthView{}, &residentservice.WorkerStartupFundingView{}); err != nil || p == nil {
		t.Fatal("pure pending allocation failed", err)
	}
	return p
}

func assertWorkerNativeNoIO(t *testing.T, p *WorkerServeIssuer) {
	t.Helper()
	n := p.raw.state
	if n.opened != 0 || n.closed != 0 || n.directory != nil || n.lock != nil || n.lockAcquired || n.uid != 0 || p.raw.generation != "" {
		t.Fatal("unissued worker reached native authority")
	}
	for _, slot := range n.slots {
		if slot != nil {
			t.Fatal("unissued worker retained a native descriptor")
		}
	}
	if p.raw.original != nil || p.raw.inheritedFence != nil || p.raw.manager != nil || p.raw.server != nil || p.raw.managerStop != nil || p.raw.serverStop != nil {
		t.Fatal("worker acquired legacy authority")
	}
}

func TestWorkerNativeConstructorAndOriginalCopiesRefuse(t *testing.T) {
	fence, birth := &residentbirth.WorkerInheritedFencePending{}, &residentservice.WorkerServeBirthView{}
	if err := NewInheritedWorkerServePending(nil, fence, birth, &residentservice.WorkerStartupFundingView{}); err == nil {
		t.Fatal("nil destination accepted")
	}
	occupied := &WorkerServeIssuer{}
	p := occupied
	if err := NewInheritedWorkerServePending(&p, fence, birth, &residentservice.WorkerStartupFundingView{}); err == nil || p != occupied {
		t.Fatal("occupied destination replaced")
	}
	p = nil
	if err := NewInheritedWorkerServePending(&p, nil, birth, &residentservice.WorkerStartupFundingView{}); err == nil || p != nil {
		t.Fatal("nil fence published custody")
	}
	if err := NewInheritedWorkerServePending(&p, fence, nil, &residentservice.WorkerStartupFundingView{}); err == nil || p != nil {
		t.Fatal("nil birth published custody")
	}
	if err := NewInheritedWorkerServePending(&p, fence, birth, nil); err == nil || p != nil {
		t.Fatal("nil funding published custody")
	}
	p = unissuedWorkerNativeData(t)
	copyPending := *p
	if err := copyPending.OpenOriginal(); err == nil {
		t.Fatal("copied original opened")
	}
	if _, err := p.ManagerView(); err == nil {
		t.Fatal("unopened issuer supplied Manager view")
	}
	if _, err := p.ServerView(); err == nil {
		t.Fatal("unopened issuer supplied Server view")
	}
	if err := p.OpenOriginal(); err == nil {
		t.Fatal("unissued native fence/birth opened")
	}
	assertWorkerNativeNoIO(t, p)
	if err := p.OpenOriginal(); err == nil {
		t.Fatal("failed original open repeated")
	}
}

func TestWorkerNativeMetadataCannotReplaceActualFenceBeforeIO(t *testing.T) {
	p := unissuedWorkerNativeData(t)
	p.raw.openTried, p.raw.openComplete = true, true // deliberately forged DATA
	v, err := p.ManagerView()
	if err == nil || v == nil || v != p.manager {
		t.Fatal("unissued fence supplied a validated view", err)
	}
	assertWorkerNativeNoIO(t, p)
	legacy := &ServeIssuer{owner: p.raw}
	if _, err := legacy.ManagerView(); err == nil {
		t.Fatal("worker raw bookkeeping supplied a legacy view")
	}
	if _, err := legacy.ServerView(); err == nil {
		t.Fatal("worker raw bookkeeping supplied a legacy Server view")
	}
}

func TestWorkerNativeLegacyAuthorityAlwaysRefusesBeforeEffects(t *testing.T) {
	for _, name := range []string{"issuer", "manager", "server", "fence", "identities", "sealed", "retired", "legacy-operation", "foreign-operation"} {
		t.Run(name, func(t *testing.T) {
			p := unissuedWorkerNativeData(t)
			switch name {
			case "issuer":
				p.raw.original = &ServeIssuer{}
			case "manager":
				p.raw.manager = &ManagerView{}
			case "server":
				p.raw.server = &ServerView{}
			case "fence":
				p.raw.inheritedFence = &residentbirth.InheritedFencePending{}
			case "identities":
				p.raw.inheritedIDs.Home = residentrefs.Identity{Inode: 1}
			case "sealed":
				p.raw.sealed = true
			case "retired":
				p.raw.retired = true
			case "legacy-operation":
				p.raw.active = &nativeEffect{owner: p.raw, kind: "validate_manager_view"}
			case "foreign-operation":
				p.raw.active = &nativeEffect{owner: &nativeOwner{}, kind: "worker_manager_current"}
			}
			prior := p.raw.active
			if err := p.OpenOriginal(); err == nil || p.raw.active != prior || p.raw.openTried || p.raw.state.opened != 0 {
				t.Fatal("legacy authority admitted a worker native effect")
			}
		})
	}
}

func TestWorkerNativeClaimsRejectZeroCopiedAndForeignViews(t *testing.T) {
	var manager WorkerManagerView
	var server WorkerServerView
	if _, err := manager.ClaimOriginal(); err == nil {
		t.Fatal("zero Manager view claimed")
	}
	if _, err := server.ClaimOriginal(); err == nil {
		t.Fatal("zero Server view claimed")
	}
	p := unissuedWorkerNativeData(t)
	v := &WorkerManagerView{owner: p}
	v.self, p.manager = v, v
	copyView := *v
	if _, err := copyView.ClaimOriginal(); err == nil {
		t.Fatal("copied Manager view claimed")
	}
	foreign := unissuedWorkerNativeData(t)
	v.owner = foreign
	if _, err := v.ValidateOriginal(); err == nil {
		t.Fatal("foreign view validated")
	}
	claim := &WorkerManagerClaim{owner: p, view: v}
	claim.self = claim
	for _, c := range []*WorkerManagerClaim{nil, {}, claim} {
		if _, err := c.ValidateOriginalContext(context.Background()); err == nil {
			t.Fatal("unissued Manager claim validated")
		}
		if _, err := c.ValidateOriginalDatabaseFence(context.Background()); err == nil {
			t.Fatal("unissued Manager claim admitted database")
		}
	}
	var serverClaim WorkerServerClaim
	if _, err := serverClaim.ValidateOriginalContext(context.Background()); err == nil {
		t.Fatal("zero Server claim validated")
	}
	assertWorkerNativeNoIO(t, p)
	assertWorkerNativeNoIO(t, foreign)
}
