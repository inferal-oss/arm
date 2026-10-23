package sharelease

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sharecodec"
)

func evidenceFixture() (Publication, Consumer, Fence) {
	at := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	p := Publication{Schema: 2, Authority: strings.Repeat("a", 32), Owner: "501", ID: strings.Repeat("b", 32), Generation: strings.Repeat("c", 32), Request: PrepareRequest{MemoryPolicy: PreparationMemory().Policy, Key: "prepare-1", Storage: strings.Repeat("d", 32), StorageRevision: 1, Reason: "publish an immutable input", TTL: time.Hour, Resources: model.Resources{CPU: 1, Memory: PreparationMemory().Total}}, Lease: strings.Repeat("e", 32), StorageIdentity: model.FileIdentity{Device: 1, Inode: 3, Owner: 501, BornSeconds: 1, BirthKnown: true}, Worker: Worker{PID: 123, PGID: 0, UID: "501", Start: "native-start", Namespace: "private-machine"}, WorkerState: "reserved", State: "preparing", Revision: 1, Created: at, Updated: at, Expires: at.Add(time.Hour), Reason: "publish an immutable input", JournalSlots: PublicationFinalSlots}
	p.PreparationWorker = p.Worker
	p.WorkerPhase = "preparation"
	q := Control{Schema: 1, Source: strings.Repeat("f", 32), SourceOwner: "502", Target: p.Authority, TargetOwner: p.Owner, Pair: strings.Repeat("1", 64), Revision: 1, Certificate: strings.Repeat("2", 64), Share: p.ID, Generation: p.Generation, ManifestSHA256: strings.Repeat("3", 64), Key: "read-key", Purpose: "file-read", Path: "dir/file", Reason: "read exact published input", TTL: time.Minute, Resources: model.Resources{CPU: 1, Memory: sharecodec.AdmissionMemory().Session}}
	a := Admission{Schema: 1, Control: q, Lease: strings.Repeat("4", 32), At: at.Add(time.Second), Expires: at.Add(time.Minute), Worker: p.Worker}
	c := Consumer{Schema: 1, Admission: a, State: "admitted", WorkerState: "reserved", Revision: 1, Updated: a.At, Reason: q.Reason, JournalSlots: ConsumerFinalSlots}
	digest, _ := q.Digest()
	ah, _ := hash(a)
	f := Fence{Schema: 1, Control: q, ControlSHA256: digest, State: "admitted", Lease: a.Lease, AdmissionSHA256: ah, At: a.At, Actor: "peer:" + q.Source, Reason: q.Reason}
	return p, c, f
}
func TestCanonicalRecordsRejectAliasesNullMissingAndExtraFields(t *testing.T) {
	p, c, f := evidenceFixture()
	fixtures := []struct {
		name   string
		value  any
		decode func([]byte) error
	}{
		{"publication", p, func(b []byte) error { _, err := DecodePublication(b); return err }},
		{"consumer", c, func(b []byte) error { _, err := DecodeConsumer(b); return err }},
		{"fence", f, func(b []byte) error { _, err := DecodeFence(b); return err }},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			raw, err := Canonical(fixture.value)
			if err != nil || fixture.decode(raw) != nil {
				t.Fatalf("valid producer %v", err)
			}
			schema := []byte(`"schema":1`)
			if fixture.name == "publication" {
				schema = []byte(`"schema":2`)
			}
			bad := [][]byte{
				bytes.Replace(raw, schema, []byte(`"Schema":1`), 1),
				bytes.Replace(raw, schema, []byte(`"schema":1,"Schema":1`), 1),
				bytes.Replace(raw, append(append([]byte{}, schema...), ','), nil, 1),
				bytes.Replace(raw, schema, []byte(`"schema":null`), 1),
				bytes.Replace(raw, []byte(`"reason":`), []byte(`"Reason":`), 1),
				append(append([]byte{}, raw...), []byte(` {}`)...),
				append([]byte(`{"extra":true,`), raw[1:]...),
			}
			for i, b := range bad {
				if fixture.decode(b) == nil {
					t.Fatalf("accepted noncanonical/missing/null alias evidence %d %s", i, b)
				}
			}
		})
	}
}
func TestClosedPublicationSettlementAndNativeEvidence(t *testing.T) {
	p, _, _ := evidenceFixture()
	for name, mutate := range map[string]func(*Publication){
		"foreign-owner":           func(p *Publication) { p.StorageIdentity.Owner = 502 },
		"unknown-birth":           func(p *Publication) { p.StorageIdentity.BirthKnown = false },
		"missing-birth":           func(p *Publication) { p.StorageIdentity.BornSeconds = 0 },
		"bad-nanos":               func(p *Publication) { p.StorageIdentity.BornNanoseconds = 1e9 },
		"noncanonical-uid":        func(p *Publication) { p.Owner = "0501" },
		"large-uid":               func(p *Publication) { p.Owner = "4294967296" },
		"false-settlement":        func(p *Publication) { p.State = "cancelled"; at := p.Created; p.Retired = &at; p.JournalSlots = 0 },
		"no-finalization-reserve": func(p *Publication) { p.JournalSlots = -1 },
		"unknown-state":           func(p *Publication) { p.State = "done" },
		"expiry-after-request":    func(p *Publication) { p.Expires = p.Created.Add(2 * p.Request.TTL) },
		"invalid-reason":          func(p *Publication) { p.Reason = "\xff" },
	} {
		t.Run(name, func(t *testing.T) {
			v := p
			mutate(&v)
			if v.Validate() == nil {
				t.Fatal("accepted contradictory/native evidence")
			}
		})
	}
	p.State = "cancelled"
	p.WorkerState = "joined"
	p.JournalSlots = 0
	at := p.Created
	p.Retired = &at
	if err := p.Validate(); err != nil {
		t.Fatal("actual joined unexposed cancellation", err)
	}
}
func TestConsumerControlRejectsUnconfinedAndUnsupportedRequests(t *testing.T) {
	_, c, _ := evidenceFixture()
	for _, path := range []string{"", "/file", "../file", "a/../file", "a//b", "a\\b", "bad\nfile", "bad\tfile", "bad\x00file", "bad\u2028file", "bad\u202efile", "bad\xfffile", strings.Repeat("x", 256)} {
		v := c.Admission.Control
		v.Path = path
		if v.Validate() == nil {
			t.Fatalf("accepted path %q", path)
		}
	}
	for name, mutate := range map[string]func(*Control){"command": func(q *Control) { q.Purpose = "shell" }, "disk-doublecharge": func(q *Control) { q.Resources.Disk = 1 }, "no-memory": func(q *Control) { q.Resources.Memory = 0 }, "no-cpu": func(q *Control) { q.Resources.CPU = 0 }, "same-authority": func(q *Control) { q.Source = q.Target }, "no-reason": func(q *Control) { q.Reason = "" }, "oversize-ttl": func(q *Control) { q.TTL = 8 * 24 * time.Hour }} {
		t.Run(name, func(t *testing.T) {
			q := c.Admission.Control
			mutate(&q)
			if q.Validate() == nil {
				t.Fatal("accepted unsupported control")
			}
		})
	}
}
func TestConsumerSettlementAndFenceCommitment(t *testing.T) {
	_, c, f := evidenceFixture()
	c.State = "terminal"
	c.WorkerState = "joined"
	end := c.Updated.Add(time.Second)
	c.Updated = end
	c.Ended = &end
	c.Outcome = "released"
	c.JournalSlots = 0
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, worker := range []string{"reserved", "running", "unknown"} {
		v := c
		v.WorkerState = worker
		if v.Validate() == nil {
			t.Fatal("settled unjoined worker", worker)
		}
	}
	f.State = "sealed"
	f.Lease = ""
	f.AdmissionSHA256 = ""
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	f.Control.Path = "retargeted"
	if f.Validate() == nil {
		t.Fatal("changed control accepted existing fence hash")
	}
}
func TestSchemaCatalogCannotBeMutatedByCaller(t *testing.T) {
	v := SchemaStatements()
	v[0] = "DROP TABLE leases"
	if strings.Contains(SchemaStatements()[0], "DROP") {
		t.Fatal("caller changed group catalog")
	}
}
func TestNestedResourcesRemainClosedCanonical(t *testing.T) {
	p, _, _ := evidenceFixture()
	raw, _ := json.Marshal(p)
	bad := bytes.Replace(raw, []byte(`"cpu":1`), []byte(`"cpu":1,"GPU":7`), 1)
	if _, err := DecodePublication(bad); err == nil {
		t.Fatal("unknown nested resource field accepted")
	}
}
