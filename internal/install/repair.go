package install

import (
	"fmt"
	"path/filepath"
	"strings"

	"getarm.org/internal/local"
	"getarm.org/internal/model"
)

// PreviewRepair reviews new identities at the original paths. Only the
// manifest is updated, after verifying the exact recorded instruction blocks.
func PreviewRepair(home string) (*Plan, error) {
	const manifestName = "install.json"
	data, err := readReadinessFile(home, manifestName, "", readinessBytes)
	if err != nil {
		return nil, err
	}
	manifest, err := decodeInstructionManifest(data)
	if err != nil {
		return nil, err
	}
	var targets []Target
	seen := map[string]bool{}
	for _, entry := range manifest.Entries {
		path := filepath.Join(entry.Root, entry.Name)
		validName := entry.Adapter == "codex" && entry.Name == "AGENTS.md" ||
			entry.Adapter == "claude" && entry.Name == "CLAUDE.md"
		if !validName || !filepath.IsAbs(entry.Root) || entry.RootID == "" || entry.Block == "" || seen[path] {
			return nil, model.Err("invalid_target", "invalid or duplicate recorded instruction target")
		}
		seen[path] = true
		targets = append(targets, entry.Target)
	}
	before, err := capture(home, targets)
	if err != nil {
		return nil, err
	}
	if before.files[filepath.Join(home, manifestName)].Hash != digest(data) {
		return nil, model.Err("plan_changed", "manifest changed during repair preview")
	}
	report := Report{
		Repair: true, DryRun: true, Complete: true, Actions: []Action{},
		Adoption: "Repair updates the installation manifest only. Instruction files and rollout policy are preserved.",
	}
	budget := readinessBytes - len(data)
	for i, entry := range manifest.Entries {
		path := filepath.Join(entry.Root, entry.Name)
		_, rootID, _ := strings.Cut(before.dirs[filepath.Clean(entry.Root)].Identity, "/")
		if rootID == entry.RootID {
			continue
		}
		if rootID == "" {
			return nil, model.Err("unsafe_path", fmt.Sprintf("cannot verify the current instruction root for %q", path))
		}
		block, err := readReadinessFile(entry.Root, entry.Name, rootID, min(budget, 1<<20))
		if err != nil {
			return nil, err
		}
		budget -= len(block)
		if digest(block) != before.files[path].Hash {
			return nil, model.Err("plan_changed", "instruction file changed during repair preview")
		}
		if matchingBlock(block, entry) == "" {
			return nil, model.Err("integration_modified", fmt.Sprintf("repair requires the exact recorded managed block in %q", path))
		}
		manifest.Entries[i].RootID = rootID
		report.Actions = append(report.Actions, Action{
			Path: path, Action: "repair-root-identity",
			Detail: fmt.Sprintf("Verified the managed block. Update the saved identity from %s to %s.", entry.RootID, rootID),
		})
	}
	if err = before.check(home, false); err != nil {
		return nil, err
	}
	if len(report.Actions) != 0 {
		before.backupID = local.ID()
		before.backupOperation = "install-repair"
		report.BackupDirectory = filepath.Join(home, "install-backups", before.backupID)
	}
	return &Plan{Report: report, home: home, repair: &manifest, snapshot: before}, nil
}

func (p *Plan) applyRepair() (Report, error) {
	report := p.Report
	report.DryRun = false
	if err := p.Check(); err != nil {
		return report, err
	}
	if p.snapshot.backupID == "" {
		return report, nil
	}
	lock, err := local.Lock(p.home, "integration.lock")
	if err != nil {
		return report, err
	}
	defer lock.Close()
	if err = p.Check(); err != nil {
		return report, err
	}
	backup := *p.snapshot
	path := filepath.Join(p.home, "install.json")
	backup.files = map[string]pathState{path: p.snapshot.files[path]}
	report.Backups, err = backup.backup(p.home)
	if err != nil {
		return report, err
	}
	report.BackupComplete = true
	if err = p.Check(); err != nil {
		return report, err
	}
	return report, save(p.home, *p.repair)
}
