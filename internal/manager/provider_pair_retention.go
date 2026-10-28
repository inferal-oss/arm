package manager

import (
	"fmt"
	"math"
	"time"

	"getarm.org/internal/providerpreparation"
)

// Unused pure retention kernel: proposals only, with no SQL, native effect or
// authority to release either lease. The future CAS publisher must retain its
// exact predecessor and publish a successor only after positive commit ACK.
type providerPairStopCause uint8

const (
	providerPairCancel providerPairStopCause = iota + 1
	providerPairExpired
	providerPairClose
	providerPairClockUnknown
	providerPairBindingUnknown
	providerPairPersistenceUnknown
)

func providerPairProgressValid(p providerpreparation.Progress) bool {
	if p.Schema != providerpreparation.SchemaVersion || !providerPairHexID(string(p.Pair)) || p.Generation != "1" {
		return false
	}
	if len(p.At) == 0 || len(p.At) > 40 {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, string(p.At))
	if err != nil || at.IsZero() || string(p.At) != at.UTC().Format(time.RFC3339Nano) {
		return false
	}
	switch p.Phase {
	case providerpreparation.Preparing:
		return p.Revision == "1" && p.Reason == "none"
	case providerpreparation.Revoked:
		return p.Revision == "2" && (p.Reason == "cancelled" || p.Reason == "expired" || p.Reason == "close")
	case providerpreparation.Unknown:
		return (p.Revision == "2" || p.Revision == "3") && (p.Reason == "clock" || p.Reason == "binding" || p.Reason == "persistence")
	default:
		return false
	}
}

func retainProviderPairProgress(current providerpreparation.Progress, cause providerPairStopCause, now time.Time) (providerpreparation.Progress, bool, error) {
	if !providerPairProgressValid(current) {
		return providerpreparation.Progress{}, false, fmt.Errorf("invalid retaining pair predecessor")
	}
	next := current
	switch cause {
	case providerPairCancel:
		next.Phase, next.Reason = providerpreparation.Revoked, "cancelled"
	case providerPairExpired:
		next.Phase, next.Reason = providerpreparation.Revoked, "expired"
	case providerPairClose:
		next.Phase, next.Reason = providerpreparation.Revoked, "close"
	case providerPairClockUnknown:
		next.Phase, next.Reason = providerpreparation.Unknown, "clock"
	case providerPairBindingUnknown:
		next.Phase, next.Reason = providerpreparation.Unknown, "binding"
	case providerPairPersistenceUnknown:
		next.Phase, next.Reason = providerpreparation.Unknown, "persistence"
	default:
		return providerpreparation.Progress{}, false, fmt.Errorf("unknown pair retention cause")
	}
	if current.Phase == providerpreparation.Unknown || current.Phase == next.Phase {
		return current, false, nil // no same-state write or extra event
	}
	if current.Phase == providerpreparation.Revoked && next.Phase != providerpreparation.Unknown {
		return current, false, nil
	}
	now = now.UTC()
	if now.IsZero() || now.Year() < 0 || now.Year() > 9999 {
		return providerpreparation.Progress{}, false, fmt.Errorf("bounded audit stamp required")
	}
	next.At = providerpreparation.Stamp(now.UTC().Format(time.RFC3339Nano))
	next.Revision = "2"
	if current.Phase == providerpreparation.Revoked {
		next.Revision = "3"
	}
	return next, true, nil
}

// Integer quantities are data, not proof that a journal/lease was decoded or
// admitted. RecordBytes is logical ledger overhead, never physical host RAM.
type providerPairQuantity struct{ cpuMillis, memoryBytes, diskBytes int64 }
type providerPairReadableCharge struct {
	id     string
	amount providerPairQuantity
}
type providerPairFloor struct {
	members     [2]providerPairReadableCharge
	total       providerPairQuantity
	recordBytes int64
	problem     error
}

// retainedProviderPairFloor counts two promises even with zero readable rows.
// Readable larger quantities replace each SQL identity componentwise, including
// terminal/missing-state members. Duplicate observations are maxima, not sums.
// This is a lower-bound helper, NOT complete accounting or available capacity:
// ancestor bindings, inventory completeness and coverage across pairs remain A2.
func retainedProviderPairFloor(ids [2]string, readable []providerPairReadableCharge) providerPairFloor {
	f := providerPairFloor{members: [2]providerPairReadableCharge{
		{id: ids[0], amount: providerPairQuantity{cpuMillis: providerpreparation.WorkerCPUMillis, memoryBytes: providerpreparation.WorkerMemoryBytes}},
		{id: ids[1], amount: providerPairQuantity{diskBytes: providerpreparation.TempDiskBytes}},
	}, recordBytes: providerpreparation.RecordReserveBytes}
	if !providerPairHexID(ids[0]) || !providerPairHexID(ids[1]) || ids[0] == ids[1] {
		f.problem = fmt.Errorf("pair SQL membership unavailable; no capacity may be derived")
		f.members[0].id, f.members[1].id = "", "" // no ordinary-row coverage claim
	}
	if len(readable) > providerpreparation.MaxMembers {
		f.problem = fmt.Errorf("readable pair inventory exceeds bound")
		readable = nil
	}
	promised := f.members
	var seen [2]bool
	for _, row := range readable {
		i := -1
		for j, member := range f.members {
			if member.id != "" && row.id == member.id {
				i = j
				break
			}
		}
		if i < 0 {
			f.problem = fmt.Errorf("readable SQL identity has no exact pair coverage")
			continue
		}
		seen[i] = true
		if row.amount != promised[i].amount {
			f.problem = fmt.Errorf("readable pair amount differs from the fixed promise")
		}
		original := &f.members[i].amount
		for _, component := range [][2]*int64{{&original.cpuMillis, &row.amount.cpuMillis}, {&original.memoryBytes, &row.amount.memoryBytes}, {&original.diskBytes, &row.amount.diskBytes}} {
			if *component[1] < 0 {
				f.problem = fmt.Errorf("malformed readable pair quantity")
			} else if *component[1] > *component[0] {
				*component[0] = *component[1]
			}
		}
	}
	if !seen[0] || !seen[1] {
		f.problem = fmt.Errorf("pair member absent or unreadable; both promises remain charged")
	}
	a, b := f.members[0].amount, f.members[1].amount
	for _, component := range [][3]*int64{{&f.total.cpuMillis, &a.cpuMillis, &b.cpuMillis}, {&f.total.memoryBytes, &a.memoryBytes, &b.memoryBytes}, {&f.total.diskBytes, &a.diskBytes, &b.diskBytes}} {
		if *component[1] > math.MaxInt64-*component[2] {
			*component[0] = math.MaxInt64
			f.problem = fmt.Errorf("pair quantity overflow; availability is unknown")
		} else {
			*component[0] = *component[1] + *component[2]
		}
	}
	return f
}
