package pressure

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

var base = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

func policy() Policy {
	return Policy{
		Resources: map[Resource]Threshold{CPU: {EnterBelow: 20, ExitAbove: 40, AllowPause: true}},
		MaxAge:    2 * time.Second, MaxSampleGap: 10 * time.Second,
		SustainFor: 5 * time.Second, Grace: 10 * time.Second, WarningEvery: 3 * time.Second,
		RecoverFor: 4 * time.Second, MinPause: 3 * time.Second, MaxPause: 30 * time.Second,
		MaxPauseJobs: 1,
		MaxJobs:      4, MaxWarnings: 3, PendingTimeout: 12 * time.Second,
	}
}

func owned() Job {
	return Job{LeaseID: "lease", ExecutionID: "execution", Generation: "generation-1", OwnershipID: "owner-1", AgentID: "agent",
		Supervised: true, StillOwned: true, PauseCapability: true, PauseAllowed: true, ExpiresAt: base.Add(time.Minute)}
}

func input(sec int, available uint64, jobs ...Job) Input {
	now := base.Add(time.Duration(sec) * time.Second)
	return Input{Epoch: "boot-1", Tick: time.Duration(sec) * time.Second, Now: now,
		Samples: map[Resource]Sample{CPU: {Known: true, ObservedAt: now, ObservedTick: time.Duration(sec) * time.Second, Epoch: "boot-1", Available: available}}, Jobs: jobs}
}

func observed(in Input, available uint64) Sample {
	return Sample{Known: true, ObservedAt: in.Now, ObservedTick: in.Tick, Epoch: in.Epoch, Available: available}
}

func advance(t *testing.T, s State, in Input, p Policy) (State, []Action) {
	t.Helper()
	next, actions, err := Advance(s, in, p)
	if err != nil {
		t.Fatal(err)
	}
	return next, actions
}

func kinds(actions []Action) []ActionKind {
	var out []ActionKind
	for _, a := range actions {
		out = append(out, a.Kind)
	}
	return out
}

func wantKinds(t *testing.T, got []Action, want ...ActionKind) {
	t.Helper()
	if !reflect.DeepEqual(kinds(got), want) {
		t.Fatalf("actions = %v, want %v", kinds(got), want)
	}
}

func reachPause(t *testing.T, p Policy, j Job) State {
	t.Helper()
	var s State
	for _, sec := range []int{0, 5, 8, 15} {
		s, _ = advance(t, s, input(sec, 10, j), p)
	}
	if len(s.Pauses) != 1 {
		t.Fatalf("pause requests = %d", len(s.Pauses))
	}
	return s
}

func resultFor(s State, j Job, applied bool) ControlResult {
	return ControlResult{Key: s.Pauses[id(j)].PauseKey, Applied: applied}
}

func pauseFromState(s State, j Job) Action {
	record := s.Pauses[id(j)]
	a := action(RequestPause, record.Reason, record.Incident, 0, id(j), j.AgentID, s.Epoch, record.Revision)
	a.PolicyDigest = record.PolicyDigest
	return a
}

func TestBurstAndSustainedGrace(t *testing.T) {
	p, j := policy(), owned()
	var s State
	var a []Action
	s, a = advance(t, s, input(0, 10, j), p)
	wantKinds(t, a)
	s, a = advance(t, s, input(2, 50, j), p)
	wantKinds(t, a)
	s, a = advance(t, s, input(6, 50, j), p)
	wantKinds(t, a)
	if s.Resources[CPU].Active {
		t.Fatal("external burst became incident")
	}
	s, a = advance(t, s, input(10, 10, j), p)
	wantKinds(t, a)
	s, a = advance(t, s, input(15, 10, j), p)
	wantKinds(t, a, WarnOperator, WarnAgent)
	if a[0].Reason != CPU || a[0].Incident != 2 || a[0].Warning != 1 {
		t.Fatalf("bad warning: %+v", a[0])
	}
	j.Acknowledged = true
	s, a = advance(t, s, input(18, 10, j), p)
	wantKinds(t, a, WarnOperator, WarnAgent)
	s, a = advance(t, s, input(24, 10, j), p)
	wantKinds(t, a, WarnOperator, WarnAgent)
	s, a = advance(t, s, input(25, 10, j), p)
	wantKinds(t, a, RequestPause)
	if a[0].Job != id(j) || a[0].Reason != CPU {
		t.Fatalf("pause lacks identity/reason: %+v", a[0])
	}
	if !a[0].Fresh || a[0].Sample.Available != 10 || !a[0].At.Equal(base.Add(25*time.Second)) {
		t.Fatalf("pause lacks observation: %+v", a[0])
	}
	_, again := advance(t, s, input(25, 10, j), p)
	wantKinds(t, again)
}

func TestCompetingResourcesAndBytesRemainObligated(t *testing.T) {
	p := policy()
	p.Resources[Memory] = Threshold{EnterBelow: 100, ExitAbove: 200}
	p.Resources[Disk] = Threshold{EnterBelow: 100, ExitAbove: 200}
	j := owned()
	var s State
	for _, sec := range []int{0, 5, 8, 15} {
		in := input(sec, 10, j)
		in.Samples[Memory] = observed(in, 50)
		in.Samples[Disk] = observed(in, 50)
		s, _ = advance(t, s, in, p)
	}
	if len(s.Pauses) != 1 || s.Pauses[id(j)].Reason != CPU {
		t.Fatalf("unexpected pause decisions: %+v", s.Pauses)
	}
	// A CPU recovery does not release the paused job while memory/disk are low.
	j.Paused = true
	for _, sec := range []int{16, 20} {
		in := input(sec, 50, j)
		if sec == 16 {
			in.Results = []ControlResult{resultFor(s, j, true)}
		}
		in.Samples[Memory] = observed(in, 50)
		in.Samples[Disk] = observed(in, 50)
		var a []Action
		s, a = advance(t, s, in, p)
		for _, x := range a {
			if x.Kind == RequestResume || x.Kind == RequestPause {
				t.Fatalf("memory/disk pressure changed pause: %+v", x)
			}
		}
	}
	if len(s.Pauses) != 1 {
		t.Fatal("pause obligation lost")
	}
}

func TestExplicitMemoryPauseRetainsObligation(t *testing.T) {
	p, j := policy(), owned()
	p.Resources = map[Resource]Threshold{Memory: {EnterBelow: 100, ExitAbove: 200, AllowPause: true}}
	var s State
	for _, sec := range []int{0, 5, 8, 15} {
		in := input(sec, 50, j)
		in.Samples = map[Resource]Sample{Memory: observed(in, 50)}
		s, _ = advance(t, s, in, p)
	}
	if len(s.Pauses) != 1 || s.Pauses[id(j)].Reason != Memory {
		t.Fatalf("explicit memory pause missing: %+v", s.Pauses)
	}
	j.Paused = true
	in := input(20, 50, j)
	in.Results = []ControlResult{resultFor(s, j, true)}
	in.Samples = map[Resource]Sample{Memory: observed(in, 50)}
	s, a := advance(t, s, in, p)
	for _, x := range a {
		if x.Kind == RequestResume {
			t.Fatalf("pressure released memory obligation: %+v", x)
		}
	}
	if len(s.Pauses) != 1 {
		t.Fatal("paused memory obligation lost")
	}
}

func TestRecoveryNeedsFreshHysteresis(t *testing.T) {
	p, j := policy(), owned()
	s := reachPause(t, p, j)
	j.Paused = true
	var a []Action
	in := input(16, 30, j)
	in.Results = []ControlResult{resultFor(s, j, true)}
	s, a = advance(t, s, in, p)
	wantKinds(t, a)
	s, a = advance(t, s, input(17, 50, j), p)
	wantKinds(t, a)
	s, a = advance(t, s, input(19, 30, j), p)
	wantKinds(t, a)
	s, a = advance(t, s, input(20, 50, j), p)
	wantKinds(t, a)
	s, a = advance(t, s, input(24, 50, j), p)
	wantKinds(t, a, RequestResume)
	if a[0].Job != id(j) || len(s.Pauses) != 1 || s.Pauses[id(j)].Phase != ResumePending {
		t.Fatalf("resume identity/state: %+v %+v", a, s.Pauses)
	}
	s, a = advance(t, s, input(25, 50, j), p)
	wantKinds(t, a)
	j.Paused = false
	in = input(26, 50, j)
	in.Results = []ControlResult{{Key: s.Pauses[id(j)].ResumeKey, Applied: true}}
	s, a = advance(t, s, in, p)
	wantKinds(t, a)
	if len(s.Pauses) != 0 {
		t.Fatal("confirmed resume retained control record")
	}
}

func TestUnknownStaleFutureAndSuspension(t *testing.T) {
	p, j := policy(), owned()
	var s State
	for _, sec := range []int{0, 5, 8} {
		s, _ = advance(t, s, input(sec, 10, j), p)
	}
	for name, sample := range map[string]Sample{
		"missing": {},
		"stale":   {Known: true, ObservedAt: base.Add(8 * time.Second), ObservedTick: 8 * time.Second, Epoch: "boot-1", Available: 10},
		"future":  {Known: true, ObservedAt: base.Add(21 * time.Second), ObservedTick: 21 * time.Second, Epoch: "boot-1", Available: 10},
	} {
		t.Run(name, func(t *testing.T) {
			in := input(20, 10, j)
			in.Samples[CPU] = sample
			next, a := advance(t, s, in, p)
			wantKinds(t, a)
			// Gap or missing evidence restarts the pressure window, even though
			// the old warning/grace deadline remains recorded.
			next, a = advance(t, next, input(21, 10, j), p)
			wantKinds(t, a)
			next, a = advance(t, next, input(26, 10, j), p)
			if len(next.Pauses) != 1 {
				t.Fatalf("fresh sustained evidence did not pause: %v", a)
			}
		})
	}
	// A long suspend gap alone cannot complete the sustained window.
	s, a := advance(t, s, input(100, 10, j), p)
	wantKinds(t, a)
	if len(s.Pauses) != 0 {
		t.Fatal("suspension manufactured sustained pressure")
	}
}

func TestExpiryReplacementLossAndCapability(t *testing.T) {
	p := policy()
	for name, mutate := range map[string]func(*Job){
		"expired":              func(j *Job) { j.ExpiresAt = base.Add(15 * time.Second) },
		"replacement":          func(j *Job) { j.Generation = "generation-2" },
		"owner lost":           func(j *Job) { j.StillOwned = false },
		"permission revoked":   func(j *Job) { j.PauseAllowed = false },
		"provider unsupported": func(j *Job) { j.PauseCapability = false },
		"revoked":              func(j *Job) { j.Revoked = true },
		"stopping":             func(j *Job) { j.Stopping = true },
	} {
		t.Run(name, func(t *testing.T) {
			j := owned()
			var s State
			for _, sec := range []int{0, 5, 8} {
				s, _ = advance(t, s, input(sec, 10, j), p)
			}
			mutate(&j)
			s, a := advance(t, s, input(15, 10, j), p)
			for _, x := range a {
				if x.Kind == RequestPause {
					t.Fatalf("unsafe pause: %+v", x)
				}
			}
			if len(s.Pauses) != 0 {
				t.Fatal("unsafe pause recorded")
			}
		})
	}
	// No old pause record may command a replacement or outlive TTL.
	j := owned()
	s := reachPause(t, p, j)
	j.Paused = true
	j.Generation = "generation-2"
	s, a := advance(t, s, input(16, 50, j), p)
	wantKinds(t, a, CancelPause)
	if len(s.Pauses) != 1 || s.Pauses[id(owned())].Phase != CancelPending {
		t.Fatal("replacement did not invalidate old pending pause")
	}
	s = reachPause(t, p, owned())
	j = owned()
	j.Paused = true
	j.ExpiresAt = base.Add(19 * time.Second)
	s, _ = advance(t, s, input(16, 50, j), p)
	_, a = advance(t, s, input(20, 50, j), p)
	wantKinds(t, a, CancelPause)
}

func TestReplacementEarnsOwnWarningAndGrace(t *testing.T) {
	p, j := policy(), owned()
	var s State
	for _, sec := range []int{0, 5, 8} {
		s, _ = advance(t, s, input(sec, 10, j), p)
	}
	j.Generation = "generation-2"
	var a []Action
	s, a = advance(t, s, input(15, 10, j), p)
	for _, x := range a {
		if x.Kind == RequestPause {
			t.Fatalf("replacement inherited old grace: %+v", x)
		}
	}
	s, a = advance(t, s, input(18, 10, j), p)
	for _, x := range a {
		if x.Kind == RequestPause {
			t.Fatalf("replacement paused on second warning: %+v", x)
		}
	}
	_, a = advance(t, s, input(25, 10, j), p)
	if len(a) == 0 || a[len(a)-1].Kind != RequestPause || a[len(a)-1].Job != id(j) {
		t.Fatalf("replacement did not earn exact pause: %+v", a)
	}
}

func TestWarningCapRetainsNewRecipientNotice(t *testing.T) {
	p, j := policy(), owned()
	p.Resources[CPU] = Threshold{EnterBelow: 20, ExitAbove: 40}
	var s State
	operator, original := 0, 0
	for _, sec := range []int{0, 5, 8, 12, 16, 20} {
		var actions []Action
		s, actions = advance(t, s, input(sec, 10, j), p)
		for _, a := range actions {
			if a.Kind == WarnOperator {
				operator++
			}
			if a.Kind == WarnAgent {
				original++
			}
		}
	}
	if operator != 3 || original != 3 {
		t.Fatalf("repeated warnings exceeded cap: operator=%d agent=%d", operator, original)
	}
	j.Generation = "replacement"
	replacement := 0
	for _, sec := range []int{24, 28, 32} {
		var actions []Action
		s, actions = advance(t, s, input(sec, 10, j), p)
		for _, a := range actions {
			if a.Kind == WarnOperator {
				operator++
			}
			if a.Kind == WarnAgent && a.Job == id(j) {
				replacement++
			}
		}
	}
	if operator != 3 || replacement != 2 {
		t.Fatalf("new recipient notices: operator=%d replacement=%d", operator, replacement)
	}
}

func TestBareSharedAndStop(t *testing.T) {
	p := policy()
	bare := owned()
	bare.Supervised = false
	bare.ExecutionID = ""
	bare.PauseCapability = false
	shared := owned()
	shared.ExecutionID = "service"
	shared.PauseAllowed = false
	var s State
	for _, sec := range []int{0, 5, 8} {
		s, _ = advance(t, s, input(sec, 10, bare, shared), p)
	}
	in := input(15, 10, bare, shared)
	in.Stopping = true
	_, a := advance(t, s, in, p)
	for _, x := range a {
		if x.Kind == RequestPause {
			t.Fatalf("unsafe pause: %+v", x)
		}
	}
	if len(s.Pauses) != 0 {
		t.Fatal("unsafe pause")
	}
}

func TestClockRollbackAndEpochChange(t *testing.T) {
	p, j := policy(), owned()
	s := reachPause(t, p, j)
	j.Paused = true
	in := input(20, 50, j)
	in.Now = base.Add(14 * time.Second)
	next, a := advance(t, s, in, p)
	wantKinds(t, a)
	if next.LastTick != s.LastTick {
		t.Fatal("rollback advanced state")
	}
	in = input(21, 50, j)
	in.Tick = 14 * time.Second
	next, a = advance(t, s, in, p)
	wantKinds(t, a)
	if next.LastTick != s.LastTick {
		t.Fatal("tick rollback advanced state")
	}
	in = input(21, 50, j)
	in.Epoch = "boot-2"
	pause := pauseFromState(s, j)
	next, a = advance(t, s, in, p)
	wantKinds(t, a, RequestReconciliation)
	if len(next.Pauses) != 0 || len(next.Resources) != 0 || len(next.Unresolved) != 1 || a[0].OriginalEpoch != "boot-1" || next.ControlRevision != s.ControlRevision {
		t.Fatal("new epoch lost obligation or adopted old execution")
	}
	if ControlAllowed(next, pause, in, p) {
		t.Fatal("old epoch signal remained authorized")
	}
	oldKey := s.Pauses[id(j)].PauseKey
	in = input(22, 50, j)
	in.Epoch = "boot-3"
	next, a = advance(t, next, in, p)
	wantKinds(t, a)
	if len(next.Unresolved) != 1 {
		t.Fatal("second restart lost unresolved obligation")
	}
	in = input(23, 50, j)
	in.Epoch = "boot-3"
	in.Reconciled = []string{oldKey}
	next, a = advance(t, next, in, p)
	wantKinds(t, a)
	if len(next.Unresolved) != 0 {
		t.Fatal("provider reconciliation did not settle obligation")
	}
}

func TestMaxPauseEscalatesOnceWithoutUnsafeResume(t *testing.T) {
	p, j := policy(), owned()
	s := reachPause(t, p, j)
	j.Paused = true
	in := input(16, 10, j)
	in.Results = []ControlResult{resultFor(s, j, true)}
	s, _ = advance(t, s, in, p)
	in = input(46, 10, j) // gap also invalidates sustained pressure
	s, a := advance(t, s, in, p)
	wantKinds(t, a, PauseLimitReached)
	_, a = advance(t, s, input(47, 10, j), p)
	wantKinds(t, a)
}

func TestUnknownRecoveryAndInvalidDuplicate(t *testing.T) {
	p, j := policy(), owned()
	s := reachPause(t, p, j)
	j.Paused = true
	in := input(16, 50, j)
	in.Results = []ControlResult{resultFor(s, j, true)}
	s, _ = advance(t, s, in, p)
	in = input(20, 50, j)
	in.Samples[CPU] = Sample{}
	s, a := advance(t, s, in, p)
	wantKinds(t, a)
	if len(s.Pauses) != 1 {
		t.Fatal("unknown sample released pause")
	}
	s, _ = advance(t, s, input(21, 50, j), p)
	_, a = advance(t, s, input(25, 50, j), p)
	wantKinds(t, a, RequestResume)
	in = input(26, 50, j, j)
	if _, _, err := Advance(s, in, p); err == nil {
		t.Fatal("duplicate job identity accepted")
	}
}

func TestRemovingResourcePolicyDoesNotProveRecovery(t *testing.T) {
	p, j := policy(), owned()
	s := reachPause(t, p, j)
	j.Paused = true
	p.Resources = map[Resource]Threshold{Memory: {EnterBelow: 100, ExitAbove: 200}}
	in := input(16, 50, j)
	in.Results = []ControlResult{resultFor(s, j, true)}
	in.Samples = map[Resource]Sample{Memory: observed(in, 300)}
	s, _ = advance(t, s, in, p)
	in = input(20, 50, j)
	in.Samples = map[Resource]Sample{Memory: observed(in, 300)}
	s, a := advance(t, s, in, p)
	for _, x := range a {
		if x.Kind == RequestResume {
			t.Fatalf("disabled CPU policy manufactured recovery: %+v", x)
		}
	}
	if len(s.Pauses) != 1 {
		t.Fatal("paused job lost on policy change")
	}
}

func TestDelayedPauseAfterRecoveryIsFencedAndReconciled(t *testing.T) {
	p, j := policy(), owned()
	s := reachPause(t, p, j)
	pause := pauseFromState(s, j)
	if !ControlAllowed(s, pause, input(15, 10, j), p) {
		t.Fatal("fresh exact pause was refused")
	}
	s, _ = advance(t, s, input(16, 50, j), p)
	s, a := advance(t, s, input(20, 50, j), p)
	wantKinds(t, a, CancelPause)
	if s.Pauses[id(j)].Phase != CancelPending || s.Pauses[id(j)].Revision == pause.Revision {
		t.Fatalf("pending pause not fenced: %+v", s.Pauses[id(j)])
	}
	if ControlAllowed(s, pause, input(20, 10, j), p) {
		t.Fatal("old outbox pause still authorized")
	}
	// A completion already in flight can still report that it applied. Keep
	// ownership of the control record and ask for resume only after MinPause.
	j.Paused = true
	in := input(21, 50, j)
	in.Results = []ControlResult{{Key: pause.Key, Applied: true}}
	s, a = advance(t, s, in, p)
	wantKinds(t, a)
	if s.Pauses[id(j)].Phase != PauseApplied {
		t.Fatalf("late pause completion lost: %+v", s.Pauses[id(j)])
	}
	s, a = advance(t, s, input(24, 50, j), p)
	wantKinds(t, a, RequestResume)
	if s.Pauses[id(j)].Phase != ResumePending {
		t.Fatal("resume not retained pending completion")
	}
}

func TestFailedResumeRetainsRecordAndFencesRetries(t *testing.T) {
	p, j := policy(), owned()
	s := reachPause(t, p, j)
	j.Paused = true
	in := input(16, 10, j)
	in.Results = []ControlResult{resultFor(s, j, true)}
	s, _ = advance(t, s, in, p)
	s, _ = advance(t, s, input(17, 50, j), p)
	s, a := advance(t, s, input(21, 50, j), p)
	wantKinds(t, a, RequestResume)
	resume := a[0]
	if !ControlAllowed(s, resume, input(21, 50, j), p) {
		t.Fatal("fresh exact resume was refused")
	}
	in = input(22, 50, j)
	in.Results = []ControlResult{{Key: resume.Key, Applied: false}}
	s, a = advance(t, s, in, p)
	wantKinds(t, a, ControlFailed)
	if s.Pauses[id(j)].Phase != ResumeFailed || ControlAllowed(s, resume, input(22, 50, j), p) {
		t.Fatal("failed resume was forgotten or remained authorized")
	}
	_, a = advance(t, s, in, p)
	wantKinds(t, a)
}

func TestAppliedPauseCapabilityLossRetainsRecord(t *testing.T) {
	p, j := policy(), owned()
	s := reachPause(t, p, j)
	j.Paused = true
	in := input(16, 10, j)
	in.Results = []ControlResult{resultFor(s, j, true)}
	s, _ = advance(t, s, in, p)
	j.PauseAllowed = false
	s, a := advance(t, s, input(17, 50, j), p)
	wantKinds(t, a, ControlFailed)
	if len(s.Pauses) != 1 || s.Pauses[id(j)].Phase != NeedsAttention {
		t.Fatalf("lost paused obligation: %+v", s.Pauses)
	}
}

func TestPendingTimeoutAndStaleResults(t *testing.T) {
	p, j := policy(), owned()
	s := reachPause(t, p, j)
	oldKey := s.Pauses[id(j)].PauseKey
	s, a := advance(t, s, input(27, 10, j), p)
	wantKinds(t, a, CancelPause, ControlFailed)
	if s.Pauses[id(j)].Phase != NeedsAttention || len(s.Pauses) != 1 {
		t.Fatalf("pending timeout lost record: %+v", s.Pauses)
	}
	in := input(28, 10, j)
	in.Results = []ControlResult{{Key: "stale-foreign-key", Applied: true}}
	s, a = advance(t, s, in, p)
	wantKinds(t, a)
	if len(s.Pauses) != 1 {
		t.Fatal("stale result settled pending control")
	}
	in = input(29, 10, j)
	in.Results = []ControlResult{{Key: oldKey, Applied: false}}
	s, a = advance(t, s, in, p)
	wantKinds(t, a)
	if len(s.Pauses) != 0 {
		t.Fatal("confirmed no-effect pause retained record")
	}
}

func TestWallStallDoesNotRefreshOldSample(t *testing.T) {
	p, j := policy(), owned()
	s, _ := advance(t, State{}, input(0, 10, j), p)
	in := input(5, 10, j)
	in.Now = base
	in.Samples[CPU] = Sample{Known: true, ObservedAt: base, ObservedTick: 0, Epoch: "boot-1", Available: 10}
	s, a := advance(t, s, in, p)
	wantKinds(t, a)
	if s.Resources[CPU].Warned {
		t.Fatal("wall-clock stall refreshed old observation")
	}
}

func TestResumePendingTimeoutKeepsUnresolvedPause(t *testing.T) {
	p, j := policy(), owned()
	s := reachPause(t, p, j)
	j.Paused = true
	in := input(16, 10, j)
	in.Results = []ControlResult{resultFor(s, j, true)}
	s, _ = advance(t, s, in, p)
	s, _ = advance(t, s, input(17, 50, j), p)
	s, a := advance(t, s, input(21, 50, j), p)
	wantKinds(t, a, RequestResume)
	resume := a[0]
	s, a = advance(t, s, input(34, 50, j), p)
	wantKinds(t, a, ControlFailed)
	if s.Pauses[id(j)].Phase != ResumeFailed || len(s.Pauses) != 1 {
		t.Fatalf("timed-out resume was forgotten: %+v", s.Pauses)
	}
	if ControlAllowed(s, resume, input(34, 50, j), p) {
		t.Fatal("timed-out resume still authorized")
	}
	j.Paused = false
	in = input(35, 50, j)
	in.Results = []ControlResult{{Key: resume.Key, Applied: true}}
	s, a = advance(t, s, in, p)
	wantKinds(t, a)
	if len(s.Pauses) != 0 {
		t.Fatal("late confirmed resume did not settle record")
	}
}

func TestNativeFenceRejectsChangedAuthorityAndEvidence(t *testing.T) {
	p, j := policy(), owned()
	s := reachPause(t, p, j)
	pause := pauseFromState(s, j)
	for name, change := range map[string]func(*Input){
		"ownership lost":  func(in *Input) { in.Jobs[0].StillOwned = false },
		"capability lost": func(in *Input) { in.Jobs[0].PauseCapability = false },
		"expired":         func(in *Input) { in.Jobs[0].ExpiresAt = in.Now },
		"stop":            func(in *Input) { in.Stopping = true },
		"new epoch":       func(in *Input) { in.Epoch = "boot-2" },
		"stale sample":    func(in *Input) { sample := in.Samples[CPU]; sample.ObservedTick = 0; in.Samples[CPU] = sample },
	} {
		t.Run(name, func(t *testing.T) {
			in := input(15, 10, j)
			change(&in)
			if ControlAllowed(s, pause, in, p) {
				t.Fatal("unsafe native apply authorized")
			}
		})
	}
}

func TestOneReusedFreshSampleCannotProveWindows(t *testing.T) {
	p, j := policy(), owned()
	p.MaxAge = 30 * time.Second
	p.MaxSampleGap = 30 * time.Second
	first := input(0, 10, j)
	s, _ := advance(t, State{}, first, p)
	for _, sec := range []int{5, 8, 15} {
		in := input(sec, 10, j)
		in.Samples[CPU] = first.Samples[CPU]
		var a []Action
		s, a = advance(t, s, in, p)
		wantKinds(t, a)
	}
	if s.Resources[CPU].Warned || len(s.Pauses) != 0 {
		t.Fatal("one reused low sample proved pressure")
	}
	// A new high sample starts recovery; replaying it cannot complete the
	// hysteresis window even under a deliberately long freshness policy.
	in := input(16, 50, j)
	s, _ = advance(t, s, in, p)
	firstHigh := in.Samples[CPU]
	in = input(24, 50, j)
	in.Samples[CPU] = firstHigh
	s, _ = advance(t, s, in, p)
	if !s.Resources[CPU].Active {
		t.Fatal("one reused high sample proved recovery")
	}
}

func TestBoundedInputAndDuplicateResults(t *testing.T) {
	p, j := policy(), owned()
	in := input(0, 10, j)
	in.Results = []ControlResult{{Key: "x"}, {Key: "x"}}
	if _, _, err := Advance(State{}, in, p); err == nil {
		t.Fatal("duplicate result accepted")
	}
	in = input(0, 10, j)
	p.MaxJobs = 257
	if _, _, err := Advance(State{}, in, p); err == nil {
		t.Fatal("unbounded roster policy accepted")
	}
}

func TestRepeatedProviderResultDoesNotMoveAppliedClock(t *testing.T) {
	p, j := policy(), owned()
	s := reachPause(t, p, j)
	j.Paused = true
	completion := resultFor(s, j, true)
	in := input(16, 10, j)
	in.Results = []ControlResult{completion}
	s, _ = advance(t, s, in, p)
	in = input(18, 10, j)
	in.Results = []ControlResult{completion}
	s, a := advance(t, s, in, p)
	for _, x := range a {
		if x.Kind == RequestResume || x.Kind == RequestPause {
			t.Fatalf("duplicate completion created control: %+v", x)
		}
	}
	if s.Pauses[id(j)].AppliedAt != 16*time.Second {
		t.Fatal("duplicate completion moved applied clock")
	}
}

func TestPolicyChangeFencesPendingPauseAndResume(t *testing.T) {
	p, j := policy(), owned()
	s := reachPause(t, p, j)
	pause := pauseFromState(s, j)
	p.Grace = 100 * time.Second
	if ControlAllowed(s, pause, input(15, 10, j), p) {
		t.Fatal("longer grace did not fence pending pause")
	}
	s, a := advance(t, s, input(16, 10, j), p)
	wantKinds(t, a, CancelPause)
	if s.Pauses[id(j)].Phase != CancelPending {
		t.Fatal("policy change kept pause pending")
	}

	p, j = policy(), owned()
	s = reachPause(t, p, j)
	j.Paused = true
	in := input(16, 10, j)
	in.Results = []ControlResult{resultFor(s, j, true)}
	s, _ = advance(t, s, in, p)
	s, _ = advance(t, s, input(17, 50, j), p)
	s, a = advance(t, s, input(21, 50, j), p)
	wantKinds(t, a, RequestResume)
	resume := a[0]
	p.MinPause = 100 * time.Second
	if ControlAllowed(s, resume, input(21, 50, j), p) {
		t.Fatal("longer minimum pause did not fence pending resume")
	}
	s, a = advance(t, s, input(22, 50, j), p)
	wantKinds(t, a, ControlFailed)
	if s.Pauses[id(j)].Phase != ResumeFailed {
		t.Fatal("policy change forgot unresolved resume")
	}
}

func TestEpochTransitionAcceptsExactSameStepReconciliation(t *testing.T) {
	p, j := policy(), owned()
	s := reachPause(t, p, j)
	in := input(21, 50, j)
	in.Epoch = "boot-2"
	in.Reconciled = []string{s.Pauses[id(j)].PauseKey}
	next, a := advance(t, s, in, p)
	wantKinds(t, a)
	if len(next.Unresolved) != 0 || len(next.Pauses) != 0 {
		t.Fatal("settled old control survived restart")
	}
}

func TestAppliedPauseSurvivesRebootAndBlocksNewControl(t *testing.T) {
	p, j := policy(), owned()
	s := reachPause(t, p, j)
	j.Paused = true
	in := input(16, 10, j)
	in.Results = []ControlResult{resultFor(s, j, true)}
	s, _ = advance(t, s, in, p)
	in = input(21, 10, j)
	in.Epoch, in.Tick = "boot-2", 0
	in.Samples[CPU] = observed(in, 10)
	s, a := advance(t, s, in, p)
	wantKinds(t, a, RequestReconciliation)
	if len(s.Unresolved) != 1 {
		t.Fatal("reboot did not retain applied pause")
	}
	for _, obligation := range s.Unresolved {
		if obligation.Record.Phase != PauseApplied {
			t.Fatal("applied pause state lost at reboot")
		}
	}
	newJob := owned()
	newJob.Generation = "generation-2"
	for _, sec := range []int{0, 5, 8, 15} {
		step := input(sec, 10, newJob)
		step.Epoch = "boot-2"
		step.Now = base.Add(21*time.Second + time.Duration(sec)*time.Second)
		step.Samples[CPU] = observed(step, 10)
		s, a = advance(t, s, step, p)
		for _, x := range a {
			if x.Kind == RequestPause {
				t.Fatal("new pause ignored unresolved old execution")
			}
		}
	}
	if len(s.Unresolved) != 1 {
		t.Fatal("old obligation disappeared during new pressure")
	}
}

func TestInputIdentityBoundsAndUTF8(t *testing.T) {
	p, j := policy(), owned()
	for name, mutate := range map[string]func(*Input){
		"invalid job UTF8": func(in *Input) { in.Jobs[0].LeaseID = string([]byte{0xff}) },
		"long agent":       func(in *Input) { in.Jobs[0].AgentID = strings.Repeat("a", maxAgentBytes+1) },
		"long epoch":       func(in *Input) { in.Epoch = strings.Repeat("e", maxEpochBytes+1) },
		"long result key":  func(in *Input) { in.Results = []ControlResult{{Key: strings.Repeat("k", maxKeyBytes+1)}} },
	} {
		t.Run(name, func(t *testing.T) {
			in := input(0, 10, j)
			mutate(&in)
			if _, _, err := Advance(State{}, in, p); err == nil {
				t.Fatal("invalid identity accepted")
			}
		})
	}
}
