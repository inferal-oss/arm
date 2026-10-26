package manager

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/yrashk/arm/internal/model"
)

const maxPeerItems = 128

func (m *Manager) HostID() string { return m.host }

func addPeerResources(a, b model.Resources) (model.Resources, error) {
	if b.CPU < 0 || math.IsNaN(b.CPU) || math.IsInf(b.CPU, 0) || b.Memory < 0 || b.Disk < 0 ||
		a.Memory > math.MaxInt64-b.Memory || a.Disk > math.MaxInt64-b.Disk || math.IsInf(a.CPU+b.CPU, 0) {
		return a, errors.New("invalid peer resource total")
	}
	return a.Add(b), nil
}

// PeerSnapshot is a bounded, narrow database projection. It never calls Status,
// takes Manager.mu, samples native resources, or initializes/repairs state.
// The server caches this on its own fixed-rate worker; peer requests never run it.
func (m *Manager) PeerSnapshot() (model.PeerSnapshot, error) {
	return m.PeerSnapshotContext(context.Background())
}
func (m *Manager) PeerSnapshotContext(ctx context.Context) (model.PeerSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	out := model.PeerSnapshot{Version: 1, Host: m.host, At: time.Now().UTC(), Items: []model.PeerItem{}}
	rows, err := m.db.QueryContext(ctx, `SELECT substr(CAST(data AS BLOB),1,65537) FROM leases WHERE json_extract(data,'$.state') IS NOT 'terminal' ORDER BY json_extract(data,'$.created_at'),id LIMIT 4097`)
	if err != nil {
		return model.PeerSnapshot{}, err
	}
	defer rows.Close()
	count, totalBytes := 0, 0
	for rows.Next() {
		if err = ctx.Err(); err != nil {
			return model.PeerSnapshot{}, err
		}
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return model.PeerSnapshot{}, err
		}
		count++
		totalBytes += len(raw)
		if count > 4096 || len(raw) > 64<<10 || totalBytes > 8<<20 {
			return model.PeerSnapshot{}, errors.New("peer projection exceeds bounded lease/byte inventory")
		}
		var l struct {
			State   string     `json:"state"`
			Expires *time.Time `json:"expires_at"`
			Request struct {
				Resources     model.Resources   `json:"resources"`
				TTL           time.Duration     `json:"ttl_ns"`
				StartDeadline *time.Time        `json:"start_deadline_at,omitempty"`
				Pools         []json.RawMessage `json:"pools"`
				Ports         []json.RawMessage `json:"ports"`
				StorageLease  string            `json:"storage_lease,omitempty"`
				PortLease     string            `json:"port_lease,omitempty"`
				ServiceLease  string            `json:"service_lease,omitempty"`
			} `json:"request"`
			QueueInfo *struct {
				Position int
				Estimate struct {
					Start *time.Time `json:"start_at"`
					Kind  string
				}
			} `json:"queue_info,omitempty"`
		}
		if err = json.Unmarshal([]byte(raw), &l); err != nil {
			return model.PeerSnapshot{}, errors.New("invalid peer projection lease record")
		}
		if !consuming(l.State) && l.State != "queued" && l.State != "reclaiming" {
			return model.PeerSnapshot{}, errors.New("unsupported peer projection lease state")
		}
		item := model.PeerItem{State: l.State, Resources: l.Request.Resources, Expires: l.Expires, StartDeadline: l.Request.StartDeadline, RequestedTTL: l.Request.TTL, OtherClaims: len(l.Request.Pools) > 0 || len(l.Request.Ports) > 0 || l.Request.PortLease != "" || l.Request.StorageLease != "" || l.Request.ServiceLease != ""}
		out.OtherClaims = out.OtherClaims || item.OtherClaims
		if l.State == "queued" {
			out.QueuedCount++
			out.Queued, err = addPeerResources(out.Queued, item.Resources)
			if l.QueueInfo != nil {
				item.QueuePosition = l.QueueInfo.Position
				item.EstimatedStart = l.QueueInfo.Estimate.Start
				item.EstimateKind = l.QueueInfo.Estimate.Kind
			}
		} else {
			out.HeldCount++
			out.Held, err = addPeerResources(out.Held, item.Resources)
		}
		if err != nil {
			return model.PeerSnapshot{}, err
		}
		if len(out.Items) < maxPeerItems {
			out.Items = append(out.Items, item)
		} else {
			out.Truncated = true
		}
	}
	if err = rows.Err(); err != nil {
		return model.PeerSnapshot{}, err
	}
	return out, nil
}
