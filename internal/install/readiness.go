package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const readinessBytes = 8 << 20

type InstructionTarget struct {
	Adapter string `json:"adapter"`
	Path    string `json:"path"`
	State   string `json:"state"`
	Detail  string `json:"detail"`
}

type InstructionReadiness struct {
	Ready   bool                `json:"ready"`
	State   string              `json:"state"`
	Summary string              `json:"summary"`
	Targets []InstructionTarget `json:"targets"`
}

func readinessDetail(err error) string {
	s := err.Error()
	if len(s) > 512 {
		s = s[:512] + "…"
	}
	return s
}

// InspectInstructions reads only recorded installation targets. It never scans
// client projects, starts clients, creates state or changes rollout authority.
// Matching files do not prove that an ongoing session has read them.
func InspectInstructions(home string) InstructionReadiness {
	out := InstructionReadiness{State: "not-installed", Summary: "No ARM-managed instructions are recorded. Run `arm install` before starting a rollout.", Targets: []InstructionTarget{}}
	b, e := readReadinessFile(home, "install.json", "", readinessBytes)
	if errors.Is(e, os.ErrNotExist) {
		return out
	}
	unknown := func(err error) InstructionReadiness {
		out.State = "unknown"
		out.Summary = "Instruction readiness could not be verified: " + readinessDetail(err)
		return out
	}
	if e != nil {
		return unknown(e)
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(&manifest); e != nil {
		return unknown(e)
	}
	if decoder.Decode(new(any)) != io.EOF || manifest.Version != 1 || len(manifest.Entries) > 128 {
		return unknown(fmt.Errorf("unsupported or invalid installation record"))
	}
	if len(manifest.Entries) == 0 {
		return out
	}
	budget := readinessBytes - len(b)
	ready := 0
	paths := map[string]bool{}
	for _, entry := range manifest.Entries {
		target := InstructionTarget{Adapter: entry.Adapter, Path: filepath.Join(entry.Root, entry.Name), State: "unknown"}
		validName := entry.Adapter == "codex" && entry.Name == "AGENTS.md" || entry.Adapter == "claude" && entry.Name == "CLAUDE.md"
		if !validName || !filepath.IsAbs(entry.Root) || entry.RootID == "" || entry.Block == "" || paths[target.Path] {
			target.Detail = "Invalid or duplicate recorded instruction target. Preserve and inspect the installation record."
		} else {
			paths[target.Path] = true
			limit := min(budget, 1<<20)
			data, err := readReadinessFile(entry.Root, entry.Name, entry.RootID, limit)
			budget -= len(data)
			switch {
			case errors.Is(err, os.ErrNotExist):
				target.State = "missing"
				target.Detail = "Recorded instruction file is absent."
			case err != nil:
				target.Detail = readinessDetail(err)
			default:
				block := matchingBlock(data, entry)
				switch {
				case block == "":
					if !strings.Contains(string(data), begin) && !strings.Contains(string(data), end) {
						target.State = "missing"
						target.Detail = "ARM-managed instruction block is absent."
					} else {
						target.State = "modified"
						target.Detail = "Managed instructions changed. Preserve edits and resolve them before reinstalling."
					}
				case entry.PendingBlock != "":
					target.State = "pending"
					target.Detail = "Instruction installation is unfinished; review `arm install` to resume."
				default:
					desired, err := instructionBlock(home, strings.HasPrefix(block, "\n\n"))
					if err != nil {
						target.Detail = readinessDetail(err)
					} else if block != desired {
						target.State = "outdated"
						target.Detail = "Recorded instructions differ from this build; review `arm install` to update."
					} else {
						target.State = "ready"
						target.Detail = "Managed instructions match this build."
						ready++
					}
				}
			}
		}
		out.Targets = append(out.Targets, target)
	}
	out.State = "not-ready"
	out.Summary = "Managed instructions need attention. Review the targets below; saved rollout policy is unchanged."
	if ready > 0 {
		out.State = "partial"
		out.Summary = fmt.Sprintf("%d of %d recorded instruction targets are ready.", ready, len(out.Targets))
	}
	if ready == len(out.Targets) {
		out.Ready = true
		out.State = "ready"
		out.Summary = "ARM-managed instructions are installed and match this build. Session acknowledgment is still required."
	}
	return out
}

func readReadinessFile(root, name, identity string, limit int) ([]byte, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("instruction-read budget exceeded")
	}
	r, e := os.OpenRoot(root)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	if identity != "" {
		if e = checkRoot(r, []string{identity}); e != nil {
			return nil, e
		}
	}
	f, e := r.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	before, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !before.Mode().IsRegular() || before.Size() > int64(limit) {
		return nil, fmt.Errorf("instruction record must be a bounded regular file")
	}
	b, e := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if e != nil {
		return nil, e
	}
	if len(b) > limit {
		return nil, fmt.Errorf("instruction-read budget exceeded")
	}
	after, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if fileIdentity(before) != fileIdentity(after) || before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		return nil, fmt.Errorf("instructions changed during readiness inspection; inspect again")
	}
	named, e := r.Lstat(name)
	if e != nil {
		return nil, e
	}
	if fileIdentity(named) != fileIdentity(after) || named.Size() != after.Size() || named.ModTime() != after.ModTime() {
		return nil, fmt.Errorf("instruction path changed during readiness inspection; inspect again")
	}
	heldRoot, e := r.Stat(".")
	if e != nil {
		return nil, e
	}
	namedRoot, e := os.Stat(root)
	if e != nil {
		return nil, e
	}
	if fileIdentity(heldRoot) != fileIdentity(namedRoot) {
		return nil, fmt.Errorf("instruction root changed during readiness inspection; inspect again")
	}
	return b, nil
}
