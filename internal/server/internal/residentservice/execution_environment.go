package residentservice

import (
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxExecutionEnvironmentBytes = 4096
const MaxExecutionEnvironmentEntries = 9

// Schema2 profile only. Omitted on schema1, preserving its EXACT canonical JSON
// and digest. Entries are explicit key=value bytes, sorted once by unique key.
// There is no merge with os.Environ, implicit PATH/debug/loader inheritance or
// source-guessed CPU/memory reservation. Values remain private operator inputs.
type ExecutionEnvironment struct {
	Schema  uint32   `json:"schema"`
	Entries []string `json:"entries"`
}

func (e *ExecutionEnvironment) Validate() (heapLimit int64, resultErr error) {
	if e == nil || e.Schema != 1 || len(e.Entries) < 2 || len(e.Entries) > MaxExecutionEnvironmentEntries {
		return 0, ErrInvalid
	}
	total, previous := 0, ""
	scheduler, heap := false, false
	for _, entry := range e.Entries {
		total += len(entry) + 1
		if total > MaxExecutionEnvironmentBytes || !utf8.ValidString(entry) {
			return 0, ErrInvalid
		}
		key, value, found := strings.Cut(entry, "=")
		if !found || key <= previous || value == "" || len(value) > 4096 {
			return 0, ErrInvalid
		}
		previous = key
		for _, letter := range value {
			if unicode.IsControl(letter) {
				return 0, ErrInvalid
			}
		}
		switch key {
		case "GOMAXPROCS":
			n, err := strconv.ParseUint(value, 10, 32)
			if err != nil || n == 0 || n > 65536 || strconv.FormatUint(n, 10) != value {
				return 0, ErrInvalid
			}
			scheduler = true
		case "GOMEMLIMIT":
			var err error
			heapLimit, err = parseMemoryEnvironment(value)
			if err != nil {
				return 0, err
			}
			heap = true
		case "GOGC":
			n, err := strconv.ParseUint(value, 10, 32)
			if err != nil || n == 0 || n > 1000000 || strconv.FormatUint(n, 10) != value {
				return 0, ErrInvalid
			}
		case "HOME", "TMPDIR":
			if !filepath.IsAbs(value) || filepath.Clean(value) != value {
				return 0, ErrInvalid
			}
		case "PATH":
			paths := filepath.SplitList(value)
			if len(paths) == 0 || len(paths) > 32 {
				return 0, ErrInvalid
			}
			for _, path := range paths {
				if !filepath.IsAbs(path) || filepath.Clean(path) != path {
					return 0, ErrInvalid
				}
			}
		case "LANG", "LC_ALL":
			if value != "C" && value != "C.UTF-8" {
				return 0, ErrInvalid
			}
		case "TZ":
			if value != "UTC" {
				return 0, ErrInvalid
			}
		default:
			return 0, ErrInvalid // Includes LD_*,DYLD_*,GODEBUG,GOFLAGS,agent/secret keys.
		}
	}
	if !scheduler || !heap {
		return 0, ErrInvalid
	}
	return heapLimit, nil
}
func parseMemoryEnvironment(value string) (int64, error) {
	for _, unit := range []struct {
		name  string
		shift uint
	}{{"EiB", 60}, {"PiB", 50}, {"TiB", 40}, {"GiB", 30}, {"MiB", 20}, {"KiB", 10}, {"B", 0}} {
		if !strings.HasSuffix(value, unit.name) {
			continue
		}
		digits := strings.TrimSuffix(value, unit.name)
		n, err := strconv.ParseUint(digits, 10, 63)
		if err != nil || n == 0 || strconv.FormatUint(n, 10) != digits || n > (uint64(1<<63-1)>>unit.shift) {
			return 0, ErrInvalid
		}
		return int64(n << unit.shift), nil
	}
	return 0, ErrInvalid
}
func equalEnvironmentEntries(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func (e *ExecutionEnvironment) Clone() *ExecutionEnvironment {
	if e == nil {
		return nil
	}
	return &ExecutionEnvironment{Schema: e.Schema, Entries: append([]string(nil), e.Entries...)}
}
func (e *ExecutionEnvironment) Equal(other *ExecutionEnvironment) bool {
	return e != nil && other != nil && e.Schema == other.Schema && equalEnvironmentEntries(e.Entries, other.Entries)
}
func cloneMeasuredProfile(profile MeasuredProfile) MeasuredProfile {
	profile.RuntimeEnvironment = profile.RuntimeEnvironment.Clone()
	return profile
}
func (p MeasuredProfile) RequireP1Environment() error {
	if p.Schema != 2 || p.RuntimeEnvironment == nil {
		return ErrInvalid
	}
	return p.Validate()
}

// Exact semantic runtime equivalence, with duplicate/injected keys refused.
// A private copy is sorted; the caller's original snapshot remains retained.
func (e *ExecutionEnvironment) MatchesActual(actual []string) error {
	if _, err := e.Validate(); err != nil {
		return err
	}
	if len(actual) != len(e.Entries) {
		return ErrInvalid
	}
	copied := append([]string(nil), actual...)
	sort.Strings(copied)
	candidate := &ExecutionEnvironment{Schema: 1, Entries: copied}
	if _, err := candidate.Validate(); err != nil {
		return err
	}
	if !equalEnvironmentEntries(copied, e.Entries) {
		return ErrInvalid
	}
	return nil
}

// Must run against the SAME actual Cmd after all potentially long image/fence
// I/O and immediately before its ONE Start; nil Env would silently inherit.
func (p *P1ServiceGroup) ValidateExecutionEnvironmentOriginal(command *exec.Cmd) error {
	if p == nil || p.p1ServiceState == nil || p.self != p || p.parent == nil || p.parent.owner == nil {
		return ErrInvalid
	}
	o := p.parent.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.p1 != p || o.active != nil || o.firstError != nil || p.command != command || command == nil || command.Env == nil || command.Process != nil || !equalEnvironmentEntries(command.Env, p.environment) {
		return ErrInvalid
	}
	return nil
}
func (p *P1ServiceGroup) ExecutionEnvironmentOriginalData() ([]string, error) {
	if p == nil || p.p1ServiceState == nil || p.self != p || p.parent == nil || p.parent.owner == nil {
		return nil, ErrInvalid
	}
	o := p.parent.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.p1 != p || o.active != nil || o.firstError != nil || o.grant == nil || o.grant.Intent.Profile.RequireP1Environment() != nil {
		return nil, ErrInvalid
	}
	return append([]string(nil), p.environment...), nil
}

// Actual public-start parent checks this on its unused original grant BEFORE
// committing a debit/creating image or fence namespace. It is DATA, no new grant.
func (p *ParentPending) P1EnvironmentOriginalData() ([]string, error) {
	if p == nil || p.owner == nil || p.owner.parent != p {
		return nil, ErrInvalid
	}
	o := p.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active != nil || o.firstError != nil || !o.opened || o.debitTried || o.grant == nil || len(o.journal.records) != 1 || o.grant.Intent.Profile.RequireP1Environment() != nil {
		return nil, ErrInvalid
	}
	return append([]string(nil), o.grant.Intent.Profile.RuntimeEnvironment.Entries...), nil
}

// DATA predicate, never process/admission authority. Exact command bytes are
// checked on the actual original by ValidateExecutionEnvironmentOriginal too.
func (e *ExecutionEnvironment) MatchesCommandData(command *exec.Cmd) error {
	if command == nil || command.Env == nil {
		return ErrInvalid
	}
	if _, err := e.Validate(); err != nil {
		return err
	}
	if !equalEnvironmentEntries(e.Entries, command.Env) {
		return ErrInvalid
	}
	return nil
}

// Pure preflight DATA from the actual UNUSED original grant; never constructs
// a debit/charge/root. Caller receives no aliases into its immutable profile.
type P1LaunchInputsData struct {
	Home              string
	Build             Build
	ProfileSHA256     string
	Environment       []string
	GrantJournalBytes int64
}

func (p *ParentPending) P1LaunchInputsOriginalData() (P1LaunchInputsData, error) {
	if p == nil || p.owner == nil || p.owner.parent != p {
		return P1LaunchInputsData{}, ErrInvalid
	}
	o := p.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active != nil || o.firstError != nil || !o.buildFromOriginalGrant || !o.opened || o.debitTried || o.grant == nil || len(o.journal.records) != 1 || o.journal.records[0].Grant != o.grant || o.grant.Intent.Profile.RequireP1Environment() != nil || len(o.reads) != 1 {
		return P1LaunchInputsData{}, ErrInvalid
	}
	read := o.reads[0]
	if read == nil || read.owner != o || read.stage != "open" || !read.finished || read.firstError != nil || read.before == nil || read.after == nil || read.bytes <= 0 || read.bytes > MaxJournalBytes || len(read.data) != read.bytes || int64(read.bytes) != o.journal.bytes || read.after.Size() != o.journal.bytes {
		return P1LaunchInputsData{}, ErrInvalid
	}
	hash, err := o.grant.Intent.Profile.SHA256()
	if err != nil {
		return P1LaunchInputsData{}, err
	}
	return P1LaunchInputsData{Home: o.path, Build: o.grant.Intent.Profile.Build, ProfileSHA256: hash, Environment: append([]string(nil), o.grant.Intent.Profile.RuntimeEnvironment.Entries...), GrantJournalBytes: int64(read.bytes)}, nil
}
