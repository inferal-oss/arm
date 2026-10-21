package observe

import (
	"strings"
	"testing"
	"time"
)

func TestLinuxMonitorParsers(t *testing.T) {
	cpu, e := parseMonitorCPU([]byte("cpu 100 200 300 400\ncpu0 10 20 30 40 50 60 70 80 9000 9000\n"))
	if e != nil || len(cpu) != 1 || cpu[0].busy != 270 || cpu[0].idle != 90 {
		t.Fatal("guest double counting or incorrect I/O wait", cpu, e)
	}
	for _, data := range []string{"cpu0 1 2 3 -1", "cpu0 18446744073709551615 2 3 1", "cpu only aggregate", "cpuN 1 2 3 4"} {
		if _, e := parseMonitorCPU([]byte(data)); e == nil {
			t.Fatal("invalid CPU accepted", data)
		}
	}
	disk, e := parseMonitorDisk([]byte("8 0 sda 1 0 3 0 1 0 5 0 0 0 0\n"), time.Now())
	if e != nil || len(disk) != 1 || disk[0].read != 1536 || disk[0].write != 2560 {
		t.Fatal("disk sectors were not 512 bytes", disk, e)
	}
	for _, data := range []string{"short", "8 0 sda 1 0 18446744073709551615 0 1 0 5 0 0 0 0", "8 0 sda 1 0 -1 0 1 0 5 0 0 0 0"} {
		if _, e := parseMonitorDisk([]byte(data), time.Now()); e == nil {
			t.Fatal("invalid disk accepted", data)
		}
	}
	network, e := parseMonitorNetwork([]byte("Inter-| Receive | Transmit\n eth0: 10 0 0 0 0 0 0 0 20 0 0 0 0 0 0 0\n"), time.Now())
	if e != nil || len(network) != 1 || network[0].read != 10 || network[0].write != 20 {
		t.Fatal(network, e)
	}
	if _, e := parseMonitorNetwork([]byte("eth0: 0 broken"), time.Now()); e == nil {
		t.Fatal("invalid network row accepted")
	}
	if rows, e := parseMonitorNetwork([]byte(strings.Repeat("eth0: 10 0 0 0 0 0 0 0 20 0 0 0 0 0 0 0\n", monitorRows+1)), time.Now()); e == nil || len(rows) != monitorRows {
		t.Fatal("unbounded network rows")
	}
}
