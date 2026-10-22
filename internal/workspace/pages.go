package workspace

import (
	"encoding/json"
	"fmt"
)

const MaxPageBytes = 48 << 10

type Page struct {
	Snapshot Snapshot `json:"snapshot"`
	Offset   int      `json:"offset"`
	Total    int      `json:"total"`
}

// Pages produces bounded complete-envelope pages; the snapshot digest is of the
// complete view and does not change per page. This is not an authorization token.
func Pages(snapshot Snapshot, maxBytes int) ([]Page, error) {
	if e := validateSnapshot(snapshot); e != nil {
		return nil, e
	}
	if maxBytes < 1024 || maxBytes > MaxPageBytes {
		return nil, fmt.Errorf("invalid workspace page byte limit")
	}
	out := []Page{}
	for offset := 0; offset < len(snapshot.Members); {
		p := Page{Snapshot: snapshot, Offset: offset, Total: len(snapshot.Members)}
		p.Snapshot.Members = []MemberCard{}
		for offset+len(p.Snapshot.Members) < len(snapshot.Members) {
			c := snapshot.Members[offset+len(p.Snapshot.Members)]
			p.Snapshot.Members = append(p.Snapshot.Members, c)
			raw, e := json.Marshal(p)
			if e != nil {
				return nil, e
			}
			if len(raw) > maxBytes {
				p.Snapshot.Members = p.Snapshot.Members[:len(p.Snapshot.Members)-1]
				break
			}
		}
		if len(p.Snapshot.Members) == 0 {
			return nil, problem("response_too_large", "one membership card cannot fit complete page envelope")
		}
		out = append(out, p)
		offset += len(p.Snapshot.Members)
	}
	return out, nil
}
func Assemble(pages []Page) (Snapshot, error) {
	if len(pages) < 1 || len(pages) > MaxMembers {
		return Snapshot{}, problem("invalid_snapshot", "bounded complete pages required")
	}
	out := pages[0].Snapshot
	out.Members = []MemberCard{}
	header := pages[0].Snapshot
	header.Members = []MemberCard{}
	total := pages[0].Total
	if total < 1 || total > MaxMembers {
		return Snapshot{}, problem("invalid_snapshot", "page total invalid")
	}
	for _, p := range pages {
		raw, e := json.Marshal(p)
		if e != nil || len(raw) > MaxPageBytes || p.Offset != len(out.Members) || p.Total != total || len(p.Snapshot.Members) == 0 {
			return Snapshot{}, problem("invalid_snapshot", "page ordering/size/total mismatch")
		}
		h := p.Snapshot
		h.Members = []MemberCard{}
		if hash(h) != hash(header) {
			return Snapshot{}, problem("changed_revision", "directory view changed between pages")
		}
		out.Members = append(out.Members, p.Snapshot.Members...)
		if len(out.Members) > total {
			return Snapshot{}, problem("invalid_snapshot", "too many paged members")
		}
	}
	if len(out.Members) != total {
		return Snapshot{}, problem("invalid_snapshot", "incomplete membership view")
	}
	if e := validateSnapshot(out); e != nil {
		return Snapshot{}, e
	}
	return out, nil
}
