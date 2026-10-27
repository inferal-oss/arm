package database

import (
	"fmt"
	"github.com/google/uuid"
	"path/filepath"
	"strings"
	"time"

	"github.com/inferal-oss/arm/internal/agents"
	"github.com/inferal-oss/arm/internal/model"
)

// Mirrors are deliberately local because importing manager would create a
// cycle. Unknown fields fail closed until this independent proof is reviewed.
type proofMessage struct {
	RoutedOrigin        *proofPublishedContext     `json:"routed_origin,omitempty"`
	SenderContact       *agents.Contact            `json:"sender_contact,omitempty"`
	AgentKey            string                     `json:"agent_operation_key,omitempty"`
	NativeReplies       map[string]string          `json:"native_replies,omitempty"`
	OriginCertificate   string                     `json:"origin_certificate,omitempty"`
	OriginGrantRevision int                        `json:"origin_grant_revision,omitempty"`
	OriginHost          string                     `json:"origin_host,omitempty"`
	OriginKey           string                     `json:"origin_key,omitempty"`
	ID                  string                     `json:"id"`
	Owner               string                     `json:"owner,omitempty"`
	OwnerUsername       string                     `json:"owner_username,omitempty"`
	Sender              string                     `json:"sender"`
	ReplyTo             string                     `json:"reply_to,omitempty"`
	Key                 string                     `json:"idempotency_key,omitempty"`
	Audience            string                     `json:"audience"`
	Body                string                     `json:"body"`
	Lease               string                     `json:"lease,omitempty"`
	Created             time.Time                  `json:"created_at"`
	Deliveries          map[string]agents.Delivery `json:"deliveries"`
}
type proofMessageRequest struct {
	Key      string           `json:"idempotency_key"`
	Audience string           `json:"audience"`
	Body     string           `json:"body"`
	Lease    string           `json:"lease,omitempty"`
	ReplyTo  string           `json:"reply_to,omitempty"`
	Targets  []agents.Contact `json:"targets"`
	Source   *agents.Contact  `json:"sender_contact,omitempty"`
	AgentKey string           `json:"agent_operation_key,omitempty"`
}
type proofGradle struct {
	Lease            string                        `json:"lease"`
	Storage          string                        `json:"storage"`
	Name             string                        `json:"name"`
	Identity         *model.FileIdentity           `json:"identity"`
	Children         map[string]model.FileIdentity `json:"children"`
	Nonce            string                        `json:"nonce"`
	Phase            string                        `json:"phase"`
	Binding          model.WorkloadBinding         `json:"binding"`
	Probe            *model.ProcessIdentity        `json:"probe,omitempty"`
	Uncertain        bool                          `json:"uncertain"`
	BlockedBy        string                        `json:"blocked_by,omitempty"`
	FinalOutcome     string                        `json:"final_outcome,omitempty"`
	ExpectedPID      int                           `json:"expected_pid,omitempty"`
	ServiceHeap      int64                         `json:"service_heap"`
	ServiceMetaspace int64                         `json:"service_metaspace"`
}

func (p *proofReader) readMessages() error {
	return p.walk("messages", func(r proofRow) error {
		v, err := rowJSON[proofMessage](r, "data", "id", "sender", "audience", "body", "created_at", "deliveries")
		if err != nil {
			return err
		}
		id, err := r.text("id")
		if err != nil {
			return err
		}
		if v.ID != id || !evidenceID(id) || v.Owner != "" && v.Owner != p.result.Owner || v.Created.IsZero() || v.Sender == "" || v.Audience == "" || v.Body == "" || len(v.Body) > 16384 || len(v.Deliveries) > 128 {
			return fmt.Errorf("invalid message identity/owner/body")
		}
		if v.Lease != "" {
			if _, ok := p.leases[v.Lease]; !ok {
				return fmt.Errorf("message references missing lease")
			}
		}
		for target, d := range v.Deliveries {
			if !evidenceID(target) {
				return fmt.Errorf("invalid delivery recipient")
			}
			if !oneOf(d.State, "available", "queued", "responded", "failed", "superseded") {
				if err = p.block("delivery", id, "A native delivery has a pending, unknown or unsupported outcome"); err != nil {
					return err
				}
			}
			if d.Mode != "" && !oneOf(d.Mode, "native", "poll") {
				return fmt.Errorf("unknown delivery mode")
			}
			if d.Receipt != nil {
				if d.Receipt.ReplyID != "" && !evidenceID(d.Receipt.ReplyID) {
					return fmt.Errorf("invalid receipt reply identity")
				}
			}
		}
		if v.SenderContact != nil && !validHistoricalContact(*v.SenderContact) {
			return fmt.Errorf("invalid recorded agent sender identity")
		}
		if v.RoutedOrigin != nil {
			link, ok := p.publishedLinks[id]
			if !ok || link.Context != *v.RoutedOrigin {
				return fmt.Errorf("routed message lost independent publication link")
			}
		}
		p.messages[id] = v
		return nil
	})
}
func (p *proofReader) readIncidents() error {
	return p.walk("usage_incidents", func(r proofRow) error {
		v, err := rowJSON[model.UsageIncident](r, "data", "id", "lease", "host", "owner", "resource", "state", "opened_at", "updated_at", "revision")
		if err != nil {
			return err
		}
		id, err := r.text("id")
		if err != nil {
			return err
		}
		lease, err := r.text("lease")
		if err != nil {
			return err
		}
		resource, err := r.text("resource")
		if err != nil {
			return err
		}
		if v.ID != id || !evidenceID(id) || v.Lease != lease || v.Resource != resource || v.Host != p.result.Host || v.Owner != p.result.Owner || v.Revision < 1 || v.Opened.IsZero() || v.Updated.Before(v.Opened) {
			return fmt.Errorf("incident identity/authority differs")
		}
		if _, ok := p.leases[lease]; !ok {
			return fmt.Errorf("incident references missing lease")
		}
		if v.State != "closed" {
			if err = p.block("incident", id, "Enforcement incident has not been closed"); err != nil {
				return err
			}
		}
		for _, id := range v.Messages {
			if _, ok := p.messages[id]; !ok {
				return fmt.Errorf("incident message missing")
			}
		}
		p.incidents[id] = v
		return nil
	})
}
func (p *proofReader) readExports() error {
	return p.walk("storage_exports", func(r proofRow) error {
		v, err := rowJSON[model.StorageExport](r, "data", "id", "request", "host", "owner", "plan", "state", "running", "created_at", "updated_at")
		if err != nil {
			return err
		}
		id, err := r.text("id")
		if err != nil {
			return err
		}
		key, err := r.text("request_key")
		if err != nil {
			return err
		}
		lease, err := r.text("lease")
		if err != nil {
			return err
		}
		state, err := r.text("state")
		if err != nil {
			return err
		}
		request, err := rowJSON[model.StorageExportRequest](r, "request", "idempotency_key", "lease", "destination", "files", "reason")
		if err != nil {
			return err
		}
		if v.ID != id || !evidenceID(id) || v.Request.Key != key || v.Request.Lease != lease || v.State != state || !equalEvidence(v.Request, request) || v.Host != p.result.Host || v.Owner != p.result.Owner || v.Created.IsZero() || v.Updated.Before(v.Created) {
			return fmt.Errorf("export identity/authority/request differs")
		}
		source, ok := p.leases[lease]
		if !ok || source.Request.Kind != "storage" {
			return fmt.Errorf("export source is missing or not storage")
		}
		if v.Running || !oneOf(state, "published", "handed_off") {
			return p.block("export", id, "Storage export is running or lacks settled publication/handoff")
		}
		if state == "handed_off" {
			if v.Handoff == nil || v.Handoff.Actor != p.result.Owner || v.Handoff.At.IsZero() || v.Handoff.Reason == "" {
				return fmt.Errorf("export handoff evidence missing")
			}
		} else if v.DestinationIdentity == nil || v.StageIdentity == nil || len(v.Plan.Files) == 0 || v.Plan.Destination != request.Destination || !filepath.IsAbs(request.Destination) {
			return fmt.Errorf("published export lacks namespace identity evidence")
		}
		for _, f := range v.Plan.Files {
			if f.Size < 0 || state == "published" && (len(f.SHA256) != 64 || f.OutputIdentity == nil) {
				return fmt.Errorf("export file publication evidence incomplete")
			}
		}
		return nil
	})
}
func (p *proofReader) readGradle() error {
	return p.walk("gradle_workloads", func(r proofRow) error {
		v, err := rowJSON[proofGradle](r, "data", "lease", "storage", "name", "identity", "children", "nonce", "phase", "binding", "uncertain", "service_heap", "service_metaspace")
		if err != nil {
			return err
		}
		id, err := r.text("lease")
		if err != nil {
			return err
		}
		l, ok := p.leases[id]
		store, hasStore := p.leases[v.Storage]
		if v.Lease != id || !ok || !hasStore || store.Request.Kind != "storage" || l.Request.StorageLease != v.Storage || !oneOf(l.Request.Profile, "gradle-build", "gradle-service") || v.Nonce == "" || !safeName(v.Name) || v.Identity == nil || !equalEvidence(l.Workload, &v.Binding) {
			return fmt.Errorf("Gradle evidence identity/binding/provider differs")
		}
		if v.Phase != "finished" || v.Uncertain || !oneOf(v.Binding.State, "settled", "stopped") {
			if err = p.block("gradle", id, "Persistent workload service or descendants have not settled"); err != nil {
				return err
			}
		}
		p.gradle[id] = v
		return nil
	})
}

func validHistoricalContact(c agents.Contact) bool {
	if !evidenceID(c.ID) || oneOf(c.ID, "operator", "broadcast") || !validHost(c.Registration) || !filepath.IsAbs(c.CWD) || c.ConfigDir != "" && !filepath.IsAbs(c.ConfigDir) || !oneOf(c.Delivery, "native", "poll") {
		return false
	}
	if _, err := uuid.Parse(c.Session); err != nil {
		return false
	}
	if !oneOf(c.Adapter, "codex", "claude", "generic") || c.Adapter == "generic" && c.Delivery != "poll" || c.Adapter == "claude" && c.Delivery == "native" && !c.Idle {
		return false
	}
	return c.Remote == "" || c.Adapter == "codex" && strings.HasPrefix(c.Remote, "unix://")
}
