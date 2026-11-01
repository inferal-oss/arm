package server

import (
	"context"
	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/manager"
	"getarm.org/internal/model"
	"getarm.org/internal/server/lifetimeview"
	"getarm.org/internal/server/serviceview"
	"testing"
)

type workerUncomparableContext struct {
	context.Context
	payload []byte
}

func TestWorkerServerRootContextAndUnissuedManagerRefuseBeforeEffects(t *testing.T) {
	root := context.Background()
	settings := daemonconfig.Settings{Resources: model.Resources{CPU: 1}, Listen: "127.0.0.1:0"}
	m, err := manager.NewResidentWorkerManagerPending(&lifetimeview.WorkerManagerView{}, settings, &serviceview.WorkerManagerCharge{})
	if err != nil {
		t.Fatal(err)
	}
	o, err := newResidentWorkerServerPending(&lifetimeview.WorkerServerView{}, m, root)
	if err != nil {
		t.Fatal(err)
	}
	actual := o.server
	if actual == nil || actual.workerResident != o || actual.resident != nil || actual.lifecycleContext != root {
		t.Fatal("original server lifetime changed")
	}
	first := o.openOriginal(root)
	if first == nil || o.server != actual || o.claim != nil || o.managerBound != nil || actual.Manager != nil || o.opened || o.started {
		t.Fatal("unissued Manager reached native Server")
	}
	if err = o.startOriginalManager(); err == nil {
		t.Fatal("unopened server started Manager")
	}
}
func TestWorkerServerContextDataAndConflictingOwnerRefuse(t *testing.T) {
	root := context.Background()
	foreign := workerUncomparableContext{Context: root, payload: []byte{1}}
	if sameWorkerServerContextData(foreign, root) || sameWorkerServerContextData(root, foreign) || sameWorkerServerContextData(nil, root) {
		t.Fatal("foreign context accepted")
	}
	o := &residentWorkerServerPending{rootContext: root, manager: &manager.ResidentWorkerManagerPending{}}
	o.self = o
	o.server = &Server{workerResident: o, resident: &residentServerPending{}, lifecycleContext: root}
	if err := o.openOriginal(root); err == nil || o.active != nil || o.openTried || o.claim != nil {
		t.Fatal("mixed owner entered original effects")
	}
}
