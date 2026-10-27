package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"getarm.org/internal/mesh"
)

type LifecycleStopScope struct {
	SHA256   string
	Blockers []ProofBlocker
}

// WithLifecycleStopScope reuses the actual lifecycle journal exclusion and the
// existing CLOSED coordinator/lifecycle evidence validators. It is not a reset
// proof: configured peer membership remains intact and creates no retirement
// requirement. fn must not acquire service/mesh locks, perform RPC or join a
// worker. Its only effect may be the short manager admission transaction.
func WithLifecycleStopScope(ctx context.Context, home, owner, host string, fn func(LifecycleStopScope) error) (err error) {
	unlock, err := mesh.LockLifecycle(home)
	if err != nil {
		return err
	}
	defer unlock()
	dir, err := OpenDirectory(home)
	if err != nil {
		return err
	}
	r := &SettledReview{source: &Source{Directory: dir}}
	defer func() {
		closeErr := r.Close()
		if err == nil {
			err = closeErr
		}
	}()
	result := LifecycleStopScope{Blockers: []ProofBlocker{}}
	pending, err := r.child("dashboard-pending")
	if err != nil {
		return err
	}
	if pending != nil {
		if err = r.existingLock(pending, ".lock"); err != nil {
			return err
		}
		files, _, e := r.readNamespace(ctx, pending, 64<<10, 65)
		if e != nil {
			return e
		}
		for _, file := range files {
			if file.Name != ".lock" {
				result.Blockers = append(result.Blockers, ProofBlocker{Kind: "client_intent", Entity: file.Name, Detail: "Retained client intent is unresolved; stop cannot claim it settled."})
			}
		}
	}
	lifecycle, err := r.child("mesh-lifecycle")
	if err != nil {
		return err
	}
	if lifecycle != nil {
		_, contents, e := r.readNamespace(ctx, lifecycle, MaxRecordBytes, 512)
		if e != nil {
			return e
		}
		coordinators := map[string][]byte{}
		life := map[string]mesh.LifecycleOperation{}
		for _, name := range sortedKeys(contents) {
			raw := contents[name]
			if strings.HasPrefix(name, "coordinator-") {
				coordinators[name] = raw
				continue
			}
			var op mesh.LifecycleOperation
			if e = strictEvidence(string(raw), &op, "actor", "transitions", "key", "kind", "request_sha256", "at", "updated_at", "phase", "reason"); e != nil {
				return e
			}
			path, e := mesh.LifecycleOperationPath(home, op.Key)
			if e != nil || filepath.Base(path) != name || op.Actor.UID != owner || !oneOf(op.Kind, "connect", "reconnect", "disconnect") || op.At.IsZero() || op.Updated.Before(op.At) || len(op.Transitions) == 0 || op.Transitions[len(op.Transitions)-1].Phase != op.Phase || op.LocalHost != "" && op.LocalHost != host {
				return fmt.Errorf("invalid lifecycle stop-scope authority")
			}
			if !oneOf(op.Phase, "connected", "disconnected") {
				result.Blockers = append(result.Blockers, ProofBlocker{Kind: "mesh_lifecycle", Entity: op.Key, Detail: "Peer setup/disconnection has not durably settled."})
			}
			life[op.Key] = op
		}
		blocks, e := proveResetCoordinations(coordinators, owner, host, life)
		if e != nil {
			return e
		}
		result.Blockers = append(result.Blockers, blocks...)
	}
	if len(result.Blockers) > MaxProofBlockers {
		return fmt.Errorf("stop scope exceeds complete blocker bound")
	}
	if err = dir.Validate(); err != nil {
		return err
	}
	encoded, err := json.Marshal(r.Namespaces)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(encoded)
	result.SHA256 = hex.EncodeToString(sum[:])
	return fn(result)
}
