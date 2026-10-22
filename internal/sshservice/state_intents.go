package sshservice

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/usage"
	"golang.org/x/sys/unix"
)

var ErrIntentExists = errors.New("SSH intention already reserved; inspect its exact key, never repeat execution")
var ErrTargetUncertain = errors.New("an unresolved SSH intention for this target has no matching live worker; inspect its exact key before another invocation")

func invocationParent() string {
	if runtime.GOOS == "darwin" {
		return "/private/tmp"
	}
	return "/tmp"
}
func validInvocationPath(path string) bool {
	base := filepath.Base(path)
	return filepath.Dir(path) == invocationParent() && strings.HasPrefix(base, "arm-ssh-") && hexString(strings.TrimPrefix(base, "arm-ssh-"), 32)
}
func invocationIdentity(path string) (InvocationDirectory, error) {
	if !validInvocationPath(path) {
		return InvocationDirectory{}, fmt.Errorf("owned short SSH invocation directory required")
	}
	d, e := privateDirectory(path)
	if e != nil {
		return InvocationDirectory{}, e
	}
	defer d.Close()
	v, e := statState(d)
	if e != nil {
		return InvocationDirectory{}, e
	}
	if e = validateDirectory(d, path); e != nil {
		return InvocationDirectory{}, e
	}
	return InvocationDirectory{Path: path, Device: v.Dev, Inode: v.Ino, Owner: fmt.Sprint(v.UID)}, nil
}

// CreateInvocationDirectory creates only an empty0700 directory. No key/socket,
// connection or execution is created. A crash here may leave an empty directory;
// a durable source reservation is required before dispatch or writing artifacts.
func CreateInvocationDirectory(ctx context.Context, home string, b Binding) (InvocationDirectory, error) {
	var out InvocationDirectory
	e := withStateMutation(ctx, home, b, true, func(s *State, _ *os.File) error {
		if e := s.guard(); e != nil {
			return e
		}
		var nonce [16]byte
		if _, e := rand.Read(nonce[:]); e != nil {
			return e
		}
		path := filepath.Join(invocationParent(), "arm-ssh-"+hex.EncodeToString(nonce[:]))
		if e := os.Mkdir(path, 0700); e != nil {
			return e
		}
		var e error
		out, e = invocationIdentity(path)
		return e
	})
	return out, e
}

// ReserveSource fsyncs one exact immutable intention before any dispatch. A
// returned error NEVER authorizes network dispatch, including publication/fsync
// ambiguity. Existing keys are inspection only, not execution retry permits.
func ReserveSource(ctx context.Context, home string, b Binding, r SourceIntent) error {
	if r.Validate() != nil || r.Source != b.Host || r.Owner != b.Owner {
		return fmt.Errorf("invalid source authority/intention")
	}
	return withStateMutation(ctx, home, b, true, func(s *State, d *os.File) error {
		for _, completed := range s.Completions {
			if intentName(completed.Intent) == intentName(r) {
				return ErrIntentExists
			}
		}
		for _, old := range s.Intents {
			if intentName(old) == intentName(r) {
				return ErrIntentExists
			}
		}
		if len(s.Intents) >= MaxSourceIntents {
			return fmt.Errorf("SSH intention roster full; no unresolved record removed")
		}
		// Every outstanding invocation reserves a future durable completion
		// slot. Interrupted settlement can retain both files for one key;
		// count that pair once rather than inventing a second obligation.
		completionSlots := map[string]bool{}
		for _, old := range s.Completions {
			completionSlots[intentName(old.Intent)] = true
		}
		for _, old := range s.Intents {
			completionSlots[intentName(old)] = true
		}
		if len(completionSlots) >= MaxCompletions {
			return fmt.Errorf("SSH completed history full; refuse new execution intention before dispatch, no settled history removed")
		}
		worker, e := usage.Capture(os.Getpid())
		if e != nil || worker != r.Worker || worker.UID != b.Owner {
			return fmt.Errorf("source worker is not the current exact native process")
		}
		directory, e := invocationIdentity(r.Directory.Path)
		if e != nil || directory != r.Directory {
			return fmt.Errorf("source invocation directory changed")
		}
		if r.Request.SourceCertificate != s.config.Identity.Fingerprint {
			return fmt.Errorf("source TLS identity changed")
		}
		found := false
		for _, pin := range s.Pins {
			if pin == r.Request.Pin {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("exact reviewed SSH target pin is no longer present")
		}
		for _, old := range s.Intents {
			if old.Request.Pin.Target == r.Request.Pin.Target {
				if e = ctx.Err(); e != nil {
					return e
				}
				current, e := usage.Capture(old.Worker.PID)
				if e != nil || current != old.Worker {
					return ErrTargetUncertain
				}
			}
		}
		raw, e := json.Marshal(r)
		if e != nil {
			return e
		}
		return atomicStateFile(ctx, s, d, intentName(r), raw, nil)
	})
}

// SettleSource accepts ONLY an exact durable terminal destination receipt. An
// SSH child exit or lookup(found:false) cannot erase uncertainty. Invocation
// artifacts must already be removed by their exact owner before settlement.
func SettleSource(ctx context.Context, home string, b Binding, expected SourceIntent, receipt model.SSHLookup) error {
	if e := MatchTerminalReceipt(expected, receipt); e != nil {
		return e
	}
	return withStateMutation(ctx, home, b, true, func(s *State, d *os.File) error {
		if _, e := os.Lstat(expected.Directory.Path); !os.IsNotExist(e) {
			return fmt.Errorf("SSH invocation artifacts remain or cannot be proved removed; retain exact intention")
		}
		var actual *SourceIntent
		for _, r := range s.Intents {
			if intentName(r) == intentName(expected) {
				v := r
				actual = &v
				break
			}
		}
		var completed *SourceCompletion
		for _, r := range s.Completions {
			if completionName(r.Intent) == completionName(expected) {
				v := r
				completed = &v
				break
			}
		}
		if actual != nil && !exactIntent(*actual, expected) {
			return fmt.Errorf("SSH pending intention changed; nothing removed")
		}
		if completed != nil && (!exactIntent(completed.Intent, expected) || !reflect.DeepEqual(completed.Receipt, receipt)) {
			return fmt.Errorf("SSH terminal receipt differs from saved completion")
		}
		if actual == nil && completed == nil {
			return fmt.Errorf("SSH intention is absent; no settlement inferred")
		}
		if completed == nil {
			if len(s.Completions) >= MaxCompletions {
				return fmt.Errorf("SSH completed history full; preserve pending intention and archive known settled history explicitly")
			}
			record := SourceCompletion{1, expected, receipt, time.Now().UTC()}
			raw, e := json.Marshal(record)
			if e != nil {
				return e
			}
			if e = record.Validate(); e != nil {
				return e
			}
			if e = atomicStateFile(ctx, s, d, completionName(expected), raw, nil); e != nil {
				return fmt.Errorf("SSH completion publication uncertain; inspect exact pending/receipt, nothing may be replayed: %w", e)
			}
		}
		if actual == nil {
			return d.Sync()
		}
		name := intentName(expected)
		raw, id, e := readStateFile(ctx, d, name, MaxIntentBytes)
		if e != nil {
			return e
		}
		want, _ := json.Marshal(expected)
		if string(raw) != string(want) || id != s.files[name] {
			return fmt.Errorf("SSH intention changed before settlement")
		}
		if e = s.guard(); e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		if e = unix.Unlinkat(int(d.Fd()), name, 0); e != nil {
			return e
		}
		if e = d.Sync(); e != nil {
			return fmt.Errorf("SSH terminal receipt is preserved but pending-removal durability is uncertain; inspect exact completion: %w", e)
		}
		return nil
	})
}
