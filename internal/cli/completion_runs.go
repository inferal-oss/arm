package cli

import (
	"context"
	"sort"
	"strings"

	"getarm.org/internal/client"
	"getarm.org/internal/readablename"
	"github.com/spf13/cobra"
)

func (a *app) completeRuns(cmd *cobra.Command, prefix string) []string {
	ctx, cancel := context.WithTimeout(a.ctx, completionDeadline)
	defer cancel()
	home := a.home
	if f := cmd.Root().Flags().Lookup("home"); f != nil {
		home = f.Value.String()
	}
	endpoint, err := readCompletionEndpoint(ctx, home)
	if err != nil {
		return nil
	}
	// Ignore commands, reasons, paths and all other private run metadata. The
	// existing authenticated inventory includes both leased and proxy runs.
	var runs []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if client.CallEndpoint(ctx, endpoint, "runs", nil, &runs) != nil || len(runs) > 4096 {
		return nil
	}
	ids := map[string]bool{}
	names := map[string]map[string]bool{}
	for _, run := range runs {
		if ctx.Err() != nil || !opaqueID(run.ID) {
			return nil
		}
		ids[run.ID] = true
		for _, name := range []string{run.Name, readablename.FromID("run", run.ID)} {
			// Execution treats every canonical-looking ID as an exact ID before
			// resolving names, even when that ID is absent from the inventory.
			if !completionToken(name) || opaqueID(name) {
				continue
			}
			if names[name] == nil {
				names[name] = map[string]bool{}
			}
			names[name][run.ID] = true
		}
	}
	var values []string
	for id := range ids {
		if strings.HasPrefix(id, prefix) {
			values = append(values, id+"\trun ID")
		}
	}
	for name, matches := range names {
		if len(matches) == 1 && strings.HasPrefix(name, prefix) {
			values = append(values, name+"\trun name")
		}
	}
	sort.Strings(values)
	if ctx.Err() != nil {
		return nil
	}
	// Resolve collisions over the entire bounded inventory before limiting the
	// output. A duplicate beyond the first page still makes a name ambiguous.
	if len(values) > 256 {
		values = values[:256]
	}
	return values
}
