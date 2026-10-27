package cli

import (
	"encoding/json"
	"fmt"
	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"getarm.org/internal/published"
	api "getarm.org/internal/workspaceapi"
	"io"
	"time"
)

func (a *app) agentPublished(args []string) error {
	if a.group("agent publish", args) {
		return nil
	}
	if len(args) == 0 {
		return model.Err("invalid_request", "use arm agent publish plan|apply|withdraw|list")
	}
	action, args := args[0], args[1:]
	f := a.flags("agent publish " + action)
	id := f.String("workspace", "", "exact local workspace ID")
	key := new(string)
	if action != "list" {
		key = f.String("key", "", "immutable operation key")
	}
	f.Bool("json", false, "JSON output (automatic outside terminal)")
	if action == "list" {
		offset := f.Int("offset", 0, "offset in immutable endpoint order")
		limit := f.Int("limit", 8, "complete records per page (1–8)")
		if e := a.parse(f, args, false); e != nil {
			return e
		}
		if len(f.Args()) != 0 {
			return model.Err("invalid_request", "no positional arguments")
		}
		var out json.RawMessage
		if e := a.call("workspace_agent_publications", api.AgentPublicationPage{Workspace: *id, Offset: *offset, Limit: *limit}, &out); e != nil {
			return e
		}
		return a.print(out)
	}
	reason := f.String("reason", "", "required purpose recorded in local journal")
	yes, dry := new(bool), new(bool)
	if action == "apply" || action == "withdraw" {
		yes = f.Bool("yes", false, "approve this exact reviewed publication change")
		dry = f.Bool("dry-run", false, "preview only; do not change publication state")
	}
	if action == "withdraw" {
		endpoint := f.String("endpoint", "", "exact opaque published endpoint ID")
		generation := f.String("generation", "", "exact immutable publication generation")
		revision := f.Int64("revision", 0, "exact active publication revision")
		if e := a.parse(f, args, false); e != nil {
			return e
		}
		if len(f.Args()) != 0 || !published.Hex(*id, 16) || !published.Hex(*endpoint, 16) || !published.Hex(*generation, 16) || !published.Text(*key, 128) || !published.Text(*reason, 512) || *revision != 1 {
			return model.Err("invalid_request", "exact workspace, endpoint, generation, revision, key and reason required")
		}
		q := api.AgentPublicationWithdraw{Workspace: *id, Endpoint: published.Address{Kind: "agent", ID: *endpoint, Generation: *generation}, Request: published.Withdrawal{Key: *key, Reason: *reason, Revision: *revision}}
		if !a.machine() {
			fmt.Fprintf(a.out, "%s\n  Workspace %s\n  Endpoint %s · generation %s\n  Effect Stop new deliveries to this exact publication; retained messages and dedup records remain.\n", a.paint("1;33", "Withdraw published agent"), *id, *endpoint, *generation)
		}
		if *dry && *yes {
			return model.Err("invalid_request", "--dry-run cannot be combined with --yes")
		}
		if *dry {
			return a.print(map[string]any{"dry_run": true, "request": q})
		}
		if e := a.publicationConfirm(*yes); e != nil {
			return e
		}
		var out json.RawMessage
		if e := a.call("workspace_agent_withdraw", q, &out); e != nil {
			return e
		}
		return a.print(out)
	}
	if action != "plan" && action != "apply" {
		return model.Err("invalid_request", "use arm agent publish plan|apply|withdraw|list")
	}
	contact := f.String("id", "", "exact registered local contact ID")
	session := f.String("session", "", "exact registered session UUID")
	registration := f.String("registration", "", "exact current contact registration")
	label := f.String("label", "", "public workspace label (no private paths or session IDs)")
	delivery := f.String("delivery", "poll", "publication delivery; only poll is supported")
	expect := new(string)
	if action == "apply" {
		expect = f.String("expect", "", "SHA256 of previously inspected full publication review")
	}
	if e := a.parse(f, args, false); e != nil {
		return e
	}
	if len(f.Args()) != 0 {
		return model.Err("invalid_request", "no positional arguments")
	}
	q := published.Request{Workspace: *id, ID: *contact, Session: *session, Registration: *registration, Label: *label, Delivery: *delivery, Key: *key, Reason: *reason}
	if e := q.Validate(); e != nil {
		return e
	}
	if *dry && *yes {
		return model.Err("invalid_request", "--dry-run cannot be combined with --yes")
	}
	if action == "plan" || *dry {
		var plan published.Plan
		if e := a.call("workspace_agent_plan", q, &plan); e != nil {
			return e
		}
		return a.print(plan)
	}
	if !published.Hex(*expect, 32) {
		return model.Err("invalid_request", "apply requires the exact --expect SHA256 returned by publish plan")
	}
	if !a.machine() {
		fmt.Fprintf(a.out, "%s\n  Workspace %s\n  Contact %s · registration %s\n  Session %s\n  Public label %s\n  Delivery Poll only; no native CLI invocation\n  Reason %s\n  Review %s\n  Effect Explicitly expose an opaque endpoint in this workspace. Changing this contact automatically withdraws it.\n", a.paint("1;36", "Publish exact registered agent"), *id, terminalSafe(*contact), *registration, terminalSafe(*session), terminalSafe(*label), terminalSafe(*reason), *expect)
	}
	if e := a.publicationConfirm(*yes); e != nil {
		return e
	}
	var out json.RawMessage
	if e := a.call("workspace_agent_publish", api.AgentPublicationCommit{Request: q, Expect: *expect}, &out); e != nil {
		return e
	}
	return a.print(out)
}
func (a *app) agentEndpoints(args []string) error {
	f := a.flags("agent endpoints")
	id := f.String("workspace", "", "exact admitted workspace ID")
	host := f.String("host", "", "exact final admitted host ID")
	offset := f.Int("offset", 0, "offset in stable endpoint order")
	limit := f.Int("limit", 16, "maximum cards (1–16)")
	revision := f.Int64("revision", 0, "publication page revision; pin on subsequent pages")
	f.Bool("json", false, "JSON output")
	if e := a.parse(f, args, false); e != nil {
		return e
	}
	if len(f.Args()) != 0 || *host == "" {
		return model.Err("invalid_request", "exact --host and --workspace required")
	}
	q := api.AgentEndpoints{Host: *host, Query: published.Query{Workspace: *id, Offset: *offset, Limit: *limit, Revision: *revision, Nonce: local.ID()}}
	if e := q.Query.Validate(); e != nil {
		return e
	}
	if e := a.resolveValidatedAliases(f); e != nil {
		return e
	}
	q.Host = *host
	if !published.Hex(q.Host, 16) {
		return model.Err("invalid_request", "exact --host and --workspace required")
	}
	var out json.RawMessage
	if e := a.call("workspace_agent_endpoints", q, &out); e != nil {
		return e
	}
	return a.print(out)
}
func (a *app) workspaceAgentMessage(action string, q api.AgentMessage) error {
	if q.Key == "" {
		if action == "message-lookup" {
			return model.Err("invalid_request", "lookup requires the original --key")
		}
		q.Key = local.ID()
	}
	if action != "message-lookup" {
		if q.Body == "-" {
			body, e := io.ReadAll(io.LimitReader(a.in, 16385))
			if e != nil {
				return e
			}
			q.Body = string(body)
		}
		if len(q.Body) < 1 || len(q.Body) > 16384 || q.TTL <= 0 || q.TTL > 24*time.Hour {
			return model.Err("invalid_request", "bounded text and positive TTL up to 24h required")
		}
		if action == "send" && (q.Target == "" || !q.To.Validate() || q.To.Kind != "agent") {
			return model.Err("invalid_request", "workspace sends require exact --host, --endpoint and --generation from arm agent endpoints")
		}
		if action == "reply" && !published.Hex(q.Message, 16) {
			return model.Err("invalid_request", "reply requires the original local inbox --message ID")
		}
	}
	if q.Target != "" {
		target, err := a.resolveHost(q.Target)
		if err != nil {
			return err
		}
		q.Target = target
	}
	if action == "send" && !published.Hex(q.Target, 16) {
		return model.Err("invalid_request", "workspace sends require exact --host, --endpoint and --generation from arm agent endpoints")
	}
	op := "workspace_agent_" + action
	if action == "message-lookup" {
		op = "workspace_agent_lookup"
	}
	var out json.RawMessage
	if e := a.workspaceMessageCall(op, q, q.Key, &out); e != nil {
		return e
	}
	return a.print(out)
}

func (a *app) publicationConfirm(yes bool) error {
	if !yes && a.machine() {
		return model.Err("confirmation_required", "explicit --yes is required after inspecting this exact publication review")
	}
	if e := a.workspaceConfirm(yes); e != nil {
		if api, ok := e.(*model.Error); ok && api.Code == "confirmation_declined" {
			return model.Err("confirmation_declined", "publication change declined; nothing changed")
		}
		return e
	}
	return nil
}
