package cli

import (
	"encoding/json"
	"fmt"
	"getarm.org/internal/agents"
	"getarm.org/internal/agentstatus"
	"getarm.org/internal/local"
)

func (a *app) agentStatus(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("use agent status list, read ID, request ID, collect ATTEMPT, generate ATTEMPT, cancel ATTEMPT or config")
	}
	op := args[0]
	f := a.flags("agent status " + op)
	f.Bool("json", false, "JSON output")
	q := struct {
		ID      string              `json:"id,omitempty"`
		Offset  int                 `json:"offset,omitempty"`
		Key     string              `json:"key,omitempty"`
		Contact *agents.Contact     `json:"contact,omitempty"`
		Config  *agentstatus.Config `json:"config,omitempty"`
		Confirm bool                `json:"confirm,omitempty"`
	}{}
	contactJSON := ""
	configure := false
	c := agentstatus.DefaultConfig()
	switch op {
	case "list":
		f.IntVar(&q.Offset, "offset", 0, "inventory page offset; page size 16")
	case "request":
		f.StringVar(&q.Key, "key", "", "original operation key; generated when omitted")
		f.StringVar(&contactJSON, "contact-json", "", "complete exact contact JSON from the inspected status entry")
		f.BoolVar(&q.Confirm, "yes", false, "confirm participation request to this exact recipient")
	case "generate":
		f.BoolVar(&q.Confirm, "yes", false, "confirm one configured model call; no automatic fallback or retry")
	case "read", "collect", "cancel":
	case "config":
		f.BoolVar(&configure, "set", false, "set reviewed configuration; otherwise inspect")
		f.IntVar(&c.Revision, "revision", 0, "exact inspected configuration revision")
		f.BoolVar(&c.Enabled, "enable", false, "explicitly enable configured paid summary generation")
		f.StringVar(&c.Provider, "provider", "openai", "openai or anthropic; no implicit provider switch")
		f.StringVar(&c.Model, "model", agentstatus.CodexModel, "exact model; GPT-6.1 Terra or the supported Claude Sonnet counterpart")
		f.StringVar(&c.CredentialEnv, "credential-env", "", "explicit existing API credential variable available to the backend; never native login state")
		f.IntVar(&c.MaxDailyRequests, "daily-limit", 20, "maximum reserved model calls per UTC day, including uncertain attempts")
		f.Float64Var(&c.EstimatedAttemptUSD, "estimated-attempt-usd", .05, "preflight USD estimate ceiling; not a guaranteed billing cap")
		f.BoolVar(&q.Confirm, "yes", false, "confirm the exact summary provider configuration")
	default:
		return fmt.Errorf("unknown status operation")
	}
	if err := a.parse(f, args[1:], false); err != nil {
		return err
	}
	if op == "list" || op == "config" {
		if f.NArg() != 0 {
			return fmt.Errorf("%s accepts no positional arguments", op)
		}
	} else {
		if f.NArg() != 1 {
			return fmt.Errorf("%s requires one identity/attempt ID", op)
		}
		q.ID = f.Arg(0)
	}
	if op == "request" {
		var target agents.Contact
		if json.Unmarshal([]byte(contactJSON), &target) != nil || target.ID == "" || target.Registration == "" {
			return fmt.Errorf("provide the complete reviewed --contact-json and --yes")
		}
		q.Contact = &target
		if q.Key == "" {
			q.Key = local.ID()
		}
	}
	if op == "config" && configure {
		q.Config = &c
	}
	var result any
	if err := a.call("agent_status_"+op, q, &result); err != nil {
		if op == "request" {
			return fmt.Errorf("status request outcome may be unknown; retain original key %s and exact recipient: %w", q.Key, err)
		}
		return err
	}
	return a.print(result)
}
