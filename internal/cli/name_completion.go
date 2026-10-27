package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"getarm.org/internal/client"
	"getarm.org/internal/customnames"
	"getarm.org/internal/local"
	"getarm.org/internal/readablename"
	"github.com/spf13/cobra"
)

func nameKinds(prefix string) []string {
	var values []string
	for _, kind := range []string{"host", "lease", "pool", "resource"} {
		if strings.HasPrefix(kind, prefix) {
			values = append(values, kind)
		}
	}
	return values
}

// Always local, within the caller's single completion deadline. Inventory was
// already authorized independently; metadata can only add labels to its IDs.
func completionCustomCandidates(ctx context.Context, endpoint local.Endpoint, kind, authority string, visible []readablename.Candidate, bindings map[string]string) ([]readablename.Candidate, error) {
	var result nameSnapshot
	diagnostic, err := client.CallEndpointWithDiagnostics(ctx, endpoint, "name_list", nameListRequest{Kind: kind, AuthorityHost: authority}, &result)
	if err != nil {
		return nil, err
	}
	if diagnostic != nil || result.NamingDiagnostic != nil {
		return nil, fmt.Errorf("optional names unavailable")
	}
	if kind != "host" && authority == "" {
		authority = result.Host
	}
	return joinCustomCandidates(kind, authority, visible, bindings, result)
}

// Clear completes retained exact bindings, including dangling/inactive names,
// without probing or refreshing the represented object. It never suggests an
// alias where clear requires the exact original ID or authority.
func (a *app) completeNameReservations(cmd *cobra.Command, args []string, prefix string, hostFlag bool) []string {
	ctx, cancel := context.WithTimeout(a.ctx, completionDeadline)
	defer cancel()
	endpoint, err := readCompletionEndpoint(ctx, a.home)
	if err != nil {
		return nil
	}
	query := nameListRequest{All: true}
	if len(args) > 0 && nameKind(args[0]) {
		query.Kind = args[0]
	}
	if !hostFlag {
		host := cmd.Flags().Lookup("host")
		if host == nil || !opaqueID(host.Value.String()) {
			return nil
		}
		query.AuthorityHost = host.Value.String()
	}
	var result nameSnapshot
	diagnostic, err := client.CallEndpointWithDiagnostics(ctx, endpoint, "name_list", query, &result)
	if err != nil || diagnostic != nil || result.NamingDiagnostic != nil || result.Version != 1 || result.Revision < 0 || len(result.Entries) > customnames.MaxEntries {
		return nil
	}
	seen := map[string]bool{}
	for _, entry := range result.Entries {
		if ctx.Err() != nil || customnames.ValidateBinding(entry) != nil || customnames.ValidateName(entry.Name) != nil {
			return nil
		}
		if query.Kind != "" && entry.Kind != query.Kind || query.AuthorityHost != "" && entry.AuthorityHost != query.AuthorityHost {
			continue
		}
		value := ""
		switch {
		case hostFlag:
			value = entry.AuthorityHost
		case len(args) == 1:
			value = entry.ID
		case len(args) == 2 && args[1] == entry.ID:
			value = entry.Name
		}
		if (completionToken(value) || (hostFlag || len(args) == 1) && completionIDToken(value)) && strings.HasPrefix(value, prefix) {
			seen[value] = true
		}
	}
	var values []string
	for value := range seen {
		values = append(values, value)
	}
	sort.Strings(values)
	if ctx.Err() != nil {
		return nil
	}
	if len(values) > 256 {
		values = values[:256]
	}
	return values
}
