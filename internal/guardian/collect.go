package guardian

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"getarm.org/internal/usage"
)

func collect(ctx context.Context, inv *Inventory) {
	pids, err := usage.ListPIDs(maxProcesses)
	if err != nil {
		inv.Complete = false
		inv.Detail = err.Error()
		if pids == nil {
			return
		}
	}
	missing := 0
	for _, pid := range pids {
		if ctx.Err() != nil {
			inv.Complete = false
			inv.Detail = "inventory cancelled"
			return
		}
		s, err := usage.Inspect(pid)
		if err != nil {
			// Exited between enumeration and inspection.
			continue
		}
		p := Process{Identity: s.Identity, PPID: s.PPID, Name: s.Name, CPUSeconds: s.CPUSeconds, Memory: s.Footprint, Stopped: s.Stopped}
		if p.Memory == nil {
			p.Memory = s.Resident
		}
		if s.DiskRead != nil && s.DiskWrite != nil {
			v := *s.DiskRead + *s.DiskWrite
			p.DiskBytes = &v
		}
		if p.CPUSeconds == nil {
			missing++
		}
		p.Executable, p.Cgroup = native(pid)
		if p.Executable != "" {
			p.Name = filepath.Base(p.Executable)
		}
		p.Kind = classify(pid, p)
		inv.Processes = append(inv.Processes, p)
	}
	if missing > 0 {
		inv.Detail = strings.TrimPrefix(inv.Detail+fmt.Sprintf("; %d processes withheld usage counters (other users or restricted)", missing), "; ")
	}
}

// browser reports whether a process belongs to a Chromium-based web
// browser, whose page renderers can be shed without closing the browser.
// Electron applications are excluded: their renderers are the windows.
func browser(p Process) bool {
	names := []string{strings.ToLower(filepath.Base(p.Executable)), strings.ToLower(p.Name)}
	if b := appBundle(p.Executable); b != "" {
		names = append(names, strings.ToLower(strings.TrimSuffix(filepath.Base(b), ".app")))
	}
	for _, n := range names {
		switch n {
		case "google chrome", "google chrome beta", "google chrome canary", "google chrome for testing", "chromium", "brave browser", "microsoft edge", "vivaldi", "opera", "arc",
			"chrome", "chromium-browser", "brave", "brave-browser", "msedge", "microsoft-edge", "vivaldi-bin":
			return true
		}
	}
	return false
}

// chromiumType classifies a helper from its arguments. Extension
// processes are renderers too, but shedding them breaks the browser.
func chromiumType(cmdline []byte) string {
	kind := ""
	for _, arg := range bytes.Split(cmdline, []byte{0}) {
		if bytes.Equal(arg, []byte("--extension-process")) {
			return ""
		}
		if v, ok := bytes.CutPrefix(arg, []byte("--type=")); ok && string(v) == "renderer" {
			kind = "renderer"
		}
	}
	return kind
}
