package usage

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync/atomic"
	"time"

	"getarm.org/internal/model"
)

const CPUOwnerLimit = 32
const CPUProcessLimit = 256
const CPUCutBudget = 100 * time.Millisecond
const CPUUnitDarwinMach = "darwin-recount-mach"

// CPUOwner is supplied only from an original live supervised job. Cookie is
// captured while its launch gate is closed, before the original Wait can reap
// that process. Neither a saved PID nor a process group establishes ownership.
type CPUOwner struct {
	Token  string
	Root   model.ProcessIdentity
	Cookie string
}

type CPUProcessCounter struct {
	Owner                CPUOwner
	Identity             model.ProcessIdentity
	ParentPID            int
	Cookie, ParentCookie string
	User, System         uint64
	Unit                 string
}

type CPUCut struct {
	Started, Finished time.Time
	Method, Detail    string
	Members           []CPUProcessCounter
	state             *cpuCutState
}
type cpuCutState struct{ closed atomic.Bool }

// Close retires this cut's lifetime evidence. Darwin uses unique process IDs,
// not retained descriptors; future platforms may need native handle cleanup.
func (c *CPUCut) Close() error {
	if c.state != nil {
		c.state.closed.Store(true)
	}
	return nil
}

func CaptureCPUAnchor(root model.ProcessIdentity) (string, error) {
	if root.PID <= 0 || int64(root.PID) > math.MaxInt32 || root.PGID != root.PID || root.UID == "" || root.Start == "" || root.Namespace == "" {
		return "", fmt.Errorf("original gated process identity required")
	}
	return captureCPUAnchor(root)
}

// CollectCPU performs CPU-only native reads. Inventory can be partial: only
// members with a complete verified ancestry chain are retained, and later
// matching credits only members present in both contained cuts.
func CollectCPU(ctx context.Context, owners []CPUOwner) CPUCut {
	ctx, cancel := context.WithTimeout(ctx, CPUCutBudget)
	defer cancel()
	cut := CPUCut{Started: time.Now(), state: &cpuCutState{}}
	if len(owners) > CPUOwnerLimit {
		cut.Detail = "CPU owner limit exceeded"
		cut.Finished = time.Now()
		return cut
	}
	copyOwners := append([]CPUOwner(nil), owners...)
	sort.Slice(copyOwners, func(i, j int) bool { return copyOwners[i].Token < copyOwners[j].Token })
	tokens, groups := map[string]int{}, map[int]int{}
	for _, o := range copyOwners {
		tokens[o.Token]++
		groups[o.Root.PGID]++
	}
	selected := []CPUOwner{}
	for _, o := range copyOwners {
		if len(selected) == CPUOwnerLimit {
			cut.Detail = "CPU owner limit reached"
			break
		}
		if o.Token == "" || o.Cookie == "" || o.Root.PID <= 0 || int64(o.Root.PID) > math.MaxInt32 || o.Root.PGID != o.Root.PID || o.Root.UID == "" || o.Root.Start == "" || o.Root.Namespace == "" || tokens[o.Token] != 1 || groups[o.Root.PGID] != 1 {
			continue
		}
		selected = append(selected, o)
	}
	if len(selected) > 0 {
		collectCPU(ctx, &cut, selected)
	} else {
		cut.Detail = "No original live CPU owners supplied"
	}
	cut.Finished = time.Now()
	return cut
}

// cpuAncestry returns the exact native process chain, including start identity
// and parent unique IDs. An unrelated same-UID process joining the PGID does
// not acquire a chain to the original captured root.
func cpuAncestry(p CPUProcessCounter, members map[int]CPUProcessCounter) bool {
	for depth := 0; depth < CPUProcessLimit; depth++ {
		if p.Cookie == "" || p.Identity.UID != p.Owner.Root.UID || p.Identity.PGID != p.Owner.Root.PGID {
			return false
		}
		if p.Identity == p.Owner.Root && p.Cookie == p.Owner.Cookie {
			return true
		}
		parent, ok := members[p.ParentPID]
		if !ok || parent.Cookie != p.ParentCookie || parent.Owner != p.Owner {
			return false
		}
		p = parent
	}
	return false
}
func sameCPUAncestry(old, current CPUProcessCounter, before, after map[int]CPUProcessCounter) bool {
	for depth := 0; depth < CPUProcessLimit; depth++ {
		if old.Identity != current.Identity || old.Cookie != current.Cookie || old.ParentPID != current.ParentPID || old.ParentCookie != current.ParentCookie || old.Owner != current.Owner || current.Cookie == "" || current.Identity.UID != current.Owner.Root.UID || current.Identity.PGID != current.Owner.Root.PGID {
			return false
		}
		if current.Identity == current.Owner.Root && current.Cookie == current.Owner.Cookie {
			return true
		}
		a, okA := before[old.ParentPID]
		b, okB := after[current.ParentPID]
		if !okA || !okB || a.Cookie != old.ParentCookie || b.Cookie != current.ParentCookie || a.Owner != old.Owner || b.Owner != current.Owner {
			return false
		}
		old, current = a, b
	}
	return false
}

func retainCPUAncestry(cut *CPUCut, owner CPUOwner, records []CPUProcessCounter) {
	byPID := map[int]CPUProcessCounter{}
	for _, p := range records {
		if _, exists := byPID[p.Identity.PID]; exists {
			return
		}
		p.Owner = owner
		byPID[p.Identity.PID] = p
	}
	root, ok := byPID[owner.Root.PID]
	if !ok || root.Identity != owner.Root || root.Cookie != owner.Cookie {
		return
	}
	pids := make([]int, 0, len(byPID))
	for pid := range byPID {
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	for _, pid := range pids {
		if len(cut.Members) == CPUProcessLimit {
			cut.Detail = "CPU member limit reached"
			return
		}
		p := byPID[pid]
		if cpuAncestry(p, byPID) {
			cut.Members = append(cut.Members, p)
		}
	}
}

type CPUProcessDelta struct {
	BeforeUser, BeforeSystem, AfterUser, AfterSystem uint64
	Identity                                         model.ProcessIdentity
	Cookie, ParentCookie                             string
	ParentPID                                        int
	User, System                                     uint64
}
type CPUOwnerDelta struct {
	Owner            CPUOwner
	User, System     uint64
	MatchedProcesses int
	Members          []CPUProcessDelta
}

// ContainedCPUDeltas returns raw, identity-verified matched-member deltas. The
// caller must separately establish that these cuts lie inside its host-counter
// interval and that the native units have a proved accounting relationship.
func ContainedCPUDeltas(previous, current CPUCut) []CPUOwnerDelta {
	if previous.state == nil || current.state == nil || previous.state.closed.Load() || current.state.closed.Load() {
		return nil
	}
	return cpuDeltas(previous.Members, current.Members)
}
func cpuDeltas(before, after []CPUProcessCounter) []CPUOwnerDelta {
	oldByOwner, newByOwner := map[CPUOwner]map[int]CPUProcessCounter{}, map[CPUOwner]map[int]CPUProcessCounter{}
	seenOwners := map[string]CPUOwner{}
	seenProcesses := map[string]CPUOwner{}
	for _, v := range []struct {
		rows []CPUProcessCounter
		dst  map[CPUOwner]map[int]CPUProcessCounter
	}{{before, oldByOwner}, {after, newByOwner}} {
		for _, p := range v.rows {
			if owner, ok := seenOwners[p.Owner.Token]; ok && owner != p.Owner {
				return nil
			}
			seenOwners[p.Owner.Token] = p.Owner
			identityKey := fmt.Sprintf("%s:%d:%s", p.Identity.Namespace, p.Identity.PID, p.Cookie)
			if owner, ok := seenProcesses[identityKey]; ok && owner != p.Owner {
				return nil
			}
			seenProcesses[identityKey] = p.Owner
			if v.dst[p.Owner] == nil {
				v.dst[p.Owner] = map[int]CPUProcessCounter{}
			}
			if _, exists := v.dst[p.Owner][p.Identity.PID]; exists {
				return nil
			}
			v.dst[p.Owner][p.Identity.PID] = p
		}
	}
	result := []CPUOwnerDelta{}
	for owner, rows := range newByOwner {
		oldRows := oldByOwner[owner]
		delta := CPUOwnerDelta{Owner: owner}
		overflow := false
		pids := make([]int, 0, len(rows))
		for pid := range rows {
			pids = append(pids, pid)
		}
		sort.Ints(pids)
		for _, pid := range pids {
			p := rows[pid]
			old, ok := oldRows[pid]
			if !ok || p.Identity != old.Identity || p.Cookie != old.Cookie || p.Unit != CPUUnitDarwinMach || old.Unit != p.Unit || p.User < old.User || p.System < old.System {
				continue
			}
			if !sameCPUAncestry(old, p, oldRows, rows) {
				continue
			}
			u, s := p.User-old.User, p.System-old.System
			if u > math.MaxUint64-delta.User || s > math.MaxUint64-delta.System {
				overflow = true
				break
			}
			delta.User += u
			delta.System += s
			delta.MatchedProcesses++
			delta.Members = append(delta.Members, CPUProcessDelta{Identity: p.Identity, Cookie: p.Cookie, ParentCookie: p.ParentCookie, ParentPID: p.ParentPID, User: u, System: s, BeforeUser: old.User, BeforeSystem: old.System, AfterUser: p.User, AfterSystem: p.System})
		}
		if !overflow && delta.MatchedProcesses > 0 {
			result = append(result, delta)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Owner.Token < result[j].Owner.Token })
	return result
}
