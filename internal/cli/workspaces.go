package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/workspace"
	api "github.com/yrashk/arm/internal/workspaceapi"
)

func readWorkspaceDocument(path string, value any) error {
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if e != nil {
		return e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() || info.Size() > 60<<10 {
		return model.Err("invalid_request", "workspace document must be a regular file of at most60KiB")
	}
	raw, e := io.ReadAll(io.LimitReader(f, 60<<10+1))
	if e != nil {
		return e
	}
	if len(raw) > 60<<10 {
		return model.Err("invalid_request", "workspace document exceeds60KiB")
	}
	if e = workspace.Decode(raw, value); e != nil {
		return model.Err("invalid_request", e.Error())
	}
	return nil
}
func writeWorkspaceDocument(path string, value any) error {
	raw, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	if len(raw) > 60<<10 {
		return model.Err("response_too_large", "complete workspace document exceeds60KiB")
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	_, e = f.Write(append(raw, '\n'))
	if e == nil {
		e = f.Sync()
	}
	return e
}
func (a *app) workspaceCall(operation string, q, result any) error {
	e := a.call(operation, q, result)
	readOnly := operation == "workspace_list" || operation == "workspace_inspect" || operation == "workspace_lookup" || operation == "workspace_invitation" || operation == "workspace_consent" || operation == "workspace_admission_review" || operation == "workspace_refresh"
	if e != nil && !readOnly {
		var apiError *model.Error
		if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) || (!errors.As(e, &apiError) || apiError.Code == "operation_failed" || apiError.Code == "internal_error" || apiError.Code == "workspace_outcome_unknown") {
			label := ""
			raw, _ := json.Marshal(q)
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(raw, &fields)
			if key := fields["key"]; len(key) > 0 {
				label = string(key)
			} else {
				var nested map[string]json.RawMessage
				_ = json.Unmarshal(fields["request"], &nested)
				label = string(nested["key"])
			}
			return model.Err("workspace_outcome_unknown", "workspace request interrupted; inspect original exact key "+label+" and current family before any separately reviewed recovery; no automatic replay")
		}
	}
	return e
}
func (a *app) workspaceConfirm(yes bool) error {
	if yes {
		return nil
	}
	if !a.inputTTY || !a.outputTTY {
		return model.Err("confirmation_required", "explicit --yes required in scripts after reading the concrete revision and review hash")
	}
	fmt.Fprint(a.out, "\nApprove this exact workspace review? [y/N] ")
	type answer struct {
		line string
		err  error
	}
	result := make(chan answer, 1)
	go func() {
		line, e := bufio.NewReader(io.LimitReader(a.in, 257)).ReadString('\n')
		result <- answer{line, e}
	}()
	select {
	case <-a.ctx.Done():
		return a.ctx.Err()
	case v := <-result:
		if (v.err == nil || v.err == io.EOF) && (strings.EqualFold(strings.TrimSpace(v.line), "y") || strings.EqualFold(strings.TrimSpace(v.line), "yes")) {
			return nil
		}
		return model.Err("confirmation_declined", "workspace approval declined; prepared intent remains inspectable")
	}
}
func (a *app) workspaceReview(op workspace.Operation) error {
	if a.machine() {
		return a.print(op)
	}
	fmt.Fprintln(a.out, a.paint("1;36", "Workspace review"))
	fmt.Fprintf(a.out, "  Intent       %s · %s\n  Workspace    %s\n  Revision     %d\n  State        %s\n  Review SHA   %s\n", terminalSafe(op.Kind), terminalSafe(op.Key), terminalSafe(op.Workspace), op.Revision, terminalSafe(op.Phase), terminalSafe(op.ReviewHash))
	if r := op.Review; r != nil {
		fmt.Fprintf(a.out, "  Name         %s\n  This node    %s · %s\n  Directory    %s · %s\n  Directory pin %s\n", terminalSafe(r.Name), terminalSafe(r.Source.Name), r.Source.Host, terminalSafe(r.Directory.Name), r.Directory.Host, r.Directory.Fingerprint)
		if r.Candidate != nil {
			fmt.Fprintf(a.out, "  Candidate    %s · %s\n  Candidate pin %s\n", terminalSafe(r.Candidate.Name), r.Candidate.Host, r.Candidate.Fingerprint)
		}
		fmt.Fprintf(a.out, "  Capacity     %d active/pending members · %d opted-in agent endpoints\n  Freshness    %s membership · %s maximum stream\n  Delegation   introducer=%t · host inbox=%t · relay=%t\n  Reason       %s\n  Review until %s\n", r.Policy.MaxMembers, r.Policy.MaxPublishedAgents, r.Policy.MembershipTTL, r.Policy.StreamTTL, r.Introducer, r.HostInbox, r.Policy.Relay, terminalSafe(r.Reason), r.Expires.Format("2006-01-02 15:04:05Z07:00"))
	}
	fmt.Fprintln(a.out, "  Scope        Delegated discovery/message membership only. No resource, execution, SSH or file grants.\n  Default      Keep unchanged; approve only this exact revision and hash.")
	return nil
}
func (a *app) workspacesCommand(args []string) (int, error) {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "-help") {
		f := a.flags("workspaces")
		return 2, a.parse(f, args, false)
	}

	if len(args) == 0 {
		args = []string{"list"}
	}
	action, args := args[0], args[1:]
	f := a.flags("workspaces " + action)
	var id, key, reason string
	var parse func() error
	parse = func() error {
		f.Bool("json", false, "JSON output (automatic outside terminal)")
		if e := a.parse(f, args, false); e != nil {
			return e
		}
		if len(f.Args()) > 0 {
			return model.Err("invalid_request", "workspace commands use named flags; unexpected positional arguments")
		}
		return nil
	}
	selectors := func() { f.StringVar(&id, "workspace", "", "exact local workspace ID") }
	keyed := func() {
		selectors()
		f.StringVar(&key, "key", "", "exact operation key; never silently replay a new key")
	}
	mutation := func() { keyed(); f.StringVar(&reason, "reason", "", "required purpose") }
	switch action {
	case "list":
		if e := parse(); e != nil {
			return 2, e
		}
		var out api.List
		e := a.workspaceCall("workspace_list", struct{}{}, &out)
		if e != nil {
			return 1, e
		}
		return 0, a.print(out)
	case "inspect":
		selectors()
		section := f.String("section", "operations", "operations, members, trusted, changes or denies")
		offset := f.Int("offset", 0, "page offset")
		limit := f.Int("limit", 10, "records per bounded page (1..20)")
		if e := parse(); e != nil {
			return 2, e
		}
		var out api.Page
		e := a.workspaceCall("workspace_inspect", api.InspectRequest{Workspace: id, Section: *section, Offset: *offset, Limit: *limit}, &out)
		if e != nil {
			return 1, e
		}
		return 0, a.print(out)
	case "lookup":
		keyed()
		principal := f.String("principal", "", "exact retained namespace if the key is ambiguous (inspection only)")
		if e := parse(); e != nil {
			return 2, e
		}
		var out api.LookupResult
		e := a.workspaceCall("workspace_lookup", api.Lookup{Workspace: id, Key: key, Principal: *principal}, &out)
		if e != nil {
			return 1, e
		}
		if out.Operation != nil && !a.machine() {
			return 0, a.workspaceReview(*out.Operation)
		}
		return 0, a.print(out)
	case "create":
		f.StringVar(&key, "key", "", "required exact create intent key")
		f.StringVar(&reason, "reason", "", "required purpose")
		name := f.String("name", "", "workspace display name")
		max := f.Int("max-members", 128, "maximum active/pending directory members")
		if e := parse(); e != nil {
			return 2, e
		}
		policy := workspace.DefaultPolicy()
		policy.MaxMembers = *max
		var op workspace.Operation
		e := a.workspaceCall("workspace_create", workspace.CreateRequest{Key: key, Name: *name, Policy: policy, Reason: reason}, &op)
		if e != nil {
			return 1, e
		}
		return 0, a.workspaceReview(op)
	case "join":
		f.StringVar(&key, "key", "", "required exact join intent key")
		f.StringVar(&reason, "reason", "", "required purpose")
		file := f.String("invitation", "", "public directory invitation file")
		intro := f.Bool("introducer", false, "request explicitly reviewed delegated introducer status")
		if e := parse(); e != nil {
			return 2, e
		}
		var invite workspace.Invitation
		if e := readWorkspaceDocument(*file, &invite); e != nil {
			return 2, e
		}
		var op workspace.Operation
		e := a.workspaceCall("workspace_join", api.Join{Key: key, Invitation: invite, Introducer: *intro, Reason: reason}, &op)
		if e != nil {
			return 1, e
		}
		return 0, a.workspaceReview(op)
	case "decide", "recovery-decide":
		keyed()
		revision := f.Int64("revision", 0, "exact prepared operation revision")
		review := f.String("review", "", "exact concrete review SHA256")
		approve := f.Bool("approve", false, "approve this exact review (default declines)")
		yes := f.Bool("yes", false, "explicitly accept the already inspected concrete review")
		if e := parse(); e != nil {
			return 2, e
		}
		var known api.LookupResult
		if e := a.workspaceCall("workspace_lookup", api.Lookup{Workspace: id, Key: key}, &known); e != nil {
			return 1, e
		}
		if known.Operation == nil || known.Operation.Revision != *revision || known.Operation.ReviewHash != *review {
			return 1, model.Err("stale_review", "inspect exact key, revision and review hash before deciding")
		}
		if !a.machine() {
			if e := a.workspaceReview(*known.Operation); e != nil {
				return 1, e
			}
		}
		if *approve {
			if e := a.workspaceConfirm(*yes); e != nil {
				return 1, e
			}
		}
		q := api.Decision{Workspace: id, Key: key, Revision: *revision, Review: *review, Approve: *approve}
		operation := "workspace_decide"
		if action == "recovery-decide" {
			operation = "workspace_recovery_decide"
		}
		var op workspace.Operation
		if e := a.workspaceCall(operation, q, &op); e != nil {
			return 1, e
		}
		return 0, a.print(op)
	case "invitation", "consent":
		selectors()
		if action == "consent" {
			f.StringVar(&key, "key", "", "exact approved join/recovery key")
		}
		file := f.String("output", "", "optional unused destination file (private mode0600, never overwritten)")
		if e := parse(); e != nil {
			return 2, e
		}
		var out any
		if action == "invitation" {
			var v workspace.Invitation
			if e := a.workspaceCall("workspace_invitation", api.Selector{Workspace: id}, &v); e != nil {
				return 1, e
			}
			out = v
		} else {
			var v workspace.Consent
			if e := a.workspaceCall("workspace_consent", api.Key{Workspace: id, Key: key}, &v); e != nil {
				return 1, e
			}
			out = v
		}
		if *file != "" {
			if e := writeWorkspaceDocument(*file, out); e != nil {
				return 1, e
			}
			return 0, a.print(map[string]string{"path": *file, "kind": action, "notice": "Public review document; not credentials or an authorization token."})
		}
		return 0, a.print(out)
	case "admit":
		mutation()
		file := f.String("consent", "", "public approved candidate consent file")
		review := f.String("review", "", "exact previously inspected admission review SHA; omit for read-only preview")
		yes := f.Bool("yes", false, "approve this exact current admission review")
		if e := parse(); e != nil {
			return 2, e
		}
		var consent workspace.Consent
		if e := readWorkspaceDocument(*file, &consent); e != nil {
			return 2, e
		}
		q := workspace.NativeAdmissionRequest{Workspace: id, Key: key, Consent: consent, Reason: reason}
		if *review != "" {
			var lookup api.LookupResult
			if e := a.workspaceCall("workspace_lookup", api.Lookup{Workspace: id, Key: key}, &lookup); e != nil {
				return 1, e
			}
			if lookup.Found {
				var existing workspace.Operation
				if e := a.workspaceCall("workspace_admit", api.AdmissionCommit{Request: q, Review: *review}, &existing); e != nil {
					return 1, e
				}
				return 0, a.print(existing)
			}
		}
		var r workspace.AdmissionReview
		if e := a.workspaceCall("workspace_admission_review", q, &r); e != nil {
			return 1, e
		}
		if *review == "" {
			return 0, a.workspaceAdmissionReview(r)
		}
		if *review != r.Hash {
			return 1, model.Err("stale_review", "admission review changed; inspect again")
		}
		if !a.machine() {
			if e := a.workspaceAdmissionReview(r); e != nil {
				return 1, e
			}
		}
		if e := a.workspaceConfirm(*yes); e != nil {
			return 1, e
		}
		var op workspace.Operation
		if e := a.workspaceCall("workspace_admit", api.AdmissionCommit{Request: q, Review: *review}, &op); e != nil {
			return 1, e
		}
		return 0, a.print(op)
	case "refresh":
		selectors()
		if e := parse(); e != nil {
			return 2, e
		}
		var out api.RefreshResult
		if e := a.workspaceCall("workspace_refresh", api.Selector{Workspace: id}, &out); e != nil {
			return 1, e
		}
		return 0, a.print(out)
	case "finalize", "recovery-run":
		keyed()
		revision := f.Int64("revision", 0, "exact current original or recovery child revision")
		if e := parse(); e != nil {
			return 2, e
		}
		var out json.RawMessage
		operation := "workspace_finalize"
		if action == "recovery-run" {
			operation = "workspace_recovery_run"
		}
		if e := a.workspaceCall(operation, api.Finalize{Workspace: id, Key: key, Revision: *revision}, &out); e != nil {
			return 1, e
		}
		return 0, a.print(json.RawMessage(out))
	case "resume-join", "resume-admission":
		mutation()
		original := f.String("original", "", "original immutable join/admission key")
		principal := f.String("principal", "", "original exact namespace if ambiguous")
		revision := f.Int64("revision", 0, "original current revision")
		proof := f.Bool("permit-proof", false, "review another candidate proof; default reconciles directory completion read-only")
		file := f.String("consent", "", "fresh candidate recovery consent (directory proof only)")
		if e := parse(); e != nil {
			return 2, e
		}
		q := api.RecoveryPrepare{Kind: strings.Replace(action, "-", "_", 1), Workspace: id, OriginalKey: *original, OriginalPrincipal: *principal, OriginalRevision: *revision, Key: key, PermitProof: *proof, Reason: reason}
		if *file != "" {
			var c workspace.Consent
			if e := readWorkspaceDocument(*file, &c); e != nil {
				return 2, e
			}
			q.CandidateConsent = &c
		}
		var op workspace.Operation
		if e := a.workspaceCall("workspace_recovery_prepare_auto", q, &op); e != nil {
			return 1, e
		}
		if op.Kind != strings.Replace(action, "-", "_", 1) {
			return 1, model.Err("invalid_request", "original kind differs from requested recovery")
		}
		return 0, a.workspaceReview(op)
	case "deny", "revoke", "retire":
		mutation()
		host := f.String("host", "", "exact member/candidate host (omit for local retirement)")
		revision := f.Int64("revision", 0, "exact local workspace revision")
		yes := f.Bool("yes", false, "confirm local authority-reducing change (no remote cleanup claim)")
		if e := parse(); e != nil {
			return 2, e
		}
		if !a.machine() {
			var page api.Page
			if e := a.workspaceCall("workspace_inspect", api.InspectRequest{Workspace: id, Section: "members", Limit: 1}, &page); e != nil {
				return 1, e
			}
			fmt.Fprintf(a.out, "%s\n  Workspace %s · %s\n  Local authority %s\n  Exact revision %d\n  Target host %s\n  Reason %s\n  Effect Local membership/deny/retirement only; no remote cleanup or process reclamation.\n", a.paint("1;33", "Workspace "+action), terminalSafe(page.Workspace.Name), id, page.Workspace.Self.Host, *revision, terminalSafe(*host), terminalSafe(reason))
		}
		if e := a.workspaceConfirm(*yes); e != nil {
			return 1, e
		}
		var out workspace.Operation
		q := api.Mutation{Workspace: id, Request: workspace.MutationRequest{Key: key, Host: *host, Revision: *revision, Reason: reason}}
		if e := a.workspaceCall("workspace_"+action, q, &out); e != nil {
			return 1, e
		}
		return 0, a.print(out)
	case "cancel-join", "cancel-admission", "cancel-recovery":
		keyed()
		source := f.String("source", "", "exact original introducer host for admission cancellation only")
		revision := f.Int64("revision", 0, "exact operation revision")
		if e := parse(); e != nil {
			return 2, e
		}
		var out workspace.Operation
		if e := a.workspaceCall("workspace_"+strings.ReplaceAll(action, "-", "_"), api.Cancel{Workspace: id, Key: key, Source: *source, Revision: *revision}, &out); e != nil {
			return 1, e
		}
		return 0, a.print(out)
	default:
		return 2, model.Err("invalid_request", "unknown workspaces command; use arm help workspaces")
	}
}

func (a *app) workspaceAdmissionReview(r workspace.AdmissionReview) error {
	if a.machine() {
		return a.print(r)
	}
	c := r.Request.Consent
	fmt.Fprintf(a.out, "%s\n  Workspace %s\n  Operation key %s\n  Directory %s · %s\n  Directory pin %s\n  Candidate %s · %s\n  Candidate pin %s\n  Policy revision %d · workspace revision %d\n  Introducer %t · host inbox %t\n  Capacity %d active/pending members\n  Reason %s\n  Review SHA %s\n  Until %s\n  Scope Delegated discovery/messages only; no resource, execution, SSH or file grants.\n  A real exact pinned candidate proof is required. Default keeps unchanged.\n", a.paint("1;36", "Admit exact candidate"), r.Request.Workspace, terminalSafe(r.Request.Key), terminalSafe(r.Directory.Name), r.Directory.Host, r.Directory.Fingerprint, terminalSafe(c.Candidate.Name), c.Candidate.Host, c.Candidate.Fingerprint, c.PolicyRevision, r.Revision, c.Introducer, c.HostInbox, r.Policy.MaxMembers, terminalSafe(r.Request.Reason), r.Hash, r.Expires.Format("2006-01-02 15:04:05Z07:00"))
	return nil
}
