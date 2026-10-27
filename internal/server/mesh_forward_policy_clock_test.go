package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/leaseclock"
	"github.com/inferal-oss/arm/internal/meshforward"
	"github.com/inferal-oss/arm/internal/meshsshrelay"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/workspace"
)

// The reader seam surrounds a real owned regular-file ReadPolicy, including its
// open/stat/read/stat/decode checks. The barrier remains inside the production
// loader's reader call and returns that actual policy. An immutable component
// deadline is bound through the original handler callback; this is not a real
// Manager hold, native clock measurement or physical host suspend experiment.
func TestRoutedForwardPolicyReadRechecksElapsedEntitlement(t *testing.T) {
	for _, boundary := range []struct {
		name      string
		load      int32
		wantDials int32
	}{{"before-service-dial", 2, 0}, {"before-ready", 4, 1}} {
		t.Run(boundary.name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.Mkdir(filepath.Join(home, "mesh"), 0700); err != nil {
				t.Fatal(err)
			}
			grant := meshforward.Grant{
				Workspace: strings.Repeat("1", 32), Generation: strings.Repeat("2", 32), Source: strings.Repeat("3", 32), Target: strings.Repeat("4", 32),
				SourceGeneration: 1, TargetGeneration: 1, DirectoryPin: strings.Repeat("5", 64), DirectorySPKI: strings.Repeat("6", 64),
				SourcePin: strings.Repeat("7", 64), SourceSPKI: strings.Repeat("8", 64), TargetPin: strings.Repeat("9", 64), TargetSPKI: strings.Repeat("a", 64),
				Account: "operator", Endpoint: meshforward.Endpoint{Name: "web", Network: "tcp", Address: "127.0.0.1:8080"},
				Revision: 1, MaxConnections: 1, BytesPerConnection: 64 << 10, BytesPerSecond: 1 << 20, TotalBytes: 64 << 10, CreditNonce: strings.Repeat("b", 32),
				Transit: model.Resources{CPU: .01, Memory: 1 << 20}, MaxTTL: time.Minute, Expires: time.Now().UTC().Add(time.Hour),
			}
			review, err := meshsshrelay.ReviewPolicy(home, grant.Target, grant.TargetPin, "grant", "policy read native fence", func(p *meshsshrelay.Policy) error {
				p.Forwards = append(p.Forwards, grant)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err = meshsshrelay.CommitPolicy(review); err != nil {
				t.Fatal(err)
			}
			st, err := os.Lstat(filepath.Join(home, "mesh", "ssh-relay-policy.json"))
			if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
				t.Fatal("fixture requires the real owned regular policy", st, err)
			}
			o := meshsshrelay.Open{
				Workspace: grant.Workspace, Generation: grant.Generation, Route: strings.Repeat("c", 32), Source: grant.Source, Target: grant.Target,
				SourceGeneration: grant.SourceGeneration, TargetGeneration: grant.TargetGeneration, DirectoryRevision: 1,
				DirectoryPin: grant.DirectoryPin, DirectorySPKI: grant.DirectorySPKI, SourcePin: grant.SourcePin, TargetPin: grant.TargetPin, SSHHostKeyPin: strings.Repeat("0", 64),
				Service: grant.Endpoint.Name, Path: []string{grant.Source, grant.Target}, Hop: 1, Class: meshsshrelay.ClassForward,
				Expires: time.Now().Add(time.Minute), Pair: strings.Repeat("d", 64), EnrollmentRevision: 1,
			}
			binding, err := o.BindingDigest(time.Now())
			if err != nil {
				t.Fatal(err)
			}
			permit := meshsshrelay.Permit{Local: workspace.Identity{Host: grant.Target, Fingerprint: grant.TargetPin}, ForwardGrant: grant}
			q := meshforward.Request{Version: 1, Workspace: grant.Workspace, Generation: grant.Generation, Source: grant.Source, Target: grant.Target, SourcePin: grant.SourcePin, TargetPin: grant.TargetPin,
				Service: grant.Endpoint.Name, GrantRevision: grant.Revision, ConnectionID: strings.Repeat("e", 32), TTL: time.Minute, RouteBindingHash: binding}
			who := meshforward.Identity{SourcePin: grant.SourcePin, SourceSPKI: grant.SourceSPKI, TargetPin: grant.TargetPin, TargetSPKI: grant.TargetSPKI, Account: grant.Account}
			if err = grant.Authorize(time.Now(), who.SourcePin, who.SourceSPKI, who.TargetPin, who.TargetSPKI, who.Account, q); err != nil {
				t.Fatal("calendar authority must initially be positive", err)
			}

			// Only this tick changes. The wall sample, calendar grant and Go
			// contexts stay live while the original policy reader is blocked.
			wall := time.Now().UTC()
			var tick atomic.Int64
			tick.Store(int64(time.Hour))
			sample := func() leaseclock.Sample {
				return leaseclock.Sample{Boot: "policy-read-clock", Tick: tick.Load(), Wall: wall}
			}
			deadline, err := leaseclock.New(sample(), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			expired := errors.New("original elapsed reservation expired")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			checkActive := func(check context.Context) error {
				if err := check.Err(); err != nil {
					return err
				}
				remaining, err := deadline.Remaining(sample(), &deadline.Wall)
				if err != nil {
					return err
				}
				if remaining <= 0 {
					return expired
				}
				return nil
			}
			outer := meshsshrelay.Outer{Source: grant.Source, Target: grant.Target, Pair: o.Pair, Certificate: grant.SourcePin, Revision: 1, Expires: o.Expires}
			h := &meshsshrelay.Handler{Revalidate: func(check context.Context, gotOuter meshsshrelay.Outer, gotOpen meshsshrelay.Open, gotPermit meshsshrelay.Permit) error {
				if gotOuter != outer || gotOpen.WireDigest() != o.WireDigest() || gotPermit.Local != permit.Local || gotPermit.ForwardGrant != grant {
					return errors.New("original bound route changed")
				}
				return checkActive(check)
			}}
			revalidate := func(check context.Context) error { return h.Revalidate(check, outer, o, permit) }
			// Positive control: the same production loader accepts this real
			// policy before elapsed expiry, without the barrier wrapper.
			baseline := routedForwardPolicyLoader(home, permit, o, revalidate, meshsshrelay.ReadPolicy)
			if got, err := baseline(ctx); err != nil || got != grant {
				t.Fatal("real policy loader must authorize before expiry", got, err)
			}

			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			var loads, dials, ready, spends atomic.Int32
			reader := func(home, local, pin string) (meshsshrelay.Policy, error) {
				policy, err := meshsshrelay.ReadPolicy(home, local, pin)
				if err != nil {
					return meshsshrelay.Policy{}, err
				}
				if got, err := policy.Forward(o); err != nil || got != grant {
					return meshsshrelay.Policy{}, fmt.Errorf("real policy did not authorize before read barrier: %v", err)
				}
				if loads.Add(1) == boundary.load {
					close(entered)
					<-release
				}
				return policy, nil
			}
			loader := routedForwardPolicyLoader(home, permit, o, revalidate, reader)
			stream, client := net.Pipe()
			service, application := net.Pipe()
			proxy := &meshforward.Proxy{
				CheckCredit: func(check context.Context, g meshforward.Grant) error {
					return meshsshrelay.CheckForwardCredit(check, home, g.Target, g.TargetPin, g)
				},
				Credit: func(check context.Context, g meshforward.Grant, n int) (int, error) {
					spends.Add(1)
					return meshsshrelay.ReserveForwardBytes(check, home, g.Target, g.TargetPin, g, n)
				},
				Dial: func(context.Context, string, string) (net.Conn, error) { dials.Add(1); return service, nil },
			}
			ended := make(chan error, 1)
			completed := false
			defer func() {
				cancel()
				unblock()
				_ = stream.Close()
				_ = service.Close()
				_ = client.Close()
				_ = application.Close()
				if !completed {
					select {
					case <-ended:
					case <-time.After(3 * time.Second):
						t.Error("original policy/proxy owner did not join")
						return
					}
				}
				proxy.Stop()
			}()
			go func() {
				err := proxy.Open(ctx, q, who, stream, loader, func() error {
					ready.Add(1)
					// A missing post-read check fails the direct READY oracle
					// and still returns promptly, without starting copy workers.
					return errors.New("unexpected READY after original expiry")
				})
				_ = stream.Close() // The original route owner also covers early refusal.
				ended <- err       // Publish completion after closing the original socket.
			}()
			select {
			case <-entered:
			case err := <-ended:
				completed = true
				t.Fatal("original loader did not reach its real policy read", err)
			case <-time.After(3 * time.Second):
				t.Fatal("original loader policy read barrier not reached")
			}
			if remaining, err := deadline.Remaining(sample(), &deadline.Wall); err != nil || remaining <= 0 {
				t.Fatal("elapsed authority was not live before the held read", remaining, err)
			}
			tick.Store(deadline.End)
			if remaining, err := deadline.Remaining(sample(), &deadline.Wall); err != nil || remaining != 0 {
				t.Fatal("controlled elapsed deadline did not expire", remaining, err)
			}
			if ctx.Err() != nil || !time.Now().Before(grant.Expires) || !time.Now().Before(o.Expires) || !sample().Wall.Equal(wall) {
				t.Fatal("Go context and calendar authority must remain live with frozen wall")
			}
			unblock()
			var completedErr error
			select {
			case completedErr = <-ended:
				completed = true
			case <-time.After(3 * time.Second):
				t.Fatal("refused original policy/proxy owner did not join")
			}
			if dials.Load() != boundary.wantDials || ready.Load() != 0 || spends.Load() != 0 || proxy.Active() != 0 {
				t.Fatal("policy read returned stale native authority", dials.Load(), ready.Load(), spends.Load(), proxy.Active())
			}
			if !errors.Is(completedErr, expired) {
				t.Fatal("loader did not report the original native refusal", completedErr)
			}
			assertClosed := func(peer net.Conn) {
				t.Helper()
				if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil && !errors.Is(err, io.ErrClosedPipe) {
					t.Fatal(err)
				}
				var one [1]byte
				if _, err := peer.Read(one[:]); err != io.EOF {
					t.Fatal("original proxy socket did not close", err)
				}
			}
			assertClosed(client)
			if boundary.wantDials != 0 {
				assertClosed(application)
			}
			// Every byte of the reviewed durable credit remains available.
			// This diagnostic spend occurs only after the original owner joins.
			if n, err := meshsshrelay.ReserveForwardBytes(ctx, home, grant.Target, grant.TargetPin, grant, int(grant.TotalBytes)); err != nil || int64(n) != grant.TotalBytes {
				t.Fatal("refused policy read consumed original credit", n, err)
			}
		})
	}
}
