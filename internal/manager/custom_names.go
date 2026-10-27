package manager

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"getarm.org/internal/customnames"
	"getarm.org/internal/model"
)

// NameObservation is constructed only from an existing authorized, complete
// read. It is not accepted from a wire request and grants no resource rights.
// Server callers recheck the pinned peer context before entering Manager.
type NameObservation struct {
	source string
	refs   []customnames.Entry
	named  []customnames.Entry
}

func ObserveNameHosts(registry model.NodeRegistry) (NameObservation, error) {
	if registry.Error != nil || registry.Source == "" || len(registry.Nodes) == 0 || len(registry.Nodes) > 1024 {
		return NameObservation{}, model.Err("name_inventory_unavailable", "complete host registry is unavailable")
	}
	observation := NameObservation{source: registry.Source}
	seen := map[string]bool{}
	locals := 0
	for _, node := range registry.Nodes {
		ref := customnames.Entry{Kind: "host", AuthorityHost: node.Host, ID: node.Host}
		if customnames.ValidateBinding(ref) != nil || seen[node.Host] {
			return NameObservation{}, model.Err("name_inventory_unavailable", "host registry has invalid or repeated identities")
		}
		seen[node.Host] = true
		if node.Local {
			if node.Host != registry.Source {
				return NameObservation{}, model.Err("name_inventory_unavailable", "local registry source differs")
			}
			locals++
		}
		observation.refs = append(observation.refs, ref)
		if node.Name != "" && node.Name != "localhost" {
			ref.Name = node.Name
			observation.named = append(observation.named, ref)
		}
	}
	if locals != 1 {
		return NameObservation{}, model.Err("name_inventory_unavailable", "one exact local registry source is required")
	}
	return observation, nil
}

// ObserveNameStatus consumes original typed models, without a JSON roundtrip.
// The caller establishes completeness/permissions and the pinned read context.
func ObserveNameStatus(source string, status model.Status) (NameObservation, error) {
	observation := NameObservation{source: source}
	if source == "" || status.Host == "" {
		return observation, model.Err("name_inventory_unavailable", "complete selected authority is required")
	}
	for _, lease := range status.Leases {
		ref := customnames.Entry{Kind: "lease", AuthorityHost: status.Host, ID: lease.ID}
		if lease.Host != status.Host || customnames.ValidateBinding(ref) != nil {
			return NameObservation{}, model.Err("name_inventory_unavailable", "lease inventory differs from selected authority")
		}
		observation.refs = append(observation.refs, ref)
	}
	portOwners := CustomNamePortReservations(status.Host, status.Resources)
	for _, resource := range status.Resources {
		if resource.Kind == "port" && portOwners[resource.ID] == "" {
			continue
		}
		ref, err := CustomNameResourceReference(status.Host, resource)
		if err != nil {
			// Infrastructure ports and rows without a disclosed reservation
			// incarnation cannot acquire a custom resource alias.
			if resource.Kind == "port" {
				continue
			}
			return NameObservation{}, err
		}
		observation.refs = append(observation.refs, ref)
		if resource.Custom != nil {
			ref.Name = resource.Custom.Pool.Name
		} else if resource.Port != nil {
			ref.Name = resource.Port.Name
		} else {
			switch resource.Kind {
			case "cpu":
				ref.Name = "CPU"
			case "memory":
				ref.Name = "Memory"
			case "disk":
				ref.Name = "Disk"
			}
		}
		if ref.Name != "" {
			observation.named = append(observation.named, ref)
		}
	}
	return observation, nil
}

// Duplicate locator rows must agree on one reservation owner across the whole
// complete snapshot. A row-local match is not enough to prove incarnation.
func CustomNamePortReservations(authority string, resources []model.ResourceView) map[string]string {
	owners := map[string]map[string]bool{}
	invalid := map[string]bool{}
	for _, resource := range resources {
		if resource.Kind != "port" {
			continue
		}
		ref, err := CustomNameResourceReference(authority, resource)
		if err != nil {
			invalid[resource.ID] = true
			continue
		}
		if owners[resource.ID] == nil {
			owners[resource.ID] = map[string]bool{}
		}
		owners[resource.ID][ref.BindingLease] = true
	}
	result := map[string]string{}
	for id, candidates := range owners {
		if !invalid[id] && len(candidates) == 1 {
			for owner := range candidates {
				result[id] = owner
			}
		}
	}
	return result
}

// CustomNameResourceReference pins a reusable port locator to the unique
// reservation owner. Attached consumers and slice order never identify it.
func CustomNameResourceReference(authority string, resource model.ResourceView) (customnames.Entry, error) {
	ref := customnames.Entry{Kind: "resource", AuthorityHost: authority, ID: resource.ID}
	if resource.Host != "" && resource.Host != authority {
		return ref, model.Err("wrong_host", "resource differs from selected authority")
	}
	switch resource.Kind {
	case "cpu", "memory", "disk":
		if resource.ID != authority+"/"+resource.Kind {
			return ref, model.Err("name_inventory_unavailable", "resource kind differs from its canonical ID")
		}
	case "custom", "port":
	default:
		return ref, model.Err("name_inventory_unavailable", "resource kind has no supported name binding")
	}
	if resource.Kind == "custom" {
		ref.Kind = "pool"
		if resource.Custom != nil && (resource.Custom.Pool.ID != resource.ID || resource.Custom.Pool.Authority != authority) {
			return ref, model.Err("wrong_host", "pool differs from selected authority")
		}
	}
	if resource.Kind == "port" {
		owners := map[string]bool{}
		for _, use := range resource.Users {
			if use.Relationship == "reservation" && use.Lease != "" {
				owners[use.Lease] = true
			}
		}
		if len(owners) != 1 {
			return ref, model.Err("name_incarnation_unavailable", "port naming requires one visible canonical reservation lease")
		}
		for id := range owners {
			ref.BindingLease = id
		}
	}
	if err := customnames.ValidateBinding(ref); err != nil {
		return ref, model.Err("name_inventory_unavailable", "resource has no supported stable name binding")
	}
	return ref, nil
}

type nameQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (m *Manager) readCustomNames(ctx context.Context, query nameQuery) (customnames.Document, *string, error) {
	// Inspect physical key/value classes and byte length before allocating the
	// optional TEXT. BLOB key aliases must not masquerade as missing metadata.
	rows, err := query.QueryContext(ctx, `SELECT typeof(key),typeof(value),length(CAST(value AS BLOB)),CASE WHEN typeof(value)='text' AND length(CAST(value AS BLOB))<=? THEN value END FROM metadata WHERE CAST(key AS BLOB)=CAST(? AS BLOB) LIMIT 2`, customnames.MaxBytes, customnames.MetadataKey)
	if err != nil {
		return customnames.Document{}, nil, err
	}
	defer rows.Close()
	var raw *string
	for rows.Next() {
		var keyType, valueType string
		var size int64
		var value sql.NullString
		if err = rows.Scan(&keyType, &valueType, &size, &value); err != nil {
			return customnames.Document{}, nil, err
		}
		if raw != nil || keyType != "text" || valueType != "text" || size < 0 || size > customnames.MaxBytes || !value.Valid {
			return customnames.Document{}, nil, invalidCustomNames()
		}
		copy := value.String
		raw = &copy
	}
	if err = rows.Err(); err != nil {
		return customnames.Document{}, nil, err
	}
	if err = rows.Close(); err != nil {
		return customnames.Document{}, nil, err
	}
	hostRows, err := query.QueryContext(ctx, `SELECT typeof(key),typeof(value),CASE WHEN typeof(value)='text' AND length(CAST(value AS BLOB))=32 THEN value END FROM metadata WHERE CAST(key AS BLOB)=CAST('host' AS BLOB) LIMIT 2`)
	if err != nil {
		return customnames.Document{}, nil, err
	}
	defer hostRows.Close()
	hosts := 0
	for hostRows.Next() {
		var keyType, valueType string
		var host sql.NullString
		if err = hostRows.Scan(&keyType, &valueType, &host); err != nil {
			return customnames.Document{}, nil, err
		}
		if keyType != "text" || valueType != "text" || !host.Valid || host.String != m.host {
			return customnames.Document{}, nil, invalidCustomNames()
		}
		hosts++
	}
	if err = hostRows.Err(); err != nil {
		return customnames.Document{}, nil, err
	}
	if hosts != 1 {
		return customnames.Document{}, nil, invalidCustomNames()
	}
	var data []byte
	if raw != nil {
		data = []byte(*raw)
	}
	doc, err := customnames.Decode(data, m.owner, m.host)
	if err != nil {
		return customnames.Document{}, raw, invalidCustomNames()
	}
	return doc, raw, nil
}

func invalidCustomNames() error {
	return model.Err("name_metadata_invalid", "custom name metadata is invalid; use exact IDs and inspect the database without repairing or dropping evidence")
}

func (m *Manager) CustomNames(ctx context.Context) (customnames.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, _, err := m.readCustomNames(ctx, m.db)
	return doc, err
}

func (m *Manager) CustomNameIdentity() (host, owner string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.host, m.owner
}

// A list payload does not prove that its ID is the physical lookup key.
// Naming reads the exact record and never repairs mismatched ledger content.
func (m *Manager) canonicalNameLease(id string) (model.Lease, error) {
	lease, err := m.get(id)
	if err != nil {
		return model.Lease{}, err
	}
	if lease.ID != id || lease.Host != m.host || lease.Owner != m.owner {
		return model.Lease{}, model.Err("name_inventory_unavailable", "exact local lease identity differs from its authority or owner")
	}
	return lease, nil
}

func (m *Manager) localNameObservation(target customnames.Entry, supplied NameObservation) (NameObservation, error) {
	if target.Kind == "host" {
		if target.ID != m.host || supplied.source != m.host {
			return NameObservation{}, model.Err("name_inventory_unavailable", "complete local host registry is required")
		}
		return supplied, nil
	}
	if target.Kind == "lease" {
		lease, err := m.canonicalNameLease(target.ID)
		if err != nil {
			return NameObservation{}, err
		}
		return NameObservation{source: m.host, refs: []customnames.Entry{{Kind: "lease", AuthorityHost: m.host, ID: lease.ID}}}, nil
	}
	if target.Kind == "pool" {
		tree, err := m.checkedPoolTree()
		if err != nil {
			return NameObservation{}, err
		}
		observation := NameObservation{source: m.host}
		for _, pool := range tree.nodes {
			ref := customnames.Entry{Kind: "pool", AuthorityHost: m.host, ID: pool.ID, Name: pool.Name}
			observation.refs = append(observation.refs, ref)
			observation.named = append(observation.named, ref)
		}
		return observation, nil
	}
	status := model.Status{Host: m.host}
	if target.Kind == "resource" {
		if strings.HasPrefix(target.ID, m.host+"/ports/") {
			leases, err := m.list()
			if err != nil {
				return NameObservation{}, err
			}
			m.portResources(&status, leases)
		} else {
			for _, kind := range []string{"cpu", "memory", "disk"} {
				status.Resources = append(status.Resources, model.ResourceView{ID: m.host + "/" + kind, Host: m.host, Kind: kind})
			}
		}
	}
	return ObserveNameStatus(m.host, status)
}

// An empty set-time port assertion is filled from independently observed
// reservation ownership. Clear always requires the exact retained incarnation.
func ValidateCustomNameTarget(operation string, target customnames.Entry) error {
	if operation != "set" && operation != "clear" {
		return model.Err("invalid_request", "set or clear is required")
	}
	if operation == "set" && target.Kind == "resource" && target.BindingLease == "" && strings.HasPrefix(target.ID, target.AuthorityHost+"/ports/") {
		target.BindingLease = strings.Repeat("0", 32) // Syntax only; never evidence or a stored binding.
	}
	return customnames.ValidateBinding(target)
}

func deriveNameBinding(target customnames.Entry, observed NameObservation) (customnames.Entry, error) {
	if target.Kind != "resource" || target.BindingLease != "" || !strings.HasPrefix(target.ID, target.AuthorityHost+"/ports/") {
		return target, nil
	}
	for _, ref := range observed.refs {
		if ref.Kind == target.Kind && ref.AuthorityHost == target.AuthorityHost && ref.ID == target.ID && ref.BindingLease != "" {
			if target.BindingLease != "" && target.BindingLease != ref.BindingLease {
				return target, model.Err("name_incarnation_unavailable", "port naming requires one visible reservation owner")
			}
			target.BindingLease = ref.BindingLease
		}
	}
	if target.BindingLease == "" {
		return target, model.Err("name_incarnation_unavailable", "port naming requires one visible reservation owner")
	}
	return target, nil
}

func (m *Manager) MutateCustomName(ctx context.Context, operation string, target customnames.Entry, expectedRevision int64, observed NameObservation) (customnames.Document, error) {
	if expectedRevision < 0 || ValidateCustomNameTarget(operation, target) != nil || customnames.ValidateName(target.Name) != nil {
		return customnames.Document{}, model.Err("invalid_request", "provide a supported exact name binding, safe name, set/clear operation and expected revision")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return customnames.Document{}, model.Err("backend_stopping", "custom names cannot change during shutdown")
	}
	if operation == "set" {
		if target.AuthorityHost == m.host {
			var err error
			observed, err = m.localNameObservation(target, observed)
			if err != nil {
				return customnames.Document{}, err
			}
		}
		if observed.source != m.host {
			return customnames.Document{}, model.Err("name_inventory_unavailable", "fresh authorized source-bound inventory is required")
		}
		var err error
		target, err = deriveNameBinding(target, observed)
		if err != nil {
			return customnames.Document{}, err
		}
		if target.AuthorityHost == m.host && target.Kind == "resource" && strings.HasPrefix(target.ID, m.host+"/ports/") {
			owner, err := m.canonicalNameLease(target.BindingLease)
			if err != nil {
				return customnames.Document{}, err
			}
			// A ghost row can borrow a real lease ID while inventing Ports.
			// Only the exact owner's current reservation proves this locator.
			canonical := model.Status{Host: m.host}
			m.portResources(&canonical, []model.Lease{owner})
			if CustomNamePortReservations(m.host, canonical.Resources)[target.ID] != target.BindingLease {
				return customnames.Document{}, model.Err("name_incarnation_unavailable", "exact local lease does not own this current port reservation")
			}
		}
		found := false
		for _, ref := range observed.refs {
			if customnames.SameBinding(ref, target) {
				found = true
			}
		}
		if !found {
			return customnames.Document{}, model.Err("name_target_unavailable", "exact binding is not in the current authorized inventory")
		}
		for _, ref := range observed.named {
			sameScope := ref.Kind == target.Kind && (ref.Kind == "host" || ref.AuthorityHost == target.AuthorityHost)
			if sameScope && strings.EqualFold(ref.Name, target.Name) && !customnames.SameBinding(ref, target) {
				return customnames.Document{}, model.Err("name_conflict", "name collides with an existing supplied resource name")
			}
		}
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return customnames.Document{}, err
	}
	defer tx.Rollback()
	doc, before, err := m.readCustomNames(ctx, tx)
	if err != nil {
		return customnames.Document{}, err
	}
	next, changed, err := customnames.Mutate(doc, operation, target, expectedRevision)
	if err != nil {
		code := "invalid_request"
		switch {
		case errors.Is(err, customnames.ErrRevisionConflict):
			code = "name_revision_conflict"
		case errors.Is(err, customnames.ErrNameConflict):
			code = "name_conflict"
		case errors.Is(err, customnames.ErrNotFound):
			code = "name_not_found"
		case errors.Is(err, customnames.ErrLimit):
			code = "name_limit"
		}
		return customnames.Document{}, model.Err(code, err.Error())
	}
	if !changed {
		return doc, nil
	}
	encoded, err := json.Marshal(next)
	if err != nil || len(encoded) > customnames.MaxBytes {
		return customnames.Document{}, model.Err("name_limit", "custom name document exceeds its bound")
	}
	var result sql.Result
	if before == nil {
		result, err = tx.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO NOTHING`, customnames.MetadataKey, string(encoded))
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE metadata SET value=? WHERE typeof(key)='text' AND key=? AND typeof(value)='text' AND CAST(value AS BLOB)=CAST(? AS BLOB)`, string(encoded), customnames.MetadataKey, *before)
	}
	if err != nil {
		return customnames.Document{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return customnames.Document{}, err
	}
	if count != 1 {
		return customnames.Document{}, model.Err("name_revision_conflict", "custom name map changed; read its revision before retrying")
	}
	audit := struct {
		Actor    string            `json:"actor"`
		Before   int64             `json:"before_revision"`
		Revision int64             `json:"revision"`
		Binding  customnames.Entry `json:"binding"`
	}{m.owner, doc.Revision, next.Revision, target}
	audit.Binding.Active = operation == "set"
	if err = event(tx, "custom_name_"+operation, target.ID, audit); err != nil {
		return customnames.Document{}, err
	}
	if err = tx.Commit(); err != nil {
		return customnames.Document{}, err
	}
	return next, nil
}
