package workspacegraph

import (
	"encoding/json"
	"fmt"
	"reflect"
)

const MaxPageBytes = 48 << 10
const MaxPages = 32
const MaxPageStreamBytes = 1 << 20

// Page's embedded Snapshot carries the complete header/hash but only the
// explicitly positioned slices. It is NOT a valid standalone Snapshot and
// cannot activate a cache until exact complete assembly and independent scope
// validation succeed. All arrays are present, never null.
type Page struct {
	NodeOffset int      `json:"node_offset"`
	EdgeOffset int      `json:"edge_offset"`
	NodeTotal  int      `json:"node_total"`
	EdgeTotal  int      `json:"edge_total"`
	Snapshot   Snapshot `json:"snapshot"`
}

func header(s Snapshot) Snapshot { s.Nodes = nil; s.Edges = nil; return s }
func (p Page) Validate() error {
	if p.NodeTotal < 1 || p.NodeTotal > MaxNodes || p.EdgeTotal < 0 || p.EdgeTotal > MaxEdges || p.NodeOffset < 0 || p.EdgeOffset < 0 || p.NodeOffset > p.NodeTotal || p.EdgeOffset > p.EdgeTotal || len(p.Snapshot.Nodes) > p.NodeTotal-p.NodeOffset || len(p.Snapshot.Edges) > p.EdgeTotal-p.EdgeOffset || p.Snapshot.Nodes == nil || p.Snapshot.Edges == nil || len(p.Snapshot.Nodes)+len(p.Snapshot.Edges) < 1 || len(p.Snapshot.Edges) > 0 && p.NodeOffset+len(p.Snapshot.Nodes) != p.NodeTotal {
		return fmt.Errorf("invalid graph page totals/offsets/order")
	}
	raw, e := json.Marshal(p)
	if e != nil || len(raw) > MaxPageBytes {
		return fmt.Errorf("complete graph page exceeds bound")
	}
	return nil
}
func Pages(s Snapshot, maxBytes int) ([]Page, error) {
	if e := s.ValidateStructure(); e != nil {
		return nil, e
	}
	if maxBytes < 2048 || maxBytes > MaxPageBytes {
		return nil, fmt.Errorf("graph page bound2048..48KiB required")
	}
	pages := []Page{}
	ni, ei, total := 0, 0, 0
	for ni < len(s.Nodes) || ei < len(s.Edges) {
		part := s
		part.Nodes = []Node{}
		part.Edges = []Edge{}
		p := Page{NodeOffset: ni, EdgeOffset: ei, NodeTotal: len(s.Nodes), EdgeTotal: len(s.Edges), Snapshot: part}
		for ni < len(s.Nodes) || ei < len(s.Edges) {
			copy := p
			if ni < len(s.Nodes) {
				copy.Snapshot.Nodes = append(append([]Node{}, p.Snapshot.Nodes...), s.Nodes[ni])
			} else {
				copy.Snapshot.Edges = append(append([]Edge{}, p.Snapshot.Edges...), s.Edges[ei])
			}
			raw, _ := json.Marshal(copy)
			if len(raw) > maxBytes {
				break
			}
			p = copy
			if ni < len(s.Nodes) {
				ni++
			} else {
				ei++
			}
		}
		if len(p.Snapshot.Nodes)+len(p.Snapshot.Edges) == 0 {
			return nil, fmt.Errorf("single complete graph entry exceeds page bound")
		}
		raw, _ := json.Marshal(p)
		total += len(raw) + 4
		if len(pages) >= MaxPages || total > MaxPageStreamBytes {
			return nil, fmt.Errorf("complete graph page stream exceeds bound; nothing truncated")
		}
		pages = append(pages, p)
	}
	return pages, nil
}
func Assemble(pages []Page) (Snapshot, error) {
	if len(pages) < 1 || len(pages) > MaxPages {
		return Snapshot{}, fmt.Errorf("complete bounded graph pages required")
	}
	out := pages[0].Snapshot
	out.Nodes = []Node{}
	out.Edges = []Edge{}
	expected := header(out)
	nodeTotal, edgeTotal := pages[0].NodeTotal, pages[0].EdgeTotal
	total := 0
	for _, p := range pages {
		if e := p.Validate(); e != nil {
			return Snapshot{}, e
		}
		if p.NodeOffset != len(out.Nodes) || p.EdgeOffset != len(out.Edges) || p.NodeTotal != nodeTotal || p.EdgeTotal != edgeTotal || !reflect.DeepEqual(header(p.Snapshot), expected) {
			return Snapshot{}, fmt.Errorf("graph pages do not form one exact ordered snapshot")
		}
		raw, _ := json.Marshal(p)
		total += len(raw) + 4
		if total > MaxPageStreamBytes {
			return Snapshot{}, fmt.Errorf("complete graph pages exceed stream bound")
		}
		out.Nodes = append(out.Nodes, p.Snapshot.Nodes...)
		out.Edges = append(out.Edges, p.Snapshot.Edges...)
	}
	if len(out.Nodes) != nodeTotal || len(out.Edges) != edgeTotal {
		return Snapshot{}, fmt.Errorf("graph snapshot is partial; no reachability cache activated")
	}
	if e := out.ValidateStructure(); e != nil {
		return Snapshot{}, e
	}
	return out, nil
}
