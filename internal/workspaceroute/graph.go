package workspaceroute

import (
	"encoding/json"
	"errors"
	"io"

	"github.com/inferal-oss/arm/internal/routing"
	"github.com/inferal-oss/arm/internal/workspace"
	"github.com/inferal-oss/arm/internal/workspacegraph"
)

// This fixed conservative reserve covers bounded outer control, both pinned
// one-certificate TLS handshakes, record framing and TLS record overhead. The
// actual full request+framed response is ALSO measured, not guessed/truncated.
const GraphTransportReserve = 96 << 10

func DirectoryReplies(q Request, b routing.GraphBundle) ([]Reply, error) {
	if q.Operation != OperationRefresh || q.Report == nil || q.Refresh == nil {
		return nil, errors.New("exact graph refresh request required")
	}
	members, e := workspace.Pages(b.Membership, workspace.MaxPageBytes-2048)
	if e != nil {
		return nil, e
	}
	graphs, e := workspacegraph.Pages(b.Snapshot, workspacegraph.MaxPageBytes-2048)
	if e != nil {
		return nil, e
	}
	raw, e := json.Marshal(q)
	if e != nil {
		return nil, e
	}
	total := GraphTransportReserve + len(raw) + 4
	out := make([]Reply, 0, len(members)+len(graphs))
	appendReply := func(r Reply) error {
		if e := r.Validate(q); e != nil {
			return e
		}
		raw, e := json.Marshal(r)
		if e != nil {
			return e
		}
		if len(raw) > MaxRequestBytes {
			return errors.New("complete graph reply exceeds record bound")
		}
		total += len(raw) + 4
		if total > MaxStreamBytes {
			return errors.New("complete framed membership+graph+request+transport reserve exceeds1MiB; nothing truncated")
		}
		out = append(out, r)
		return nil
	}
	for _, p := range members {
		r := q.Response()
		r.Page = &p
		if e = appendReply(r); e != nil {
			return nil, e
		}
	}
	for _, p := range graphs {
		r := q.Response()
		r.GraphPage = &p
		if e = appendReply(r); e != nil {
			return nil, e
		}
	}
	return out, nil
}

// ReadDirectoryBundle enforces the ACTUAL framed byte total, including raw JSON
// whitespace, through a limited reader. Membership must finish before topology;
// missing/repeated/out-of-order pages never yield a partially usable bundle.
func ReadDirectoryBundle(reader io.Reader, q Request) (routing.GraphBundle, error) {
	raw, e := json.Marshal(q)
	if e != nil {
		return routing.GraphBundle{}, e
	}
	budget := MaxStreamBytes - GraphTransportReserve - len(raw) - 4
	if budget < 1 || q.Refresh == nil || q.Report == nil {
		return routing.GraphBundle{}, errors.New("invalid complete graph stream budget/request")
	}
	r := &io.LimitedReader{R: reader, N: int64(budget)}
	members := []workspace.Page{}
	graphs := []workspacegraph.Page{}
	count := 0
	for {
		reply, e := ReadReply(r, q)
		if e != nil {
			return routing.GraphBundle{}, e
		}
		if reply.Failure != nil {
			return routing.GraphBundle{}, errors.New(reply.Failure.Code + ": " + reply.Failure.Message)
		}
		if reply.Page == nil || reply.Page.Offset != count || len(members) >= workspace.MaxMembers {
			return routing.GraphBundle{}, errors.New("complete ordered membership pages required before graph")
		}
		members = append(members, *reply.Page)
		count += len(reply.Page.Snapshot.Members)
		if count == reply.Page.Total {
			break
		}
	}
	membership, e := workspace.Assemble(members)
	if e != nil {
		return routing.GraphBundle{}, e
	}
	nodes, edges := 0, 0
	for {
		reply, e := ReadReply(r, q)
		if e != nil {
			return routing.GraphBundle{}, e
		}
		if reply.Failure != nil {
			return routing.GraphBundle{}, errors.New(reply.Failure.Code + ": " + reply.Failure.Message)
		}
		p := reply.GraphPage
		if p == nil || len(graphs) >= workspacegraph.MaxPages || p.NodeOffset != nodes || p.EdgeOffset != edges {
			return routing.GraphBundle{}, errors.New("complete ordered graph pages required")
		}
		graphs = append(graphs, *p)
		nodes += len(p.Snapshot.Nodes)
		edges += len(p.Snapshot.Edges)
		if nodes == p.NodeTotal && edges == p.EdgeTotal {
			break
		}
	}
	graph, e := workspacegraph.Assemble(graphs)
	if e != nil {
		return routing.GraphBundle{}, e
	}
	// Shape alone never confers authority: Runtime.AcceptGraph subsequently
	// validates the pinned directory and original process-bound pending request.
	if e = graph.ValidateFor(routing.GraphScope(membership, 1)); e != nil {
		return routing.GraphBundle{}, e
	}
	return routing.GraphBundle{Membership: membership, Snapshot: graph}, nil
}
