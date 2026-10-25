package manager

import "sort"

type snapshotStopControl struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	Returned bool   `json:"returned"`
	Unknown  bool   `json:"unknown"`
}

func (m *Manager) snapshotStopControls() []snapshotStopControl {
	out := []snapshotStopControl{}
	if m.snapshots != nil {
		for id, c := range m.snapshots.controls {
			out = append(out, snapshotStopControl{id, c.source, c.returned, c.unknown})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
