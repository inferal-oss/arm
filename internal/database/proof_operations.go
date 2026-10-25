package database

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/model"
)

func (p *proofReader) readOperations() error {
	if err := p.walk("pool_operations", func(r proofRow) error {
		key, e := r.text("key")
		if e != nil {
			return e
		}
		q, e := rowJSON[model.PoolMutation](r, "request", "operation", "reason", "idempotency_key")
		if e != nil {
			return e
		}
		requestRaw, _ := r.text("request")
		if e = p.validatePoolReviewRecord(requestRaw, q); e != nil {
			return e
		}
		v, e := rowJSON[model.PoolChange](r, "result", "sequence", "operation", "idempotency_key", "actor", "reason", "at", "after", "delta")
		if e != nil {
			return e
		}
		journal, ok := p.poolChanges[v.Sequence]
		if !ok || !equalEvidence(journal, v) || key != q.Key || key != v.Key || q.Operation != v.Operation || q.Reason != v.Reason || q.ID != "" && q.ID != v.Pool.ID {
			return fmt.Errorf("pool operation/result/journal mismatch")
		}
		if p.poolOperationSequences[v.Sequence] {
			return fmt.Errorf("duplicate pool operation journal scope")
		}
		p.poolOperationSequences[v.Sequence] = true
		if e = matchPoolTransition(q, v); e != nil {
			return e
		}
		if v.Operation == "create" && v.Pool.Parent != "" {
			if q.ParentRevision == nil || *q.ParentRevision != p.poolParentRevisions[v.Sequence] {
				return fmt.Errorf("child creation request differs from historical parent revision")
			}
		} else if q.Parent != "" || q.ParentRevision != nil {
			return fmt.Errorf("unexpected immutable parent mutation request")
		}
		return nil
	}); err != nil {
		return err
	}
	if err := p.walk("budget_operations", func(r proofRow) error {
		key, e := r.text("key")
		if e != nil {
			return e
		}
		q, e := rowJSON[model.BudgetMutation](r, "request", "operation", "reason", "idempotency_key")
		if e != nil {
			return e
		}
		v, e := rowJSON[model.BudgetChange](r, "result", "sequence", "operation", "idempotency_key", "actor", "reason", "at", "after")
		if e != nil {
			return e
		}
		journal, ok := p.budgetChanges[v.Sequence]
		if !ok || !equalEvidence(journal, v) || key != q.Key || key != v.Key || q.Operation != v.Operation || q.Reason != v.Reason || q.ID != "" && q.ID != v.Budget.ID {
			return fmt.Errorf("policy operation/result/journal mismatch")
		}
		if p.budgetOperationSequences[v.Sequence] {
			return fmt.Errorf("duplicate policy operation journal scope")
		}
		p.budgetOperationSequences[v.Sequence] = true
		if q.Limits != nil && !equalEvidence(*q.Limits, v.Budget.Limits) || q.Revision != nil && (v.Previous == nil || *q.Revision != v.Previous.Revision) {
			return fmt.Errorf("policy request does not match result transition")
		}
		return nil
	}); err != nil {
		return err
	}
	if err := p.walk("message_operations", func(r proofRow) error {
		key, e := r.text("key")
		if e != nil {
			return e
		}
		id, e := r.text("message")
		if e != nil {
			return e
		}
		q, e := rowJSON[proofMessageRequest](r, "request", "idempotency_key", "audience", "body", "targets")
		if e != nil {
			return e
		}
		v, ok := p.messages[id]
		if !ok || key != q.Key || v.Key != key || q.Audience != v.Audience || q.Body != v.Body || q.Lease != v.Lease || q.ReplyTo != v.ReplyTo || q.AgentKey != v.AgentKey || !equalEvidence(q.Source, v.SenderContact) {
			return fmt.Errorf("message operation/result mismatch")
		}
		if len(q.Targets) != len(v.Deliveries) {
			return fmt.Errorf("reviewed target/delivery scope differs")
		}
		for _, target := range q.Targets {
			if !validHistoricalContact(target) {
				return fmt.Errorf("invalid reviewed recipient identity")
			}
			delivery, ok := v.Deliveries[target.ID]
			if !ok || delivery.Registration != target.Registration {
				return fmt.Errorf("reviewed recipient identity differs")
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return p.walk("mesh_operations", func(r proofRow) error {
		peer, e := r.text("principal")
		if e != nil {
			return e
		}
		key, e := r.text("key")
		if e != nil {
			return e
		}
		op, e := r.text("operation")
		if e != nil {
			return e
		}
		if !validHost(peer) || !evidenceID(key) || !oneOf(op, "release", "cancel", "renew") {
			return fmt.Errorf("unsupported peer operation intent")
		}
		var admission struct {
			Peer        string    `json:"peer"`
			Host        string    `json:"host"`
			Certificate string    `json:"certificate"`
			Revision    int       `json:"grant_revision"`
			At          time.Time `json:"admitted_at"`
		}
		raw, e := r.text("admission")
		if e != nil {
			return e
		}
		if e = strictEvidence(raw, &admission, "peer", "host", "certificate", "grant_revision", "admitted_at"); e != nil {
			return e
		}
		if admission.Peer != peer || admission.Host != p.result.Host || admission.At.IsZero() {
			return fmt.Errorf("peer admission authority differs")
		}
		var request struct {
			ID     string        `json:"id"`
			TTL    time.Duration `json:"ttl_ns,omitempty"`
			Offset int64         `json:"offset,omitempty"`
		}
		raw, e = r.text("request")
		if e != nil {
			return e
		}
		if e = strictEvidence(raw, &request, "id"); e != nil {
			return e
		}
		l, ok := p.leases[request.ID]
		if !ok || l.Request.Peer != peer {
			return fmt.Errorf("peer operation references another/missing principal lease")
		}
		if r["result"] == nil {
			return p.block("mesh_operation", key, "Peer operation has an intent without a durable result")
		}
		var response struct {
			Value json.RawMessage `json:"value"`
			Error *model.Error    `json:"error,omitempty"`
		}
		raw, e = r.text("result")
		if e != nil {
			return e
		}
		if e = strictEvidence(raw, &response); e != nil {
			return e
		}
		if response.Error == nil {
			var value model.Lease
			if e = strictEvidence(string(response.Value), &value, "id", "host", "owner", "request", "state", "revision"); e != nil {
				return e
			}
			if value.ID != l.ID || value.Host != p.result.Host || value.Owner != p.result.Owner || value.Request.Peer != peer {
				return fmt.Errorf("peer result authority differs")
			}
		} else if response.Error.Code == "" || response.Error.Message == "" {
			return fmt.Errorf("invalid peer error result")
		}
		return nil
	})
}
func (p *proofReader) readOther() error {
	if err := p.walk("agents", func(r proofRow) error {
		v, e := rowJSON[agents.Contact](r, "data", "id", "adapter", "session", "cwd", "registration")
		if e != nil {
			return e
		}
		id, e := r.text("id")
		if e != nil {
			return e
		}
		if v.ID != id || !validHistoricalContact(v) {
			return fmt.Errorf("invalid historical contact identity/delivery")
		}
		return nil
	}); err != nil {
		return err
	}
	if err := p.walk("policy_outbox", func(r proofRow) error {
		id, e := r.text("message")
		if e != nil {
			return e
		}
		state, e := r.text("state")
		if e != nil {
			return e
		}
		var v struct {
			Incident string         `json:"incident"`
			Target   agents.Contact `json:"target"`
		}
		raw, e := r.text("data")
		if e != nil {
			return e
		}
		if e = strictEvidence(raw, &v, "incident", "target"); e != nil {
			return e
		}
		incident, ok := p.incidents[v.Incident]
		msg, has := p.messages[id]
		delivery, delivered := msg.Deliveries[v.Target.ID]
		if !ok || !has || !delivered || delivery.Registration != v.Target.Registration {
			return fmt.Errorf("policy outbox references missing/mismatched evidence")
		}
		found := false
		for _, message := range incident.Messages {
			if message == id {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("policy warning missing from incident scope")
		}
		if !oneOf(state, "done", "skipped") {
			return p.block("policy_outbox", id, "Native warning delivery is unsettled")
		}
		return nil
	}); err != nil {
		return err
	}
	if err := p.walk("agent_inbox", func(r proofRow) error {
		seq, e := r.integer("sequence")
		if e != nil {
			return e
		}
		id, e := r.text("message")
		if e != nil {
			return e
		}
		target, e := r.text("recipient")
		if e != nil {
			return e
		}
		registration, e := r.text("registration")
		if e != nil {
			return e
		}
		msg, ok := p.messages[id]
		delivery, exists := msg.Deliveries[target]
		if seq < 1 || !ok || !exists || delivery.Registration != registration {
			return fmt.Errorf("inbox references missing/mismatched message delivery")
		}
		_, e = rowJSON[agents.Receipt](r, "receipt")
		return e
	}); err != nil {
		return err
	}
	if err := p.walk("cancelled_requests", func(r proofRow) error {
		key, e := r.text("key")
		if e != nil {
			return e
		}
		if !evidenceID(key) {
			return fmt.Errorf("invalid cancelled operation key")
		}
		return nil
	}); err != nil {
		return err
	}
	if err := p.walk("sqlite_sequence", func(r proofRow) error {
		name, e := r.text("name")
		if e != nil {
			return e
		}
		seq, e := r.integer("seq")
		if e != nil {
			return e
		}
		if seq < 0 || !oneOf(name, "events", "agent_inbox", "pool_changes", "budget_changes", "workspace_changes", "routing_events", "lease_reviews") || name == "workspace_changes" && !p.workspaceSchema || name == "routing_events" && !p.routingSchema || name == "lease_reviews" && !p.leaseReviewSchema {
			return fmt.Errorf("unknown sequence authority")
		}
		if _, exists := p.recordedSequences[name]; exists {
			return fmt.Errorf("duplicate sequence authority")
		}
		p.recordedSequences[name] = seq
		return nil
	}); err != nil {
		return err
	}
	return p.walk("events", func(r proofRow) error {
		seq, e := r.integer("sequence")
		if e != nil {
			return e
		}
		kind, e := r.text("type")
		if e != nil {
			return e
		}
		entity, e := r.text("entity")
		if e != nil {
			return e
		}
		at, e := r.text("at")
		if e != nil {
			return e
		}
		stamp, e := time.Parse(time.RFC3339Nano, at)
		if e != nil || stamp.IsZero() || seq < 1 || len(kind) > 128 || kind == "" || len(entity) > 256 || strings.ContainsAny(kind, "\x00\n\r") {
			return fmt.Errorf("invalid audit event envelope")
		}
		if r["data"] != nil {
			raw, e := r.text("data")
			if e != nil {
				return e
			}
			var object any
			return strictEvidence(raw, &object)
		}
		return nil
	})
}
