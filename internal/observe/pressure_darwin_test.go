package observe

import (
	"os/exec"
	"regexp"
	"strconv"
	"testing"
)

func TestDarwinPressureNative(t *testing.T) {
	p := darwinPressure()
	if p.loadErr != nil || p.load[0] < 0 {
		t.Fatalf("load %v %v", p.load, p.loadErr)
	}
	if p.memErr != nil || (p.memLevel != 1 && p.memLevel != 2 && p.memLevel != 4) {
		t.Fatalf("memory status %d %v", p.memLevel, p.memErr)
	}
	if p.vmErr != nil || p.pageSize == 0 {
		t.Fatalf("vm %v", p.vmErr)
	}
	out, err := exec.Command("/usr/bin/vm_stat").Output()
	if err != nil {
		t.Skip("vm_stat unavailable")
	}
	for i, name := range map[int]string{1: "Swapins", 2: "Swapouts", 0: "Pageouts"} {
		m := regexp.MustCompile(name + `:\s+(\d+)`).FindSubmatch(out)
		if m == nil {
			t.Fatalf("vm_stat lacks %s", name)
		}
		want, _ := strconv.ParseUint(string(m[1]), 10, 64)
		// Counters advance between the two reads; allow forward drift.
		if p.vm[i] > want || want-p.vm[i] > 1<<20 {
			t.Fatalf("%s: native %d vm_stat %d", name, p.vm[i], want)
		}
	}
}
