package tui

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshops"
)

type meshConnectDraft struct {
	Destination    string     `json:"ssh_destination,omitempty"`
	Connector      string     `json:"connector,omitempty"`
	Reason         string     `json:"reason"`
	Forward        mesh.Grant `json:"source_permissions_on_target"`
	Reverse        mesh.Grant `json:"target_permissions_on_source"`
	ReplaceRevoked bool       `json:"replace_revoked"`
}
type meshReconnectDraft struct {
	Host        string     `json:"host"`
	Destination string     `json:"ssh_destination,omitempty"`
	Connector   string     `json:"connector,omitempty"`
	Reason      string     `json:"reason"`
	Forward     mesh.Grant `json:"source_permissions_on_target"`
	Reverse     mesh.Grant `json:"target_permissions_on_source"`
}
type meshPrepareRequest struct {
	Key        string                   `json:"key"`
	Kind       string                   `json:"kind"`
	Connect    *meshConnectDraft        `json:"connect,omitempty"`
	Reconnect  *meshReconnectDraft      `json:"reconnect,omitempty"`
	Disconnect *meshops.DisconnectDraft `json:"disconnect,omitempty"`
}
type meshRevisionRequest struct {
	Key      string `json:"key"`
	Revision int64  `json:"revision"`
	Hash     string `json:"review_sha256,omitempty"`
	Reason   string `json:"reason,omitempty"`
}
type meshPrompt struct {
	title, text string
	request     meshIntent
	retry       bool
}
type meshField struct {
	label, value string
	choices      []string
}
type meshForm struct {
	contextReady  bool
	contextSeen   bool
	blockers      []meshops.ReconnectBlocker
	blockerIndex  int
	reconnectHost string
	fields        []meshField
	selected      int
}

func newMeshForm() *meshForm {
	fields := []meshField{{label: "SSH destination or @reviewed-connector"}, {label: "Reason"}}
	for _, direction := range []string{"We may do THERE", "Peer may do HERE"} {
		mode := "none"
		if direction == "We may do THERE" {
			mode = "summary"
		}
		fields = append(fields, meshField{direction + " · access", mode, []string{"none", "summary", "leases", "run"}}, meshField{"CPU ceiling (cores)", "1", nil}, meshField{"Memory ceiling (GiB)", "1", nil}, meshField{"Disk ceiling (GiB)", "0", nil}, meshField{"Maximum lease TTL", "10m", nil}, meshField{"Allow messaging", "no", []string{"no", "yes"}}, meshField{"Exact allowed contact IDs", "", nil}, meshField{"Allow operator inbox", "no", []string{"no", "yes"}})
	}
	fields = append(fields, meshField{"Replace revoked enrollment", "no", []string{"no", "yes"}})
	return &meshForm{fields: fields}
}

func meshGiB(text string) (int64, error) {
	text = strings.TrimSpace(text)
	if len(text) == 0 || len(text) > 24 || strings.Count(text, ".") > 1 {
		return 0, fmt.Errorf("enter a bounded decimal GiB quantity")
	}
	for _, r := range text {
		if (r < '0' || r > '9') && r != '.' {
			return 0, fmt.Errorf("use ordinary decimal GiB, without exponent or fractions")
		}
	}
	n, ok := new(big.Rat).SetString(text)
	if !ok || n.Sign() < 0 {
		return 0, fmt.Errorf("enter a nonnegative GiB quantity")
	}
	n.Mul(n, new(big.Rat).SetInt64(1<<30))
	if !n.IsInt() || !n.Num().IsInt64() {
		return 0, fmt.Errorf("GiB quantity must resolve to whole bytes within int64")
	}
	return n.Num().Int64(), nil
}
func (f *meshForm) grant(offset int) (mesh.Grant, error) {
	g := mesh.Grant{Permissions: []string{}, Contacts: []string{}}
	mode := f.fields[offset].value
	if mode != "none" && mode != "summary" && mode != "leases" && mode != "run" {
		return g, fmt.Errorf("choose none, summary, leases or run explicitly")
	}
	if mode == "none" {
		return g, nil
	}
	g.Permissions = []string{"observe-host"}
	if mode == "leases" || mode == "run" {
		g.Permissions = append(g.Permissions, "observe-own", "lease-own")
		if mode == "run" {
			g.Permissions = append(g.Permissions, "run")
		}
		var err error
		g.Limits.CPU, err = strconv.ParseFloat(f.fields[offset+1].value, 64)
		if err != nil {
			return g, fmt.Errorf("enter a valid CPU ceiling")
		}
		g.Limits.Memory, err = meshGiB(f.fields[offset+2].value)
		if err != nil {
			return g, err
		}
		g.Limits.Disk, err = meshGiB(f.fields[offset+3].value)
		if err != nil {
			return g, err
		}
		g.MaxTTL, err = time.ParseDuration(f.fields[offset+4].value)
		if err != nil {
			return g, fmt.Errorf("use a duration such as 10m or 2h (at most 168h)")
		}
	}
	if f.fields[offset+5].value == "yes" {
		g.Permissions = append(g.Permissions, "message")
		seen := map[string]bool{}
		for _, id := range strings.FieldsFunc(f.fields[offset+6].value, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
			if !seen[id] {
				g.Contacts = append(g.Contacts, id)
				seen[id] = true
			}
		}
		g.OperatorInbox = f.fields[offset+7].value == "yes"
		if len(g.Contacts) > 0 {
			g.Permissions = append(g.Permissions, "contact-read")
		}
		if len(g.Contacts) == 0 && !g.OperatorInbox {
			return g, fmt.Errorf("messaging needs exact contact IDs or explicit operator inbox access")
		}
	}
	return g, g.Validate()
}
func (f *meshForm) request() (meshIntent, error) {
	forward, err := f.grant(2)
	if err != nil {
		return meshIntent{}, fmt.Errorf("We may do THERE: %w", err)
	}
	reverse, err := f.grant(10)
	if err != nil {
		return meshIntent{}, fmt.Errorf("Peer may do HERE: %w", err)
	}
	if f.reconnectHost != "" {
		d := &meshReconnectDraft{Host: f.reconnectHost, Destination: strings.TrimSpace(f.fields[0].value), Reason: f.fields[1].value, Forward: forward, Reverse: reverse}
		if strings.HasPrefix(d.Destination, "@") {
			d.Connector = strings.TrimPrefix(d.Destination, "@")
			d.Destination = ""
		}
		if strings.TrimSpace(d.Reason) == "" || len(d.Reason) > 4096 {
			return meshIntent{}, fmt.Errorf("reason is required, up to 4096 bytes")
		}
		if d.Destination != "" {
			if err = (mesh.Connector{ID: "terminal", Destination: d.Destination, Binary: "arm", Reason: d.Reason}).Validate(); err != nil {
				return meshIntent{}, err
			}
		}
		key := local.ID()
		return newMeshIntent("mesh_operator_prepare", key, meshPrepareRequest{Key: key, Kind: "reconnect", Reconnect: d})
	}
	d := &meshConnectDraft{Destination: strings.TrimSpace(f.fields[0].value), Reason: f.fields[1].value, Forward: forward, Reverse: reverse, ReplaceRevoked: f.fields[18].value == "yes"}
	if strings.HasPrefix(d.Destination, "@") {
		d.Connector = strings.TrimPrefix(d.Destination, "@")
		d.Destination = ""
	}
	if strings.TrimSpace(d.Reason) == "" || len(d.Reason) > 4096 {
		return meshIntent{}, fmt.Errorf("reason is required, up to4096 bytes")
	}
	if d.Connector == "" {
		if err = (mesh.Connector{ID: "terminal", Destination: d.Destination, Binary: "arm", Reason: d.Reason}).Validate(); err != nil {
			return meshIntent{}, err
		}
	}
	key := local.ID()
	return newMeshIntent("mesh_operator_prepare", key, meshPrepareRequest{Key: key, Kind: "connect", Connect: d})
}

func meshExactJSON(value any) string {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "Record unavailable: " + safe(err.Error())
	}
	return safe(string(raw))
}
