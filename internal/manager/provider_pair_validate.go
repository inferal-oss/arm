package manager

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/model"
	"getarm.org/internal/providerpreparation"
)

func providerPairSHA(raw []byte) string { s := sha256.Sum256(raw); return hex.EncodeToString(s[:]) }
func providerPairFramedSHA(domain string, values ...[]byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(domain))
	var n [8]byte
	for _, v := range values {
		binary.BigEndian.PutUint64(n[:], uint64(len(v)))
		_, _ = h.Write(n[:])
		_, _ = h.Write(v)
	}
	return hex.EncodeToString(h.Sum(nil))
}
func providerPairClockSHA(raw []byte) (string, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields["elapsed_deadline"] == nil || fields["expires_at"] == nil {
		return "", fmt.Errorf("protected raw clock values required")
	}
	return providerPairFramedSHA("provider-preparing-clock-v1\x00", fields["elapsed_deadline"], fields["expires_at"]), nil
}

// C1 source candidate, not an activated format commitment. Ordered member bytes
// and this ancestor's identity/revision are framed independently; equal decoded
// sidecars cannot replace different protected original serializations.
func providerPairAncestorSHA(budget, revision string, worker, temp []byte) string {
	return providerPairFramedSHA("provider-preparing-parent-budget-v1\x00", []byte(budget), []byte(revision), worker, temp)
}

type providerPairDecoded struct {
	initial                 providerpreparation.Initial
	progress                providerpreparation.Progress
	rawInitial, rawProgress []byte
	rows                    [2]providerPairLeaseRow
	leases                  [2]providerPairLeaseWire
	budgets                 [2]providerPairBudgetWire
	clocks                  [2]leaseclock.Deadline
}

func decodeProviderPairInitial(raw []byte) (providerpreparation.Initial, error) {
	var v providerpreparation.Initial
	if e := decodeProviderPairJSON(raw, &v, providerpreparation.MaxInitialBytes, providerpreparation.MaxEnvelopeDepth, true); e != nil {
		return v, e
	}
	if v.Schema != 1 || !providerPairHexID(string(v.Pair)) || !providerPairRequestKey(string(v.OperationKey)) || !providerPairHexID(string(v.Authority)) || !providerPairUID(string(v.Owner)) || !providerPairHexID(string(v.OpenEpoch)) || v.Profile != providerpreparation.InertProfile || v.LifetimeNS != "300000000000" || v.DiskScope != "state-filesystem" || !providerPairStamp(string(v.Created)) {
		return v, fmt.Errorf("inert pair initial domain")
	}
	for i, m := range v.Members {
		want := []providerpreparation.Role{providerpreparation.Worker, providerpreparation.Temp}[i]
		if m.Role != want || !providerPairHexID(string(m.Row.ID)) || !providerPairRequestKey(string(m.Row.RequestKey)) || m.Row.RunKind != "sql-null" || m.Row.Revision != "1" || !providerPairDigest(string(m.Row.DataSHA256)) || !providerPairDigest(string(m.Row.BudgetSHA256)) || !providerPairDigest(string(m.Row.ClockSHA256)) || len(m.LeaseBytes) == 0 || len(m.BudgetBytes) == 0 || providerPairSHA(m.LeaseBytes) != string(m.Row.DataSHA256) || providerPairSHA(m.BudgetBytes) != string(m.Row.BudgetSHA256) {
			return v, fmt.Errorf("inert pair original member domain/hash")
		}
	}
	if v.Members[0].Row.ID == v.Members[1].Row.ID || v.Members[0].Row.RequestKey == v.Members[1].Row.RequestKey {
		return v, fmt.Errorf("aliased pair member")
	}
	seen := map[string]bool{}
	tail := false
	count := 0
	for _, p := range v.Parents {
		if p == nil {
			tail = true
			continue
		}
		if tail || !providerPairRequestKey(string(p.Budget)) || seen[string(p.Budget)] || !providerPairDigest(string(p.Binding)) {
			return v, fmt.Errorf("pair ancestry order/identity")
		}
		if _, e := providerPairDecimal(string(p.Revision), true); e != nil {
			return v, e
		}
		if p.Amounts.CPUMillis != "1000" || p.Amounts.MemoryBytes != "67108864" || p.Amounts.DiskBytes != "67108864" {
			return v, fmt.Errorf("pair ancestor quantities")
		}
		seen[string(p.Budget)] = true
		count++
	}
	if count == 0 {
		return v, fmt.Errorf("pair ancestry required")
	}
	return v, nil
}

func decodeProviderPairMember(row providerPairLeaseRow, initial providerpreparation.Initial, index int) (providerPairLeaseWire, providerPairBudgetWire, error) {
	var l providerPairLeaseWire
	var b providerPairBudgetWire
	if !row.bounded() || index < 0 || index > 1 {
		return l, b, fmt.Errorf("bounded member required")
	}
	expected := initial.Members[index]
	if row.id != string(expected.Row.ID) || row.requestKey != string(expected.Row.RequestKey) || !bytes.Equal(row.lease, expected.LeaseBytes) || !bytes.Equal(row.budget, expected.BudgetBytes) {
		return l, b, fmt.Errorf("protected original bytes differ")
	}
	if e := decodeProviderPairJSON(row.lease, &l, providerpreparation.MaxLeaseBytes, providerpreparation.MaxLeaseDepth, false); e != nil {
		return l, b, e
	}
	if e := decodeProviderPairJSON(row.budget, &b, providerpreparation.MaxBudgetBytes, providerpreparation.MaxLeaseDepth, false); e != nil {
		return l, b, e
	}
	kind := "provider-prepare-worker"
	resources := providerPairResourcesWire{CPU: 1, Memory: 64 << 20}
	if index == 1 {
		kind = "storage"
		resources = providerPairResourcesWire{Disk: 64 << 20}
	}
	if l.ID != row.id || l.Host != string(initial.Authority) || l.Owner != string(initial.Owner) || l.Revision != 1 || l.State != "provisioning" || l.Ref.Schema != 1 || l.Ref.Pair != initial.Pair || l.Ref.Role != expected.Role || l.Ref.Generation != "1" || len(l.Capabilities) != 0 || !budgetText(l.Username, 1024, false) || l.Created.UTC().Format(time.RFC3339Nano) != string(initial.Created) {
		return l, b, fmt.Errorf("paired lease identity/state/ref")
	}
	q := l.Request
	if q.Host != l.Host || q.Key != row.requestKey || q.Kind != kind || q.TTL != time.Duration(providerpreparation.LifetimeNS) || q.Queue || q.Resources != resources || !budgetText(q.Reason, 4096, true) || !budgetText(q.Project, 1024, false) || !budgetText(q.Agent, 1024, false) {
		return l, b, fmt.Errorf("paired request shape/quantities")
	}
	clockHash, e := providerPairClockSHA(row.lease)
	if e != nil || clockHash != string(expected.Row.ClockSHA256) {
		return l, b, fmt.Errorf("raw original clock hash")
	}
	d := l.Clock
	if d.Version != 1 || d.Boot == "" || len(d.Boot) > 256 || d.Anchor < 0 || d.End <= d.Anchor || d.End-d.Anchor != providerpreparation.LifetimeNS || !d.Wall.Equal(l.Expires) || !l.Created.Add(time.Duration(providerpreparation.LifetimeNS)).Equal(d.Wall) {
		return l, b, fmt.Errorf("original deadline/audit binding")
	}
	if b.Authority != l.Host || b.Owner != l.Owner || len(b.Path) == 0 || len(b.Path) > providerpreparation.MaxParentCharges || b.Selected != b.Path[len(b.Path)-1] || b.Path[0] != ownerBudgetID(l.Host, l.Owner) || !reflect.DeepEqual(l.Budget, b) || len(l.Policies) != len(b.Path) {
		return l, b, fmt.Errorf("original budget sidecar binding")
	}
	if q.Budget != "" && q.Budget != b.Selected {
		return l, b, fmt.Errorf("request budget selector differs")
	}
	if q.Budget == "" && b.Selected != ownerBudgetID(l.Host, l.Owner) {
		return l, b, fmt.Errorf("implicit selector differs from root")
	}
	seen := map[string]bool{}
	for i, id := range b.Path {
		if !providerPairRequestKey(id) || seen[id] || l.Policies[i].ID != id || l.Policies[i].Revision <= 0 {
			return l, b, fmt.Errorf("original budget policy ancestry")
		}
		limits := l.Policies[i].Limits
		if e := validBudgetLimits(model.BudgetLimits{CPU: limits.CPU, Memory: limits.Memory, Disk: limits.Disk, Pools: limits.Pools}); e != nil {
			return l, b, e
		}
		seen[id] = true
	}
	return l, b, nil
}

func validateProviderPairDecoded(p *providerPairDecoded) error {
	for i := range p.rows {
		l, b, e := decodeProviderPairMember(p.rows[i], p.initial, i)
		if e != nil {
			return e
		}
		p.leases[i], p.budgets[i], p.clocks[i] = l, b, l.Clock
	}
	a, b := p.budgets[0], p.budgets[1]
	if !reflect.DeepEqual(a.Path, b.Path) || a.Selected != b.Selected || !reflect.DeepEqual(p.leases[0].Policies, p.leases[1].Policies) || p.clocks[0] != p.clocks[1] {
		return fmt.Errorf("paired ancestry/policy/original clock differs")
	}
	for i := 0; i < providerpreparation.MaxParentCharges; i++ {
		parent := p.initial.Parents[i]
		if i >= len(a.Path) {
			if parent != nil {
				return fmt.Errorf("unexpected ancestor")
			}
			continue
		}
		if parent == nil || string(parent.Budget) != a.Path[i] || string(parent.Revision) != fmt.Sprint(p.leases[0].Policies[i].Revision) || string(parent.Binding) != providerPairAncestorSHA(string(parent.Budget), string(parent.Revision), p.rows[0].budget, p.rows[1].budget) {
			return fmt.Errorf("parent role/revision/raw-sidecar hash")
		}
	}
	return nil
}
