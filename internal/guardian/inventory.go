// Package guardian observes host-wide process consumption and, when an
// operator enables it, acts on unmanaged processes under host pressure. It is
// separate from lease policy: observation never grants process authority,
// and every action revalidates the target's identity first.
package guardian

import (
	"context"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"getarm.org/internal/model"
)

const maxProcesses = 8192

// Process is one inventory row. Counters are cumulative so two inventories
// give interval rates; a missing counter is unknown, not zero.
type Process struct {
	Identity   model.ProcessIdentity
	PPID       int
	Name       string
	Executable string
	Cgroup     string // Linux unified cgroup path
	CPUSeconds *float64
	Memory     *uint64 // footprint (Darwin) or resident (Linux) bytes
	DiskBytes  *uint64 // cumulative read+write bytes
	Kind       string  // profile role, such as "renderer"
	Stopped    bool    // already stopped by its owner or a debugger
}

type Inventory struct {
	At        time.Time
	Processes []Process
	Complete  bool
	Detail    string
}

// Collect enumerates host processes through bounded native interfaces. It
// reads names, executable paths and cgroups, not environments; command lines
// are read only to classify members of a known profile.
func Collect(ctx context.Context) Inventory {
	inv := Inventory{At: time.Now(), Complete: true}
	collect(ctx, &inv)
	return inv
}

// Usage is the interval estimate for one process between two inventories.
type Usage struct {
	Process
	CPUCores  *float64
	DiskRate  *float64
	Available bool
}

// Rates matches processes by full identity between inventories. New
// processes have unknown rates; PID reuse never inherits counters.
func Rates(current, previous Inventory) []Usage {
	prior := make(map[model.ProcessIdentity]Process, len(previous.Processes))
	for _, p := range previous.Processes {
		prior[p.Identity] = p
	}
	dt := current.At.Sub(previous.At).Seconds()
	out := make([]Usage, 0, len(current.Processes))
	for _, p := range current.Processes {
		u := Usage{Process: p}
		if old, ok := prior[p.Identity]; ok && dt > 0 {
			if p.CPUSeconds != nil && old.CPUSeconds != nil && *p.CPUSeconds >= *old.CPUSeconds {
				v := (*p.CPUSeconds - *old.CPUSeconds) / dt
				u.CPUCores = &v
			}
			if p.DiskBytes != nil && old.DiskBytes != nil && *p.DiskBytes >= *old.DiskBytes {
				v := float64(*p.DiskBytes-*old.DiskBytes) / dt
				u.DiskRate = &v
			}
			u.Available = true
		}
		out = append(out, u)
	}
	return out
}

// Group is a set of processes a person recognizes as one program.
type Group struct {
	ID, Kind string
	Name     string
	UID      string
	Members  []Usage
	CPU      float64 // sum of known member rates
	Memory   uint64
	Disk     float64
	// Known reports whether any member has interval CPU evidence.
	Known bool
}

// groupOf keys a process group by its leader's full identity when the
// leader is alive, so a reused PGID never inherits an earlier group's state.
func groupOf(p Process, leaders map[string]string) (id, name, kind string) {
	if bundle := appBundle(p.Executable); bundle != "" {
		return "app:" + p.Identity.UID + ":" + bundle, strings.TrimSuffix(filepath.Base(bundle), ".app"), "app-bundle"
	}
	if unit := cgroupUnit(p.Cgroup); unit != "" {
		return "unit:" + p.Identity.UID + ":" + p.Cgroup, unit, "service-unit"
	}
	pg := p.Identity.Namespace + ":" + strconv.Itoa(p.Identity.PGID)
	return "pgid:" + pg + ":" + leaders[pg], "", "process-group"
}

// appBundle returns the outermost macOS application bundle containing path.
func appBundle(path string) string {
	if i := strings.Index(path, ".app/"); i > 0 {
		return path[:i+4]
	}
	return ""
}

// cgroupUnit names a dedicated systemd scope or service. Login sessions and
// the per-user manager contain unrelated programs and do not form a group.
func cgroupUnit(path string) string {
	if path == "" {
		return ""
	}
	leaf := filepath.Base(path)
	lower := strings.ToLower(leaf)
	if !(strings.HasSuffix(leaf, ".scope") || strings.HasSuffix(leaf, ".service")) {
		return ""
	}
	// Terminal tab and pane scopes hold the operator's shell with its jobs;
	// process groups separate those safely.
	if strings.HasPrefix(leaf, "session-") || strings.HasPrefix(leaf, "user@") || leaf == "init.scope" || strings.HasPrefix(lower, "vte-spawn-") || strings.HasPrefix(lower, "tmux-spawn-") || terminalScope(lower) {
		return ""
	}
	name := strings.TrimSuffix(strings.TrimSuffix(leaf, ".scope"), ".service")
	// app-gnome-firefox-1234 / app-org.gnome.Terminal-5678 style scopes.
	name = strings.TrimPrefix(name, "app-")
	if i := strings.LastIndex(name, "-"); i > 0 {
		if _, err := strconv.Atoi(name[i+1:]); err == nil {
			name = name[:i]
		}
	}
	name = strings.ReplaceAll(name, `\x2d`, "-")
	return name
}

// terminalScope recognizes systemd scopes of terminal emulators, which
// contain the operator's shells.
func terminalScope(leaf string) bool {
	for _, t := range []string{"terminal", "konsole", "xterm", "urxvt", "rxvt", "foot", "kitty", "alacritty", "wezterm", "tilix", "terminator", "ghostty", "guake", "yakuake"} {
		if strings.Contains(leaf, t) {
			return true
		}
	}
	return false
}

// Groups aggregates process rates. Within a process group the leader names
// the group; otherwise the busiest member does.
func Groups(rows []Usage) []*Group {
	byID := map[string]*Group{}
	leaders, starts := map[string]string{}, map[string]string{}
	for _, u := range rows {
		if u.Identity.PID == u.Identity.PGID {
			starts[u.Identity.Namespace+":"+strconv.Itoa(u.Identity.PGID)] = u.Identity.Start
		}
	}
	for _, u := range rows {
		id, name, kind := groupOf(u.Process, starts)
		g := byID[id]
		if g == nil {
			g = &Group{ID: id, Kind: kind, Name: name, UID: u.Identity.UID}
			byID[id] = g
		}
		g.Members = append(g.Members, u)
		if u.CPUCores != nil {
			g.CPU += *u.CPUCores
			g.Known = true
		}
		if u.Memory != nil {
			g.Memory += *u.Memory
		}
		if u.DiskRate != nil {
			g.Disk += *u.DiskRate
		}
		if kind == "process-group" && u.Identity.PID == u.Identity.PGID {
			leaders[id] = u.Name
		}
	}
	out := make([]*Group, 0, len(byID))
	for id, g := range byID {
		sort.Slice(g.Members, func(i, j int) bool { return rate(g.Members[i].CPUCores) > rate(g.Members[j].CPUCores) })
		if g.Name == "" {
			g.Name = leaders[id]
		}
		if g.Name == "" && len(g.Members) > 0 {
			g.Name = g.Members[0].Name
		}
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func rate(v *float64) float64 {
	if v == nil {
		return -1
	}
	return *v
}
