package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"getarm.org/internal/storage"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"golang.org/x/sys/unix"
)

type startupFixtureOptions struct {
	identity        string
	inspectError    *model.Error
	dropInspection  bool
	blockInspection bool
	replaceEndpoint bool
	draining        bool
}

type startupFixture struct {
	home         string
	requests     chan string
	statusCalls  atomic.Int32
	inspectCalls atomic.Int32
}

// Only owned temp files and loopback requests; no daemon process is launched.
func newStartupFixture(t *testing.T, options startupFixtureOptions) *startupFixture {
	t.Helper()
	home := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := os.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	id, identityErr := storage.DirectoryIdentity(int(directory.Fd()))
	if err = errors.Join(identityErr, directory.Close()); err != nil {
		t.Fatal(err)
	}
	status := model.Status{Host: strings.Repeat("a", 32), Owner: strconv.Itoa(os.Geteuid()), Draining: options.draining}
	authority := model.BackendAuthority{Host: status.Host, Owner: status.Owner, Home: home, Directory: id, Generation: strings.Repeat("1", 32)}
	switch options.identity {
	case "home":
		authority.Home = filepath.Join(home, "different")
	case "generation":
		authority.Generation = strings.Repeat("2", 32)
	case "owner":
		authority.Owner = "different"
	case "effective-owner":
		status.Owner, authority.Owner = "different", "different"
	case "directory":
		authority.Directory.Inode++
	case "host":
		authority.Host = strings.Repeat("b", 32)
	}
	f := &startupFixture{home: home, requests: make(chan string, 32)}
	release := make(chan struct{})
	var handlers sync.WaitGroup
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlers.Add(1)
		defer handlers.Done()
		if r.Header.Get("Authorization") != "Bearer startup-fixture" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		var request model.WireRequest
		if wsjson.Read(r.Context(), connection, &request) != nil {
			return
		}
		select {
		case f.requests <- request.Operation:
		default:
		}
		response := model.WireResponse{Version: 1, ID: request.ID}
		switch request.Operation {
		case "status":
			f.statusCalls.Add(1)
			response.Result = status
		case "startup_inspect":
			f.inspectCalls.Add(1)
			if options.blockInspection {
				<-release
				return
			}
			if options.dropInspection {
				return
			}
			response.Result, response.Error = authority, options.inspectError
			if options.replaceEndpoint {
				changed, err := local.ReadEndpoint(home)
				if err != nil {
					t.Error(err)
					return
				}
				changed.Generation = strings.Repeat("3", 32)
				if err := local.AtomicJSON(filepath.Join(home, "endpoint.json"), changed); err != nil {
					t.Error(err)
					return
				}
			}
		default:
			response.Error = &model.Error{Code: "forbidden", Message: "fixture permits only startup inspection"}
		}
		_ = wsjson.Write(r.Context(), connection, response)
	}))
	endpoint := local.Endpoint{URL: "ws" + strings.TrimPrefix(backend.URL, "http"), Token: "startup-fixture", PID: os.Getpid(), Generation: strings.Repeat("1", 32)}
	t.Cleanup(func() {
		close(release)
		backend.Close()
		// No new handlers can register after Close. Join each original handler,
		// including its original WebSocket CloseNow, before temp-home cleanup.
		handlers.Wait()
	})
	if err := local.AtomicJSON(filepath.Join(home, "endpoint.json"), endpoint); err != nil {
		t.Fatal(err)
	}
	return f
}

func requireStartupError(t *testing.T, err error, code string) {
	t.Helper()
	var detail *model.Error
	if !errors.As(err, &detail) || detail.Code != code {
		t.Fatalf("got %v, want %s", err, code)
	}
}

func TestStartUnknownInspectionDoesNotLaunch(t *testing.T) {
	for _, payload := range []string{"{", `{"url":"ws://127.0.0.1:0/v1/ws"}`} {
		t.Run(payload, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "state")
			if err := os.Mkdir(home, 0700); err != nil {
				t.Fatal(err)
			}
			// Even a regression must stop at this owned lock instead of
			// launching a detached copy of the test executable.
			lock, err := local.Lock(home, "daemon.lock")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := errors.Join(unix.Flock(int(lock.Fd()), unix.LOCK_UN), lock.Close()); err != nil {
					t.Error(err)
				}
			})
			if err := os.WriteFile(filepath.Join(home, "endpoint.json"), []byte(payload), 0600); err != nil {
				t.Fatal(err)
			}
			var out, stderr bytes.Buffer
			code := Main([]string{"--home", home, "start", "--json"}, strings.NewReader(""), &out, &stderr)
			if code != 1 || out.Len() != 0 || !strings.Contains(stderr.String(), "startup_outcome_unknown") {
				t.Fatal(code, out.String(), stderr.String())
			}
			entries, err := os.ReadDir(home)
			if err != nil || len(entries) != 2 || entries[0].Name() != "daemon.lock" || entries[1].Name() != "endpoint.json" {
				t.Fatal("failed inspection created startup state", entries, err)
			}
		})
	}
}

func TestStartupAbsenceAndPrelaunchCancellation(t *testing.T) {
	home := filepath.Join(t.TempDir(), "custom absent home")
	a := app{home: home, ctx: t.Context()}
	_, running, err := a.existingStartup()
	if err != nil || running {
		t.Fatal(running, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var out, stderr bytes.Buffer
	a.ctx, a.in, a.out, a.err = ctx, strings.NewReader(""), &out, &stderr
	code, err := a.daemon("start", []string{"--dry-run"})
	if !errors.Is(err, context.Canceled) || a.finishCommand(code, err) != 130 || !strings.Contains(stderr.String(), `"code":"interrupted"`) || strings.Contains(stderr.String(), "startup_outcome_unknown") {
		t.Fatal(code, err, stderr.String())
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("absent/cancelled startup created home", err)
	}
}

func TestStartupReadinessRequiresExactAuthority(t *testing.T) {
	for _, identity := range []string{"home", "generation", "owner", "effective-owner", "directory", "host"} {
		t.Run(identity, func(t *testing.T) {
			fixture := newStartupFixture(t, startupFixtureOptions{identity: identity})
			a := app{home: fixture.home, ctx: t.Context()}
			_, err := a.waitForStartup()
			requireStartupError(t, err, "node_generation_changed")
			if fixture.statusCalls.Load() != 1 || fixture.inspectCalls.Load() != 1 {
				t.Fatal("hard identity failure was polled", fixture.statusCalls.Load(), fixture.inspectCalls.Load())
			}
		})
	}
	for _, fixtureCase := range []struct {
		name    string
		options startupFixtureOptions
		code    string
	}{
		{"draining", startupFixtureOptions{draining: true}, "backend_draining"},
		{"endpoint-replaced", startupFixtureOptions{replaceEndpoint: true}, "node_generation_changed"},
		{"inspection-refused", startupFixtureOptions{inspectError: &model.Error{Code: "forbidden", Message: "inspection refused"}}, "forbidden"},
	} {
		t.Run(fixtureCase.name, func(t *testing.T) {
			fixture := newStartupFixture(t, fixtureCase.options)
			a := app{home: fixture.home, ctx: t.Context()}
			_, err := a.waitForStartup()
			requireStartupError(t, err, fixtureCase.code)
			if fixture.statusCalls.Load() != 1 || fixture.inspectCalls.Load() != 1 {
				t.Fatal("hard startup refusal was retried")
			}
		})
	}
}

func TestStartupPollingConfirmsAuthorityAndRejectsStatusOnlySuccess(t *testing.T) {
	fixture := newStartupFixture(t, startupFixtureOptions{})
	a := app{home: fixture.home, ctx: t.Context()}
	status, err := a.waitForStartup()
	if err != nil || status.Host == "" || fixture.statusCalls.Load() != 1 || fixture.inspectCalls.Load() != 1 {
		t.Fatal(status.Host, err, fixture.statusCalls.Load(), fixture.inspectCalls.Load())
	}
	broken := newStartupFixture(t, startupFixtureOptions{dropInspection: true})
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Millisecond)
	defer cancel()
	a.home, a.ctx = broken.home, ctx
	_, err = a.waitForStartup()
	// A successful status and failed authority response are never readiness.
	requireStartupError(t, err, "startup_outcome_unknown")
	if broken.statusCalls.Load() == 0 || broken.inspectCalls.Load() == 0 {
		t.Fatal("fixture did not reach the authority boundary")
	}
}

func TestStartupCancellationDuringAuthorityInspection(t *testing.T) {
	fixture := newStartupFixture(t, startupFixtureOptions{blockInspection: true})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	a := app{home: fixture.home, ctx: ctx}
	done := make(chan error, 1)
	go func() { _, err := a.waitForStartup(); done <- err }()
	for {
		select {
		case operation := <-fixture.requests:
			if operation == "startup_inspect" {
				cancel()
				select {
				case err := <-done:
					requireStartupError(t, err, "startup_outcome_unknown")
				case <-time.After(time.Second):
					t.Fatal("cancelled original startup wait did not return")
				}
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatal("fixture did not reach original authority inspection")
		}
	}
}

func TestStartupPollingHonorsWholeWaitAndParentDeadline(t *testing.T) {
	for _, parentDeadline := range []bool{false, true} {
		t.Run(strconv.FormatBool(parentDeadline), func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "absent")
			ctx := t.Context()
			if parentDeadline {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 40*time.Millisecond)
				defer cancel()
			}
			a := app{home: home, ctx: ctx}
			began := time.Now()
			_, err := a.waitForStartup()
			elapsed := time.Since(began)
			requireStartupError(t, err, "startup_outcome_unknown")
			if parentDeadline && elapsed > time.Second || !parentDeadline && (elapsed < 14*time.Second || elapsed > 20*time.Second) {
				t.Fatal("startup polling exceeded its whole/parent target", elapsed)
			}
			if _, err := os.Stat(home); !os.IsNotExist(err) {
				t.Fatal("polling created an absent home", err)
			}
		})
	}
}

func TestStartupUnknownSurvivesSharedInterruption(t *testing.T) {
	for _, human := range []bool{false, true} {
		for _, wrapped := range []bool{false, true} {
			t.Run(fmt.Sprintf("human=%v/wrapped=%v", human, wrapped), func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				var out, stderr bytes.Buffer
				home := filepath.Join(t.TempDir(), "selected custom home")
				a := app{home: home, ctx: ctx, out: &out, err: &stderr, outputTTY: human, errorTTY: human}
				_, err := a.waitForStartup()
				if wrapped {
					err = fmt.Errorf("wrapped startup result: %w", err)
				}
				if code := a.finishCommand(1, err); code != 130 {
					t.Fatal(code)
				}
				if !strings.Contains(stderr.String(), "startup_outcome_unknown") || !strings.Contains(stderr.String(), filepath.Join(home, "daemon.log")) || !strings.Contains(stderr.String(), "may still be running") || strings.Contains(stderr.String(), "operation interrupted") || out.Len() != 0 {
					t.Fatal("original startup uncertainty was erased", out.String(), stderr.String())
				}
				if !human {
					var response struct {
						Error *model.Error `json:"error"`
					}
					if json.Unmarshal(stderr.Bytes(), &response) != nil || response.Error == nil || response.Error.Code != "startup_outcome_unknown" {
						t.Fatal(stderr.String())
					}
				}
			})
		}
	}
}

func TestStartupDanglingEndpointIsUnknown(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-endpoint-target", filepath.Join(home, "endpoint.json")); err != nil {
		t.Fatal(err)
	}
	a := app{home: home, ctx: t.Context()}
	_, running, err := a.existingStartup()
	requireStartupError(t, err, "startup_outcome_unknown")
	if running {
		t.Fatal("dangling endpoint became ready")
	}
	for _, name := range []string{"daemon.lock", "daemon.log", "state.db"} {
		if _, err := os.Stat(filepath.Join(home, name)); !os.IsNotExist(err) {
			t.Fatalf("failed inspection created %s: %v", name, err)
		}
	}
}
