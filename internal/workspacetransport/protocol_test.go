package workspacetransport

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

func validOpen() (Open, time.Time) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	o := Open{Version: 1, Workspace: strings.Repeat("a", 32), Generation: strings.Repeat("b", 32), Route: strings.Repeat("c", 32), Source: strings.Repeat("d", 32), Target: strings.Repeat("e", 32), Class: ClassDirectory, Expires: now.Add(30 * time.Second), Pair: strings.Repeat("f", 64), Revision: 1}
	o.Path = []string{o.Source, o.Target}
	return o, now
}

func TestControlRoundTripAndClosedClasses(t *testing.T) {
	for _, class := range []string{ClassDirectory, ClassJoinProof} {
		t.Run(class, func(t *testing.T) {
			o, now := validOpen()
			o.Class = class
			if class == ClassJoinProof {
				o.Consent = strings.Repeat("1", 64)
			}
			var encoded bytes.Buffer
			if err := WriteOpen(&encoded, o, now); err != nil {
				t.Fatal(err)
			}
			got, err := ReadOpen(&encoded, now)
			if err != nil || !reflect.DeepEqual(got, o) || encoded.Len() != 0 {
				t.Fatalf("roundtrip got %#v, %v, remaining %d", got, err, encoded.Len())
			}
		})
	}
	for _, class := range []string{"", "message", "ssh", "run", "acquire", "status", "mesh_rpc", "native", "join_proof/resource"} {
		o, now := validOpen()
		o.Class = class
		if err := o.Validate(now); err == nil {
			t.Errorf("accepted unsupported class %q", class)
		}
	}
}

func TestControlRejectsAliasesMissingNullExtraAndAlternateOptional(t *testing.T) {
	o, now := validOpen()
	raw, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	base := string(raw)
	cases := []string{
		strings.Replace(base, `"schema_version":1`, `"Schema_version":1`, 1),
		strings.Replace(base, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1),
		strings.Replace(base, `"schema_version":1,`, "", 1),
		strings.Replace(base, `"schema_version":1`, `"schema_version":null`, 1),
		strings.TrimSuffix(base, "}") + `,"consent_sha256":null}`,
		strings.TrimSuffix(base, "}") + `,"consent_sha256":""}`,
		strings.TrimSuffix(base, "}") + `,"address":"127.0.0.1:22"}`,
		strings.TrimSuffix(base, "}") + `,"operation":"acquire"}`,
		base + "{}",
		strings.Repeat(" ", MaxControlBytes) + base,
	}
	for i, raw := range cases {
		if _, err := DecodeOpen([]byte(raw), now); err == nil {
			t.Errorf("accepted malformed control case %d", i)
		}
	}
}

func TestControlNoPathOrDeadlineAuthorityExpansion(t *testing.T) {
	tests := map[string]func(*Open){
		"route through relay":   func(o *Open) { o.Path = []string{o.Source, strings.Repeat("1", 32), o.Target} },
		"repeated path":         func(o *Open) { o.Path = []string{o.Source, o.Source} },
		"swapped path":          func(o *Open) { o.Path = []string{o.Target, o.Source} },
		"local target":          func(o *Open) { o.Target = o.Source; o.Path[1] = o.Source },
		"expired":               func(o *Open) { o.Expires = o.Expires.Add(-30 * time.Second) },
		"future":                func(o *Open) { o.Expires = o.Expires.Add(6 * time.Second) },
		"noncanonical ID":       func(o *Open) { o.Workspace = strings.ToUpper(o.Workspace) },
		"zero revision":         func(o *Open) { o.Revision = 0 },
		"missing proof consent": func(o *Open) { o.Class = ClassJoinProof },
		"directory consent":     func(o *Open) { o.Consent = strings.Repeat("1", 64) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			o, now := validOpen()
			mutate(&o)
			if err := o.Validate(now); err == nil {
				t.Fatal("accepted expanded/invalid authority assertion")
			}
		})
	}
}

func TestControlReadRefusesUncheckedAllocationAndPartialFrames(t *testing.T) {
	for _, n := range []uint32{0, MaxControlBytes + 1, ^uint32(0)} {
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], n)
		reader := bytes.NewReader(append(header[:], 123))
		if _, err := ReadOpen(reader, time.Now()); err == nil || reader.Len() != 1 {
			t.Fatalf("length %d read body or accepted: %v, remaining %d", n, err, reader.Len())
		}
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], 1024)
	if _, err := ReadOpen(bytes.NewReader(append(header[:], 123)), time.Now()); err != io.ErrUnexpectedEOF {
		t.Fatalf("partial payload: %v", err)
	}
}

type noProgress struct{}

func (noProgress) Write([]byte) (int, error) { return 0, nil }

func TestControlWriteDoesNotReportSuccessOnShortWrite(t *testing.T) {
	o, now := validOpen()
	if err := WriteOpen(noProgress{}, o, now); err != io.ErrShortWrite {
		t.Fatalf("short write: %v", err)
	}
}
