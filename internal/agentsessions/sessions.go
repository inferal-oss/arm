// Package agentsessions discovers native session metadata without ARM enrollment.
// It never reads transcripts, starts agent turns or scans arbitrary processes.
package agentsessions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
)

const Limit = 512

type Session struct {
	Adapter       string    `json:"adapter"`
	ID            string    `json:"session"`
	CWD           string    `json:"cwd"`
	Home          string    `json:"client_home"`
	Started       time.Time `json:"native_started_at,omitempty"`
	InstanceStart time.Time `json:"native_instance_started_at,omitempty"`
	Updated       time.Time `json:"metadata_updated_at"`
	Activity      string    `json:"native_status"`
	Evidence      string    `json:"evidence"`
	Generation    string    `json:"generation"`
}
type Result struct {
	Adapter  string    `json:"adapter"`
	Sessions []Session `json:"sessions"`
	Warnings []string  `json:"warnings"`
}
type Source interface {
	Name() string
	Discover(context.Context) Result
}

// Normalize rejects unsupported identities/paths. A generation pins identity,
// original native start and client home; heartbeat timestamps are not identity.
func Normalize(s Session) (Session, error) {
	id, err := uuid.Parse(s.ID)
	if err != nil || s.Adapter != "codex" && s.Adapter != "claude" || !filepath.IsAbs(s.CWD) || !filepath.IsAbs(s.Home) || len(s.CWD) > 4096 || len(s.Home) > 4096 {
		return s, fmt.Errorf("unsupported session metadata")
	}
	s.ID = id.String()
	for _, p := range []string{s.CWD, s.Home} {
		for _, r := range p {
			if r < 32 || r == 127 {
				return s, fmt.Errorf("unsupported control characters in session paths")
			}
		}
	}
	for _, p := range []*string{&s.CWD, &s.Home} {
		*p, err = filepath.EvalSymlinks(*p)
		if err != nil {
			return s, fmt.Errorf("session directory unavailable")
		}
		info, err := os.Stat(*p)
		if err != nil || !info.IsDir() {
			return s, fmt.Errorf("session directory unavailable")
		}
	}
	if !s.Started.IsZero() && s.Started.After(time.Now().Add(time.Minute)) {
		return s, fmt.Errorf("native start is in the future")
	}
	switch s.Activity {
	case "active", "idle", "busy", "waiting", "notLoaded":
	default:
		s.Activity = "unknown"
	}
	info, _ := os.Stat(s.Home)
	nativeHomeID := ""
	if info != nil {
		if id, ok := info.Sys().(*syscall.Stat_t); ok {
			nativeHomeID = fmt.Sprintf("%d:%d", id.Dev, id.Ino)
		}
	}
	cwdID := ""
	if info, e := os.Stat(s.CWD); e == nil {
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			cwdID = fmt.Sprintf("%d:%d", st.Dev, st.Ino)
		}
	}
	b, _ := json.Marshal(map[string]any{"adapter": s.Adapter, "id": s.ID, "cwd": s.CWD, "cwd_identity": cwdID, "home": s.Home, "home_identity": nativeHomeID, "started": s.Started, "instance_started": s.InstanceStart})
	h := sha256.Sum256(b)
	s.Generation = hex.EncodeToString(h[:])
	return s, nil
}

func timestamp(raw json.RawMessage) time.Time {
	var str string
	if json.Unmarshal(raw, &str) == nil {
		t, _ := time.Parse(time.RFC3339Nano, str)
		return t.UTC()
	}
	var n int64
	if json.Unmarshal(raw, &n) == nil && n > 0 {
		if n > 100000000000 {
			return time.UnixMilli(n).UTC()
		}
		return time.Unix(n, 0).UTC()
	}
	return time.Time{}
}

type Claude struct{ Home string }

func (Claude) Name() string { return "claude" }
func (c Claude) Discover(ctx context.Context) Result {
	out := Result{Adapter: c.Name(), Sessions: []Session{}, Warnings: []string{}}
	warn := func(s string) {
		if !slices.Contains(out.Warnings, s) {
			out.Warnings = append(out.Warnings, s)
		}
	}
	path := filepath.Join(c.Home, "sessions")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return out
	}
	if err != nil || !info.IsDir() || !filepath.IsAbs(c.Home) {
		warn("Claude session metadata directory is unavailable or unsupported.")
		return out
	}
	r, err := os.OpenRoot(path)
	if err != nil {
		warn("Claude session metadata directory is unavailable.")
		return out
	}
	defer r.Close()
	held, err := r.Stat(".")
	if err != nil || !os.SameFile(info, held) {
		warn("Claude session metadata directory changed.")
		return out
	}
	d, err := r.Open(".")
	if err != nil {
		warn("Claude session markers could not be listed.")
		return out
	}
	defer d.Close()
	entries, err := d.ReadDir(Limit + 1)
	if err != nil && err != io.EOF {
		warn("Claude session markers could not be listed.")
		return out
	}
	if len(entries) > Limit {
		entries = entries[:Limit]
		warn("Claude session discovery is truncated.")
	}
	budget := 4 << 20
	seen := map[string]bool{}
	ambiguous := map[string]bool{}
	for _, entry := range entries {
		if ctx.Err() != nil {
			warn("Claude session discovery was interrupted.")
			break
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		f, e := r.OpenFile(entry.Name(), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if e != nil {
			warn("Some Claude session markers are unavailable.")
			continue
		}
		fi, e := f.Stat()
		if e != nil || !fi.Mode().IsRegular() || fi.Size() > 65536 || fi.Size() > int64(budget) {
			f.Close()
			warn("Claude session metadata exceeds its read bounds.")
			continue
		}
		b, e := io.ReadAll(io.LimitReader(f, int64(min(65536, budget))+1))
		after, statErr := f.Stat()
		f.Close()
		budget -= len(b)
		if e != nil || len(b) > 65536 || budget < 0 || statErr != nil || fi.Size() != after.Size() || !fi.ModTime().Equal(after.ModTime()) {
			warn("Some Claude session metadata changed or exceeded its read bounds.")
			continue
		}
		var marker struct {
			ID      string          `json:"sessionId"`
			CWD     string          `json:"cwd"`
			Started json.RawMessage `json:"startedAt"`
			Status  string          `json:"status"`
		}
		if json.Unmarshal(b, &marker) != nil {
			warn("Some Claude session metadata uses an unsupported format.")
			continue
		}
		// Process start does not establish the original conversation start.
		s, e := Normalize(Session{Adapter: "claude", ID: marker.ID, CWD: marker.CWD, Home: c.Home, InstanceStart: timestamp(marker.Started), Updated: fi.ModTime().UTC(), Activity: marker.Status, Evidence: "native session marker; original conversation start, status and liveness are unverified"})
		if e != nil {
			warn("Some Claude session identities or directories are unsupported.")
			continue
		}
		if ambiguous[s.ID] {
			continue
		}
		if seen[s.ID] {
			for _, old := range out.Sessions {
				if old.ID == s.ID && old.Generation != s.Generation {
					ambiguous[s.ID] = true
					warn("Conflicting Claude markers for one session were omitted.")
				}
			}
			if ambiguous[s.ID] {
				out.Sessions = slices.DeleteFunc(out.Sessions, func(old Session) bool { return old.ID == s.ID })
			}
			continue
		}
		if !seen[s.ID] {
			out.Sessions = append(out.Sessions, s)
			seen[s.ID] = true
		}
	}
	current, e := os.Lstat(path)
	if e != nil || !os.SameFile(info, current) {
		out.Sessions = []Session{}
		warn("Claude session metadata directory changed during discovery.")
	}
	return out
}
