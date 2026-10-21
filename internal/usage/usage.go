// Package usage observes a captured process group. It never grants process
// control authority and never reads command lines or process environments.
package usage

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/yrashk/arm/internal/model"
)

const MaxMembers = 256

type Snapshot struct {
	Started      time.Time
	Finished     time.Time
	Root         model.ProcessIdentity
	Verified     bool
	Complete     bool
	Method       string
	MemoryMetric string
	Detail       string
	Members      []model.ProcessUsage
}

func Capture(pid int) (model.ProcessIdentity, error) {
	if pid <= 0 || int64(pid) > math.MaxInt32 {
		return model.ProcessIdentity{}, fmt.Errorf("PID must be a positive signed 32-bit value")
	}
	return capture(pid)
}
func Collect(ctx context.Context, root model.ProcessIdentity) Snapshot {
	started := time.Now()
	s := Snapshot{Started: started, Root: root, Complete: true, Members: []model.ProcessUsage{}}
	if ctx.Err() != nil {
		s.Complete = false
		s.Detail = "collection cancelled"
		s.Finished = time.Now()
		return s
	}
	if root.PID <= 0 || int64(root.PID) > math.MaxInt32 || root.PGID != root.PID || root.UID == "" || root.Start == "" || root.Namespace == "" {
		s.Complete = false
		s.Detail = "a captured group leader identity is required"
		s.Finished = time.Now()
		return s
	}
	current, err := capture(root.PID)
	if err != nil || current != root {
		s.Complete = false
		s.Detail = "captured group leader identity is unavailable or changed"
		s.Finished = time.Now()
		return s
	}
	s.Verified = true
	collect(ctx, &s)
	current, err = capture(root.PID)
	if err != nil || current != root {
		s.Verified = false
		s.Complete = false
		s.Detail += "; group leader identity changed during collection"
	}
	s.Finished = time.Now()
	return s
}

func View(current Snapshot, previous *Snapshot) model.WorkloadUsage {
	v := model.WorkloadUsage{Observed: current.Finished.UTC(), Live: true, Root: current.Root, IdentityVerified: current.Verified, Complete: current.Complete, Method: current.Method, MemoryMetric: current.MemoryMetric, Scope: "captured live process-group members; excludes escaped and already exited processes", Detail: current.Detail, Members: current.Members, MemberCount: len(current.Members), CPUQuality: "unavailable", MemoryQuality: "unavailable"}
	if !current.Verified {
		return v
	}
	var memory uint64
	memoryKnown := len(current.Members) > 0
	for _, p := range current.Members {
		if p.AccountedMemoryBytes == nil {
			memoryKnown = false
			continue
		}
		if *p.AccountedMemoryBytes > math.MaxUint64-memory {
			memoryKnown = false
			v.Detail += "; memory sum overflow"
			break
		}
		memory += *p.AccountedMemoryBytes
	}
	if memoryKnown {
		v.AccountedMemoryBytes = &memory
		v.MemoryQuality = "reported-sum"
		if !current.Complete {
			v.MemoryQuality = "partial-reported-sum"
		}
	}
	if !current.Complete {
		v.Detail += "; partial sample: unavailable or raced metadata is not treated as zero"
	}
	if previous == nil || !previous.Verified || previous.Root != current.Root {
		v.Detail += "; CPU requires two observations of the same captured group"
		return v
	}
	window := current.Finished.Sub(previous.Started)
	if window <= 0 {
		v.Detail += "; observation clock did not advance"
		return v
	}
	v.Window = window
	before := map[model.ProcessIdentity]model.ProcessUsage{}
	for _, p := range previous.Members {
		before[p.Identity] = p
	}
	delta := float64(0)
	matched := false
	for _, p := range current.Members {
		old, ok := before[p.Identity]
		if !ok || p.CPUSeconds == nil || old.CPUSeconds == nil {
			continue
		}
		if *p.CPUSeconds < *old.CPUSeconds {
			v.Detail += "; CPU counter regressed"
			continue
		}
		delta += *p.CPUSeconds - *old.CPUSeconds
		matched = true
	}
	if matched && !math.IsNaN(delta) && !math.IsInf(delta, 0) {
		cores := delta / window.Seconds()
		v.CPUCores = &cores
		v.CPUQuality = "lower-bound-overlap-average"
		v.Detail += "; CPU is a lower-bound interval average from members present in both observations"
	}
	return v
}
