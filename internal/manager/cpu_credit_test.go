package manager

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/observe"
)

// Native interval/ancestry accuracy has separate collector tests. These tests
// control only the resulting candidate, retaining a real original owned job,
// its durable lease and the actual Manager decision/cleanup paths.
func cpuCreditFixture(t *testing.T) (*Manager, model.Lease, *job) {
	t.Helper()
	m := newManager(t)
	m.capacity.CPU = 4
	cpuReading(m, 4, 4, 0, 4, 0)
	r := request("credit-original-run")
	l, err := m.Acquire(r, &RunRequest{Request: r, Command: []string{"/bin/sleep", "30"}, CWD: filepath.Dir(m.home)})
	if err != nil {
		t.Fatal(err)
	}
	j := m.jobs[l.ID]
	if j == nil || j.identity == nil || l.Process == nil || *j.identity != *l.Process {
		t.Fatal("fixture lacks original job/native identity", l)
	}
	// A controlled compatible native accounting scope is an input here, even
	// on platforms where the production collector deliberately earns no credit.
	if j.cpuAnchor == "" {
		j.cpuAnchor = "controlled-original-native-cookie"
	}
	return m, l, j
}

func setCPUCredit(m *Manager, l model.Lease, j *job, cores float64) {
	m.observationMu.Lock()
	m.cpuCredits = []cpuCreditSample{{id: l.ID, job: j, identity: *j.identity, cookie: j.cpuAnchor, cores: cores}}
	m.observationMu.Unlock()
}

func closeCPUValue(a, b float64) bool { return math.Abs(a-b) < 1e-12 }

func TestManagedCPUCreditUsesOwnedConsumptionOnceForRenewalAndAdmission(t *testing.T) {
	m, l, j := cpuCreditFixture(t)
	cpuReading(m, 4, 4, 3.5, .5, 0)
	setCPUCredit(m, l, j, 1)
	s, err := m.Status()
	if err != nil || !s.CPU.Known || !closeCPUValue(s.CPU.ManagedCredit, 1) || !closeCPUValue(s.CPU.Capacity, 1.5) || !closeCPUValue(s.CPU.Available, .5) || s.CPU.Reserved != 1 {
		t.Fatal("owned use was not credited against its own full promise", s.CPU, err)
	}
	if _, err := m.Renew(l.ID, 2*time.Minute); err != nil {
		t.Fatal("self counted twice in renewal", err)
	}
	r := request("credit-spare-headroom")
	r.Resources.CPU = .5
	if _, err := m.Acquire(r, nil); err != nil {
		t.Fatal("verified spare headroom unavailable", err)
	}
	r.Key, r.Resources.CPU = "credit-not-spent-twice", .01
	if _, err := m.Acquire(r, nil); errorCode(err) != "insufficient_capacity" {
		t.Fatal("credit spent more than once", err)
	}
}

func TestManagedCPUCreditCannotBorrowIdleLeasePromises(t *testing.T) {
	m, l, j := cpuCreditFixture(t)
	r := request("credit-other-idle-promise")
	r.Resources.CPU = 2
	if _, err := m.Acquire(r, nil); err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 4, 4, 3.5, .5, 0)
	setCPUCredit(m, l, j, 3)
	s, err := m.Status()
	if err != nil || s.CPU.Reserved != 3 || !closeCPUValue(s.CPU.ManagedCredit, 1) || !closeCPUValue(s.CPU.Capacity, 1.5) || !closeCPUValue(s.CPU.Deficit, 1.5) {
		t.Fatal("busy job borrowed the other lease's unused promise", s.CPU, err)
	}
	if _, err := m.Renew(l.ID, 2*time.Minute); errorCode(err) != "renewal_denied" {
		t.Fatal("deficit renewal used excess credit", err)
	}
}

func TestManagedCPUCreditInvalidatesOwnershipAndUncertainEvidence(t *testing.T) {
	for _, change := range []string{"generation", "cookie", "wait-returned", "lease-process", "lease-terminal", "stale", "unknown", "nan", "infinity", "negative"} {
		t.Run(change, func(t *testing.T) {
			m, l, j := cpuCreditFixture(t)
			cpuReading(m, 4, 4, 4, 0, 0)
			setCPUCredit(m, l, j, 1)
			switch change {
			case "generation":
				m.jobs[l.ID] = &job{cmd: j.cmd, done: make(chan struct{}), identity: j.identity, cpuAnchor: j.cpuAnchor}
				defer func() { m.jobs[l.ID] = j }()
			case "cookie":
				old := j.cpuAnchor
				j.cpuAnchor = "replacement-native-cookie"
				defer func() { j.cpuAnchor = old }()
			case "wait-returned":
				j.cpuExited.Store(true)
				defer j.cpuExited.Store(false)
			case "lease-process", "lease-terminal":
				changed := l
				if change == "lease-process" {
					p := *l.Process
					p.Start += ":foreign"
					changed.Process = &p
				} else {
					changed.State = "terminal"
				}
				if err := m.put(changed, "fixture.changed"); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := m.put(l, "fixture.restored"); err != nil {
						t.Error(err)
					}
				}()
			case "stale":
				cpuReading(m, 4, 4, 4, 0, cpuStaleAfter+time.Second)
			case "unknown":
				m.cpuAvailability.Known = false
			case "nan":
				setCPUCredit(m, l, j, math.NaN())
			case "infinity":
				setCPUCredit(m, l, j, math.Inf(1))
			case "negative":
				setCPUCredit(m, l, j, -1)
			}
			s, err := m.Status()
			if err != nil || s.CPU.ManagedCredit != 0 || s.CPU.Capacity != 0 {
				t.Fatal("invalid credit authorized capacity", s.CPU, err)
			}
		})
	}
}

func TestManagedCPUCreditDisappearsAfterOriginalWorkerJoins(t *testing.T) {
	m, l, j := cpuCreditFixture(t)
	cpuReading(m, 4, 4, 4, 0, 0)
	setCPUCredit(m, l, j, 1)
	if _, err := m.End(l.ID, "released"); err != nil {
		t.Fatal(err)
	}
	// Release can remain ending until the real original process/group joins.
	// Its outstanding promise is retained during that interval.
	select {
	case <-j.done:
	case <-time.After(6 * time.Second):
		t.Fatal("original worker did not join")
	}
	s, err := m.Status()
	if err != nil || s.CPU.Reserved != 0 || s.CPU.ManagedCredit != 0 || s.CPU.Capacity != 0 {
		t.Fatal("retired job left usable credit", s.CPU, err)
	}
	r := request("credit-after-release")
	r.Resources.CPU = .1
	if _, err := m.Acquire(r, nil); errorCode(err) != "insufficient_capacity" {
		t.Fatal("new claim borrowed stale retired credit", err)
	}
}

func TestManagedCPUCreditBindingRejectsDuplicateAndChangedNativeOwners(t *testing.T) {
	m, l, j := cpuCreditFixture(t)
	c := observe.CPUOwnerCredit{Token: l.ID, Root: *j.identity, Cookie: j.cpuAnchor, Cores: 1, MatchedProcesses: 1}
	jobs := map[string]*job{l.ID: j}
	for _, frame := range []observe.CPUCreditFrame{
		{Eligible: false, Credits: []observe.CPUOwnerCredit{c}},
		{Eligible: true, Credits: []observe.CPUOwnerCredit{c, c}},
	} {
		if got := bindCPUCredits(frame, jobs); len(got) != 0 {
			t.Fatal("ambiguous/unsupported frame got credit", got)
		}
	}
	c.Cookie += ":replaced"
	if got := bindCPUCredits(observe.CPUCreditFrame{Eligible: true, Credits: []observe.CPUOwnerCredit{c}}, jobs); len(got) != 0 {
		t.Fatal("changed native generation got credit", got)
	}
	_ = m
}
