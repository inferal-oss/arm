// Package rollout manages cooperative agent adoption, independently of resource
// admission and messaging contacts. Reading policy never discovers agents.
package rollout

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"getarm.org/internal/local"
	"github.com/google/uuid"
)

const (
	Protocol    = 1
	Liveness    = 15 * time.Minute
	maxBytes    = 8 << 20
	maxProjects = 128
	maxSessions = 1024
	maxEvents   = 4096
)

type Binding struct {
	Kind         string `json:"kind"`
	Root         string `json:"root"`
	Identity     string `json:"directory_identity"`
	GitDirectory string `json:"git_directory,omitempty"`
	GitIdentity  string `json:"git_directory_identity,omitempty"`
}
type ClientBinding struct {
	Adapter string `json:"adapter"`
	ID      string `json:"id"`
}
type Project struct {
	ID       string          `json:"id"`
	Bindings []Binding       `json:"bindings"`
	Clients  []ClientBinding `json:"clients"`
	Mode     string          `json:"mode"`
	Paused   bool            `json:"paused"`
	Revision int             `json:"revision"`
	NewAfter time.Time       `json:"new_after,omitempty"`
}
type Session struct {
	Project        string    `json:"project"`
	Adapter        string    `json:"adapter"`
	Session        string    `json:"session"`
	Identity       string    `json:"identity"`
	ClientProject  string    `json:"client_project,omitempty"`
	CWD            string    `json:"cwd"`
	Started        time.Time `json:"started_at,omitempty"`
	FirstSeen      time.Time `json:"first_seen"`
	LastSeen       time.Time `json:"last_seen"`
	Expires        time.Time `json:"expires_at"`
	Protocol       int       `json:"protocol"`
	Needs          string    `json:"needs"`
	Capabilities   []string  `json:"capabilities"`
	State          string    `json:"state"`
	PolicyRevision int       `json:"policy_revision"`
}
type Event struct {
	Revision int       `json:"revision"`
	At       time.Time `json:"at"`
	Actor    string    `json:"actor"`
	Action   string    `json:"action"`
	Project  string    `json:"project"`
	Sessions []string  `json:"sessions,omitempty"`
	Reason   string    `json:"reason"`
	Policy   *Project  `json:"policy,omitempty"`
}
type State struct {
	Version  int       `json:"version"`
	Revision int       `json:"revision"`
	Projects []Project `json:"projects"`
	Sessions []Session `json:"sessions"`
	History  []Event   `json:"history"`
}
type Store struct {
	Home string
	Now  func() time.Time
}

func (s Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func empty() State {
	return State{Version: Protocol, Projects: []Project{}, Sessions: []Session{}, History: []Event{}}
}
func fail(format string, args ...any) error { return fmt.Errorf("rollout: "+format, args...) }
func short(v string, max int) bool {
	return v != "" && len(v) <= max && !strings.ContainsAny(v, "\x00\r\n")
}
func adapter(v string) bool { return v == "codex" || v == "claude" || v == "generic" }
func projectID(v string) bool {
	if !short(v, 64) {
		return false
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func canonical(path string) (string, error) {
	p, e := filepath.Abs(path)
	if e != nil {
		return "", e
	}
	p, e = filepath.EvalSymlinks(p)
	if e != nil {
		return "", e
	}
	fi, e := os.Stat(p)
	if e != nil {
		return "", e
	}
	if !fi.IsDir() {
		return "", fail("scope must be a directory")
	}
	return p, nil
}
func directoryIdentity(path string) (string, error) {
	fi, e := os.Stat(path)
	if e != nil {
		return "", e
	}
	if !fi.IsDir() {
		return "", fail("scope is no longer a directory")
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fail("directory identity unavailable")
	}
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino), nil
}
func gitDirectory(path, selector string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", selector)
	// Caller Git overrides must not redirect the selected repository.
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "GIT_") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	b, e := cmd.Output()
	if e != nil {
		return "", fail("cannot resolve Git checkout for selected directory")
	}
	return canonical(strings.TrimSuffix(string(b), "\n"))
}
func gitRoot(path string) (string, error) { return gitDirectory(path, "--show-toplevel") }
func NewBinding(kind, path string) (Binding, error) {
	p, e := canonical(path)
	if e != nil {
		return Binding{}, e
	}
	if kind == "git" {
		p, e = gitRoot(p)
		if e != nil {
			return Binding{}, e
		}
	} else if kind != "directory" {
		return Binding{}, fail("binding kind must be directory or git")
	}
	id, e := directoryIdentity(p)
	if e != nil {
		return Binding{}, e
	}
	b := Binding{Kind: kind, Root: p, Identity: id}
	if kind == "git" {
		b.GitDirectory, e = gitDirectory(p, "--absolute-git-dir")
		if e != nil {
			return Binding{}, e
		}
		b.GitIdentity, e = directoryIdentity(b.GitDirectory)
	}
	return b, e
}
func within(root, path string) bool {
	rel, e := filepath.Rel(root, path)
	return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

func (s Store) Read() (State, error) {
	out := empty()
	root, e := os.OpenRoot(s.Home)
	if errors.Is(e, os.ErrNotExist) {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	defer root.Close()
	f, e := root.OpenFile("rollout.json", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(e, os.ErrNotExist) {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	defer f.Close()
	fi, e := f.Stat()
	if e != nil {
		return out, e
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxBytes {
		return out, fail("ledger must be a bounded regular file")
	}
	b, e := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if e != nil {
		return out, e
	}
	if len(b) > maxBytes {
		return out, fail("ledger exceeds size bound")
	}
	out = State{}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(&out); e != nil {
		return out, e
	}
	if decoder.Decode(new(any)) != io.EOF {
		return out, fail("invalid trailing ledger data")
	}
	if e = validate(out); e != nil {
		return out, e
	}
	return out, nil
}
func validate(st State) error {
	if st.Version != Protocol || len(st.Projects) > maxProjects || len(st.Sessions) > maxSessions || len(st.History) > maxEvents || st.Revision < 0 || st.Revision == int(^uint(0)>>1) {
		return fail("unsupported or invalid ledger; preserve it for inspection")
	}
	projects := map[string]Project{}
	for _, p := range st.Projects {
		if !projectID(p.ID) || p.Revision < 1 || p.Revision > st.Revision || len(p.Bindings) == 0 || len(p.Bindings) > 16 || len(p.Clients) > 16 {
			return fail("invalid project ledger; preserve it for inspection")
		}
		if _, exists := projects[p.ID]; exists {
			return fail("duplicate project identity")
		}
		projects[p.ID] = p
		if p.Mode != "disabled" && p.Mode != "observe" && p.Mode != "new-sessions" && p.Mode != "all" || p.Mode == "new-sessions" && p.NewAfter.IsZero() {
			return fail("invalid project policy")
		}
		for _, b := range p.Bindings {
			if (b.Kind != "directory" && b.Kind != "git") || !filepath.IsAbs(b.Root) || filepath.Clean(b.Root) != b.Root || b.Identity == "" || b.Kind == "git" && (!filepath.IsAbs(b.GitDirectory) || b.GitIdentity == "") {
				return fail("invalid project boundary")
			}
		}
		for _, b := range p.Clients {
			if !adapter(b.Adapter) || !short(b.ID, 128) {
				return fail("invalid client project binding")
			}
		}
	}
	identities := map[string]bool{}
	sessions := map[string]bool{}
	for _, x := range st.Sessions {
		p, exists := projects[x.Project]
		id, e := uuid.Parse(x.Session)
		if !exists || e != nil || id.String() != x.Session || !adapter(x.Adapter) || len(x.Identity) != 32 || x.Protocol != Protocol || !filepath.IsAbs(x.CWD) || !short(x.Needs, 512) || len(x.Capabilities) > 8 || len(x.ClientProject) > 128 || x.PolicyRevision < 0 || x.PolicyRevision > p.Revision {
			return fail("invalid retained session identity")
		}
		if _, e = hex.DecodeString(x.Identity); e != nil {
			return fail("invalid identity generation")
		}
		if x.State != "observed" && x.State != "invited" && x.State != "enrolled" && x.State != "revoked" {
			return fail("invalid participation state")
		}
		if x.FirstSeen.IsZero() || x.LastSeen.Before(x.FirstSeen) || !x.Expires.Equal(x.LastSeen.Add(Liveness)) || !x.Started.IsZero() && x.Started.After(x.LastSeen) {
			return fail("invalid observation timestamps")
		}
		for _, c := range x.Capabilities {
			if !short(c, 64) {
				return fail("invalid capability metadata")
			}
		}
		k := x.Project + ":" + key(x)
		if identities[x.Identity] || sessions[k] {
			return fail("duplicate session identity")
		}
		identities[x.Identity] = true
		sessions[k] = true
	}
	previous := 0
	for _, e := range st.History {
		if e.Revision <= previous || e.Revision > st.Revision || e.Policy == nil || e.Policy.ID != e.Project {
			return fail("invalid audit history")
		}
		previous = e.Revision
	}
	return nil
}
func (s Store) save(st State) error {
	if e := validate(st); e != nil {
		return e
	}
	b, e := json.MarshalIndent(st, "", "  ")
	if e != nil {
		return e
	}
	if len(b)+1 > maxBytes {
		return fail("ledger capacity reached; history was preserved")
	}
	if e = local.AtomicJSON(filepath.Join(s.Home, "rollout.json"), st); e != nil {
		return e
	}
	d, e := os.Open(s.Home)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func find(st *State, id string) *Project {
	for i := range st.Projects {
		if st.Projects[i].ID == id {
			return &st.Projects[i]
		}
	}
	return nil
}
func key(s Session) string { return s.Adapter + ":" + s.Session }
func record(st *State, p *Project, action, actor, reason string, ids []string, at time.Time) error {
	if len(st.History) >= maxEvents {
		return fail("audit capacity reached; no history was discarded")
	}
	st.Revision++
	e := Event{Revision: st.Revision, At: at, Actor: actor, Action: action, Project: p.ID, Reason: reason, Sessions: ids}
	// Deep copy: event provenance must never change with later policy edits.
	b, _ := json.Marshal(p)
	var snapshot Project
	_ = json.Unmarshal(b, &snapshot)
	e.Policy = &snapshot
	st.History = append(st.History, e)
	return nil
}

type Query struct {
	CWD           string `json:"cwd"`
	Adapter       string `json:"adapter,omitempty"`
	ClientProject string `json:"client_project,omitempty"`
	Session       string `json:"session,omitempty"`
}
type Decision struct {
	Protocol    int      `json:"protocol"`
	Project     *Project `json:"project,omitempty"`
	Session     *Session `json:"session,omitempty"`
	Eligible    bool     `json:"eligible"`
	Coordinate  bool     `json:"coordinate"`
	Live        bool     `json:"live"`
	Why         string   `json:"why"`
	Obligations string   `json:"obligations"`
	Suggestion  string   `json:"suggestion,omitempty"`
	Provenance  string   `json:"provenance"`
}

// resolve requires a filesystem boundary even for client-project bindings.
// Most-specific roots contain broad grants. Equal roots across projects refuse.
func resolve(st State, q Query) (*Project, string, error) {
	path, e := canonical(q.CWD)
	if e != nil {
		return nil, "", e
	}
	var selected *Project
	blocked := ""
	depth := -1
	for _, p := range st.Projects {
		for _, b := range p.Bindings {
			if !within(b.Root, path) {
				continue
			}
			actual, e := canonical(b.Root)
			if e != nil || actual != b.Root {
				return nil, "", fail("scope boundary changed; review project %s", p.ID)
			}
			id, e := directoryIdentity(b.Root)
			if e != nil || id != b.Identity {
				return nil, "", fail("scope directory identity changed; review project %s", p.ID)
			}
			// An unmapped client label cannot fall back to a broader grant.
			why := ""
			if q.ClientProject != "" && !slices.Contains(p.Clients, ClientBinding{Adapter: q.Adapter, ID: q.ClientProject}) {
				why = "client project is not explicitly bound; no adoption authorized"
			}
			if b.Kind == "git" {
				root, e := gitRoot(path)
				if e != nil || root != b.Root {
					why = "working directory is outside the bound Git checkout; no adoption authorized"
				} else {
					dir, e := gitDirectory(path, "--absolute-git-dir")
					id, identityErr := directoryIdentity(dir)
					if e != nil || identityErr != nil || dir != b.GitDirectory || id != b.GitIdentity {
						why = "Git repository identity changed; review its project binding"
					}
				}
			}
			if len(b.Root) > depth {
				copy := p
				selected = &copy
				depth = len(b.Root)
				blocked = why
			} else if len(b.Root) == depth && selected != nil && selected.ID != p.ID {
				return nil, "", fail("ambiguous project boundary; no adoption authorized")
			}
		}
	}
	return selected, blocked, nil
}
func eligible(p Project, x Session, now time.Time) (bool, string) {
	if p.Mode == "disabled" {
		return false, "project adoption is disabled"
	}
	if x.State == "enrolled" {
		return true, "session acknowledged enrollment"
	}
	if p.Paused {
		return false, "further enrollment is paused"
	}
	if x.State == "revoked" {
		return false, "session enrollment was revoked; a new explicit invitation is required"
	}
	if x.State == "invited" && x.PolicyRevision == p.Revision {
		return true, "operator invited this exact live identity"
	}
	if p.Mode == "all" {
		return true, "all sessions in this project are eligible; acknowledgment is required"
	}
	if p.Mode == "new-sessions" {
		if x.Started.IsZero() || x.Started.After(now) {
			return false, "new-session eligibility requires a valid, self-reported original session start"
		}
		if !x.Started.After(p.NewAfter) || !x.FirstSeen.After(p.NewAfter) {
			return false, "session predates activation; invite it explicitly to opt in"
		}
		return true, "session reports starting after activation; acknowledgment is required"
	}
	return false, "observation only; an operator invitation is required"
}
func decision(st State, q Query, now time.Time) (Decision, error) {
	d := Decision{Protocol: Protocol, Provenance: "local operator project policy; installation and messaging registration grant no adoption", Obligations: "Existing leases, ownership, running work and cleanup/accounting obligations remain in force.", Suggestion: "Choose a project and explicitly enable new-sessions (suggested) or all; observation supports manual pilots."}
	if q.Session != "" {
		id, e := uuid.Parse(q.Session)
		if e != nil {
			return d, fail("session must be an exact stable UUID")
		}
		q.Session = id.String()
	}
	p, blocked, e := resolve(st, q)
	if e != nil {
		return d, e
	}
	d.Project = p
	if p == nil {
		d.Why = "no matching project; adoption is disabled"
		return d, nil
	}
	if blocked != "" {
		d.Why = blocked
		return d, nil
	}
	x := Session{Started: time.Time{}, FirstSeen: now}
	for _, v := range st.Sessions {
		if v.Project == p.ID && v.Adapter == q.Adapter && v.Session == q.Session {
			copy := v
			d.Session = &copy
			x = v
			break
		}
	}
	d.Eligible, d.Why = eligible(*p, x, now)
	if d.Session != nil {
		d.Live = now.Before(x.Expires) && !now.Before(x.LastSeen)
		if !d.Live {
			d.Eligible = false
			d.Why = "identity is stale; self-identify again and review its new identity before enrollment"
		}
		d.Coordinate = d.Live && x.State == "enrolled" && p.Mode != "disabled"
	}
	return d, nil
}
func (s Store) Check(q Query) (Decision, error) {
	st, e := s.Read()
	if e != nil {
		return Decision{}, e
	}
	return decision(st, q, s.now())
}

type Participation struct {
	Session
	Live       bool   `json:"live"`
	Eligible   bool   `json:"eligible"`
	Coordinate bool   `json:"coordinate"`
	Why        string `json:"why"`
}

// Roster checks each distinct reported scope once. Liveness describes recent
// voluntary reporting, never a process scan or proof that execution ended.
func (s Store) Roster(st State, project string) []Participation {
	type scope struct {
		p       *Project
		blocked string
		err     error
	}
	cache := map[Query]scope{}
	out := []Participation{}
	now := s.now()
	for _, x := range st.Sessions {
		if project != "" && x.Project != project {
			continue
		}
		q := Query{CWD: x.CWD, Adapter: x.Adapter, ClientProject: x.ClientProject}
		resolved, ok := cache[q]
		if !ok {
			resolved.p, resolved.blocked, resolved.err = resolve(st, q)
			cache[q] = resolved
		}
		v := Participation{Session: x, Live: now.Before(x.Expires) && !now.Before(x.LastSeen)}
		switch {
		case resolved.err != nil:
			v.Why = resolved.err.Error()
		case resolved.p == nil || resolved.p.ID != x.Project:
			v.Why = "reported working directory no longer resolves to this project"
		case resolved.blocked != "":
			v.Why = resolved.blocked
		case !v.Live:
			v.Why = "identity is stale; recent reporting and fresh acknowledgment are required"
		default:
			v.Eligible, v.Why = eligible(*resolved.p, x, now)
			v.Coordinate = x.State == "enrolled" && resolved.p.Mode != "disabled"
		}
		out = append(out, v)
	}
	return out
}

type Change struct {
	Action   string         `json:"action"`
	Project  string         `json:"project"`
	Binding  *Binding       `json:"binding,omitempty"`
	Client   *ClientBinding `json:"client,omitempty"`
	Mode     string         `json:"mode,omitempty"`
	Sessions []string       `json:"sessions,omitempty"`
	Reason   string         `json:"reason"`
}
type Plan struct {
	Review   string    `json:"review_sha256"`
	Revision int       `json:"revision"`
	Change   Change    `json:"change"`
	Before   *Project  `json:"before,omitempty"`
	After    Project   `json:"after"`
	Affected []Session `json:"affected_sessions"`
	Effect   string    `json:"effect"`
}

func normalize(c Change) (Change, error) {
	if !projectID(c.Project) || !short(c.Reason, 512) {
		return c, fail("project ID and a reason of 1..512 bytes are required")
	}
	if c.Binding != nil && c.Action != "project" || c.Client != nil && c.Action != "bind" || c.Mode != "" && c.Action != "policy" || len(c.Sessions) > 0 && c.Action != "invite" && c.Action != "revoke" {
		return c, fail("change contains selectors unrelated to its action")
	}
	if c.Binding != nil {
		b, e := NewBinding(c.Binding.Kind, c.Binding.Root)
		if e != nil {
			return c, e
		}
		c.Binding = &b
	}
	if c.Client != nil && (!adapter(c.Client.Adapter) || !short(c.Client.ID, 128)) {
		return c, fail("client binding requires an adapter and bounded project ID")
	}
	c.Sessions = slices.Clone(c.Sessions)
	slices.Sort(c.Sessions)
	c.Sessions = slices.Compact(c.Sessions)
	return c, nil
}
func digest(st State, c Change) string {
	b, _ := json.Marshal(struct {
		State  State
		Change Change
	}{st, c})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func transition(st *State, c Change, at time.Time) (Plan, error) {
	plan := Plan{Revision: st.Revision, Change: c, Affected: []Session{}, Effect: "Adoption only. No jobs, contacts, leases, resource admission or cleanup obligations are changed. New-session cutoff takes effect at apply time."}
	p := find(st, c.Project)
	if p != nil {
		b, _ := json.Marshal(p)
		_ = json.Unmarshal(b, &plan.Before)
	}
	if c.Action == "project" {
		if c.Binding == nil {
			return plan, fail("project requires a directory or Git binding")
		}
		if p == nil {
			if len(st.Projects) >= maxProjects {
				return plan, fail("project capacity reached")
			}
			st.Projects = append(st.Projects, Project{ID: c.Project, Mode: "disabled", Bindings: []Binding{}, Clients: []ClientBinding{}})
			p = &st.Projects[len(st.Projects)-1]
		}
		replaced := false
		for _, other := range st.Projects {
			for i, b := range other.Bindings {
				if b.Root == c.Binding.Root {
					if other.ID != p.ID || b == *c.Binding {
						return plan, fail("root already bound to project %s", other.ID)
					}
					p.Bindings[i] = *c.Binding
					replaced = true
				}
			}
		}
		if !replaced {
			if len(p.Bindings) >= 16 {
				return plan, fail("project binding capacity reached")
			}
			p.Bindings = append(p.Bindings, *c.Binding)
		}
	} else {
		if p == nil {
			return plan, fail("project does not exist")
		}
		switch c.Action {
		case "bind":
			if c.Client == nil {
				return plan, fail("bind requires client identity")
			}
			if len(p.Clients) >= 16 {
				return plan, fail("client binding capacity reached")
			}
			if slices.Contains(p.Clients, *c.Client) {
				return plan, fail("client binding already exists")
			}
			p.Clients = append(p.Clients, *c.Client)
		case "policy":
			if c.Mode != "disabled" && c.Mode != "observe" && c.Mode != "new-sessions" && c.Mode != "all" {
				return plan, fail("mode must be disabled, observe, new-sessions or all")
			}
			if c.Mode == "new-sessions" && p.Mode != "new-sessions" {
				p.NewAfter = at
			}
			p.Mode = c.Mode
		case "pause":
			p.Paused = true
		case "resume":
			p.Paused = false
		case "invite", "revoke":
			if len(c.Sessions) == 0 || len(c.Sessions) > 64 {
				return plan, fail("select 1..64 exact session identities")
			}
			for _, id := range c.Sessions {
				found := false
				for i := range st.Sessions {
					x := &st.Sessions[i]
					if x.Project != p.ID || x.Identity != id {
						continue
					}
					found = true
					if c.Action == "invite" {
						if x.State == "enrolled" {
							return plan, fail("session is already enrolled")
						}
						if p.Paused || p.Mode == "disabled" {
							return plan, fail("enable observation and resume enrollment before inviting")
						}
						if !at.Before(x.Expires) || at.Before(x.LastSeen) {
							return plan, fail("stale identity must identify again before invitation")
						}
						x.State = "invited"
						x.PolicyRevision = p.Revision
					} else {
						x.State = "revoked"
					}
					plan.Affected = append(plan.Affected, *x)
				}
				if !found {
					return plan, fail("unknown identity %s", id)
				}
			}
		default:
			return plan, fail("unknown change action")
		}
	}
	if c.Action != "invite" && c.Action != "revoke" {
		p.Revision++
		for _, x := range st.Sessions {
			if x.Project == p.ID {
				plan.Affected = append(plan.Affected, x)
			}
		}
	}
	plan.After = *p
	if e := record(st, p, c.Action, "operator", c.Reason, c.Sessions, at); e != nil {
		return plan, e
	}
	return plan, nil
}
func (s Store) Preview(c Change) (Plan, error) {
	c, e := normalize(c)
	if e != nil {
		return Plan{}, e
	}
	st, e := s.Read()
	if e != nil {
		return Plan{}, e
	}
	hash := digest(st, c)
	p, e := transition(&st, c, s.now())
	p.Review = hash
	return p, e
}
func (s Store) Apply(c Change, expect string) (Plan, error) {
	c, e := normalize(c)
	if e != nil {
		return Plan{}, e
	}
	// Validate and compare before creating even the lock or state directory.
	preview, e := s.Preview(c)
	if e != nil {
		return Plan{}, e
	}
	if expect == "" || preview.Review != expect {
		return Plan{}, fail("review changed; preview again before applying")
	}
	lock, e := local.Lock(s.Home, "rollout.lock")
	if e != nil {
		return Plan{}, e
	}
	defer lock.Close()
	st, e := s.Read()
	if e != nil {
		return Plan{}, e
	}
	c, e = normalize(c)
	if e != nil {
		return Plan{}, e
	}
	if digest(st, c) != expect {
		return Plan{}, fail("review changed; no policy was applied")
	}
	p, e := transition(&st, c, s.now())
	p.Review = expect
	if e != nil {
		return p, e
	}
	return p, s.save(st)
}

type Identification struct {
	Query
	Started      time.Time `json:"started_at,omitempty"`
	Protocol     int       `json:"protocol"`
	Needs        string    `json:"needs"`
	Capabilities []string  `json:"capabilities"`
}

func (s Store) Identify(in Identification) (Decision, error) {
	now := s.now()
	parsed, e := uuid.Parse(in.Session)
	if e != nil || !adapter(in.Adapter) {
		return Decision{}, fail("identify requires adapter and exact stable session UUID")
	}
	in.Session = parsed.String()
	if in.Protocol != Protocol || !short(in.Needs, 512) || len(in.Capabilities) > 8 || (!in.Started.IsZero() && in.Started.After(now)) || len(in.ClientProject) > 128 {
		return Decision{}, fail("unsupported protocol or invalid bounded identity metadata")
	}
	for _, v := range in.Capabilities {
		if !short(v, 64) {
			return Decision{}, fail("capability exceeds metadata bounds")
		}
	}
	in.CWD, e = canonical(in.CWD)
	if e != nil {
		return Decision{}, e
	}
	st, e := s.Read()
	if e != nil {
		return Decision{}, e
	}
	p, blocked, e := resolve(st, in.Query)
	if e != nil {
		return Decision{}, e
	}
	if p == nil || p.Mode == "disabled" || blocked != "" {
		return Decision{}, fail("identification is disabled for this scope")
	}
	lock, e := local.Lock(s.Home, "rollout.lock")
	if e != nil {
		return Decision{}, e
	}
	defer lock.Close()
	st, e = s.Read()
	if e != nil {
		return Decision{}, e
	}
	p, blocked, e = resolve(st, in.Query)
	if e != nil {
		return Decision{}, e
	}
	if p == nil || p.Mode == "disabled" || blocked != "" {
		return Decision{}, fail("identification is disabled for this scope")
	}
	x := Session{Project: p.ID, Adapter: in.Adapter, Session: in.Session, Identity: local.ID(), ClientProject: in.ClientProject, CWD: in.CWD, Started: in.Started, FirstSeen: now, LastSeen: now, Expires: now.Add(Liveness), Protocol: Protocol, Needs: in.Needs, Capabilities: in.Capabilities, State: "observed"}
	index := -1
	for i, v := range st.Sessions {
		if key(v) == key(x) {
			if !v.Started.Equal(x.Started) {
				return Decision{}, fail("original session start is immutable, including an unknown start")
			}
			if v.FirstSeen.Before(x.FirstSeen) {
				x.FirstSeen = v.FirstSeen
			}
			if v.Project == p.ID {
				index = i
				if now.Before(v.LastSeen) {
					return Decision{}, fail("local clock precedes the last observation; enrollment is not refreshed")
				}
				if now.Before(v.Expires) {
					x.Identity = v.Identity
					x.State = v.State
					x.PolicyRevision = v.PolicyRevision
				}
				// Revocation survives liveness expiry and future all-session policies.
				if v.State == "revoked" {
					x.State = "revoked"
				}
			}
		}
	}
	newIdentity := index < 0 || st.Sessions[index].Identity != x.Identity
	if index < 0 {
		if len(st.Sessions) >= maxSessions {
			return Decision{}, fail("session capacity reached; retained identities were preserved")
		}
		st.Sessions = append(st.Sessions, x)
	} else {
		st.Sessions[index] = x
	}
	if newIdentity { // identity creation only; heartbeats do not exhaust audit history
		if e = record(&st, p, "identify", key(x), "voluntary resource coordination discovery", []string{x.Identity}, now); e != nil {
			return Decision{}, e
		}
	} else {
		st.Revision++
	}
	if e = s.save(st); e != nil {
		return Decision{}, e
	}
	return decision(st, in.Query, now)
}

func (s Store) Acknowledge(q Query, identity string, revision int) (Decision, error) {
	// Disabled, unmatched and invalid acknowledgments must create no state.
	d, e := s.Check(q)
	if e != nil {
		return d, e
	}
	if d.Project == nil || d.Session == nil || d.Session.Identity != identity || revision != d.Project.Revision || !d.Live || !d.Eligible {
		return d, fail("acknowledgment requires exact live identity, current policy revision and eligibility")
	}
	lock, e := local.Lock(s.Home, "rollout.lock")
	if e != nil {
		return Decision{}, e
	}
	defer lock.Close()
	st, e := s.Read()
	if e != nil {
		return Decision{}, e
	}
	now := s.now()
	d, e = decision(st, q, now)
	if e != nil {
		return d, e
	}
	if d.Project == nil || d.Session == nil || d.Session.Identity != identity || revision != d.Project.Revision || !d.Live || !d.Eligible {
		return d, fail("acknowledgment requires exact live identity, current policy revision and eligibility")
	}
	if d.Coordinate {
		return d, nil
	}
	for i := range st.Sessions {
		if st.Sessions[i].Identity == identity {
			st.Sessions[i].State = "enrolled"
			st.Sessions[i].PolicyRevision = revision
		}
	}
	if e = record(&st, d.Project, "acknowledge", key(*d.Session), "session acknowledged project adoption policy", []string{identity}, now); e != nil {
		return d, e
	}
	if e = s.save(st); e != nil {
		return d, e
	}
	return decision(st, q, now)
}
