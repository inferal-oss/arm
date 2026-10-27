package cli

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"getarm.org/internal/client"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/readablename"
	"github.com/spf13/cobra"
)

const completionDeadline = 250 * time.Millisecond

func (a *app) registerCompletion(cmd *cobra.Command, name string) {
	kind := ""
	switch name {
	case "host":
		kind = "host"
	case "pool", "parent":
		if name == "pool" || strings.HasPrefix(cmd.CommandPath(), "arm pool ") {
			kind = "pool"
		}
	case "lease":
		kind = "lease"
	case "storage", "port-lease", "service":
		if cmd.CommandPath() == "arm run" {
			kind = "lease"
		}
	}
	_ = cmd.RegisterFlagCompletionFunc(name, func(c *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
		if kind == "" {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		values := a.completeObjects(c, kind, prefix)
		if name == "pool" {
			if strings.Contains(prefix, "=") {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			for i, value := range values {
				token, description, _ := strings.Cut(value, "\t")
				values[i] = token + "=\t" + description
			}
			return values, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
		}
		return values, cobra.ShellCompDirectiveNoFileComp
	})
}

func (a *app) completeArguments(cmd *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
	path := strings.TrimPrefix(cmd.CommandPath(), "arm ")
	kind := ""
	if len(args) == 0 {
		switch path {
		case "inspect", "release", "cancel", "reconcile", "renew", "usage", "storage cleanup", "storage handoff", "storage export", "storage retain", "reviews policy", "reviews configure":
			kind = "lease"
		case "pool inspect", "pool history", "pool set", "pool adjust", "pool rename", "pool describe", "pool drain", "pool resume", "pool delete", "pool children", "pool consumers":
			kind = "pool"
		case "ssh", "ssh lookup", "ssh seal", "mesh send", "mesh message-lookup", "mesh disconnect", "mesh reconnect", "mesh stop", "mesh uninstall", "mesh revoke", "mesh call", "mesh contacts":
			kind = "host"
		case "peers":
			return []string{"status", "on", "off"}, cobra.ShellCompDirectiveNoFileComp
		}
	}
	if kind == "" {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return a.completeObjects(cmd, kind, prefix), cobra.ShellCompDirectiveNoFileComp
}

// Only public identifier tokens and fixed descriptions enter the shell
// protocol. No reasons, paths, credentials, owner names or arbitrary labels are
// interpolated into scripts. Unusual user names remain accessible by exact ID.
func completionToken(s string) bool {
	if len(s) == 0 || len(s) > 128 || strings.HasPrefix(s, "-") {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._:-", c)) {
			return false
		}
	}
	return true
}

func (a *app) completeObjects(cmd *cobra.Command, kind, prefix string) []string {
	ctx, cancel := context.WithTimeout(a.ctx, completionDeadline)
	defer cancel()
	home := a.home
	if f := cmd.Root().Flags().Lookup("home"); f != nil {
		home = f.Value.String()
	}
	host := "localhost"
	if f := cmd.Flags().Lookup("host"); f != nil && f.Value.String() != "" {
		host = f.Value.String()
	}
	endpoint, err := readCompletionEndpoint(ctx, home)
	if err != nil {
		return nil
	}
	if kind != "host" && host != "localhost" {
		var registry model.NodeRegistry
		if client.CallEndpoint(ctx, endpoint, "node_list", nil, &registry) != nil || registry.Error != nil {
			return nil
		}
		var candidates []readablename.Candidate
		for _, node := range registry.Nodes {
			candidates = append(candidates, readablename.Candidate{ID: node.Host, Name: node.Name})
		}
		resolved, err := readablename.Resolve(readablename.Host, host, candidates, true)
		if err != nil {
			return nil
		}
		for _, node := range registry.Nodes {
			if node.Host != resolved {
				continue
			}
			if node.Local {
				host = "localhost"
			} else if node.Trust.State == "active" {
				host = node.Host
			} else {
				return nil
			}
			break
		}
	}
	call := func(op string, params, result any) error {
		if kind != "host" && host != "localhost" {
			raw, _ := json.Marshal(params)
			return client.CallEndpoint(ctx, endpoint, "mesh_dispatch", struct {
				Host    string
				Request mesh.Request
			}{host, mesh.Request{Operation: op, Params: raw}}, result)
		}
		return client.CallEndpoint(ctx, endpoint, op, params, result)
	}
	ids, allIDs, names, allowedNames := map[string]bool{}, map[string]bool{}, map[string]map[string]bool{}, map[string]bool{}
	add := func(id, name string, eligible bool) {
		allIDs[id] = true
		if eligible && completionToken(id) {
			ids[id] = true
		}
		for _, alias := range []string{name, readablename.FromID(kind, id)} {
			if completionToken(alias) && alias != id {
				if names[alias] == nil {
					names[alias] = map[string]bool{}
				}
				names[alias][id] = true
				if eligible {
					allowedNames[alias] = true
				}
			}
		}
	}
	switch kind {
	case "host":
		var result model.NodeRegistry
		if call("node_list", nil, &result) != nil {
			return nil
		}
		if result.Error != nil {
			return nil
		}
		for _, node := range result.Nodes {
			if ctx.Err() != nil {
				return nil
			}
			add(node.Host, node.Name, node.Local || node.Trust.State == "active")
		}
		ids["localhost"] = true
		allIDs["localhost"] = true
	case "pool":
		var result []model.PoolView
		if call("pool_list", map[string]bool{"all": true}, &result) != nil {
			return nil
		}
		for _, view := range result {
			if ctx.Err() != nil {
				return nil
			}
			add(view.Pool.ID, view.Pool.Name, true)
		}
	case "lease":
		var result struct{ Leases []struct{ ID string } }
		if call("status", nil, &result) != nil {
			return nil
		}
		for _, lease := range result.Leases {
			if ctx.Err() != nil {
				return nil
			}
			add(lease.ID, "", true)
		}
	}
	var values []string
	for id := range ids {
		if strings.HasPrefix(id, prefix) {
			values = append(values, id+"\t"+kind+" ID")
		}
	}
	for name, matches := range names {
		if len(matches) == 1 && allowedNames[name] && !allIDs[name] && completionToken(name) && strings.HasPrefix(name, prefix) {
			values = append(values, name+"\t"+kind+" name")
		}
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
