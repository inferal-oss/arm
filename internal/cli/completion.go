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
	if cmd.CommandPath() == "arm node availability set" && (name == "profile" || name == "host") {
		values := []string{"localhost"}
		if name == "profile" {
			values = []string{"unspecified", "stable", "intermittent"}
		}
		_ = cmd.RegisterFlagCompletionFunc(name, func(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
			var matches []string
			for _, value := range values {
				if strings.HasPrefix(value, prefix) {
					matches = append(matches, value)
				}
			}
			return matches, cobra.ShellCompDirectiveNoFileComp
		})
		return
	}
	kind := ""
	if strings.HasPrefix(cmd.CommandPath(), "arm name ") && name == "kind" {
		_ = cmd.RegisterFlagCompletionFunc(name, func(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
			return nameKinds(prefix), cobra.ShellCompDirectiveNoFileComp
		})
		return
	}
	if cmd.CommandPath() == "arm name clear" && name == "host" {
		_ = cmd.RegisterFlagCompletionFunc(name, func(c *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
			return a.completeNameReservations(c, args, prefix, true), cobra.ShellCompDirectiveNoFileComp
		})
		return
	}
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
	if path == "name set" || path == "name clear" {
		if len(args) == 0 {
			return nameKinds(prefix), cobra.ShellCompDirectiveNoFileComp
		}
		if path == "name clear" {
			return a.completeNameReservations(cmd, args, prefix, false), cobra.ShellCompDirectiveNoFileComp
		}
		if len(args) == 1 && nameKind(args[0]) {
			return a.completeObjects(cmd, args[0], prefix), cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
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
func completionToken(s string) bool   { return completionTokenLimit(s, 128, false) }
func completionIDToken(s string) bool { return completionTokenLimit(s, 256, true) }
func completionTokenLimit(s string, limit int, resourceID bool) bool {
	if len(s) == 0 || len(s) > limit || strings.HasPrefix(s, "-") {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._:-", c) || resourceID && c == '/') {
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
		if selectorNeedsCustom(host, candidates) {
			var active []readablename.Candidate
			for _, node := range registry.Nodes {
				if node.Local || node.Trust.State == "active" {
					active = append(active, readablename.Candidate{ID: node.Host, Name: node.Name})
				}
			}
			joined, problem := completionCustomCandidates(ctx, endpoint, "host", "", active, nil)
			if problem != nil {
				return nil
			}
			candidates = append(candidates, joined...)
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
	var candidates []readablename.Candidate
	bindings := map[string]string{}
	authority := ""
	eligibleIDs := map[string]bool{}
	normalizedHealthy := true
	ids, allIDs, names, allowedNames := map[string]bool{}, map[string]bool{}, map[string]map[string]bool{}, map[string]bool{}
	add := func(id, name string, eligible bool) {
		candidates = append(candidates, readablename.Candidate{ID: id, Name: name})
		eligibleIDs[id] = eligibleIDs[id] || eligible
		allIDs[id] = true
		if eligible && completionIDToken(id) {
			ids[id] = true
		}
		domain := kind
		if kind == readablename.Resource && strings.HasPrefix(id, "pool_") && opaqueID(strings.TrimPrefix(id, "pool_")) {
			domain = readablename.Pool
		}
		for _, alias := range []string{name, readablename.FromID(domain, id)} {
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
		if cmd.CommandPath() == "arm name set" && host != "localhost" {
			var status model.Status
			if call("status", nil, &status) != nil || status.Host != host {
				return nil
			}
			authority = status.Host
			for _, resource := range status.Resources {
				if id, name, ok := namingPoolResource(resource); ok {
					result = append(result, model.PoolView{Pool: model.Pool{ID: id, Name: name}})
				}
			}
		} else {
			if call("pool_list", map[string]bool{"all": true}, &result) != nil {
				return nil
			}
		}
		for _, view := range result {
			if ctx.Err() != nil {
				return nil
			}
			add(view.Pool.ID, view.Pool.Name, true)
		}
	case "lease":
		var result model.Status
		if call("status", nil, &result) != nil {
			return nil
		}
		authority = result.Host
		if host != "localhost" && authority != host {
			return nil
		}
		for _, lease := range result.Leases {
			if ctx.Err() != nil {
				return nil
			}
			add(lease.ID, "", true)
		}
	case "resource":
		var result model.Status
		if call("status", nil, &result) != nil {
			return nil
		}
		authority = result.Host
		if host != "localhost" && authority != host {
			return nil
		}
		visibleBindings, eligibleBindings := resourceBindings(result.Resources)
		for _, resource := range result.Resources {
			if ctx.Err() != nil {
				return nil
			}
			if _, _, ok := namingPoolResource(resource); ok {
				// A pool-backed resource uses the pool naming namespace.
				continue
			}
			binding, eligible := visibleBindings[resource.ID], eligibleBindings[resource.ID]
			if eligible {
				bindings[resource.ID] = binding
			}
			add(resource.ID, "", eligible)
		}
		var pools []readablename.Candidate
		for _, resource := range result.Resources {
			if id, name, ok := namingPoolResource(resource); ok {
				pools = append(pools, readablename.Candidate{ID: id, Name: name})
			}
		}
		joined := pools
		if custom, err := completionCustomCandidates(ctx, endpoint, "pool", authority, pools, nil); err == nil {
			joined = custom
		} else {
			normalizedHealthy = false
		}
		for _, pool := range joined {
			add(pool.ID, pool.Name, true)
			add(pool.ID, readablename.FromID(readablename.Pool, pool.ID), true)
		}
	}
	customHealthy := false
	if joined, err := completionCustomCandidates(ctx, endpoint, kind, authority, candidates, bindings); err == nil {
		customHealthy = normalizedHealthy
		for _, candidate := range joined {
			add(candidate.ID, candidate.Name, eligibleIDs[candidate.ID])
		}
	}
	var values []string
	for id := range ids {
		if strings.HasPrefix(id, prefix) {
			values = append(values, id+"\t"+kind+" ID")
		}
	}
	for name, matches := range names {
		if !customHealthy && selectorNeedsCustom(name, nil) {
			continue
		}
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
