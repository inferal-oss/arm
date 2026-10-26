package usage

import (
	"math"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

func cpuTestOwner() CPUOwner {
	return CPUOwner{Token: "lease-original", Cookie: "root-cookie", Root: model.ProcessIdentity{PID: 11, PGID: 11, UID: "user", Start: "root-start", Namespace: "boot-ns"}}
}
func cpuTestRecord(owner CPUOwner, pid, parent int, cookie, parentCookie string, u, s uint64) CPUProcessCounter {
	id := owner.Root
	if pid != id.PID {
		id.PID = pid
		id.Start = cookie + "-start"
	}
	return CPUProcessCounter{Owner: owner, Identity: id, ParentPID: parent, Cookie: cookie, ParentCookie: parentCookie, User: u, System: s, Unit: CPUUnitDarwinMach}
}
func cpuTestTree() []CPUProcessCounter {
	o := cpuTestOwner()
	return []CPUProcessCounter{cpuTestRecord(o, 11, 1, o.Cookie, "backend", 10, 10), cpuTestRecord(o, 22, 11, "cargo-child", o.Cookie, 20, 20), cpuTestRecord(o, 33, 22, "rustc-grandchild", "cargo-child", 30, 30)}
}
func TestCPUCreditVerifiedCargoAncestryAndRawDeltas(t *testing.T) {
	before := cpuTestTree()
	after := append([]CPUProcessCounter(nil), before...)
	for i := range after {
		after[i].User += uint64(100 + i)
		after[i].System += uint64(50 + i)
	}
	got := cpuDeltas(before, after)
	if len(got) != 1 || got[0].MatchedProcesses != 3 || got[0].User != 303 || got[0].System != 153 {
		t.Fatal("verified child/grandchild CPU lost or doubled", got)
	}
	if got[0].Members[2].BeforeUser != 30 || got[0].Members[2].AfterUser != 132 || got[0].Members[2].User != 102 {
		t.Fatal("raw provenance lost", got[0].Members)
	}
	// A foreign same-UID same-PGID process has no original parent chain.
	foreign := cpuTestRecord(cpuTestOwner(), 44, 99, "foreign", "unrelated", 1000, 1000)
	before = append(before, foreign)
	foreign.User += 9000
	after = append(after, foreign)
	if d := cpuDeltas(before, after); len(d) != 1 || d[0].User != 303 || d[0].MatchedProcesses != 3 {
		t.Fatal("unrelated PGID join gained credit", d)
	}
}
func TestCPUCreditChangedOrMissingEvidenceCannotBeAttributed(t *testing.T) {
	for _, name := range []string{"reused", "changed-start", "reparented", "wrong-parent-cookie", "wrong-uid", "wrong-pgid", "regressed", "wrong-unit", "new", "exited"} {
		t.Run(name, func(t *testing.T) {
			before := cpuTestTree()
			after := append([]CPUProcessCounter(nil), before...)
			after[2].User += 1000
			switch name {
			case "reused":
				after[2].Cookie = "reused-pid"
			case "changed-start":
				after[2].Identity.Start = "new-start"
			case "reparented":
				after[2].ParentPID = 11
				after[2].ParentCookie = after[0].Cookie
			case "wrong-parent-cookie":
				after[2].ParentCookie = "foreign"
			case "wrong-uid":
				after[2].Identity.UID = "another-user"
			case "wrong-pgid":
				after[2].Identity.PGID = 99
			case "regressed":
				after[2].System = 0
			case "wrong-unit":
				after[2].Unit = "independent-seconds"
			case "new":
				before = before[:2]
			case "exited":
				after = after[:2]
			}
			d := cpuDeltas(before, after)
			if len(d) != 1 || d[0].User != 0 || d[0].System != 0 || d[0].MatchedProcesses != 2 {
				t.Fatal("bad member gained CPU credit", name, d)
			}
		})
	}
}
func TestCPUCreditDuplicateGenerationAndOverflowRefused(t *testing.T) {
	before := cpuTestTree()
	after := append([]CPUProcessCounter(nil), before...)
	if d := cpuDeltas(append(before, before[0]), after); len(d) != 0 {
		t.Fatal("duplicate process accepted", d)
	}
	other := after[0]
	other.Owner.Token = "other-owner"
	if d := cpuDeltas(before, append(after, other)); len(d) != 0 {
		t.Fatal("same native process credited to two jobs", d)
	}
	other = after[0]
	other.Owner.Cookie = "new-original-root"
	if d := cpuDeltas(before, append(after, other)); len(d) != 0 {
		t.Fatal("same token changed original generation", d)
	}
	after[0].User = math.MaxUint64
	after[1].User = math.MaxUint64
	if d := cpuDeltas(before, after); len(d) != 0 {
		t.Fatal("overflowed CPU owner sum gained credit", d)
	}
}
func TestCPUCutLifetimeAndInventoryBound(t *testing.T) {
	o := cpuTestOwner()
	records := []CPUProcessCounter{cpuTestRecord(o, 11, 1, o.Cookie, "backend", 0, 0)}
	for i := 0; i < CPUProcessLimit+20; i++ {
		records = append(records, cpuTestRecord(o, 100+i, 11, "child"+time.Duration(i).String(), o.Cookie, 1, 1))
	}
	cut := CPUCut{Started: time.Now(), state: &cpuCutState{}}
	retainCPUAncestry(&cut, o, records)
	if len(cut.Members) != CPUProcessLimit {
		t.Fatal("global CPU member bound lost", len(cut.Members))
	}
	before := CPUCut{Members: cpuTestTree(), state: &cpuCutState{}}
	after := CPUCut{Members: cpuTestTree(), state: &cpuCutState{}}
	if len(ContainedCPUDeltas(before, after)) != 1 {
		t.Fatal("live private cuts did not match")
	}
	if err := before.Close(); err != nil {
		t.Fatal(err)
	}
	if len(ContainedCPUDeltas(before, after)) != 0 {
		t.Fatal("retired lifetime evidence gained credit")
	}
	if len(ContainedCPUDeltas(CPUCut{Members: cpuTestTree()}, after)) != 0 {
		t.Fatal("ordinary metadata became native lifetime evidence")
	}
}
