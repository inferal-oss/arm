package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"getarm.org/internal/local"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/sshservice"
)

var historyHostPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var historyHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func historyText(s string, limit int) bool {
	if !utf8.ValidString(s) || strings.TrimSpace(s) == "" || len(s) > limit {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	return true
}

// Authority inspection deliberately opens no private mesh store or ordinary SSH
// state: a pending recovery fence must not make its own inspection inaccessible.
func (a *app) sshOfflineAuthority() (string, mesh.Config, sshservice.Binding, error) {
	var config mesh.Config
	var binding sshservice.Binding
	home, e := filepath.EvalSymlinks(a.home)
	if e != nil {
		return "", config, binding, e
	}
	config, exists, e := mesh.ReadRegistry(home)
	if e != nil {
		return "", config, binding, e
	}
	if !exists {
		return "", config, binding, fmt.Errorf("mesh is not initialized")
	}
	native := local.CurrentIdentity()
	binding = sshservice.Binding{Host: config.Identity.Host, Owner: native.UID, Account: native.Username}
	if e = binding.Validate(); e != nil {
		return "", config, binding, e
	}
	if config.Identity.Owner != binding.Owner || config.Identity.OwnerUsername != binding.Account {
		return "", config, binding, fmt.Errorf("current native account differs from local mesh owner")
	}
	return home, config, binding, nil
}
func historyTarget(config mesh.Config, target string) (string, error) {
	if historyHostPattern.MatchString(target) {
		if target == config.Identity.Host {
			return "", model.Err("invalid_request", "target must be another host")
		}
		return target, nil
	}
	peer, e := mesh.ResolveConfig(config, target)
	if e != nil {
		return "", model.Err("not_found", "unknown or ambiguous local node name; use the exact stable host ID for removed peers")
	}
	return peer.Identity.Host, nil
}
func (a *app) historyParse(f *flag.FlagSet, args []string) (map[string]bool, error) {
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		name, _, inline := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		v := f.Lookup(name)
		if v == nil {
			continue
		}
		if seen[name] {
			return nil, model.Err("invalid_request", "repeated --"+name+" is ambiguous")
		}
		seen[name] = true
		isBool := false
		if b, ok := v.Value.(interface{ IsBoolFlag() bool }); ok {
			isBool = b.IsBoolFlag()
		}
		if !inline && !isBool {
			i++
		}
	}
	if e := a.parse(f, args, false); e != nil {
		return nil, e
	}
	visited := map[string]bool{}
	f.Visit(func(v *flag.Flag) { visited[v.Name] = true })
	return visited, nil
}
func historyReviewSHA(v any) string { raw, _ := json.Marshal(v); return sshDigest(raw) }
func historyUnknown(key, kind string, e error) error {
	return model.Err("history_outcome_unknown", fmt.Sprintf("Original %s key %q may retain an unfinished outcome: %v. Inspect with arm history ssh-source %sinspect %q; never substitute a new key", kind, key, e, map[bool]string{true: "recovery ", false: ""}[kind == "recovery"], key))
}

type historyReview struct {
	Operation string                        `json:"operation"`
	Binding   sshservice.Binding            `json:"binding"`
	Key       string                        `json:"key"`
	Reason    string                        `json:"reason"`
	Digest    string                        `json:"review_sha256"`
	Phase     string                        `json:"phase,omitempty"`
	Plan      *sshservice.SourceHistoryPlan `json:"plan,omitempty"`
	Request   any                           `json:"request,omitempty"`
	DryRun    bool                          `json:"dry_run"`
}

func (a *app) historyConfirm(v historyReview, yes, dry bool) error {
	if yes && dry {
		return model.Err("invalid_request", "--yes and --dry-run cannot be combined")
	}
	v.DryRun = dry
	if dry && a.machine() {
		return a.print(v)
	}
	if a.machine() {
		if !yes {
			return model.Err("confirmation_required", "review the history plan, then supply --yes and its exact digest; no history mutation started")
		}
		return nil
	}
	fmt.Fprintln(a.out, a.paint("1;36", "╭─ SSH source history review ──────────────────────────────╮"))
	fmt.Fprintf(a.out, "  %s\n  Host: %s\n  Owner: %s (UID %s)\n  Key: %s\n  Reason: %s\n  Review: %s\n", terminalSafe(v.Operation), terminalSafe(v.Binding.Host), terminalSafe(v.Binding.Account), terminalSafe(v.Binding.Owner), terminalSafe(v.Key), terminalSafe(v.Reason), terminalSafe(v.Digest))
	if v.Phase != "" {
		fmt.Fprintf(a.out, "  Phase: %s\n", terminalSafe(v.Phase))
	}
	if v.Plan != nil {
		pending := 0
		for _, m := range v.Plan.Members {
			if m.PendingFile != "" {
				pending++
			}
		}
		fmt.Fprintf(a.out, "  Selected: %d receipts · %d logical bytes\n  Hot: %d · Archived keys: %d · Identical pending copies: %d\n", len(v.Plan.Members), v.Plan.Bytes, v.Plan.HotBefore, v.Plan.ColdBefore, pending)
	}
	if q, ok := v.Request.(sshservice.SourceHistoryRecoveryRequest); ok {
		fmt.Fprintf(a.out, "  Catalog: %s\n  Journal: %d bytes · SHA256 %s\n  Journal identity: device %d / inode %d\n", terminalSafe(q.Marker.CatalogID), q.JournalBytes, terminalSafe(q.JournalSHA256), q.Journal.Device, q.Journal.Inode)
	}
	fmt.Fprintln(a.out, "  Preserves permanent old-key fences and unresolved work.\n  No remote execution, artifact cleanup or resource grants.\n  Catalog limit: 256 packs / 16,384 keys; no pruning.\n"+a.paint("1;36", "╰──────────────────────────────────────────────────────────╯"))
	if dry {
		fmt.Fprintln(a.out, a.paint("1;33", "Dry run — nothing changed."))
		return nil
	}
	if yes {
		return nil
	}
	if !a.inputTTY {
		return model.Err("confirmation_required", "terminal input or explicit --yes required; nothing changed")
	}
	fmt.Fprint(a.out, a.paint("1;33", "Continue this exact history operation? [y/N] "))
	type answer struct {
		text string
		err  error
	}
	ch := make(chan answer, 1)
	go func() { s, e := bufio.NewReader(io.LimitReader(a.in, 257)).ReadString('\n'); ch <- answer{s, e} }()
	select {
	case <-a.ctx.Done():
		return a.ctx.Err()
	case r := <-ch:
		fmt.Fprintln(a.out)
		if r.err != nil || len(r.text) > 256 {
			return model.Err("confirmation_declined", "history unchanged")
		}
		s := strings.ToLower(strings.TrimSpace(r.text))
		if s != "y" && s != "yes" {
			return model.Err("confirmation_declined", "history unchanged")
		}
	}
	return nil
}
func (a *app) historyContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(a.ctx, 2*time.Minute)
}

func (a *app) sshSourcePrecheck(node, key string) error {
	home, registry, b, e := a.sshOfflineAuthority()
	if e != nil {
		return e
	}
	target, e := historyTarget(registry, node)
	if e != nil {
		return e
	}
	state, e := sshservice.ReadState(a.ctx, home, b)
	if e != nil {
		return model.Err("ssh_evidence_unavailable", e.Error())
	}
	if key != "" {
		for _, old := range state.Intents {
			if old.Request.Pin.Target == target && old.Request.Key == key {
				return model.Err("ssh_intent_exists", fmt.Sprintf("Original SSH key %q is pending; inspect with arm ssh lookup %s --key %q; execution cannot be repeated", key, target, key))
			}
		}
		result, e := sshservice.LookupSourceHistory(a.ctx, home, b, target, key)
		if e != nil {
			return model.Err("ssh_evidence_unavailable", e.Error())
		}
		if result.Found {
			return model.Err("ssh_intent_exists", fmt.Sprintf("Original SSH key %q is retained locally; inspect with arm history ssh-source receipt %s --key %q; execution cannot be repeated", key, target, key))
		}
	}
	if e := state.CheckSourceCapacity(); e != nil {
		if errors.Is(e, sshservice.ErrHistoryBusy) {
			return model.Err("ssh_history_busy", e.Error())
		}
		return model.Err("capacity_exceeded", e.Error())
	}

	return nil
}

// Only typed definite pre-publication refusals can clean their exact empty
// invocation directory. Generic storage/commit errors preserve unknown state.
func sshReservationRefusal(ctx context.Context, intent sshservice.SourceIntent, e error) (error, bool) {
	if !errors.Is(e, sshservice.ErrIntentExists) && !errors.Is(e, sshservice.ErrTargetUncertain) && !errors.Is(e, sshservice.ErrSourceCapacity) {
		return nil, false
	}
	if cleanupErr := sshservice.DiscardEmptyInvocationDirectory(ctx, intent.Directory); cleanupErr != nil {
		return fmt.Errorf("reservation refused; exact empty directory cleanup unavailable: %v: %w", cleanupErr, e), true
	}
	if errors.Is(e, sshservice.ErrSourceCapacity) {
		return model.Err("capacity_exceeded", e.Error()), true
	}
	return model.Err("ssh_intent_exists", e.Error()), true
}
