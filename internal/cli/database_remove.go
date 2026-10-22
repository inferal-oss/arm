package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/yrashk/arm/internal/database"
	"github.com/yrashk/arm/internal/model"
)

type databaseRemovePreview struct {
	Operation    string             `json:"operation"`
	DryRun       bool               `json:"dry_run"`
	Confirmation bool               `json:"confirmation_required"`
	Continuation bool               `json:"continuation"`
	Phase        string             `json:"phase"`
	Plan         database.ResetPlan `json:"plan"`
}
type databaseRemoveOutcome struct {
	Operation string               `json:"operation"`
	Plan      database.ResetPlan   `json:"reviewed_plan"`
	Result    database.ResetResult `json:"result"`
}

func (a *app) databaseRemove(args []string) (int, error) {
	f := a.flags("database remove")
	f.Bool("json", false, "structured preview/result, automatic when output is redirected")
	yes := f.Bool("yes", false, "apply the exact reviewed removal without an interactive prompt")
	dry := f.Bool("dry-run", false, "read-only preview; never create reset receipts/locks or remove anything")
	retire := f.Bool("retire-mesh", false, "explicitly archive local mesh identity/key and lifecycle records; requires re-pairing, no remote revocation")
	reason := f.String("reason", "", "required purpose for a new reset, saved in its retained receipt")
	continuation := f.String("continue", "", "review and explicitly finish this exact retained reset operation ID; never starts automatically")
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if *dry && *yes {
		return 2, fmt.Errorf("--dry-run cannot be combined with --yes")
	}
	if *continuation != "" && (*reason != "" || *retire) {
		return 2, fmt.Errorf("--continue uses the immutable saved reason and retirement scope; omit --reason/--retire-mesh")
	}
	if *continuation == "" && !*dry && strings.TrimSpace(*reason) == "" {
		return 2, fmt.Errorf("--reason is required for removal; preview with --dry-run first")
	}
	var plan database.ResetPlan
	var apply func(context.Context) (database.ResetResult, error)
	phase := "reviewed"
	ctx, cancel := context.WithTimeout(a.ctx, database.FullTimeout)
	if *continuation != "" {
		review, err := database.ReviewResetContinuation(ctx, a.home, *continuation)
		cancel()
		if err != nil {
			return 1, err
		}
		defer review.Close()
		plan, phase, apply = review.Receipt.Plan, review.Receipt.Phase, review.Continue
	} else {
		review, err := database.ReviewReset(ctx, a.home, *reason, *retire)
		cancel()
		if err != nil {
			return 1, err
		}
		defer review.Close()
		plan, apply = review.Plan, review.Apply
	}
	preview := databaseRemovePreview{Operation: "database_remove", DryRun: *dry, Confirmation: !*dry && !*yes, Continuation: *continuation != "", Phase: phase, Plan: plan}
	if *dry || !*yes || !a.machine() || !plan.Proof.Settled || plan.Mesh != nil && !plan.RetireMesh {
		if err := a.printDatabaseRemovalPreview(preview); err != nil {
			return 1, err
		}
	}
	if *dry {
		return 0, nil
	}
	if !plan.Proof.Complete || !plan.Proof.Settled || len(plan.Proof.Blockers) > 0 {
		return 1, model.Err("database_obligations_unsettled", "resource, provider or operation evidence remains unresolved; no removal was performed")
	}
	if plan.Mesh != nil && !plan.RetireMesh {
		return 1, model.Err("mesh_retirement_required", "review --dry-run --retire-mesh, then explicitly authorize local identity retirement; nothing was removed")
	}
	if !*yes {
		if a.machine() || !a.inputTTY {
			return 1, model.Err("confirmation_required", "review the concrete preview, then pass --yes with a reason to authorize removal; nothing was removed")
		}
		if err := a.confirmDatabaseRemoval(); err != nil {
			return 1, err
		}
	}
	ctx, cancel = context.WithTimeout(a.ctx, database.FullTimeout)
	defer cancel()
	result, err := apply(ctx)
	if result.Operation != "" {
		if outputErr := a.printDatabaseRemovalOutcome(databaseRemoveOutcome{Operation: "database_remove", Plan: plan, Result: result}); outputErr != nil {
			return 1, outputErr
		}
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}

func (a *app) printDatabaseRemovalOutcome(outcome databaseRemoveOutcome) error {
	if a.machine() {
		return a.print(outcome)
	}
	result := outcome.Result
	color, title := "1;33", "Database reset needs continuation"
	if result.Completed {
		color, title = "1;32", "Reviewed database reset completed"
	}
	fmt.Fprintln(a.out, "\n"+a.paint(color, title))
	fmt.Fprintln(a.out, terminalSafe(result.Detail))
	fmt.Fprintf(a.out, "Operation: %s\nReceipt: %s\n", terminalSafe(result.Operation), terminalSafe(result.Receipt))
	if result.Completed {
		fmt.Fprintln(a.out, "Start ARM normally to install the fresh local authority. Retired mesh nodes require new pairing.")
	} else {
		fmt.Fprintf(a.out, "Review again using database remove --continue %s with the same --home. Recovery never starts automatically.\n", terminalSafe(result.Operation))
	}
	return nil
}

func (a *app) confirmDatabaseRemoval() error {
	if _, err := fmt.Fprint(a.out, "\n"+a.paint("1;31", "Permanently erase ONLY these SQLite files? [y/N] ")); err != nil {
		return err
	}
	type response struct {
		text string
		err  error
	}
	answer := make(chan response, 1)
	go func() {
		text, err := bufio.NewReader(io.LimitReader(a.in, 257)).ReadString('\n')
		answer <- response{text, err}
	}()
	select {
	case <-a.ctx.Done():
		return a.ctx.Err()
	case r := <-answer:
		fmt.Fprintln(a.out)
		if r.err != nil || len(r.text) > 256 {
			return model.Err("confirmation_declined", "Kept database unchanged; no reset intent or deletion was started")
		}
		value := strings.ToLower(strings.TrimSpace(r.text))
		if value != "y" && value != "yes" {
			return model.Err("confirmation_declined", "Kept database unchanged; no reset intent or deletion was started")
		}
	}
	return nil
}

func (a *app) printDatabaseRemovalPreview(preview databaseRemovePreview) error {
	if a.machine() {
		return a.print(preview)
	}
	plan := preview.Plan
	var b strings.Builder
	fmt.Fprintln(&b, a.paint("1;36", "ARM · Database removal review"))
	fmt.Fprintln(&b, "No removal has been performed by this review.")
	fmt.Fprintf(&b, "\nState directory: %s\nOperation: %s\nPhase: %s\n", terminalSafe(plan.Home), terminalSafe(plan.ID), terminalSafe(preview.Phase))
	fmt.Fprintf(&b, "Authority: %s → %s (new identity)\n", terminalSafe(plan.OldHost), terminalSafe(plan.NewHost))
	fmt.Fprintf(&b, "Owner: %s (UID %s)\nReason: %s\n", terminalSafe(plan.Actor.Username), terminalSafe(plan.Actor.UID), terminalSafe(plan.Reason))
	fmt.Fprintf(&b, "History to discard: %d leases; all database audit and idempotency history\n", plan.Proof.Tables["leases"])
	fmt.Fprintf(&b, "Database definitions/messages to discard: %d agent registrations · %d messages · %d pools · %d policy budgets\n", plan.Proof.Tables["agents"], plan.Proof.Tables["messages"], plan.Proof.Tables["pools"], plan.Proof.Tables["budgets"])
	fmt.Fprintln(&b, "\n"+a.paint("1;31", "Exact SQLite erase scope"))
	for _, file := range plan.Files {
		if file.Present {
			fmt.Fprintf(&b, "  %s · %d bytes\n", terminalSafe(file.Name), file.Size)
		} else {
			fmt.Fprintf(&b, "  %s · absent (must remain absent)\n", terminalSafe(file.Name))
		}
	}
	if plan.Mesh != nil {
		status := "REQUIRES --retire-mesh"
		if plan.RetireMesh {
			status = "explicit local retirement included"
		}
		fmt.Fprintf(&b, "\nMesh: %s\n  Host %s · pin %s · %d peer records\n", a.paint("33", status), terminalSafe(plan.Mesh.Host), terminalSafe(plan.Mesh.Fingerprint), plan.Mesh.Peers)
	}
	fmt.Fprintln(&b, "\n"+a.paint("1", "Effects"))
	for _, text := range plan.Effects {
		fmt.Fprintln(&b, "  • "+terminalSafe(text))
	}
	fmt.Fprintln(&b, "\n"+a.paint("1", "Preserved"))
	for _, text := range plan.Preserved {
		fmt.Fprintln(&b, "  • "+terminalSafe(text))
	}
	if len(plan.Proof.Blockers) > 0 {
		fmt.Fprintln(&b, "\n"+a.paint("1;31", "Removal blocked"))
		for _, block := range plan.Proof.Blockers {
			fmt.Fprintf(&b, "  %s %s: %s\n", terminalSafe(block.Kind), terminalSafe(block.Entity), terminalSafe(block.Detail))
		}
	}
	fmt.Fprintln(&b, "\n"+a.paint("33", "Default: keep unchanged. ARM will not stop processes, erase artifacts or revoke remote trust."))
	_, err := io.WriteString(a.out, b.String())
	return err
}
