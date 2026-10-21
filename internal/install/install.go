package install

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
)

const begin = "<!-- arm:begin v1 -->"
const end = "<!-- arm:end -->"
const instructions = begin + `
## Agent Resource Manager

Use arm for resource-intensive commands. Start with arm status --json.
Use arm run --cpu N --memory SIZE --ttl DURATION --reason "purpose" -- COMMAND.
Use --wait to queue for capacity. A grant is required before work begins.
Use arm release LEASE_ID when done and arm renew LEASE_ID --ttl DURATION
before expiry when more time is needed. Limits and expiry can terminate
supervised work; follow ARM warnings and stop using revoked resources.
Register your exact session with arm agent register so ARM can contact you.
Use arm message operator --text "message" to contact the human operator.
Use arm messages --json to inspect the inbox and delivery status.
` + end + "\n"

type Target struct {
	Adapter string `json:"adapter"`
	Root    string `json:"root"`
	Name    string `json:"name"`
}
type Entry struct {
	Target
	Block        string `json:"block"`
	Created      bool   `json:"created"`
	OriginalHash string `json:"original_hash"`
	RootID       string `json:"root_id"`
}
type Manifest struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}
type Action struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Detail string `json:"detail,omitempty"`
}
type Report struct {
	DryRun   bool     `json:"dry_run"`
	Complete bool     `json:"complete"`
	Actions  []Action `json:"actions"`
	Retained []string `json:"retained,omitempty"`
}

func readManifest(home string) (Manifest, error) {
	m := Manifest{Version: 1, Entries: []Entry{}}
	b, e := os.ReadFile(filepath.Join(home, "install.json"))
	if os.IsNotExist(e) {
		return m, nil
	}
	if e != nil {
		return m, e
	}
	e = json.Unmarshal(b, &m)
	if e == nil && m.Version != 1 {
		e = fmt.Errorf("unsupported installation manifest version")
	}
	return m, e
}
func save(home string, m Manifest) error {
	return local.AtomicJSON(filepath.Join(home, "install.json"), m)
}

func read(root, name string, expected ...string) ([]byte, error) {
	r, e := os.OpenRoot(root)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	defer r.Close()
	if e = checkRoot(r, expected); e != nil {
		return nil, e
	}
	info, e := r.Lstat(name)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() {
		return nil, model.Err("unsafe_path", "instruction target must be a regular file")
	}
	return r.ReadFile(name)
}
func write(root, name string, b []byte, expected ...string) error {
	r, e := os.OpenRoot(root)
	if e != nil {
		return e
	}
	defer r.Close()
	if e = checkRoot(r, expected); e != nil {
		return e
	}
	mode := os.FileMode(0600)
	if info, e := r.Lstat(name); e == nil {
		if !info.Mode().IsRegular() {
			return model.Err("unsafe_path", "instruction target must be a regular file")
		}
		mode = info.Mode().Perm()
	}
	tmp := ".arm-" + local.ID()
	f, e := r.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer r.Remove(tmp)
	if e = f.Chmod(mode); e != nil {
		f.Close()
		return e
	}
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return r.Rename(tmp, name)
}

func Install(home string, targets []Target, dry bool) (Report, error) {
	report := Report{DryRun: dry, Complete: true, Actions: []Action{}}
	if !dry {
		lock, e := local.Lock(home, "integration.lock")
		if e != nil {
			return report, e
		}
		defer lock.Close()
	}
	m, e := readManifest(home)
	if e != nil {
		return report, e
	}
	// Validate every target before modifying any instruction file.
	for _, t := range targets {
		if !filepath.IsAbs(t.Root) || (t.Name != "AGENTS.md" && t.Name != "CLAUDE.md") {
			return report, model.Err("invalid_target", "integration target must be an absolute supported agent root")
		}
		b, e := read(t.Root, t.Name)
		if e != nil {
			return report, e
		}
		known := false
		for _, entry := range m.Entries {
			if entry.Root == t.Root && entry.Name == t.Name {
				known = true
				if _, e = read(t.Root, t.Name, entry.RootID); e != nil {
					return report, e
				}
				if strings.Count(string(b), entry.Block) != 1 && digest(b) != entry.OriginalHash {
					return report, model.Err("integration_modified", "managed instructions changed; preserve and resolve them before reinstalling")
				}
			}
		}
		if !known && (strings.Contains(string(b), begin) || strings.Contains(string(b), end)) {
			return report, model.Err("integration_conflict", "unrecognized managed marker already exists")
		}
	}
	if !dry {
		if e = local.Prepare(home); e != nil {
			return report, e
		}
	}
	for _, t := range targets {
		path := filepath.Join(t.Root, t.Name)
		found := false
		for _, entry := range m.Entries {
			if entry.Root == t.Root && entry.Name == t.Name {
				found = true
			}
		}
		if found {
			var existing Entry
			for _, entry := range m.Entries {
				if entry.Root == t.Root && entry.Name == t.Name {
					existing = entry
					break
				}
			}
			b, e := read(t.Root, t.Name, existing.RootID)
			if e != nil {
				return report, e
			}
			action := "unchanged"
			if strings.Count(string(b), existing.Block) != 1 {
				action = "resume-install"
				if !dry {
					if e = os.MkdirAll(t.Root, 0700); e != nil {
						return report, e
					}
					if e = write(t.Root, t.Name, append(b, []byte(existing.Block)...), existing.RootID); e != nil {
						return report, e
					}
				}
			}
			report.Actions = append(report.Actions, Action{Path: path, Action: action})
			continue
		}
		b, e := read(t.Root, t.Name)
		if e != nil {
			return report, e
		}
		block := instructions
		executable, e := os.Executable()
		if e != nil {
			return report, e
		}
		binding := "\nARM executable: `" + executable + "`\nARM state directory: `" + home + "`\nUse this exact command prefix for this installation: `" + shellQuote(executable) + " --home " + shellQuote(home) + "`. In the examples above, arm is shorthand for this prefix.\n"
		block = strings.Replace(block, end, binding+end, 1)
		if len(b) > 0 {
			block = "\n\n" + block
		}
		report.Actions = append(report.Actions, Action{Path: path, Action: "add-managed-instructions"})
		if dry {
			continue
		}
		_, statErr := os.Stat(path)
		if e = os.MkdirAll(t.Root, 0700); e != nil {
			return report, e
		}
		rootInfo, e := os.Stat(t.Root)
		if e != nil {
			return report, e
		}
		entry := Entry{Target: t, Block: block, Created: os.IsNotExist(statErr), OriginalHash: digest(b)}
		entry.RootID = fileIdentity(rootInfo)
		m.Entries = append(m.Entries, entry)
		if e = save(home, m); e != nil {
			return report, e
		}
		if e = os.MkdirAll(t.Root, 0700); e != nil {
			return report, e
		}
		if e = write(t.Root, t.Name, append(b, []byte(block)...), entry.RootID); e != nil {
			return report, e
		}
	}
	return report, nil
}

func Uninstall(home string, dry bool) (Report, error) {
	report := Report{DryRun: dry, Complete: true, Actions: []Action{}, Retained: []string{home}}
	if !dry {
		lock, e := local.Lock(home, "integration.lock")
		if e != nil {
			return report, e
		}
		defer lock.Close()
	}
	m, e := readManifest(home)
	if e != nil {
		return report, e
	}
	remaining := []Entry{}
	for _, entry := range m.Entries {
		path := filepath.Join(entry.Root, entry.Name)
		b, e := read(entry.Root, entry.Name, entry.RootID)
		if e != nil {
			return report, e
		}
		if len(b) == 0 {
			report.Actions = append(report.Actions, Action{Path: path, Action: "already-absent"})
			continue
		}
		if strings.Count(string(b), entry.Block) != 1 {
			if digest(b) == entry.OriginalHash {
				report.Actions = append(report.Actions, Action{Path: path, Action: "already-absent"})
				continue
			}
			report.Complete = false
			remaining = append(remaining, entry)
			report.Actions = append(report.Actions, Action{Path: path, Action: "preserved", Detail: "managed instructions were edited; manual resolution required"})
			continue
		}
		rest := []byte(strings.Replace(string(b), entry.Block, "", 1))
		action := "remove-managed-instructions"
		if len(rest) == 0 && entry.Created {
			action = "remove-created-file"
		}
		report.Actions = append(report.Actions, Action{Path: path, Action: action})
		if dry {
			remaining = append(remaining, entry)
			continue
		}
		if action == "remove-created-file" {
			r, e := os.OpenRoot(entry.Root)
			if e != nil {
				return report, e
			}
			if e = checkRoot(r, []string{entry.RootID}); e != nil {
				r.Close()
				return report, e
			}
			e = r.Remove(entry.Name)
			r.Close()
			if e != nil {
				return report, e
			}
		} else if e = write(entry.Root, entry.Name, rest, entry.RootID); e != nil {
			return report, e
		}
	}
	if !dry && len(m.Entries) > 0 {
		m.Entries = remaining
		if e = save(home, m); e != nil {
			return report, e
		}
	}
	return report, nil
}

func digest(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

func fileIdentity(info os.FileInfo) string {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino)
}
func checkRoot(root *os.Root, expected []string) error {
	if len(expected) == 0 || expected[0] == "" {
		return nil
	}
	info, e := root.Stat(".")
	if e != nil {
		return e
	}
	if fileIdentity(info) != expected[0] {
		return model.Err("unsafe_path", "agent root was replaced; refusing to modify a different directory")
	}
	return nil
}
