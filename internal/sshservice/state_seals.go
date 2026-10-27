package sshservice

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"getarm.org/internal/model"
	"golang.org/x/sys/unix"
)

// ControlCommitment reconstructs ONLY public immutable control evidence; never
// command text, an ephemeral signer, connection or execution capability.
func (r SourceIntent) ControlCommitment() (model.SSHControlCommitment, error) {
	q := r.Request
	c := model.SSHControlCommitment{Schema: 1, Source: r.Source, Target: q.Pin.Target, Pair: q.Pin.Pair, Revision: q.Pin.Revision, Certificate: q.SourceCertificate, Owner: q.TargetOwner, Account: q.Pin.Account, HostKeySHA256: publicKeyHash(q.Pin.PublicKey), Key: q.Key, PublicKeySHA256: publicKeyHash(q.PublicKey), Reason: q.Reason, TTL: q.TTL, Resources: q.Resources, CWD: q.CWD, Kind: q.Kind, CommandSHA256: q.CommandSHA256, PTY: q.PTY}
	if e := r.Validate(); e != nil {
		return c, e
	}
	return c, c.Validate()
}

type SourceSealCompletion struct {
	Schema int               `json:"schema"`
	Intent SourceIntent      `json:"intent"`
	Seal   model.SSHKeyFence `json:"seal"`
	At     time.Time         `json:"settled_at"`
}

func MatchSourceSeal(intent SourceIntent, seal model.SSHKeyFence) error {
	expected, e := intent.ControlCommitment()
	if e != nil {
		return e
	}
	if e = seal.Validate(); e != nil {
		return e
	}
	if seal.State != "sealed" || seal.Control == nil || !reflect.DeepEqual(expected, *seal.Control) {
		return fmt.Errorf("exact durable nonexecution seal required; absent status/child exit is not proof")
	}
	return nil
}
func (r SourceSealCompletion) Validate() error {
	if r.Schema != 1 || r.At.IsZero() || r.At.Before(r.Intent.Created) {
		return fmt.Errorf("invalid source-local seal settlement chronology")
	}
	return MatchSourceSeal(r.Intent, r.Seal)
}
func sealCompletionName(r SourceIntent) string {
	return strings.Replace(intentName(r), "ssh-client-", "ssh-sealed-", 1)
}

// SettleSourceSeal preserves a matching destination tombstone before removing
// pending evidence. A lost seal reply remains lookup-only; this never sends or
// retries a seal and never settles from a bare absent snapshot.
func SettleSourceSeal(ctx context.Context, home string, b Binding, expected SourceIntent, seal model.SSHKeyFence) error {
	if e := MatchSourceSeal(expected, seal); e != nil {
		return e
	}
	verified, e := preverifySourceHistory(ctx, home, b, expected.Request.Pin.Target, expected.Request.Key)
	if e != nil {
		return e
	}
	if verified != nil && verified.Absence == nil && (verified.Lookup.Seal == nil || !exactIntent(verified.Lookup.Seal.Intent, expected) || !reflect.DeepEqual(verified.Lookup.Seal.Seal, seal)) {
		return fmt.Errorf("archived SSH seal differs; original key remains fenced")
	}
	return withStateMutation(ctx, home, b, true, func(s *State, d *os.File) error {
		if verified != nil && verified.Absence == nil {
			return recheckVerifiedSourceHistory(ctx, s, verified)
		}
		if verified == nil || verified.Absence == nil {
			return fmt.Errorf("verified absence required for hot settlement")
		}
		bindSourceAbsenceGuard(s, *verified.Absence)
		if fence, e := sourceStateFence(ctx, s, expected.Request.Pin.Target, expected.Request.Key); e != nil {
			return e
		} else if fence != nil {
			return fmt.Errorf("source was archived concurrently; inspect/retry exact read-only settlement")
		}

		if _, e := os.Lstat(expected.Directory.Path); !os.IsNotExist(e) {
			return fmt.Errorf("SSH invocation artifacts remain or cannot be verified absent")
		}
		var actual *SourceIntent
		for _, r := range s.Intents {
			if intentName(r) == intentName(expected) {
				copy := r
				actual = &copy
			}
		}
		if actual != nil && !exactIntent(*actual, expected) {
			return fmt.Errorf("pending SSH intention differs")
		}
		for _, r := range s.Completions {
			if intentName(r.Intent) == intentName(expected) {
				return fmt.Errorf("SSH key already records admitted work; no nonexecution settlement")
			}
		}
		var saved *SourceSealCompletion
		for _, r := range s.Seals {
			if intentName(r.Intent) == intentName(expected) {
				copy := r
				saved = &copy
			}
		}
		if saved != nil && (!exactIntent(saved.Intent, expected) || !reflect.DeepEqual(saved.Seal, seal)) {
			return fmt.Errorf("saved exact SSH seal differs")
		}
		if actual == nil && saved == nil {
			return fmt.Errorf("SSH intention absent; no settlement inferred")
		}
		if saved == nil {
			if len(s.Completions)+len(s.Seals) >= MaxCompletions {
				return fmt.Errorf("SSH completed history full; preserve exact pending intention")
			}
			record := SourceSealCompletion{1, expected, seal, time.Now().UTC()}
			if e := record.Validate(); e != nil {
				return e
			}
			raw, e := json.Marshal(record)
			if e != nil {
				return e
			}
			if e = recheckVerifiedSourceHistory(ctx, s, verified); e != nil {
				return e
			}
			if e = atomicStateFile(ctx, s, d, sealCompletionName(expected), raw, nil); e != nil {
				return fmt.Errorf("SSH seal completion durability uncertain; inspect exact record without retry: %w", e)
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
			return fmt.Errorf("SSH intention changed before seal settlement")
		}
		if e = s.guard(); e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		if e = recheckVerifiedSourceHistory(ctx, s, verified); e != nil {
			return e
		}
		if e = unix.Unlinkat(int(d.Fd()), name, 0); e != nil {
			return e
		}
		if e = d.Sync(); e != nil {
			return fmt.Errorf("SSH seal preserved but pending-removal durability uncertain: %w", e)
		}
		return nil
	})
}
