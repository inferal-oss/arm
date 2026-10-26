package cli

import (
	"errors"
	"fmt"
	"github.com/inferal-oss/arm/internal/routing"
	api "github.com/inferal-oss/arm/internal/workspaceapi"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/inferal-oss/arm/internal/agents"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/manager"
	"github.com/inferal-oss/arm/internal/model"
)

var errAgentDeliveryReported = errors.New("recorded agent delivery failure already printed")

func (a *app) agentMessage(action string, args []string) error {
	f := a.flags("agent " + action)
	var request manager.AgentMessageRequest
	f.StringVar(&request.ID, "id", "", "your registered sender contact ID")
	f.StringVar(&request.Session, "session", "", "your exact registered session UUID")
	f.StringVar(&request.Registration, "registration", "", "your exact registration identity (required)")
	f.StringVar(&request.Key, "key", "", "exact request key (generated for sends; required for lookup)")
	toRegistration := ""
	if action == "send" {
		f.StringVar(&request.Audience, "to", "", "one registered recipient ID, or operator")
		f.StringVar(&toRegistration, "to-registration", "", "optional exact recipient registration to pin")
	}
	if action == "reply" {
		f.StringVar(&request.ReplyTo, "message", "", "original message ID received by your registration")
	}
	if action != "message-lookup" {
		f.StringVar(&request.Body, "text", "", "message text, or - to read stdin")
		f.StringVar(&request.Lease, "lease", "", "related lease ID")
	}
	workspaceID := f.String("workspace", "", "explicit workspace routing; otherwise this host only")
	host := f.String("host", "", "exact admitted destination host for a workspace send")
	endpoint := f.String("endpoint", "", "opaque published destination endpoint ID")
	generation := f.String("generation", "", "exact destination publication generation")
	ttl := f.Duration("ttl", time.Hour, "workspace message lifetime, maximum 24h")
	reconcile := f.Bool("reconcile", false, "lookup final routed receipt without resending")
	f.Bool("json", false, "force JSON output")
	if err := a.parse(f, args, false); err != nil {
		return err
	}
	id, err := uuid.Parse(request.Session)
	if len(f.Args()) != 0 || request.ID == "" || request.Registration == "" || err != nil {
		return model.Err("invalid_request", "provide --id, exact --session UUID and --registration; no positional arguments")
	}
	request.Session = id.String()
	if len(request.Key) > 128 {
		return model.Err("invalid_request", "message key must be at most 128 bytes")
	}
	if *workspaceID != "" {
		if request.Lease != "" || request.Audience != "" || toRegistration != "" {
			return model.Err("invalid_request", "workspace messages use --host/--endpoint/--generation rather than local --to/--lease selectors")
		}
		return a.workspaceAgentMessage(action, api.AgentMessage{Workspace: *workspaceID, ID: request.ID, Session: request.Session, Registration: request.Registration, Key: request.Key, Target: *host, To: routing.Endpoint{Kind: "agent", ID: *endpoint, Generation: *generation}, Message: request.ReplyTo, Body: request.Body, TTL: *ttl, Reconcile: *reconcile})
	}
	if *host != "" || *endpoint != "" || *generation != "" || *reconcile {
		return model.Err("invalid_request", "routed selectors require explicit --workspace")
	}
	if action == "message-lookup" {
		if request.Key == "" {
			return model.Err("invalid_request", "lookup requires the original --key")
		}
		var result manager.MessageLookup
		if err := a.call("agent_message_lookup", request, &result); err != nil {
			return err
		}
		return a.print(result)
	}
	if request.Body == "-" {
		body, err := io.ReadAll(io.LimitReader(a.in, 16385))
		if err != nil {
			return err
		}
		request.Body = string(body)
	}
	if request.Body == "" || len(request.Body) > 16384 || (action == "reply" && request.ReplyTo == "") || (action == "send" && (request.Audience == "" || request.Audience == "broadcast")) {
		return model.Err("invalid_request", "provide 1 to 16384 text bytes and --to CONTACT|operator for send or --message ID for reply; agent broadcasts are not supported")
	}
	if request.Key == "" {
		request.Key = local.ID()
	}
	if action == "send" {
		request.Targets = []agents.Contact{}
		if request.Audience != "operator" {
			var contacts []agents.Contact
			if err := a.call("agents", nil, &contacts); err != nil {
				return err
			}
			for _, contact := range contacts {
				if contact.ID == request.Audience {
					if toRegistration != "" && toRegistration != contact.Registration {
						return model.Err("contact_changed", "recipient registration differs from --to-registration")
					}
					request.Targets = append(request.Targets, contact)
				}
			}
			if len(request.Targets) != 1 {
				return model.Err("not_found", "recipient is not registered; inspect arm agent list --json")
			}
		} else if toRegistration != "" {
			return model.Err("invalid_request", "operator inbox has no agent registration")
		}
	}
	var result manager.Message
	if err := a.call("agent_message", request, &result); err != nil {
		if a.ctx.Err() != nil {
			return model.Err("agent_message_outcome_unknown", fmt.Sprintf("send interrupted; agent message key %q may have committed. Inspect with arm agent message-lookup using the same sender selectors before an exact retry", request.Key))
		}
		var api *model.Error
		if errors.As(err, &api) {
			return model.Err(api.Code, fmt.Sprintf("%s (agent message key %q; inspect with arm agent message-lookup using the same sender selectors)", api.Message, request.Key))
		}
		return fmt.Errorf("agent message key %q; inspect with arm agent message-lookup before an exact retry: %w", request.Key, err)
	}
	if err := a.print(result); err != nil {
		return err
	}
	for _, delivery := range result.Deliveries {
		if delivery.State == "failed" {
			return errAgentDeliveryReported
		}
	}
	return nil
}
