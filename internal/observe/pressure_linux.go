package observe

import (
	"context"
	"fmt"
	"strings"
)

// linuxPressure reads host PSI and the load average. Kernels without PSI,
// or with it disabled, report PSI as unavailable rather than zero.
func linuxPressure(ctx context.Context) pressureRaw {
	p := pressureRaw{loadMethod: "proc:/proc/loadavg", psi: map[string]psiLine{}}
	if data, err := readMonitorFile(ctx, "/proc/loadavg"); err != nil {
		p.loadErr = err
	} else {
		p.load, p.loadErr = parseLoadavg(data)
	}
	for _, kind := range []string{"cpu", "memory", "io"} {
		data, err := readMonitorFile(ctx, "/proc/pressure/"+kind)
		if err == nil {
			err = parsePSI(kind, data, p.psi)
		}
		if err != nil {
			p.psiErr = err
			p.psi = nil
			break
		}
	}
	return p
}

func parseLoadavg(data []byte) ([3]float64, error) {
	var load [3]float64
	f := strings.Fields(string(data))
	if len(f) < 3 {
		return load, fmt.Errorf("invalid loadavg")
	}
	for i := range load {
		if _, err := fmt.Sscanf(f[i], "%g", &load[i]); err != nil || load[i] < 0 {
			return load, fmt.Errorf("invalid loadavg value")
		}
	}
	return load, nil
}
