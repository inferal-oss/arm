package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"getarm.org/internal/install"
	"getarm.org/internal/model"
)

func (a *app) approve(plan *install.Plan, yes bool) error {
	if yes {
		return plan.Check()
	}
	if a.machine() || !a.inputTTY {
		if err := a.print(plan.Report); err != nil {
			return err
		}
		return model.Err("confirmation_required", "review with --dry-run, then pass --yes to apply without an interactive prompt")
	}
	if err := a.print(plan.Report); err != nil {
		return err
	}
	if _, err := fmt.Fprint(a.out, "\n"+a.paint("1;33", "Apply these changes? [y/N] ")); err != nil {
		return err
	}
	type reply struct {
		text string
		err  error
	}
	input := make(chan reply, 1)
	go func() {
		text, err := bufio.NewReader(io.LimitReader(a.in, 257)).ReadString('\n')
		input <- reply{text, err}
	}()
	select {
	case <-a.ctx.Done():
		fmt.Fprintln(a.out)
		return a.ctx.Err()
	case r := <-input:
		fmt.Fprintln(a.out)
		if r.err != nil || len(r.text) > 256 {
			return model.Err("confirmation_declined", "Cancelled; no integration changes were made")
		}
		answer := strings.ToLower(strings.TrimSpace(r.text))
		if answer != "y" && answer != "yes" {
			return model.Err("confirmation_declined", "Cancelled; no integration changes were made")
		}
	}
	return plan.Check()
}

func (a *app) installationReport(report install.Report) error {
	var b strings.Builder
	verb := "Install"
	if a.commandName == "uninstall" {
		verb = "Uninstall"
	}
	if report.DryRun {
		verb += " preview"
	} else {
		verb += " result"
	}
	showDiff := a.showDiff && !a.diffShown
	hasChanges := false
	fmt.Fprintln(&b, a.paint("1;36", "ARM · "+verb))
	if report.Adoption != "" {
		fmt.Fprintln(&b, terminalSafe(report.Adoption))
	}
	if report.DryRun {
		fmt.Fprintln(&b, "Review the instruction-file changes below. Nothing has been changed.")
	}
	explanations := map[string]string{
		"stop-idle-backend":            "Stop the local backend if it is running and idle",
		"backend-idle":                 "Local backend is stopped; retain state and history",
		"add-managed-instructions":     "Add ARM instructions; preserve existing content",
		"upgrade-managed-instructions": "Update only the verified ARM instruction block",
		"resume-upgrade":               "Finish an interrupted instruction upgrade",
		"resume-install":               "Finish an interrupted instruction installation",
		"unchanged":                    "Already up to date; preserve this file",
		"already-absent":               "ARM instructions are already absent",
		"preserved":                    "Keep this edited file; manual resolution is needed",
		"remove-managed-instructions":  "Remove only the verified ARM block; keep other content",
		"remove-created-file":          "Remove the empty instruction file originally created by ARM",
	}
	for _, action := range report.Actions {
		text := explanations[action.Action]
		if text == "" {
			text = action.Action
		}
		color := "32"
		if action.Action == "preserved" {
			color = "33"
		}
		fmt.Fprintln(&b, "\n  "+a.paint(color, text))
		fmt.Fprintln(&b, "    "+terminalSafe(action.Path))
		if action.Detail != "" {
			fmt.Fprintln(&b, "    "+terminalSafe(action.Detail))
		}
		if change := action.Change; change != nil {
			hasChanges = true
			kind := "Modify existing file"
			if !change.BeforeExists {
				kind = "Create new file"
			}
			if !change.AfterExists {
				kind = "Delete ARM-created file"
			}
			if change.Binary {
				fmt.Fprintf(&b, "    %s · non-UTF-8 content\n", kind)
			} else {
				fmt.Fprintf(&b, "    %s · %d line(s) added, %d removed\n", kind, change.Added, change.Removed)
			}
			fmt.Fprintf(&b, "    %s → %s\n", humanBytes(fmt.Sprint(change.BeforeBytes)), humanBytes(fmt.Sprint(change.AfterBytes)))
			if change.Binary {
				fmt.Fprintln(&b, "    Non-UTF-8 file: textual hunks unavailable; exact bytes are identified by SHA-256.")
				fmt.Fprintln(&b, "    Before: "+change.BeforeHash+"\n    After:  "+change.AfterHash)
			}
			if showDiff {
				fmt.Fprintln(&b)
				for _, line := range strings.Split(strings.TrimSuffix(change.Diff, "\n"), "\n") {
					color := "2"
					if strings.HasPrefix(line, "+") {
						color = "32"
					}
					if strings.HasPrefix(line, "-") {
						color = "31"
					}
					if strings.HasPrefix(line, "@@") {
						color = "36"
					}
					fmt.Fprintln(&b, "    "+a.paint(color, terminalSafe(line)))
				}
			}
		}
	}
	if len(report.Actions) == 0 {
		if report.Complete {
			fmt.Fprintln(&b, "\nNo managed instruction files to change.")
		} else {
			fmt.Fprintln(&b, "\nNo instruction files were changed before the operation stopped.")
		}
	}
	if report.DryRun && hasChanges && !a.showDiff {
		fmt.Fprintln(&b, "\nUse --diff to inspect the exact changes, or --dry-run --diff for a read-only preview.")
	}
	if showDiff {
		a.diffShown = true
	}
	if report.BackupDirectory != "" {
		label := "Planned backup directory (originals and metadata; retained on uninstall):"
		if !report.DryRun {
			label = "Backup directory (originals and metadata; retained on uninstall):"
			if !report.BackupComplete {
				label = "Backup directory (incomplete; inspect the reported error):"
			}
		}
		fmt.Fprintln(&b, "\n"+label+"\n  "+terminalSafe(report.BackupDirectory))
		for _, entry := range report.Backups {
			if entry.Exists {
				fmt.Fprintln(&b, "  "+terminalSafe(entry.Source)+" → "+terminalSafe(entry.Saved))
			} else {
				fmt.Fprintln(&b, "  "+terminalSafe(entry.Source)+" was absent; recorded in "+terminalSafe(entry.Metadata))
			}
		}
	}
	if len(report.Retained) > 0 {
		fmt.Fprintln(&b, "\nRetained state and history:")
		for _, path := range report.Retained {
			fmt.Fprintln(&b, "  "+terminalSafe(path))
		}
		fmt.Fprintln(&b, "The ARM executable is also kept. Uninstall removes managed agent instructions.")
	}
	if !report.Complete {
		fmt.Fprintln(&b, "\n"+a.paint("33", "Some changes are incomplete. Review preservation decisions and any reported error."))
	}
	_, err := io.WriteString(a.out, b.String())
	return err
}
