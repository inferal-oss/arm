package manager

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

type bulkOperation struct {
	Request       model.BulkEndRequest `json:"request"`
	Actor         string               `json:"actor"`
	ActorUsername string               `json:"actor_username,omitempty"`
	At            time.Time            `json:"started_at"`
	IDs           []string             `json:"ids"`
}

// BulkEnd captures its exact scope durably before ending any lease. Retrying a
// key can complete that scope, but cannot sweep up work created afterwards.
func (m *Manager) BulkEnd(request model.BulkEndRequest) (model.BulkEndReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bulkEnd(request)
}

func leaseResult(l model.Lease, err error) model.LeaseResult {
	r := model.LeaseResult{ID: l.ID, State: l.State, Outcome: l.Outcome, Detail: l.Failure}
	if err != nil {
		var api *model.Error
		if errors.As(err, &api) {
			copy := *api
			r.Error = &copy
		} else {
			r.Error = &model.Error{Code: "operation_failed", Message: "lease operation failed; inspect the allocation and retry the same operation key"}
		}
	}
	return r
}

func outstanding(l model.Lease) bool { return consuming(l.State) || l.State == "queued" }

func (m *Manager) bulkEnd(request model.BulkEndRequest) (model.BulkEndReport, error) {
	var report model.BulkEndReport
	if request.Action != "release" && request.Action != "cancel" || request.Key == "" || len(request.Key) > 128 {
		return report, model.Err("invalid_request", "bulk end requires release or cancel and an operation key (1..128 bytes)")
	}
	if m.closing {
		return report, model.Err("backend_stopping", "backend is shutting down")
	}
	key := "bulk:" + request.Key
	var raw []byte
	err := m.db.QueryRow(`SELECT value FROM metadata WHERE key=?`, key).Scan(&raw)
	var operation bulkOperation
	if err == nil {
		if err = json.Unmarshal(raw, &operation); err != nil {
			return report, err
		}
		if operation.Request != request {
			return report, model.Err("idempotency_conflict", "bulk operation key was used with a different action")
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		ls, err := m.list()
		if err != nil {
			return report, err
		}
		// Consumers precede their managed parents. Parent providers retain their
		// ordinary checks and can still defer reclamation until a child settles.
		sort.SliceStable(ls, func(i, j int) bool {
			return leaseDependencyRank(ls[i]) < leaseDependencyRank(ls[j])
		})
		operation = bulkOperation{Request: request, Actor: m.owner, ActorUsername: m.ownerUsername, At: time.Now().UTC(), IDs: []string{}}
		for _, l := range ls {
			if outstanding(l) && l.Host == m.host && l.Owner == m.owner {
				operation.IDs = append(operation.IDs, l.ID)
			}
		}
		b, _ := json.Marshal(operation)
		tx, err := m.db.Begin()
		if err != nil {
			return report, err
		}
		defer tx.Rollback()
		if _, err = tx.Exec(`INSERT INTO metadata(key,value) VALUES(?,?)`, key, string(b)); err != nil {
			return report, err
		}
		if err = event(tx, "leases.bulk_started", request.Key, operation); err != nil {
			return report, err
		}
		if err = tx.Commit(); err != nil {
			return report, err
		}
	} else {
		return report, err
	}
	report = model.BulkEndReport{Key: request.Key, Action: request.Action, At: operation.At, Results: []model.LeaseResult{}, Remaining: []model.LeaseResult{}}
	for _, id := range operation.IDs {
		l, err := m.get(id)
		if err == nil && (l.Host != m.host || l.Owner != m.owner) {
			err = model.Err("authority_unavailable", "allocation does not belong to this local host and user")
		}
		if err == nil {
			l, err = m.end(id, map[string]string{"release": "released", "cancel": "cancelled"}[request.Action])
		}
		item := leaseResult(l, err)
		item.ID = id
		report.Results = append(report.Results, item)
		if outstanding(l) || err != nil {
			report.Remaining = append(report.Remaining, item)
		}
	}
	return report, nil
}
