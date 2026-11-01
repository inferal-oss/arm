package residentrefs

import (
	"os"
	"strings"
)

// Capture only bounded command selection at process initialization, before a
// library caller can substitute a synthetic CLI argument slice. Do not retain
// raw arguments, paths, keys or other command payload. This is an entry scope,
// never a resource grant, process-exit witness or native child census.
var workerLauncherInvocation = struct {
	selected bool
	pid      int
}{workerLauncherArgsData(os.Args), os.Getpid()}

func workerLauncherArgsData(args []string) bool {
	if len(args) < 2 || len(args) > 256 {
		return false
	}
	bytes := 0
	for _, arg := range args {
		if len(arg) > (64<<10)-bytes {
			return false
		}
		bytes += len(arg)
	}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			i++
			return i < len(args) && (args[i] == "start" || args[i] == "daemon" && i+1 < len(args) && args[i+1] == "start")
		}
		switch {
		case arg == "--home" || arg == "-home":
			i++
			if i >= len(args) || args[i] == "" {
				return false
			}
		case strings.HasPrefix(arg, "--home=") || strings.HasPrefix(arg, "-home="):
			if strings.HasSuffix(arg, "=") {
				return false
			}
		case arg == "--json" || arg == "-json" || arg == "--json=true" || arg == "--json=false" || arg == "-json=true" || arg == "-json=false":
		case arg == "start":
			return true
		case arg == "daemon":
			return i+1 < len(args) && args[i+1] == "start"
		default:
			return false
		}
	}
	return false
}

// The real main passes this exact view into its CLI application, then into the
// actual parent. A running core/worker/serve process cannot claim it from a
// request boolean or later CLI argument list. All entry kinds share exclusion.
type WorkerLauncherOriginal struct {
	self *WorkerLauncherOriginal
	pid  int
}

func ClaimOriginalWorkerLauncherForCurrentEntry() (*WorkerLauncherOriginal, error) {
	if !workerLauncherInvocation.selected {
		return nil, nil
	}
	pid := os.Getpid()
	processChildClaim.mu.Lock()
	defer processChildClaim.mu.Unlock()
	if pid != workerLauncherInvocation.pid || processChildClaim.attempted {
		return nil, ErrOriginal
	}
	processChildClaim.attempted = true // consume before allocation/abnormal unwind
	v := &WorkerLauncherOriginal{pid: pid}
	v.self = v
	processChildClaim.launcher = v
	return v, nil
}

func (v *WorkerLauncherOriginal) RequireOriginalStandaloneStart() error {
	if v == nil || v.self != v || !workerLauncherInvocation.selected || v.pid != workerLauncherInvocation.pid || os.Getpid() != v.pid {
		return ErrOriginal
	}
	processChildClaim.mu.Lock()
	defer processChildClaim.mu.Unlock()
	if !processChildClaim.attempted || processChildClaim.launcher != v || processChildClaim.original != nil {
		return ErrOriginal
	}
	return nil
}

func (*WorkerLauncherOriginal) MarshalJSON() ([]byte, error) { return nil, ErrOriginal }
func (*WorkerLauncherOriginal) UnmarshalJSON([]byte) error   { return ErrOriginal }
