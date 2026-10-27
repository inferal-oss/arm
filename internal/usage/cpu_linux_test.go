package usage

import (
	"testing"

	"getarm.org/internal/model"
)

func TestLinuxManagedCPUAccountingFallback(t *testing.T) {
	root := model.ProcessIdentity{PID: 1, PGID: 1, UID: "test", Start: "controlled-start", Namespace: "controlled-pidns"}
	if cookie, err := CaptureCPUAnchor(root); err == nil || cookie != "" {
		t.Fatal("unsupported Linux accounting created a managed anchor", cookie, err)
	}
	if q, unit, err := CPUClockQuantum(); err == nil || q != 0 || unit != "" {
		t.Fatal("unsupported Linux host/process units became creditable", q, unit, err)
	}
	cut := CollectCPU(t.Context(), []CPUOwner{{Token: "unsupported-owner", Root: root, Cookie: "caller-cannot-enable-credit"}})
	defer cut.Close()
	if len(cut.Members) != 0 || cut.Detail == "" {
		t.Fatal("unsupported native CPU collection fabricated member evidence", cut)
	}
}
