package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"

	"getarm.org/internal/manager"
	"getarm.org/internal/model"
)

// Keep uninterpreted response fields as raw JSON. Only the typed visible
// identity fields and presentation strings are read or changed; quantities,
// unknown additive fields and original request/review objects stay opaque.
type rawNameField struct {
	key string
	raw json.RawMessage
}
type rawNameView struct {
	fields  []rawNameField
	changed bool
}

func readRawNameView(raw json.RawMessage) (rawNameView, error) {
	view := rawNameView{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return view, model.Err("name_inventory_unavailable", "remote view is not a complete object")
	}
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return view, model.Err("name_inventory_unavailable", "remote view has duplicate or invalid fields")
		}
		seen[name] = true
		switch strings.ToLower(name) {
		case "id", "host", "kind", "lease", "relationship", "leases", "resources", "users", "display_name", "host_display_name", "lease_display_name":
			if strings.ToLower(name) != name {
				return view, model.Err("name_inventory_unavailable", "remote view has noncanonical identity fields")
			}
		}
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return view, err
		}
		switch name {
		case "id", "host", "kind", "lease", "relationship":
			var identity string
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &identity) != nil {
				return view, model.Err("name_inventory_unavailable", "remote view has nonstring identity fields")
			}
		}
		view.fields = append(view.fields, rawNameField{key: name, raw: value})
	}
	if _, err = decoder.Token(); err != nil {
		return view, err
	}
	if _, err = decoder.Token(); err != io.EOF {
		return view, model.Err("name_inventory_unavailable", "remote view has trailing data")
	}
	return view, nil
}
func (view rawNameView) field(key string) json.RawMessage {
	for _, field := range view.fields {
		if field.key == key {
			return field.raw
		}
	}
	return nil
}
func (view rawNameView) text(key string) string {
	var result string
	_ = json.Unmarshal(view.field(key), &result)
	return result
}
func (view *rawNameView) set(key string, raw json.RawMessage) {
	for i := range view.fields {
		if view.fields[i].key == key {
			if !bytes.Equal(view.fields[i].raw, raw) {
				view.fields[i].raw = raw
				view.changed = true
			}
			return
		}
	}
	view.fields = append(view.fields, rawNameField{key: key, raw: raw})
	view.changed = true
}
func (view *rawNameView) label(key, label string) {
	if label != "" {
		raw, _ := json.Marshal(label)
		view.set(key, raw)
	}
}
func (view rawNameView) encoded() json.RawMessage {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, field := range view.fields {
		if i > 0 {
			out.WriteByte(',')
		}
		key, _ := json.Marshal(field.key)
		out.Write(key)
		out.WriteByte(':')
		out.Write(field.raw)
	}
	out.WriteByte('}')
	return out.Bytes()
}

func rawNameArray(raw json.RawMessage, project func(json.RawMessage) (json.RawMessage, error)) (json.RawMessage, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return raw, nil
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	changed := false
	for i := range rows {
		view, err := project(rows[i])
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(view, rows[i]) {
			changed = true
			rows[i] = view
		}
	}
	if !changed {
		return raw, nil
	}
	return json.Marshal(rows)
}

func (index customNameIndex) remoteLease(authority string, raw json.RawMessage) (json.RawMessage, error) {
	view, err := readRawNameView(raw)
	if err != nil {
		return nil, err
	}
	if view.text("host") != authority {
		return nil, model.Err("wrong_host", "remote lease differs from selected authority")
	}
	if !exactNodeID(view.text("id")) {
		return nil, model.Err("name_inventory_unavailable", "remote lease has no canonical visible identity")
	}
	view.label("display_name", index.name("lease", authority, view.text("id"), ""))
	view.label("host_display_name", index.name("host", authority, authority, ""))
	if !view.changed {
		return raw, nil
	}
	return view.encoded(), nil
}
func rawNameResource(authority string, view rawNameView) (model.ResourceView, error) {
	resource := model.ResourceView{ID: view.text("id"), Host: view.text("host"), Kind: view.text("kind")}
	if resource.ID == "" || resource.Kind == "" {
		return model.ResourceView{}, model.Err("name_inventory_unavailable", "remote resource identity is incomplete")
	}
	if resource.Host != "" && resource.Host != authority {
		return model.ResourceView{}, model.Err("wrong_host", "remote resource differs from selected authority")
	}
	users := view.field("users")
	if len(users) > 0 && !bytes.Equal(bytes.TrimSpace(users), []byte("null")) {
		var rows []json.RawMessage
		if json.Unmarshal(users, &rows) != nil {
			return model.ResourceView{}, model.Err("name_inventory_unavailable", "remote resource consumers are incomplete")
		}
		for _, raw := range rows {
			use, err := readRawNameView(raw)
			if err != nil {
				return model.ResourceView{}, err
			}
			// Infrastructure rows explicitly have an empty lease; other
			// visible references must retain their original canonical ID.
			if len(use.field("lease")) == 0 || use.text("lease") != "" && !exactNodeID(use.text("lease")) {
				return model.ResourceView{}, model.Err("name_inventory_unavailable", "remote consumer has no canonical visible identity")
			}
			resource.Users = append(resource.Users, model.ResourceUse{Lease: use.text("lease"), Relationship: use.text("relationship")})
		}
	}
	return resource, nil
}

func (index customNameIndex) remoteResource(authority string, raw json.RawMessage, portOwners map[string]string) (json.RawMessage, error) {
	view, err := readRawNameView(raw)
	if err != nil {
		return nil, err
	}
	resource, err := rawNameResource(authority, view)
	if err != nil {
		return nil, err
	}
	users := view.field("users")
	if ref, refErr := manager.CustomNameResourceReference(authority, resource); refErr == nil && (resource.Kind != "port" || portOwners[resource.ID] == ref.BindingLease) {
		view.label("display_name", index[ref])
	}
	if resource.Host == authority {
		view.label("host_display_name", index.name("host", authority, authority, ""))
	}
	if len(users) > 0 {
		projected, err := rawNameArray(users, func(raw json.RawMessage) (json.RawMessage, error) {
			use, err := readRawNameView(raw)
			if err != nil {
				return nil, err
			}
			use.label("lease_display_name", index.name("lease", authority, use.text("lease"), ""))
			if !use.changed {
				return raw, nil
			}
			return use.encoded(), nil
		})
		if err != nil {
			return nil, err
		}
		view.set("users", projected)
	}
	if !view.changed {
		return raw, nil
	}
	return view.encoded(), nil
}

func (index customNameIndex) remoteResources(authority string, raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return raw, nil
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	resources := make([]model.ResourceView, len(rows))
	for i, row := range rows {
		view, err := readRawNameView(row)
		if err != nil {
			return nil, err
		}
		resources[i], err = rawNameResource(authority, view)
		if err != nil {
			return nil, err
		}
	}
	owners := manager.CustomNamePortReservations(authority, resources)
	return rawNameArray(raw, func(row json.RawMessage) (json.RawMessage, error) {
		return index.remoteResource(authority, row, owners)
	})
}

type sourceNamedRead struct {
	raw        json.RawMessage
	diagnostic *model.Error
}

func (view sourceNamedRead) MarshalJSON() ([]byte, error) { return view.raw, nil }

// Called only on the local source's already-authorized read result, never on
// destination dispatch or immutable remote mutation/retry receipts.
func (s *Server) sourceNamedView(ctx context.Context, authority, operation string, raw json.RawMessage) (any, error) {
	if operation != "status" && operation != "resources" && operation != "inspect" {
		return raw, nil
	}
	doc, err := s.Manager.CustomNames(ctx)
	if err != nil {
		return sourceNamedRead{raw: raw, diagnostic: nameReadDiagnostic(err)}, nil
	}
	index := makeCustomNameIndex(doc)
	if len(index) == 0 {
		return raw, nil
	}
	var projected json.RawMessage
	switch operation {
	case "inspect":
		projected, err = index.remoteLease(authority, raw)
	case "resources":
		projected, err = index.remoteResources(authority, raw)
	case "status":
		var view rawNameView
		view, err = readRawNameView(raw)
		if err != nil {
			break
		}
		if view.text("host") != authority {
			err = model.Err("wrong_host", "remote status differs from selected authority")
			break
		}
		view.label("display_name", index.name("host", authority, authority, ""))
		for _, field := range []string{"leases", "resources"} {
			value := view.field(field)
			if len(value) == 0 {
				continue
			}
			var named json.RawMessage
			if field == "leases" {
				named, err = rawNameArray(value, func(row json.RawMessage) (json.RawMessage, error) { return index.remoteLease(authority, row) })
			} else {
				named, err = index.remoteResources(authority, value)
			}
			if err != nil {
				break
			}
			view.set(field, named)
		}
		if err == nil {
			if view.changed {
				projected = view.encoded()
			} else {
				projected = raw
			}
		}
	}
	if err != nil {
		return sourceNamedRead{raw: raw, diagnostic: nameReadDiagnostic(err)}, nil
	}
	if !bytes.Equal(projected, raw) && len(projected) > 3<<20 {
		return nil, model.Err("response_too_large", "complete named remote view exceeds the 3 MiB response budget; no authorized scope was truncated")
	}
	return projected, nil
}
