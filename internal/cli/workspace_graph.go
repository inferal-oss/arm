package cli

import (
	"encoding/json"

	"github.com/yrashk/arm/internal/model"
	api "github.com/yrashk/arm/internal/workspaceapi"
)

func (a *app) workspaceGraphCommand(args []string) (int, error) {
	f := a.flags("workspaces graph")
	id := f.String("workspace", "", "exact local workspace ID")
	section := f.String("section", "nodes", "nodes, edges or paths (reachability only)")
	target := f.String("to", "", "exact destination ID for paths; saved paths allow pinned directory only")
	offset := f.Int("offset", 0, "offset within this graph hash")
	limit := f.Int("limit", 20, "maximum rows (1..40)")
	hash := f.String("hash", "", "exact observation hash for stable subsequent pages")
	f.Bool("json", false, "JSON output (automatic outside terminal)")
	if e := a.parse(f, args, false); e != nil {
		return 2, e
	}
	if *id == "" || len(f.Args()) != 0 || *offset < 0 || *limit < 1 || *limit > 40 || *section != "nodes" && *section != "edges" && *section != "paths" || *section == "paths" && *target == "" || *section != "paths" && *target != "" {
		return 2, model.Err("invalid_request", "--workspace required; section nodes/edges/paths; --to only and required for paths; offset>=0, limit1..40")
	}
	var out json.RawMessage
	if e := a.call("workspace_graph_read", api.GraphRead{Workspace: *id, Section: *section, Target: *target, Offset: *offset, Limit: *limit, Hash: *hash}, &out); e != nil {
		return 1, e
	}
	return 0, a.print(out)
}
