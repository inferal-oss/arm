package residentrefs

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"getarm.org/internal/local"
)

func controlEndpointData(t *testing.T, change func(*local.Endpoint)) []byte {
	t.Helper()
	v := local.Endpoint{Generation: strings.Repeat("a", 32), Token: strings.Repeat("b", 64), URL: "ws://127.0.0.1:1/v1/ws", PID: 1}
	if change != nil {
		change(&v)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestResidentControlEndpointCanonicalDataLimits(t *testing.T) {
	valid := controlEndpointData(t, nil)
	if _, _, err := validateResidentControlData(valid); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{
		"hostname":          controlEndpointData(t, func(v *local.Endpoint) { v.URL = "ws://localhost:1/v1/ws" }),
		"remote":            controlEndpointData(t, func(v *local.Endpoint) { v.URL = "ws://192.0.2.1:1/v1/ws" }),
		"userinfo":          controlEndpointData(t, func(v *local.Endpoint) { v.URL = "ws://user@127.0.0.1:1/v1/ws" }),
		"query":             controlEndpointData(t, func(v *local.Endpoint) { v.URL = "ws://127.0.0.1:1/v1/ws?" }),
		"wrong-path":        controlEndpointData(t, func(v *local.Endpoint) { v.URL = "ws://127.0.0.1:1/" }),
		"zero-port":         controlEndpointData(t, func(v *local.Endpoint) { v.URL = "ws://127.0.0.1:0/v1/ws" }),
		"noncanonical-port": controlEndpointData(t, func(v *local.Endpoint) { v.URL = "ws://127.0.0.1:01/v1/ws" }),
		"token":             controlEndpointData(t, func(v *local.Endpoint) { v.Token = "short" }),
		"generation":        controlEndpointData(t, func(v *local.Endpoint) { v.Generation = "short" }),
		"process-data":      controlEndpointData(t, func(v *local.Endpoint) { v.PID = 0 }),
		"whitespace":        append([]byte(" "), valid...),
		"duplicate":         []byte(strings.TrimSuffix(string(valid), "}") + `,"pid":1}`),
		"trailing":          append(append([]byte{}, valid...), valid...),
		"oversize":          []byte(strings.Repeat("x", MaxResidentControlBytes+1)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := validateResidentControlData(raw); err != ErrOriginal {
				t.Fatalf("refusal = %v", err)
			}
		})
	}
}

func TestResidentControlPublisherUnissuedCopiedRefuseBeforeEffects(t *testing.T) {
	p := &ResidentControlPublicationPending{service: &ServiceOriginal{}, home: &originalEntry{}}
	p.self = p
	// Copy identity DATA without copying a live synchronization primitive.
	copied := ResidentControlPublicationPending{self: p, service: p.service, home: p.home}
	for name, owner := range map[string]*ResidentControlPublicationPending{"nil": nil, "zero": {}, "unissued": p, "copied": &copied} {
		t.Run(name, func(t *testing.T) {
			if err := owner.PublishOriginal(context.Background(), controlEndpointData(t, nil)); err != ErrOriginal {
				t.Fatalf("refusal = %v", err)
			}
		})
	}
	if p.call != nil || p.firstError != nil {
		t.Fatal("unissued publication acquired original operation")
	}
}

func TestResidentControlPublicationRequiresOriginalServiceConstructor(t *testing.T) {
	service := &ServiceOriginal{}
	service.self = service
	copied := *service
	for _, source := range []*ServiceOriginal{nil, {}, service, &copied} {
		if p, err := NewOriginalResidentControlPublication(source); p != nil || err != ErrOriginal {
			t.Fatalf("constructor = %v, %v", p, err)
		}
	}
}
