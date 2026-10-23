package providerexec

import (
	"bytes"
	"encoding/json"
	"github.com/yrashk/arm/internal/providerproto"
	"reflect"
	"strings"
	"testing"
)

func TestDerivedTerminalBoundAndAllPointerVariants(t *testing.T) {
	typ := reflect.TypeFor[Terminal]()
	max := maxSize(typ)
	if max != 33866 {
		t.Fatalf("schema changed; review reserve: %d", max)
	}
	fields := []int{}
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).Type.Kind() == reflect.Pointer {
			fields = append(fields, i)
		}
	}
	for mask := 0; mask < (1 << len(fields)); mask++ {
		v := worst(typ)
		for bit, field := range fields {
			if mask&(1<<bit) != 0 {
				v.Field(field).SetZero()
			}
		}
		b, err := encodeTerminal(v.Interface().(Terminal))
		if err != nil || len(b) > 34816 {
			t.Fatalf("variant %d bytes%d %v", mask, len(b), err)
		}
		var decoded Terminal
		if err = json.Unmarshal(b, &decoded); err != nil {
			t.Fatal(err)
		}
	}
	// Maximal syntactic superset uses six escaped bytes for every string byte,
	// false's longer JSON token, every pointer present and all eight history slots.
	v := worst(typ).Interface().(Terminal)
	b, err := encodeTerminal(v)
	if err != nil || len(b) != max {
		t.Fatal(len(b), max, err)
	}
	v.Failure.Message = Message(strings.Repeat("\x01", 513))
	if _, err = encodeTerminal(v); err == nil {
		t.Fatal("overbound failure message")
	}
}
func TestSelectedBoundUsesFinalEscapingAndFallbackFits(t *testing.T) {
	v := worst(reflect.TypeFor[Terminal]()).Interface().(Terminal)
	v.Result = Selected(`"` + strings.Repeat("<", 4096) + `"`)
	if len(v.Result) > SelectedCap {
		t.Fatal("raw fixture too large")
	}
	if _, err := encodeTerminal(v); err == nil {
		t.Fatal("HTML escaped contribution must refuse despite small raw bytes")
	}
	v.Result = Selected(`null`)
	v.Failure = &Failure{Code: "result_too_large", Message: "selected result exceeds persisted bound"}
	if b, err := encodeTerminal(v); err != nil || len(b) > 34816 {
		t.Fatal(len(b), err)
	}
	for _, s := range []string{"<", ">", "&", "\u2028", "\u2029", "\x01", "é", "\\", "\""} {
		raw, err := json.Marshal(strings.Repeat(s, 64))
		if err != nil {
			t.Fatal(err)
		}
		v.Result = Selected(raw)
		if _, err = encodeTerminal(v); err != nil {
			t.Fatal(s, err)
		}
	}
}
func TestImmutableRequestEscapingAndInitialBudget(t *testing.T) {
	rawConfig := `["` + strings.Repeat("<", 4090) + `","` + strings.Repeat("&", 4090) + `","` + strings.Repeat(">", 4090) + `","` + strings.Repeat("<", 4090) + `"]`
	config, err := providerproto.ParseData([]byte(rawConfig))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := providerproto.ParseData([]byte(`{"payload":"fixture"}`))
	if err != nil {
		t.Fatal(err)
	}
	name := providerproto.Username(strings.Repeat("é", 128))
	q := providerproto.Request{Protocol: providerproto.Protocol, Version: 1, Kind: "request", RequestID: "request", Registration: providerproto.Registration{ID: "registration", Generation: "1", CodeSHA256: providerproto.Digest(strings.Repeat("1", 64)), ImplementationVersion: "fixture", ResourceType: "object", ConfigSHA256: config.SHA256(), ExternalScope: providerproto.ExternalScope{Account: "account", Project: "project", Namespace: "namespace"}}, Configuration: config, CredentialRefs: []string{}, Method: providerproto.Plan, Context: providerproto.Context{Authority: "authority", Scope: providerproto.Scope{Kind: "host", HostID: "host"}, OwnerUID: "1000", OwnerName: &name, Reason: providerproto.Text(strings.Repeat("\x01", 4096)), OperationKey: "operation", IntentSHA256: providerproto.Digest(strings.Repeat("0", 64))}, Params: providerproto.PlanParams{Specification: spec, Budgets: []providerproto.Budget{}, ExpiresAt: "2027-01-01T00:00:00Z"}}
	q.Context.IntentSHA256, err = providerproto.IntentDigest(q)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := providerproto.EncodeRequest(q)
	if err != nil {
		t.Fatal(err)
	}
	initial := Initial{Attempt: "attempt", Generation: "generation", Authority: "authority", Request: wire, RequestSHA256: Digest(q.Context.IntentSHA256), WorkerLease: "lease", TempReservation: "temp", CPU: "1000", Memory: "134217728", TempDisk: "67108864"}
	full, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("valid wire=%d; durable initial after HTML/control escaping=%d", len(wire), len(full))
	if len(full) <= InitialCap {
		t.Fatal("fixture must exceed persisted initial budget")
	}
	if _, err = encodeInitial(initial); err == nil {
		t.Fatal("accepted oversized initial")
	}
	// No reason/config copying into terminal: its bound is independent of immutable
	// initial bytes, which are checked separately before reserving/launching.
	emptyConfig, _ := providerproto.ParseData([]byte(`null`))
	q.Configuration = emptyConfig
	q.Registration.ConfigSHA256 = emptyConfig.SHA256()
	q.Context.IntentSHA256, err = providerproto.IntentDigest(q)
	if err != nil {
		t.Fatal(err)
	}
	initial.Request, err = providerproto.EncodeRequest(q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = encodeInitial(initial); err != nil {
		t.Fatal("bounded reason accepted", err)
	}
}
func TestReservationArithmeticRetainsUnknownAndFinalization(t *testing.T) {
	const cap = 128 << 10
	fixed := 65536 // fixture generation immutable/current other evidence charge
	ceiling := 34816
	for _, terminalBytes := range []int{4, 100, 16000, 33866} {
		remaining := ceiling - terminalBytes
		if remaining < 0 || fixed+terminalBytes+remaining > cap || fixed+terminalBytes+remaining != fixed+ceiling {
			t.Fatal("unknown must retain constant reserved charge")
		}
		// Terminal replacement consumes reserved bytes; it cannot require a second
		// fresh reserve or silently erase old immutable bytes to make room.
		if fixed+33866 > fixed+terminalBytes+remaining {
			t.Fatal("finalization exceeded reservation")
		}
	}
	if cap-34816+1+ceiling <= cap {
		t.Fatal("overfull generation admitted")
	}
}

func worst(t reflect.Type) reflect.Value {
	if t == reflect.TypeFor[Selected]() {
		return reflect.ValueOf(Selected(append(append([]byte{'"'}, bytes.Repeat([]byte{'a'}, SelectedCap-2)...), '"')))
	}
	if t.Kind() == reflect.Pointer {
		v := reflect.New(t.Elem())
		v.Elem().Set(worst(t.Elem()))
		return v
	}
	v := reflect.New(t).Elem()
	switch t.Kind() {
	case reflect.String:
		v.SetString(strings.Repeat("\x01", maxString(t))) // syntactic superset, no NUL
	case reflect.Bool:
		v.SetBool(false)
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			v.Index(i).Set(worst(t.Elem()))
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			v.Field(i).Set(worst(t.Field(i).Type))
		}
	default:
		panic(t.String())
	}
	return v
}
