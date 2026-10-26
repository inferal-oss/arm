package syncservice

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type testAdmission struct {
	bytes  int64
	active bool
}

func (a testAdmission) CheckActive(context.Context) error {
	if !a.active {
		return errors.New("admission inactive")
	}
	return nil
}
func (a testAdmission) MemoryBytes() int64 { return a.bytes }

func TestPreparationWireRoundTripAndBounds(t *testing.T) {
	local, _, rows := intents(t)
	p := Preparation{Version: Schema, Intent: local, Manifest: rows}
	var buffer bytes.Buffer
	if err := WritePreparation(&buffer, p); err != nil {
		t.Fatal(err)
	}
	got, err := ReadPreparation(context.Background(), &buffer, testAdmission{bytes: 1 << 20, active: true})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, p) {
		t.Fatal("wire preparation changed")
	}
	var oversized [8]byte
	binary.BigEndian.PutUint64(oversized[:], MaxPreparationBytes+1)
	if _, err = ReadPreparation(context.Background(), bytes.NewReader(oversized[:]), testAdmission{bytes: 1 << 20, active: true}); err == nil {
		t.Fatal("oversized frame accepted")
	}
	var truncated bytes.Buffer
	binary.BigEndian.PutUint64(oversized[:], 100)
	truncated.Write(oversized[:])
	truncated.WriteString("short")
	if _, err = ReadPreparation(context.Background(), &truncated, testAdmission{bytes: 1 << 20, active: true}); err == nil {
		t.Fatal("truncated frame accepted")
	}
	var bounded [8]byte
	binary.BigEndian.PutUint64(bounded[:], 200<<10)
	if _, err = ReadPreparation(context.Background(), bytes.NewReader(bounded[:]), testAdmission{bytes: 256 << 10, active: true}); err == nil || !strings.Contains(err.Error(), "admitted decode memory") {
		t.Fatalf("payload read before memory admission: %v", err)
	}
	if _, err = ReadPreparation(context.Background(), bytes.NewReader(bounded[:]), testAdmission{bytes: 1 << 20}); err == nil || !strings.Contains(err.Error(), "inactive") {
		t.Fatalf("inactive hold accepted: %v", err)
	}
}

func TestPreparationRejectsUnknownFieldsAndInsufficientPromises(t *testing.T) {
	local, _, rows := intents(t)
	local.LocalResources.Memory = 1
	if err := WritePreparation(&bytes.Buffer{}, Preparation{Version: Schema, Intent: local, Manifest: rows}); err == nil {
		t.Fatal("underfunded memory promise accepted")
	}
	local, _, rows = intents(t)
	local.RemoteResources.Disk = 1
	if err := WritePreparation(&bytes.Buffer{}, Preparation{Version: Schema, Intent: local, Manifest: rows}); err == nil {
		t.Fatal("underfunded disk promise accepted")
	}
	local, _, rows = intents(t)
	body, _ := json.Marshal(Preparation{Version: Schema, Intent: local, Manifest: rows})
	body = append(body[:len(body)-1], []byte(`,"unknown_control":true}`)...)
	var frame bytes.Buffer
	var header [8]byte
	binary.BigEndian.PutUint64(header[:], uint64(len(body)))
	frame.Write(header[:])
	frame.Write(body)
	if _, err := ReadPreparation(context.Background(), &frame, testAdmission{bytes: 1 << 20, active: true}); err == nil {
		t.Fatal("unknown control field accepted")
	}
	// A changed native name or selected entry is not a wire-compatible plan.
	local, _, rows = intents(t)
	rows[0].NameKey = "different"
	if err := WritePreparation(&bytes.Buffer{}, Preparation{Version: Schema, Intent: local, Manifest: rows}); err == nil {
		t.Fatal("changed manifest accepted")
	}
}

func TestPreparationRejectsSparseProposalBeforeTypedDecode(t *testing.T) {
	local, _, rows := intents(t)
	body, err := json.Marshal(Preparation{Version: Schema, Intent: local, Manifest: rows})
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]json.RawMessage
	if err = json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	var intent map[string]json.RawMessage
	if err = json.Unmarshal(envelope["intent"], &intent); err != nil {
		t.Fatal(err)
	}
	var proposal map[string]json.RawMessage
	if err = json.Unmarshal(intent["proposal"], &proposal); err != nil {
		t.Fatal(err)
	}
	var sparse bytes.Buffer
	sparse.WriteByte('{')
	for n := 0; n < 1000; n++ {
		if n > 0 {
			sparse.WriteByte(',')
		}
		fmt.Fprintf(&sparse, `"p%04d":{}`, n)
	}
	sparse.WriteByte('}')
	proposal["local"] = append(json.RawMessage(nil), sparse.Bytes()...)
	intent["proposal"], err = json.Marshal(proposal)
	if err != nil {
		t.Fatal(err)
	}
	envelope["intent"], err = json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	body, err = json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	const admitted int64 = 256 << 10
	if int64(len(body))*decodeFrameCopies+decodeReserveBytes >= admitted {
		t.Fatalf("fixture does not reach structural check: %d bytes", len(body))
	}
	var frame bytes.Buffer
	var header [8]byte
	binary.BigEndian.PutUint64(header[:], uint64(len(body)))
	frame.Write(header[:])
	frame.Write(body)
	if _, err = ReadPreparation(context.Background(), &frame, testAdmission{bytes: admitted, active: true}); err == nil || !strings.Contains(err.Error(), "structural decode memory") {
		t.Fatalf("sparse proposal was fully decoded: %v", err)
	}
}

func TestPreparationCountsCaseVariantProposalBeforeTypedDecode(t *testing.T) {
	var body bytes.Buffer
	body.WriteString(`{"intent":{"Proposal":{"LOCAL":{`)
	for n := 0; n <= 200000; n++ {
		if n > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `"p%06d":{}`, n)
	}
	body.WriteString(`}}},"manifest":[]}`)
	var frame bytes.Buffer
	var header [8]byte
	binary.BigEndian.PutUint64(header[:], uint64(body.Len()))
	frame.Write(header[:])
	frame.Write(body.Bytes())
	if _, err := ReadPreparation(context.Background(), &frame, testAdmission{bytes: 1 << 30, active: true}); err == nil || !strings.Contains(err.Error(), "object exceeds count limit") {
		t.Fatalf("case variant escaped proposal count: %v", err)
	}
}

func TestPreparationPreflightBoundsUnknownAncestorPaths(t *testing.T) {
	key := strings.Repeat("x", 1<<20)
	raw := []byte(`{"` + key + `":` + strings.Repeat(`{"x":`, 61) + `0` + strings.Repeat(`}`, 62))
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	err := preflightPreparationJSON(context.Background(), raw, 12<<20, testAdmission{bytes: 32 << 20, active: true})
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 16<<20 {
		t.Fatalf("preflight copied unknown ancestor keys: %d allocated bytes", allocated)
	}
}

func TestPreparationChargesCanonicalHTMLEscaping(t *testing.T) {
	local, _, rows := intents(t)
	local.Binding.Local.ID = strings.Repeat("<", 8<<10)
	local.Proposal.Binding = local.Binding
	encoded, err := json.Marshal(Preparation{Version: Schema, Intent: local, Manifest: rows})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(strings.ReplaceAll(string(encoded), `\u003c`, "<"))
	const admitted int64 = 512 << 10
	if int64(len(body))*decodeFrameCopies+decodeReserveBytes >= admitted {
		t.Fatalf("fixture exceeds raw frame allowance: %d", len(body))
	}
	var frame bytes.Buffer
	var header [8]byte
	binary.BigEndian.PutUint64(header[:], uint64(len(body)))
	frame.Write(header[:])
	frame.Write(body)
	if _, err = ReadPreparation(context.Background(), &frame, testAdmission{bytes: admitted, active: true}); err == nil || !strings.Contains(err.Error(), "structural decode memory") {
		t.Fatalf("canonical HTML expansion escaped admission: %v", err)
	}
}
