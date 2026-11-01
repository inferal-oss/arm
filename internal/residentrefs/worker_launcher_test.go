package residentrefs

import "testing"

func TestWorkerLauncherSelectionUsesOnlyStandaloneStartCommandShapeData(t *testing.T) {
	for _, args := range [][]string{{"arm", "start"}, {"arm", "--home", "/selected", "--json", "start", "--resident"}, {"arm", "--json=false", "daemon", "start"}, {"arm", "--", "start"}} {
		if !workerLauncherArgsData(args) {
			t.Fatal("standalone start shape refused", args)
		}
	}
	for _, args := range [][]string{nil, {"arm"}, {"arm", "serve"}, {"arm", "--arm-resident-worker-core-v1"}, {"arm", "--arm-api-worker-v1"}, {"arm", "daemon", "stop"}, {"arm", "--home", "start"}, {"arm", "mesh", "connect"}, {"arm", "--json", "false", "start"}} {
		if workerLauncherArgsData(args) {
			t.Fatal("nonlauncher process shape accepted", args)
		}
	}
}

func TestWorkerLauncherUnissuedAndCopiedViewsCannotAuthorizeSchedulerChange(t *testing.T) {
	v := &WorkerLauncherOriginal{}
	v.self = v
	copy := *v
	for _, value := range []*WorkerLauncherOriginal{nil, {}, v, &copy} {
		if value.RequireOriginalStandaloneStart() == nil {
			t.Fatal("DATA supplied actual launcher entry scope")
		}
	}
}
