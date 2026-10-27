package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"getarm.org/internal/model"
	api "getarm.org/internal/workspaceapi"
)

func workspaceMessageAction(s string) bool {
	switch s {
	case "send", "reply", "inbox", "outbox", "message-lookup", "message-cancel", "messages-retire":
		return true
	}
	return false
}
func (a *app) workspaceMessageCall(operation string, q any, key string, out any) error {
	e := a.call(operation, q, out)
	if e == nil {
		return nil
	}
	var apiError *model.Error
	if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) || !errors.As(e, &apiError) || apiError.Code == "operation_failed" || apiError.Code == "internal_error" {
		return model.Err("routing_outcome_unknown", "workspace messaging request interrupted; retain exact original key "+key+" and inspect message-lookup before any new delivery; no automatic retry")
	}
	return e
}
func (a *app) workspaceMessagesCommand(action string, args []string) (int, error) {
	f := a.flags("workspaces " + action)
	id := f.String("workspace", "", "exact local workspace ID")
	jsonOutput := f.Bool("json", false, "JSON output (automatic outside terminal)")
	_ = jsonOutput
	var q any
	var operation, key string
	switch action {
	case "send", "reply":
		keyFlag := f.String("key", "", "required immutable client key; repeating it only inspects the original result")
		body := f.String("text", "", "message body (at most16KiB UTF-8)")
		ttl := f.Duration("ttl", time.Hour, "message deadline; maximum24h, expiry does not settle unknown delivery")
		target := new(string)
		message := new(string)
		if action == "send" {
			target = f.String("to", "", "exact admitted destination host ID (host inbox only)")
		} else {
			message = f.String("message", "", "exact original LOCAL inbox message ID; reply returns to its original sender")
		}
		if e := a.parse(f, args, false); e != nil {
			return 2, e
		}
		key = *keyFlag
		if *id == "" || key == "" || *body == "" || *ttl <= 0 || *ttl > 24*time.Hour || len(f.Args()) != 0 || action == "send" && *target == "" || action == "reply" && *message == "" {
			return 2, model.Err("invalid_request", "--workspace, --key, --text and --to/--message required; --ttl must be positive and at most24h")
		}
		operation = "workspace_message_" + action
		if action == "send" {
			q = api.MessageSend{Workspace: *id, Key: key, Target: *target, Body: *body, TTL: *ttl}
		} else {
			q = api.MessageReply{Workspace: *id, Key: key, Message: *message, Body: *body, TTL: *ttl}
		}
	case "message-lookup":
		keyFlag := f.String("key", "", "exact original local outbound key")
		reconcile := f.Bool("reconcile", false, "explicit fresh receipt lookup at pinned destination; never resends original delivery")
		if e := a.parse(f, args, false); e != nil {
			return 2, e
		}
		key = *keyFlag
		if *id == "" || key == "" || len(f.Args()) != 0 {
			return 2, model.Err("invalid_request", "exact --workspace and --key required")
		}
		operation = "workspace_message_lookup"
		q = api.MessageLookup{Workspace: *id, Key: key, Reconcile: *reconcile}
	case "inbox", "outbox":
		offset := f.Int("offset", 0, "offset in stable message-ID order")
		limit := f.Int("limit", 10, "maximum full records in page (1..20; page byte limit may return fewer)")
		revision := f.Int64("revision", 0, "routing snapshot revision for subsequent pages (0 starts a new page)")
		if e := a.parse(f, args, false); e != nil {
			return 2, e
		}
		if *id == "" || *offset < 0 || *limit < 1 || *limit > 20 || *revision < 0 || len(f.Args()) != 0 {
			return 2, model.Err("invalid_request", "--workspace, offset>=0, limit1..20 and revision>=0 required")
		}
		var out json.RawMessage
		e := a.call("workspace_message_page", api.MessagePage{Workspace: *id, Side: action, Offset: *offset, Limit: *limit, Revision: *revision}, &out)
		if e != nil {
			return 1, e
		}
		return 0, a.print(out)
	case "message-cancel", "messages-retire":
		keyFlag := f.String("key", "", "original undispatched outbound key, or immutable local retirement key")
		revision := f.Int64("revision", 0, "exact current outbound or routing-root revision")
		yes := f.Bool("yes", false, "confirm this exact local settlement; no remote cleanup is claimed")
		if e := a.parse(f, args, false); e != nil {
			return 2, e
		}
		key = *keyFlag
		if *id == "" || key == "" || *revision < 1 || len(f.Args()) != 0 {
			return 2, model.Err("invalid_request", "--workspace, --key and positive --revision required")
		}
		if !a.machine() {
			fmt.Fprintf(a.out, "%s\n  Workspace %s\n  Key %s · revision %d\n  Effect Local settlement only. Dispatched/unknown delivery cannot be cancelled; routing retirement requires an explicitly retired parent workspace.\n", a.paint("1;33", "Workspace "+action), terminalSafe(*id), terminalSafe(key), *revision)
		}
		if e := a.workspaceConfirm(*yes); e != nil {
			return 1, e
		}
		operation = "workspace_" + strings.ReplaceAll(action, "-", "_")
		q = api.RevisionRequest{Workspace: *id, Key: key, Revision: *revision}
	}
	var out json.RawMessage
	if e := a.workspaceMessageCall(operation, q, key, &out); e != nil {
		return 1, e
	}
	return 0, a.print(out)
}
