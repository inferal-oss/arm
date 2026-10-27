package providerfixture

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	p "getarm.org/internal/providerproto"
)

func TestFixtureChild(t *testing.T) {
	if os.Getenv("ARM_PROVIDER_FIXTURE_CHILD") != "1" {
		return
	}
	for n, arg := range os.Args {
		if arg == "--" {
			os.Exit(Run(os.Args[n+1:], os.Stdin, os.Stdout, os.Stderr))
		}
	}
	os.Exit(2)
}
func mustData(t *testing.T, v any) p.Data {
	t.Helper()
	d, err := data(v)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func sealed(t *testing.T, q p.Request) p.Request {
	t.Helper()
	d, err := p.IntentDigest(q)
	if err != nil {
		t.Fatal(err)
	}
	q.Context.IntentSHA256 = d
	return q
}
func create(t *testing.T, key, name string, config Config) p.Request {
	t.Helper()
	c := mustData(t, config)
	spec := mustData(t, specification{Name: name})
	username := p.Username("fixture owner")
	q := p.Request{Protocol: p.Protocol, Version: p.Version, Kind: "request", RequestID: "wire", Registration: p.Registration{ID: "fixture", Generation: "1", CodeSHA256: p.Digest(strings.Repeat("1", 64)), ImplementationVersion: "fixture-1", ResourceType: "fixture-object", ConfigSHA256: c.SHA256(), ExternalScope: p.ExternalScope{Account: "test", Project: "disposable", Namespace: "fixture"}}, Configuration: c, CredentialRefs: []string{}, Method: p.Create, Context: p.Context{Authority: "test-authority", Scope: p.Scope{Kind: "host", HostID: "test-host"}, OwnerUID: "1000", OwnerName: &username, Reason: "disposable external fixture", OperationKey: p.Key(key)}, Params: p.CreateParams{Specification: spec, SpecificationSHA256: spec.SHA256(), Budgets: []p.Budget{{Unit: "objects", Limit: "1"}}, ExpiresAt: p.Timestamp(time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)), Dependencies: []p.Identity{}}}
	return sealed(t, q)
}
func lookup(q p.Request) p.Request {
	params := p.QueryParams{TargetMethod: q.Method}
	switch v := q.Params.(type) {
	case p.ReleaseParams:
		i, d := v.ExternalIdentity, v.PriorReceiptSHA256
		params.ExternalIdentity = &i
		params.PriorReceiptSHA256 = &d
	case p.RenewParams:
		i, d := v.ExternalIdentity, v.PriorReceiptSHA256
		params.ExternalIdentity = &i
		params.PriorReceiptSHA256 = &d
	}
	q.Method = p.Lookup
	q.Params = params
	q.RequestID = "query-wire"
	return q
}
func mutation(t *testing.T, q p.Request, key string, rec p.OperationReceipt, release bool) p.Request {
	t.Helper()
	digest, err := ReceiptDigest(rec)
	if err != nil {
		t.Fatal(err)
	}
	q.Context.OperationKey = p.Key(key)
	if release {
		q.Method = p.Release
		q.Params = p.ReleaseParams{ExternalIdentity: *rec.ExternalIdentity, PriorReceiptSHA256: digest, ExpectedRevision: rec.Revision, Action: "delete"}
	} else {
		q.Method = p.Renew
		q.Params = p.RenewParams{ExternalIdentity: *rec.ExternalIdentity, PriorReceiptSHA256: digest, ExpectedRevision: rec.Revision, ExpiresAt: p.Timestamp(time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339Nano))}
	}
	return sealed(t, q)
}
func home(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}
func command(ctx context.Context, dir string, flags ...string) *exec.Cmd {
	binary := os.Getenv("ARM_PROVIDER_FIXTURE_BINARY")
	args := []string{}
	if binary == "" {
		binary, _ = os.Executable()
		args = append(args, "-test.run=^TestFixtureChild$", "--")
	}
	args = append(args, "--enable-test-provider", "--home", dir)
	args = append(args, flags...)
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = []string{"GOMAXPROCS=1", "ARM_PROVIDER_FIXTURE_CHILD=1"}
	return cmd
}
func invoke(dir string, q p.Request) (p.Response, error) {
	var in, out, stderr bytes.Buffer
	if err := p.WriteRequest(&in, q); err != nil {
		return p.Response{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := command(ctx, dir)
	cmd.Stdin = &in
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return p.Response{}, fmt.Errorf("child: %w: %s", err, stderr.String())
	}
	r, err := p.ReadResponse(&out)
	return r.Response(), err
}
func call(t *testing.T, dir string, q p.Request) p.Response {
	t.Helper()
	r, err := invoke(dir, q)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func operation(t *testing.T, r p.Response, state string) p.OperationReceipt {
	t.Helper()
	if r.Error != nil {
		t.Fatalf("protocol error: %+v", r.Error)
	}
	rec, ok := r.Result.(p.OperationReceipt)
	if !ok || rec.State != state {
		t.Fatalf("result %#v, expected %s", r.Result, state)
	}
	return rec
}
func denied(t *testing.T, r p.Response, code string) {
	t.Helper()
	if r.Error == nil || r.Error.Code != code {
		t.Fatalf("expected %s, got %+v", code, r)
	}
}

var defaultConfig = Config{Capacity: "4", ReadyDelayMS: "0"}

func TestConcurrentDurableCreateAndChangedIntent(t *testing.T) {
	dir := home(t)
	q := create(t, "same-key", "first", defaultConfig)
	// Initialize schema; later calls still use separate freshly executed images.
	call(t, dir, lookup(q))
	const contenders = 2
	results := make(chan p.Response, contenders)
	errors := make(chan error, contenders)
	var wg sync.WaitGroup
	for n := 0; n < contenders; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); r, e := invoke(dir, q); results <- r; errors <- e }()
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first *p.OperationReceipt
	for r := range results {
		rec := operation(t, r, "ready")
		if first == nil {
			first = &rec
		} else if !reflect.DeepEqual(*first, rec) {
			t.Fatal("same key allocated another incarnation")
		}
	}
	changed := q
	changed.Context.Reason = "changed intent"
	changed = sealed(t, changed)
	denied(t, call(t, dir, changed), "conflict")
	changed = q
	changed.Registration.Generation = "2"
	changed = sealed(t, changed)
	denied(t, call(t, dir, changed), "conflict")
	query := lookup(q)
	query.Context.OwnerUID = "2000"
	denied(t, call(t, dir, query), "conflict")
	different := create(t, "new-config-key", "new-object", Config{Capacity: "64", ReadyDelayMS: "0"})
	denied(t, call(t, dir, different), "conflict")
	rec := operation(t, call(t, dir, lookup(q)), "ready")
	if !reflect.DeepEqual(*first, rec) {
		t.Fatal("restart lost original create")
	}
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var count int
	if err = store.db.QueryRow("SELECT count(*) FROM objects").Scan(&count); err != nil || count != 1 {
		t.Fatalf("objects=%d err=%v", count, err)
	}
}
func TestDelayedReadinessExpiryRenewalAndHistoricalLookup(t *testing.T) {
	dir := home(t)
	q := create(t, "delayed", "slow", Config{Capacity: "2", ReadyDelayMS: "150"})
	pending := operation(t, call(t, dir, q), "pending")
	deadline := time.Now().Add(3 * time.Second)
	var ready p.OperationReceipt
	for {
		r := call(t, dir, lookup(q))
		if r.Error != nil {
			t.Fatal(r.Error)
		}
		rec := r.Result.(p.OperationReceipt)
		if rec.State == "ready" {
			ready = rec
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never became ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
	pendingDigest, err := ReceiptDigest(pending)
	if err != nil {
		t.Fatal(err)
	}
	anchored := lookup(q)
	anchored.Params = p.QueryParams{TargetMethod: p.Create, ExternalIdentity: pending.ExternalIdentity, PriorReceiptSHA256: &pendingDigest}
	operation(t, call(t, dir, anchored), "ready")
	operation(t, call(t, dir, q), "pending") // immutable original operation receipt
	readyDigest, err := ReceiptDigest(ready)
	if err != nil {
		t.Fatal(err)
	}
	currentAnchor := lookup(q)
	currentAnchor.Params = p.QueryParams{TargetMethod: p.Create, ExternalIdentity: ready.ExternalIdentity, PriorReceiptSHA256: &readyDigest}
	operation(t, call(t, dir, currentAnchor), "ready")
	renew := mutation(t, q, "renew", ready, false)
	renewed := operation(t, call(t, dir, renew), "ready")
	if renewed.Revision != "2" {
		t.Fatal("renew revision")
	}
	operation(t, call(t, dir, lookup(q)), "unknown")
	stale := mutation(t, q, "stale-delete", ready, true)
	denied(t, call(t, dir, stale), "conflict")
	release := mutation(t, q, "delete", renewed, true)
	operation(t, call(t, dir, release), "released")
	operation(t, call(t, dir, lookup(release)), "released")
	operation(t, call(t, dir, currentAnchor), "unknown")
	// Expiry ends readiness, but does not delete or return external capacity.
	short := create(t, "short", "expires", Config{Capacity: "1", ReadyDelayMS: "0"})
	v := short.Params.(p.CreateParams)
	v.ExpiresAt = p.Timestamp(time.Now().Add(800 * time.Millisecond).UTC().Format(time.RFC3339Nano))
	short.Params = v
	short = sealed(t, short)
	separate := home(t)
	operation(t, call(t, separate, short), "ready")
	expiry, err := time.Parse(time.RFC3339Nano, string(v.ExpiresAt))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(expiry) + 50*time.Millisecond)
	operation(t, call(t, separate, lookup(short)), "unknown")
	other := create(t, "capacity", "other", Config{Capacity: "1", ReadyDelayMS: "0"})
	denied(t, call(t, separate, other), "denied")
}
func TestBudgetDenialDependenciesAndReusedNames(t *testing.T) {
	dir := home(t)
	config := Config{Capacity: "2", ReadyDelayMS: "0"}
	parent := create(t, "parent", "shared", config)
	pr := operation(t, call(t, dir, parent), "ready")
	child := create(t, "child", "dependent", config)
	v := child.Params.(p.CreateParams)
	v.Dependencies = []p.Identity{*pr.ExternalIdentity}
	child.Params = v
	child = sealed(t, child)
	cr := operation(t, call(t, dir, child), "ready")
	full := create(t, "full", "full", config)
	denied(t, call(t, dir, full), "denied")
	blocked := mutation(t, parent, "blocked-delete", pr, true)
	denied(t, call(t, dir, blocked), "conflict")
	releaseChild := mutation(t, child, "delete-child", cr, true)
	operation(t, call(t, dir, releaseChild), "released")
	// Failed keys retain their original outcome, even once a dependency disappears.
	denied(t, call(t, dir, blocked), "conflict")
	denied(t, call(t, dir, lookup(blocked)), "conflict")
	releaseParent := mutation(t, parent, "delete-parent", pr, true)
	operation(t, call(t, dir, releaseParent), "released")
	replacement := create(t, "replacement", "shared", config)
	rr := operation(t, call(t, dir, replacement), "ready")
	if *rr.ExternalIdentity == *pr.ExternalIdentity {
		t.Fatal("reused name reused identity")
	}
	operation(t, call(t, dir, releaseParent), "released")
	operation(t, call(t, dir, lookup(replacement)), "ready")
	missing := mutation(t, replacement, "missing", rr, true)
	mv := missing.Params.(p.ReleaseParams)
	mv.ExternalIdentity.Incarnation = "missing"
	missing.Params = mv
	missing = sealed(t, missing)
	denied(t, call(t, dir, missing), "not_found")
}
func TestDeniedCleanupRetainsExternalCapacity(t *testing.T) {
	dir := home(t)
	c := Config{Capacity: "1", ReadyDelayMS: "0", DenyDelete: true}
	q := create(t, "retained", "protected", c)
	rec := operation(t, call(t, dir, q), "ready")
	end := mutation(t, q, "denied-delete", rec, true)
	denied(t, call(t, dir, end), "denied")
	denied(t, call(t, dir, lookup(end)), "denied")
	operation(t, call(t, dir, lookup(q)), "ready")
	denied(t, call(t, dir, create(t, "second", "second", c)), "denied")
}

// killAt retains the original Cmd/Process and waits for that exact child. A
// marker alone is not process closure or reclamation evidence.
func killAt(t *testing.T, dir string, q p.Request, stage string) {
	t.Helper()
	var in, out bytes.Buffer
	if err := p.WriteRequest(&in, q); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	cmd := command(ctx, dir, "--fault", "hold-"+stage)
	cmd.Stdin = &in
	cmd.Stdout = &out
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(stderr)
	if !scanner.Scan() || scanner.Text() != "fixture: "+stage {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatalf("missing original stage marker: %s", scanner.Text())
	}
	if err = cmd.Process.Kill(); err != nil {
		cmd.Wait()
		t.Fatal(err)
	}
	err = cmd.Wait()
	if err == nil || cmd.ProcessState == nil || cmd.ProcessState.Success() {
		t.Fatalf("original exit absent: %v", err)
	}
	if out.Len() != 0 {
		t.Fatal("fault unexpectedly returned a response")
	}
	t.Logf("original child pid=%d joined: %v", cmd.Process.Pid, cmd.ProcessState)
}
func TestActualProcessLossAroundCreationAndDeletion(t *testing.T) {
	for _, stage := range []string{"before-commit", "after-commit"} {
		t.Run(stage, func(t *testing.T) {
			dir := home(t)
			q := create(t, "create-key", "object", defaultConfig)
			killAt(t, dir, q, stage)
			observed := call(t, dir, lookup(q))
			if stage == "before-commit" {
				denied(t, observed, "not_found")
			} else {
				operation(t, observed, "ready")
			}
			created := operation(t, call(t, dir, q), "ready")
			if stage == "after-commit" && *observed.Result.(p.OperationReceipt).ExternalIdentity != *created.ExternalIdentity {
				t.Fatal("lost create was duplicated")
			}
			end := mutation(t, q, "delete-key", created, true)
			killAt(t, dir, end, stage)
			observed = call(t, dir, lookup(end))
			if stage == "before-commit" {
				denied(t, observed, "not_found")
				operation(t, call(t, dir, lookup(q)), "ready")
			} else {
				operation(t, observed, "released")
				operation(t, call(t, dir, lookup(q)), "unknown")
			}
			operation(t, call(t, dir, end), "released")
		})
	}
}
func TestExplicitEnableAndMalformedRequestHaveNoState(t *testing.T) {
	dir := home(t)
	var diagnostic bytes.Buffer
	if Run([]string{"--home", dir}, strings.NewReader(""), &bytes.Buffer{}, &diagnostic) != 2 {
		t.Fatal("implicit enable")
	}
	if Run([]string{"--enable-test-provider", "--home", dir}, strings.NewReader("bad frame"), &bytes.Buffer{}, &diagnostic) != 2 {
		t.Fatal("malformed accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "external.db")); !os.IsNotExist(err) {
		t.Fatal("pre-validation external state")
	}
}
func TestPersistentOperationBudget(t *testing.T) {
	dir := home(t)
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	// Fill the bounded journal through real fixture transactions. Capacity
	// refusals are durable original operations too; no rows are fabricated.
	for n := 0; n < MaxOperations; n++ {
		q := create(t, fmt.Sprintf("history-%d", n), fmt.Sprintf("object-%d", n), defaultConfig)
		r, e := store.Handle(ctx, q, nil)
		if e != nil {
			t.Fatal(e)
		}
		if n < 4 {
			operation(t, r, "ready")
		} else {
			denied(t, r, "denied")
		}
	}
	q := create(t, "exhausted", "new", defaultConfig)
	denied(t, call(t, dir, q), "denied")
	var count int
	if err = store.db.QueryRow("SELECT count(*) FROM objects").Scan(&count); err != nil || count != 4 {
		t.Fatal("budget failure changed allocations")
	}
}

func TestCapabilitiesAndExactProtocolMatching(t *testing.T) {
	dir := home(t)
	original := create(t, "original", "matched", defaultConfig)
	reg, err := p.RegistrationFromTrusted(original.Registration, original.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	verify := func(sent, saved p.Request, target *p.ExpectedTarget) p.Response {
		t.Helper()
		raw, err := p.EncodeRequest(sent)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := p.DecodeRequest(raw)
		if err != nil {
			t.Fatal(err)
		}
		trusted, err := p.OriginalFromTrusted(saved)
		if err != nil {
			t.Fatal(err)
		}
		matched, err := p.MatchRequest(decoded, reg, trusted, target)
		if err != nil {
			t.Fatal(err)
		}
		response := call(t, dir, sent)
		body, err := p.EncodeResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		reply, err := p.DecodeResponse(body)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = p.MatchResponse(reply, matched, reg, trusted, target); err != nil {
			t.Fatal(err)
		}
		return response
	}
	for _, method := range []p.Method{p.Describe, p.Validate, p.Plan} {
		q := original
		q.Method = method
		q.Context.OperationKey = p.Key(method)
		switch method {
		case p.Describe:
			q.Params = p.DescribeParams{}
		case p.Validate:
			q.Params = p.ValidateParams{}
		case p.Plan:
			v := original.Params.(p.CreateParams)
			q.Params = p.PlanParams{Specification: v.Specification, Budgets: v.Budgets, ExpiresAt: v.ExpiresAt}
		}
		q = sealed(t, q)
		verify(q, q, nil)
	}
	rec := operation(t, verify(original, original, nil), "ready")
	verify(lookup(original), original, nil)
	query := lookup(original)
	query.Method = p.Reconcile
	verify(query, original, nil)
	digest, err := ReceiptDigest(rec)
	if err != nil {
		t.Fatal(err)
	}
	target, err := p.TargetFromTrusted(*rec.ExternalIdentity, digest, rec.Revision, rec.SpecificationSHA256)
	if err != nil {
		t.Fatal(err)
	}
	query.Params = p.QueryParams{TargetMethod: p.Create, ExternalIdentity: rec.ExternalIdentity, PriorReceiptSHA256: &digest}
	verify(query, original, &target)
	renewed := mutation(t, original, "matched-renew", rec, false)
	next := operation(t, verify(renewed, renewed, &target), "ready")
	verify(lookup(renewed), renewed, &target)
	end := mutation(t, original, "matched-release", next, true)
	nextDigest, err := ReceiptDigest(next)
	if err != nil {
		t.Fatal(err)
	}
	nextTarget, err := p.TargetFromTrusted(*next.ExternalIdentity, nextDigest, next.Revision, next.SpecificationSHA256)
	if err != nil {
		t.Fatal(err)
	}
	verify(end, end, &nextTarget)
	verify(lookup(end), end, &nextTarget)
}

func TestFixtureConfigurationRejectsAliasesAndMissingFields(t *testing.T) {
	for _, configuration := range []any{
		map[string]any{"Capacity": "4", "ready_delay_ms": "0", "deny_delete": false},
		map[string]any{"capacity": "4", "ready_delay_ms": "0"},
		map[string]any{"capacity": "4", "ready_delay_ms": "0", "deny_delete": nil},
		map[string]any{"capacity": "4", "ready_delay_ms": "0", "deny_delete": false, "extra": true},
	} {
		dir := home(t)
		q := create(t, "invalid", "unused", defaultConfig)
		q.Method = p.Validate
		q.Params = p.ValidateParams{}
		q.Configuration = mustData(t, configuration)
		q.Registration.ConfigSHA256 = q.Configuration.SHA256()
		q = sealed(t, q)
		denied(t, call(t, dir, q), "invalid_configuration")
	}
}

func TestMutationsCannotCrossOriginalOwnerOrHostScope(t *testing.T) {
	dir := home(t)
	q := create(t, "owned", "original-owner", defaultConfig)
	rec := operation(t, call(t, dir, q), "ready")
	wrongOwner := mutation(t, q, "wrong-owner", rec, true)
	wrongOwner.Context.OwnerUID = "2000"
	wrongOwner = sealed(t, wrongOwner)
	denied(t, call(t, dir, wrongOwner), "conflict")
	wrongScope := mutation(t, q, "wrong-host", rec, true)
	wrongScope.Context.Scope.HostID = "other-host"
	wrongScope = sealed(t, wrongScope)
	denied(t, call(t, dir, wrongScope), "conflict")
	operation(t, call(t, dir, lookup(q)), "ready")
}

func TestMissingExternalStateIsUnknownAndNeverReleased(t *testing.T) {
	dir := home(t)
	q := create(t, "missing-external-state", "external", defaultConfig)
	born := operation(t, call(t, dir, q), "ready")
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.db.Exec("DELETE FROM objects WHERE id=?", string(born.ExternalIdentity.ID))
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		store.Close()
		t.Fatal(n, err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, method := range []p.Method{p.Lookup, p.Reconcile} {
		query := lookup(q)
		query.Method = method
		rec := operation(t, call(t, dir, query), "unknown")
		if *rec.ExternalIdentity != *born.ExternalIdentity || rec.Revision != born.Revision {
			t.Fatal("lost original identity or revision")
		}
	}
	release := mutation(t, q, "missing-state-release", born, true)
	denied(t, call(t, dir, release), "not_found")
	denied(t, call(t, dir, lookup(release)), "not_found")
	operation(t, call(t, dir, lookup(q)), "unknown")
	// Retain the original successful operation as history without re-creating.
	operation(t, call(t, dir, q), "ready")
}
