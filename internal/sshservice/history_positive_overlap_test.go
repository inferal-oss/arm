package sshservice

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Context deadline inspection marks the real boundary between off-lock payload
// verification and the subsequent source mutation lock. No product test hook or
// replacement verifier is needed to change the retained file at that boundary.
type beforeSourceMutationContext struct {
	context.Context
	once   sync.Once
	mutate func()
}

func (c *beforeSourceMutationContext) Deadline() (time.Time, bool) {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, ".withSourceMutation") {
			c.once.Do(c.mutate)
			break
		}
		if !more {
			break
		}
	}
	return c.Context.Deadline()
}

func TestSourceHistoryPositiveSettlementRechecksCurrentOverlap(t *testing.T) {
	for _, family := range []string{"completion", "seal"} {
		for _, overlap := range []string{"hot", "pending"} {
			t.Run(family+"/"+overlap, func(t *testing.T) {
				home, b := stateFixture(t)
				intent := sourceFixture(t, home, b, statePin(t, home, b), "positive-overlap")
				if e := ReserveSource(t.Context(), home, b, intent); e != nil {
					t.Fatal(e)
				}
				if e := os.Remove(intent.Directory.Path); e != nil {
					t.Fatal(e)
				}
				var settle func(context.Context) error
				name := completionName(intent)
				if family == "completion" {
					receipt := terminalReceipt(intent)
					settle = func(ctx context.Context) error { return SettleSource(ctx, home, b, intent, receipt) }
				} else {
					name = sealCompletionName(intent)
					seal := sourceSeal(t, intent)
					settle = func(ctx context.Context) error { return SettleSourceSeal(ctx, home, b, intent, seal) }
				}
				if e := settle(t.Context()); e != nil {
					t.Fatal(e)
				}
				path := filepath.Join(home, "mesh", name)
				original, e := os.ReadFile(path)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = ArchiveSourceHistory(t.Context(), home, b, sourceArchiveRequest(t, home, b, "positive-overlap")); e != nil {
					t.Fatal(e)
				}
				var changed []byte
				if overlap == "pending" {
					path = filepath.Join(home, "mesh", intentName(intent))
					original, e = json.Marshal(intent)
					v := intent
					v.Request.Reason = "Different individually valid retained intention"
					if e = v.Validate(); e != nil {
						t.Fatal(e)
					}
					changed, e = json.Marshal(v)
				} else if family == "completion" {
					var v SourceCompletion
					if e = DecodeStateRecord(original, &v); e != nil {
						t.Fatal(e)
					}
					v.At = v.At.Add(time.Second)
					if e = v.Validate(); e != nil {
						t.Fatal(e)
					}
					changed, e = json.Marshal(v)
				} else {
					var v SourceSealCompletion
					if e = DecodeStateRecord(original, &v); e != nil {
						t.Fatal(e)
					}
					v.At = v.At.Add(time.Second)
					if e = v.Validate(); e != nil {
						t.Fatal(e)
					}
					changed, e = json.Marshal(v)
				}
				if e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(path, original, 0600); e != nil {
					t.Fatal(e)
				}
				fired := false
				ctx := &beforeSourceMutationContext{Context: t.Context(), mutate: func() {
					fired = true
					if e := os.WriteFile(path, changed, 0600); e != nil {
						t.Fatal(e)
					}
				}}
				e = settle(ctx)
				if !fired {
					t.Fatal("did not reach post-preverification mutation boundary", e)
				}
				if e == nil {
					t.Error("read-only cold settlement accepted current contradictory hot/pending evidence")
				}
				retained, readErr := os.ReadFile(path)
				if readErr != nil || string(retained) != string(changed) {
					t.Fatal("changed evidence was not retained", readErr)
				}
			})
		}
	}
}
