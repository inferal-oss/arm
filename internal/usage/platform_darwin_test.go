package usage

import (
	"math"
	"os"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

func TestNativeDarwinLayoutsAndCPUTimebase(t *testing.T) {
	if unsafe.Sizeof(bsdInfo{}) != 136 || unsafe.Sizeof(rusageInfo{}) != 96 {
		t.Fatal("public libproc layouts do not match the supported ABI")
	}
	if err := nativeInit(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(100 * time.Millisecond)
	n := uint64(1)
	for time.Now().Before(deadline) {
		for range 10000 {
			n = n*1664525 + 1013904223
		}
	}
	var before, after unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &before); err != nil {
		t.Fatal(err)
	}
	var observed rusageInfo
	if native.rusage(int32(os.Getpid()), 0, &observed) != 0 {
		t.Fatal("native self rusage unavailable")
	}
	if err := unix.Getrusage(unix.RUSAGE_SELF, &after); err != nil {
		t.Fatal(err)
	}
	seconds := (float64(observed.User) + float64(observed.System)) * float64(native.numer) / float64(native.denom) / 1e9
	cpu := func(r unix.Rusage) float64 {
		return float64(r.Utime.Sec+r.Stime.Sec) + float64(r.Utime.Usec+r.Stime.Usec)/1e6
	}
	if math.IsNaN(seconds) || seconds < cpu(before)-.005 || seconds > cpu(after)+.005 {
		t.Fatalf("Mach clock units differ from native getrusage: before=%g proc=%g after=%g", cpu(before), seconds, cpu(after))
	}
	t.Log(n)
}
