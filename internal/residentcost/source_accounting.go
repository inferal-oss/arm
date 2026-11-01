package residentcost

// OwnHeaderData describes one explicitly named compiled ARM struct. Bytes
// includes its fixed arrays and padding, never the pointees of its references.
type OwnHeaderData struct {
	Name  string
	Bytes uint64
}

// OwnHeaderReservationData reserves a deliberately coarse number of each
// listed header. The source assembly separately covers dynamic backings and
// records why its finite cardinalities fit this reservation. Native/runtime
// internals are absent here; they require explicitly named unknown allowances.
func OwnHeaderReservationData(groups [][]OwnHeaderData, copies uint64, ceiling uint64) (uint64, error) {
	if len(groups) == 0 || len(groups) > 16 || copies == 0 || copies > 4096 || ceiling == 0 {
		return 0, ErrInvalid
	}
	var total uint64
	for _, group := range groups {
		if len(group) == 0 || len(group) > 256 {
			return 0, ErrInvalid
		}
		prior := ""
		for _, header := range group {
			if !textData(header.Name, 128) || header.Name <= prior || header.Bytes > 1<<20 {
				return 0, ErrInvalid
			}
			prior = header.Name
			bytes, err := multiplyData(header.Bytes, copies)
			if err != nil {
				return 0, err
			}
			total, err = addData(total, bytes)
			if err != nil || total > ceiling {
				return 0, ErrInvalid
			}
		}
	}
	return total, nil
}

// Linux pending startup performs two initial CPU samples (each has before/
// after scopes), and two MemoryAvailability calls (Snapshot and sampleMemory).
// A CPU scope reads at most4096 thread memberships +3 leader/mount inputs;
// only ONE mount per cpu/v2 controller can pass the ambiguity check. Each of
// <=128 ancestors therefore reads at most3v1+2v2 files, not64mounts*128files.
// Memory reads two4096-thread inventories +5 leader/meminfo inputs +3*128
// ancestor files. Each readMonitorFile public call consumes at most1MiB+1.
// These are public bounded read-call bytes, not underlying syscall counts,
// actual CPU collection bytes or physical filesystem traffic.
func PendingLinuxObserverReadBoundsData() (calls, bytes uint64) {
	const cpuScope = 4096 + 3 + 128*(3+2)
	const cpu = 2 * (2*cpuScope + 1)
	const memory = 2 * (2*4096 + 5 + 3*128)
	return cpu + memory, (cpu + memory) * ((1 << 20) + 1)
}
