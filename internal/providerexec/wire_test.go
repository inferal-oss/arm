package providerexec

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"
)

func TestControlWireRejectsLossyAndAmbiguousIdentity(t *testing.T) {
	for _, raw := range []string{`{"request_sha256":"x","request_sha256":"y"}`, `{"request_sha256":"\uD800"}`, `{"request_sha256":"\uDC00"}`, `{"request_sha256":"\uD800\u0061"}`, `{"request_sha256":null}`, `{}`, `{"request_sha256":"x","extra":true}`, `{"REQUEST_SHA256":"x"}`, `{"request_sha256":"x"} {}`} {
		var grant grantPacket
		if strictJSON([]byte(raw), &grant) == nil {
			t.Fatal("ambiguous/lossy control packet accepted", raw)
		}
	}
	var grant grantPacket
	if err := strictJSON([]byte(`{"request_sha256":"\uD83D\uDE00"}`), &grant); err != nil || grant.RequestSHA256 != "😀" {
		t.Fatal("paired Unicode changed", err)
	}
	var head [4]byte
	binary.BigEndian.PutUint32(head[:], maxControlFrame+1)
	if _, err := readControlBody(bytes.NewReader(head[:])); err == nil {
		t.Fatal("oversized frame allocated")
	}
	b, _ := json.Marshal(packet{Version: 1, Nonce: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Sequence: 2, Kind: "grant", Data: json.RawMessage(`{"request_sha256":"digest"}`)})
	binary.BigEndian.PutUint32(head[:], uint32(len(b)))
	frame := append(head[:], b...)
	if readPacket(bytes.NewReader(frame), "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", 2, "grant", &grant) == nil {
		t.Fatal("foreign nonce accepted")
	}
}

func TestProcessBearingStartErrorIsNotKnownNoStart(t *testing.T) {
	// No native call or signal: malformed/partial native result must not acquire
	// stronger positive closure merely because the API returned an error.
	h := &Handle{owner: &Owner{}, command: &exec.Cmd{Process: &os.Process{Pid: 123}}, helloDone: make(chan struct{}), observation: Observation{StartPending: true}}
	h.noStart(fmt.Errorf("partial start outcome"))
	o := h.Snapshot()
	if o.KnownNotStarted || o.ScopedNativeClosed || o.Failure != "start_outcome_unknown" {
		t.Fatal("partial process handle erased", o)
	}
}
