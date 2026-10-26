package usage

import "testing"

func TestDarwinCPUAccountingFamilyGate(t *testing.T) {
	version := "Darwin Kernel Version 25.6.0: Sat Jul 11 15:27:04 PDT 2026; root:xnu-12377.161.13~4/RELEASE_ARM64_T6050"
	if !darwinCPUAccountingFamily("arm64", "25.6.0", version, 0) {
		t.Fatal("reviewed native recount family rejected")
	}
	for _, v := range []struct {
		arch, release, version string
		translated             int32
	}{
		{"amd64", "25.6.0", version, 0}, {"arm64", "26.0.0", version, 0}, {"arm64", "25.6.0", version, 1}, {"arm64", "25.6.0", version, -1},
		{"arm64", "25.5.0", version, 0}, {"arm64", "25.6.0", "Darwin Kernel Version 25.6.0: time; root:xnu-12378.1.1~1/RELEASE_ARM64_T6050", 0},
		{"arm64", "25.6.0", "Darwin Kernel Version 25.6.0: time; root:xnu-12377.1.1~1/DEVELOPMENT_ARM64_T6050", 0},
		{"arm64", "25.6.0", "Darwin Kernel Version 25.6.0: time; root:xnu-12377.1.1~1/DEBUG_ARM64_T6050", 0},
		{"arm64", "25.6.0", version + "\nforged", 0}, {"arm64", "25.06.0", version, 0},
	} {
		if darwinCPUAccountingFamily(v.arch, v.release, v.version, v.translated) {
			t.Fatal("unsupported native accounting family admitted", v)
		}
	}
}
func TestCPUExactMachQuantum(t *testing.T) {
	for _, v := range []struct {
		numer, denom uint32
		q            uint64
	}{{1, 1, 10000000}, {125, 3, 240000}, {1000000000, 101, 1}} {
		q, err := cpuMachQuantum(v.numer, v.denom)
		if err != nil || q != v.q {
			t.Fatal(v, q, err)
		}
	}
	for _, v := range [][2]uint32{{0, 1}, {1, 0}, {3, 1}, {1, 5}, {1000000000, 1}} {
		if _, err := cpuMachQuantum(v[0], v[1]); err == nil {
			t.Fatal("inexact/unsupported timebase accepted", v)
		}
	}
}
