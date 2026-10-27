package manager

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/leaseclock"
	"github.com/inferal-oss/arm/internal/meshforward"
	"github.com/inferal-oss/arm/internal/model"
)

// A controlled suspend-inclusive jump leaves wall time and both Go contexts
// live. Admission observations are controlled separately; this is not a native
// CPU measurement or a physical host suspend experiment.
func TestForwardCreditWaitRechecksOriginalElapsedEntitlement(t *testing.T) {
	for _, boundary := range []struct {
		name       string
		creditCall int32
		wantDials  int32
	}{{"before-service-dial", 1, 0}, {"before-ready", 2, 1}} {
		t.Run(boundary.name, func(t *testing.T) {
			clock := &controlledLeaseClock{sample: leaseclock.Sample{Boot: "credit-clock", Tick: int64(time.Hour), Wall: time.Now().UTC()}}
			m, err := openWithLeaseClock(filepath.Join(t.TempDir(), "arm"), model.Resources{CPU: 1, Memory: 64 << 20}, clock.read)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			cpuReading(m, 64, 64, 0, 64, 0)
			joined := make(chan struct{})
			defer close(joined)
			hold, err := m.AcquireRelayTransit(t.Context(), model.Request{Key: "credit-cut:" + boundary.name, Reason: "original credit start fence", Resources: model.Resources{CPU: .01, Memory: 1 << 20}, TTL: time.Minute}, joined)
			if err != nil {
				t.Fatal(err)
			}
			grant := meshforward.Grant{
				Workspace: strings.Repeat("a", 32), Generation: strings.Repeat("b", 32), Source: strings.Repeat("c", 32), Target: strings.Repeat("d", 32),
				SourceGeneration: 1, TargetGeneration: 1, DirectoryPin: strings.Repeat("1", 64), DirectorySPKI: strings.Repeat("2", 64), SourcePin: strings.Repeat("3", 64), SourceSPKI: strings.Repeat("4", 64),
				TargetPin: strings.Repeat("5", 64), TargetSPKI: strings.Repeat("6", 64), Account: "operator", Endpoint: meshforward.Endpoint{Name: "web", Network: "tcp", Address: "127.0.0.1:8080"},
				Revision: 1, MaxConnections: 1, BytesPerConnection: 1 << 20, BytesPerSecond: 1 << 20, TotalBytes: 4 << 20, CreditNonce: strings.Repeat("e", 32),
				Transit: model.Resources{CPU: .01, Memory: 1 << 20}, MaxTTL: time.Minute, Expires: time.Now().Add(time.Hour),
			}
			request := meshforward.Request{Version: 1, Workspace: grant.Workspace, Generation: grant.Generation, Source: grant.Source, Target: grant.Target, SourcePin: grant.SourcePin, TargetPin: grant.TargetPin, Service: grant.Endpoint.Name, GrantRevision: grant.Revision, ConnectionID: strings.Repeat("7", 32), TTL: time.Minute, RouteBindingHash: strings.Repeat("8", 64)}
			identity := meshforward.Identity{SourcePin: grant.SourcePin, SourceSPKI: grant.SourceSPKI, TargetPin: grant.TargetPin, TargetSPKI: grant.TargetSPKI, Account: grant.Account}
			stream, client := net.Pipe()
			defer client.Close()
			service, application := net.Pipe()
			defer application.Close()
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			ctx, cancel := context.WithCancel(hold.Context())
			var checks, dials, ready, spends atomic.Int32
			proxy := &meshforward.Proxy{
				CheckCredit: func(context.Context, meshforward.Grant) error {
					if checks.Add(1) == boundary.creditCall {
						close(entered)
						<-release
					}
					return nil
				},
				Credit: func(_ context.Context, _ meshforward.Grant, n int) (int, error) { spends.Add(1); return n, nil },
				Dial:   func(context.Context, string, string) (net.Conn, error) { dials.Add(1); return service, nil },
			}
			ended := make(chan error, 1)
			completed := false
			defer func() {
				cancel()
				unblock()
				stream.Close()
				service.Close()
				if !completed {
					select {
					case <-ended:
					case <-time.After(3 * time.Second):
						t.Error("original proxy did not join")
					}
				}
				proxy.Stop()
			}()
			go func() {
				ended <- proxy.Open(ctx, request, identity, stream, func(check context.Context) (meshforward.Grant, error) {
					if err := hold.CheckActive(check); err != nil {
						return meshforward.Grant{}, err
					}
					return grant, nil
				}, func() error { ready.Add(1); return nil })
			}()
			select {
			case <-entered:
			case err := <-ended:
				completed = true
				t.Fatal("proxy did not reach its original held credit check", err)
			case <-time.After(3 * time.Second):
				t.Fatal("original credit check was not reached")
			}
			wall := clock.sample.Wall
			clock.sample.Tick += int64(time.Minute)
			if hold.Context().Err() != nil || ctx.Err() != nil || !time.Now().Before(grant.Expires) || !clock.sample.Wall.Equal(wall) {
				t.Fatal("fixture must retain live Go contexts and calendar authority")
			}
			unblock()
			select {
			case err := <-ended:
				completed = true
				if err == nil {
					t.Fatal("elapsed original entitlement was not rechecked after credit")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("refused proxy did not join")
			}
			if dials.Load() != boundary.wantDials || ready.Load() != 0 || spends.Load() != 0 || proxy.Active() != 0 {
				t.Fatal("expired credit boundary started new work", dials.Load(), ready.Load(), spends.Load(), proxy.Active())
			}
			assertClosed := func(peer net.Conn) {
				t.Helper()
				if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil && !errors.Is(err, io.ErrClosedPipe) {
					t.Fatal(err)
				}
				var one [1]byte
				if _, err := peer.Read(one[:]); err != io.EOF {
					t.Fatal("original proxy socket was not positively closed", err)
				}
			}
			assertClosed(client)
			if boundary.wantDials != 0 {
				assertClosed(application)
			}
			status, err := m.Status()
			if err != nil || status.Reserved.Memory < 1<<20 {
				t.Fatal("refusal certified settlement before original join", status.Reserved, err)
			}
		})
	}
}
