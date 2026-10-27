package cli

import (
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"getarm.org/internal/customnames"
	"getarm.org/internal/model"
	"getarm.org/internal/readablename"
)

// These are local naming RPCs. AuthorityHost describes the represented object;
// it never selects a remote writer or changes an operation's canonical target.
type nameListRequest struct {
	Kind          string `json:"kind,omitempty"`
	AuthorityHost string `json:"authority_host,omitempty"`
	All           bool   `json:"all,omitempty"`
}
type nameSnapshot struct {
	customnames.Document
	NamingDiagnostic *model.Error `json:"naming_diagnostic,omitempty"`
}
type nameMutation struct {
	Operation        string `json:"operation"`
	Kind             string `json:"kind"`
	AuthorityHost    string `json:"authority_host"`
	ID               string `json:"id"`
	BindingLease     string `json:"binding_lease"`
	Name             string `json:"name"`
	ExpectedRevision int64  `json:"expected_revision"`
}

func nameKind(kind string) bool {
	return kind == "host" || kind == "lease" || kind == "pool" || kind == "resource"
}

func visitedFlag(f *flag.FlagSet, name string) bool {
	found := false
	f.Visit(func(v *flag.Flag) {
		if v.Name == name {
			found = true
		}
	})
	return found
}

func (a *app) names(args []string) (int, error) {
	if len(args) == 0 {
		return 2, fmt.Errorf("use arm name list|set|clear")
	}
	operation, args := args[0], args[1:]
	if operation != "list" && operation != "set" && operation != "clear" {
		return 2, fmt.Errorf("unknown name command %q", operation)
	}
	f := a.flags("name " + operation)
	host := f.String("host", "localhost", "represented resource authority; naming is always written locally")
	f.Bool("json", false, "force JSON output")
	if operation == "list" {
		kind := f.String("kind", "", "host, lease, pool or resource")
		all := f.Bool("all", false, "include inactive and dangling name reservations")
		if err := a.parse(f, args, false); err != nil {
			return 2, err
		}
		if *host == "" {
			return 2, fmt.Errorf("--host cannot be empty")
		}
		if *kind != "" && !nameKind(*kind) {
			return 2, fmt.Errorf("kind must be host, lease, pool or resource")
		}
		query := nameListRequest{Kind: *kind, All: *all}
		if visitedFlag(f, "host") {
			canonical, err := a.nameAuthority(*host)
			if err != nil {
				return 1, err
			}
			query.AuthorityHost = canonical
		}
		var result nameSnapshot
		if err := a.call("name_list", query, &result); err != nil {
			return 1, err
		}
		return 0, a.print(result)
	}
	binding := f.String("binding-lease", "", "exact owning reservation lease for a leased port resource")
	revisionText := f.String("revision", "", "required current custom-name map revision")
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	kind, target, name := f.Arg(0), f.Arg(1), f.Arg(2)
	if target == "" || *host == "" {
		return 2, fmt.Errorf("target and --host cannot be empty")
	}
	if !nameKind(kind) {
		return 2, fmt.Errorf("kind must be host, lease, pool or resource")
	}
	if err := customnames.ValidateName(name); err != nil {
		return 2, err
	}
	revision, err := strconv.ParseInt(*revisionText, 10, 64)
	if err != nil || revision < 0 {
		return 2, fmt.Errorf("--revision requires a nonnegative map revision")
	}
	if *binding != "" && (!opaqueID(*binding) || kind != "resource") {
		return 2, fmt.Errorf("--binding-lease requires an exact reservation lease ID for a resource")
	}
	var entry customnames.Entry
	if operation == "clear" {
		// Clear addresses even inactive/dangling reservations without probing the
		// old object or consulting optional metadata. Reuse is a separate set.
		if !visitedFlag(f, "host") || !opaqueID(*host) {
			return 2, fmt.Errorf("clear requires --host EXACT_HOST_ID")
		}
		entry = customnames.Entry{Kind: kind, AuthorityHost: *host, ID: target, BindingLease: *binding, Name: name}
		if err := customnames.ValidateBinding(entry); err != nil {
			return 2, err
		}
	} else {
		entry, err = a.nameTarget(kind, target, *host)
		if err != nil {
			return 1, err
		}
		entry.Name = name
		if kind == "host" && visitedFlag(f, "host") {
			authority, err := a.nameAuthority(*host)
			if err != nil {
				return 1, err
			}
			if authority != entry.AuthorityHost {
				return 2, fmt.Errorf("host target differs from --host authority")
			}
		}
		if *binding != "" && *binding != entry.BindingLease {
			return 2, fmt.Errorf("--binding-lease differs from the visible owning reservation")
		}
	}
	request := nameMutation{Operation: operation, Kind: entry.Kind, AuthorityHost: entry.AuthorityHost, ID: entry.ID, BindingLease: entry.BindingLease, Name: entry.Name, ExpectedRevision: revision}
	var result nameSnapshot
	if err := a.call("name_mutate", request, &result); err != nil {
		return 1, err
	}
	return 0, a.print(result)
}

func (a *app) nameAuthority(selector string) (string, error) {
	if opaqueID(selector) {
		return selector, nil
	}
	if selector != "" && selector != "localhost" {
		return a.resolveHost(selector)
	}
	var status model.Status
	if err := a.call("status", nil, &status); err != nil {
		return "", err
	}
	if !opaqueID(status.Host) {
		return "", aliasError(readablename.ErrIncomplete)
	}
	return status.Host, nil
}

// customCandidates only joins names onto a complete authorized inventory.
// Retained metadata by itself never establishes target visibility or identity.
func (a *app) customCandidates(kind, authority string, visible []readablename.Candidate, bindings map[string]string) ([]readablename.Candidate, error) {
	if kind != "host" && !opaqueID(authority) {
		return nil, aliasError(readablename.ErrIncomplete)
	}
	var hostLegacy []readablename.Candidate
	if kind == "host" {
		var registry model.NodeRegistry
		if err := a.call("node_list", nil, &registry); err != nil {
			return nil, err
		}
		if registry.Error != nil {
			return nil, aliasError(readablename.ErrIncomplete)
		}
		permitted := map[string]bool{}
		for _, object := range visible {
			permitted[object.ID] = true
		}
		localCount := 0
		var current []readablename.Candidate
		for _, node := range registry.Nodes {
			if node.Local {
				localCount++
			}
			if len(permitted) > 0 && !permitted[node.Host] {
				continue
			}
			candidate := readablename.Candidate{ID: node.Host, Name: node.Name}
			hostLegacy = append(hostLegacy, candidate)
			if node.Local || node.Trust.State == "active" {
				current = append(current, candidate)
			}
		}
		if localCount != 1 {
			return nil, aliasError(readablename.ErrIncomplete)
		}
		visible = current
	}
	snapshot, err := a.customNameSnapshot(kind, authority)
	if err != nil {
		return nil, err
	}
	joined, err := joinCustomCandidates(kind, authority, visible, bindings, snapshot)
	if err != nil {
		return nil, err
	}
	return append(hostLegacy, joined...), nil
}
func (a *app) customNameSnapshot(kind, authority string) (nameSnapshot, error) {
	var snapshot nameSnapshot
	diagnostic, err := a.callDiagnostic("name_list", nameListRequest{Kind: kind, AuthorityHost: authority}, &snapshot)
	if err != nil {
		return snapshot, err
	}
	if diagnostic != nil || snapshot.NamingDiagnostic != nil {
		return snapshot, model.Err("name_metadata_invalid", "optional custom names are invalid; use an exact ID")
	}
	return snapshot, nil
}

func joinCustomCandidates(kind, authority string, visible []readablename.Candidate, bindings map[string]string, snapshot nameSnapshot) ([]readablename.Candidate, error) {
	if snapshot.Version != 1 || snapshot.Revision < 0 || len(snapshot.Entries) > customnames.MaxEntries {
		return nil, model.Err("name_metadata_invalid", "optional custom names are invalid; use an exact ID")
	}
	ids := make(map[string]bool, len(visible))
	for _, c := range visible {
		ids[c.ID] = true
	}
	result := append([]readablename.Candidate(nil), visible...)
	for _, entry := range snapshot.Entries {
		if err := customnames.ValidateBinding(entry); err != nil {
			return nil, model.Err("name_metadata_invalid", "optional custom names are invalid; use an exact ID")
		}
		if err := customnames.ValidateName(entry.Name); err != nil {
			return nil, model.Err("name_metadata_invalid", "optional custom names are invalid; use an exact ID")
		}
		if entry.Active && entry.Kind == kind && (authority == "" || entry.AuthorityHost == authority) && ids[entry.ID] && (kind != "resource" || bindings[entry.ID] == entry.BindingLease) {
			result = append(result, readablename.Candidate{ID: entry.ID, Name: entry.Name})
		}
	}
	return result, nil
}

// Generated labels, original candidate IDs and spellings outside the custom
// namespace cannot acquire custom collisions. Other supplied/custom spellings
// require the full joined inventory even if a legacy match already exists.
func selectorNeedsCustom(target string, objects []readablename.Candidate) bool {
	for _, object := range objects {
		if object.ID == target {
			return false
		}
	}
	return customnames.ValidateName(target) == nil
}
func (a *app) resolveWithCustom(kind, target, authority string, objects []readablename.Candidate, bindings map[string]string) (string, error) {
	id, err := readablename.Resolve(kind, target, objects, true)
	if errors.Is(err, readablename.ErrAmbiguous) || !selectorNeedsCustom(target, objects) {
		return id, err
	}
	objects, err = a.customCandidates(kind, authority, objects, bindings)
	if err != nil {
		return "", err
	}
	return readablename.Resolve(kind, target, objects, true)
}

// nameTarget resolves a set target exactly once from fresh existing permitted
// reads. The server independently validates the resulting binding and CAS.
func (a *app) nameTarget(kind, target, host string) (customnames.Entry, error) {
	entry := customnames.Entry{Kind: kind}
	var candidates []readablename.Candidate
	bindings := map[string]string{}
	if kind == "host" {
		var registry model.NodeRegistry
		if err := a.call("node_list", nil, &registry); err != nil {
			return entry, err
		}
		if registry.Error != nil {
			return entry, aliasError(readablename.ErrIncomplete)
		}
		for _, node := range registry.Nodes {
			if node.Local || node.Trust.State == "active" {
				candidates = append(candidates, readablename.Candidate{ID: node.Host, Name: node.Name})
			}
			if node.Local && target == "localhost" {
				target = node.Host
			}
		}
		id, err := a.resolveWithCustom(kind, target, "", candidates, nil)
		if err != nil {
			return entry, aliasErrorPreserving(err)
		}
		entry.ID, entry.AuthorityHost = id, id
	} else {
		selected, err := a.resolveHost(host)
		if err != nil {
			return entry, err
		}
		var localStatus model.Status
		if err = a.call("status", nil, &localStatus); err != nil {
			return entry, err
		}
		status := localStatus
		localAuthority := selected == "" || selected == "localhost" || selected == localStatus.Host
		if !localAuthority {
			if err = a.hostCall(selected, "status", nil, &status); err != nil {
				return entry, err
			}
		}
		if !opaqueID(status.Host) || (selected != "" && selected != "localhost" && selected != status.Host) {
			return entry, aliasError(readablename.ErrIncomplete)
		}
		entry.AuthorityHost = status.Host
		if kind == "resource" {
			return a.resolveNamingResource(target, status)
		}
		switch kind {
		case "lease":
			for _, lease := range status.Leases {
				candidates = append(candidates, readablename.Candidate{ID: lease.ID})
			}
		case "pool":
			if localAuthority {
				var views []model.PoolView
				if err = a.call("pool_list", map[string]bool{"all": true}, &views); err != nil {
					return entry, err
				}
				for _, v := range views {
					candidates = append(candidates, readablename.Candidate{ID: v.Pool.ID, Name: v.Pool.Name})
				}
			} else {
				for _, r := range status.Resources {
					if id, name, ok := namingPoolResource(r); ok {
						candidates = append(candidates, readablename.Candidate{ID: id, Name: name})
					}
				}
			}
		}
		id, err := a.resolveWithCustom(kind, target, status.Host, candidates, bindings)
		if err != nil {
			return entry, aliasErrorPreserving(err)
		}
		entry.ID = id
	}
	if err := customnames.ValidateBinding(entry); err != nil {
		return entry, err
	}
	return entry, nil
}

// Pool-backed resource rows use the pool namespace before any selector is
// finalized. A resource selector sees both current namespaces jointly, so a
// supplied pool match cannot hide another pool's custom name (or vice versa).
func (a *app) resolveNamingResource(target string, status model.Status) (customnames.Entry, error) {
	entry := customnames.Entry{AuthorityHost: status.Host}
	var resources, pools []readablename.Candidate
	bindings, eligible := resourceBindings(status.Resources)
	visibleBindings := map[string]string{}
	for _, row := range status.Resources {
		if id, name, ok := namingPoolResource(row); ok {
			pools = append(pools, readablename.Candidate{ID: id, Name: name})
			continue
		}
		resources = append(resources, readablename.Candidate{ID: row.ID})
		if eligible[row.ID] {
			visibleBindings[row.ID] = bindings[row.ID]
		}
	}
	all := append(append([]readablename.Candidate{}, resources...), pools...)
	// IDs outrank every supplied/custom alias across the entire resource
	// surface, including a pool whose legacy name equals a capability ID.
	for _, inventory := range []struct {
		kind    string
		objects []readablename.Candidate
	}{{readablename.Resource, resources}, {readablename.Pool, pools}} {
		for _, object := range inventory.objects {
			if object.ID == target {
				entry.Kind, entry.ID = inventory.kind, target
				if entry.Kind == readablename.Resource {
					if !eligible[target] {
						return entry, model.Err("name_target_unavailable", "resource has no unique visible reservation binding")
					}
					entry.BindingLease = bindings[target]
				}
				if err := customnames.ValidateBinding(entry); err != nil {
					return entry, err
				}
				return entry, nil
			}
		}
	}
	if selectorNeedsCustom(target, all) {
		snapshot, err := a.customNameSnapshot("", status.Host)
		if err != nil {
			return entry, err
		}
		resources, err = joinCustomCandidates(readablename.Resource, status.Host, resources, visibleBindings, snapshot)
		if err != nil {
			return entry, err
		}
		pools, err = joinCustomCandidates(readablename.Pool, status.Host, pools, nil, snapshot)
		if err != nil {
			return entry, err
		}
	}
	for _, inventory := range []struct {
		kind    string
		objects []readablename.Candidate
	}{{readablename.Resource, resources}, {readablename.Pool, pools}} {
		id, err := readablename.Resolve(inventory.kind, target, inventory.objects, true)
		if errors.Is(err, readablename.ErrNotFound) {
			continue
		}
		if err != nil {
			return entry, aliasErrorPreserving(err)
		}
		if entry.ID != "" && entry.ID != id {
			return entry, aliasError(readablename.ErrAmbiguous)
		}
		entry.Kind, entry.ID = inventory.kind, id
	}
	if entry.ID == "" {
		return entry, aliasError(readablename.ErrNotFound)
	}
	if entry.Kind == readablename.Resource {
		if !eligible[entry.ID] {
			return entry, model.Err("name_target_unavailable", "resource has no unique visible reservation binding")
		}
		entry.BindingLease = bindings[entry.ID]
	}
	if err := customnames.ValidateBinding(entry); err != nil {
		return entry, err
	}
	return entry, nil
}

// Remote observe-host summaries retain the canonical kind and ID, while
// omitting private pool definitions and supplied names. Those original fields
// are enough to choose the pool namespace without inventing any extra access.
func namingPoolResource(resource model.ResourceView) (string, string, bool) {
	if resource.Kind != "custom" && resource.Custom == nil {
		return "", "", false
	}
	id, name := resource.ID, ""
	if resource.Custom != nil {
		if resource.Custom.Pool.ID != id {
			return "", "", false
		}
		name = resource.Custom.Pool.Name
	}
	if !strings.HasPrefix(id, "pool_") || !opaqueID(strings.TrimPrefix(id, "pool_")) {
		return "", "", false
	}
	return id, name, true
}

func reservationBinding(r model.ResourceView) (string, bool) {
	if r.Port == nil {
		return "", !strings.Contains(r.ID, "/ports/")
	}
	owner := ""
	for _, use := range r.Users {
		if use.Relationship == "reservation" {
			if !opaqueID(use.Lease) || owner != "" && owner != use.Lease {
				return "", false
			}
			owner = use.Lease
		}
	}
	return owner, owner != ""
}

func resourceBinding(resources []model.ResourceView, id string) (string, bool) {
	bindings, eligible := resourceBindings(resources)
	return bindings[id], eligible[id]
}

func resourceBindings(resources []model.ResourceView) (map[string]string, map[string]bool) {
	bindings, eligible, seen := map[string]string{}, map[string]bool{}, map[string]bool{}
	for _, resource := range resources {
		binding, ok := reservationBinding(resource)
		if seen[resource.ID] {
			ok = ok && eligible[resource.ID] && bindings[resource.ID] == binding
		}
		bindings[resource.ID], eligible[resource.ID], seen[resource.ID] = binding, ok, true
	}
	return bindings, eligible
}

func aliasErrorPreserving(err error) error {
	if errors.Is(err, readablename.ErrNotFound) || errors.Is(err, readablename.ErrAmbiguous) || errors.Is(err, readablename.ErrIncomplete) {
		return aliasError(err)
	}
	return err
}
