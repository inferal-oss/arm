package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

func TestPeersOptOutAndHumanTiming(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	var out, stderr bytes.Buffer
	a := app{ctx: context.Background(), home: home, out: &out, err: &stderr, outputTTY: false}
	if code, err := a.peers([]string{"off"}); code != 0 || err != nil {
		t.Fatalf("off: %d %v", code, err)
	}
	if _, err := os.Stat(filepath.Join(home, "presence-disabled")); err != nil {
		t.Fatal(err)
	}
	if code, err := a.peers([]string{"on"}); code != 0 || err != nil {
		t.Fatalf("on: %d %v", code, err)
	}
	if _, err := os.Stat(filepath.Join(home, "presence-disabled")); !os.IsNotExist(err) {
		t.Fatalf("opt-out remains: %v", err)
	}
	expiry := time.Now().UTC().Add(time.Minute)
	estimate := expiry.Add(time.Minute)
	out.Reset()
	a.outputTTY = true
	report := model.PeerOverview{Instances: []model.PeerInstance{{UID: 501, Host: "peer\x1b[2J", Snapshot: &model.PeerSnapshot{Held: model.Resources{Memory: 1024}, HeldCount: 1, Queued: model.Resources{Memory: 2048}, QueuedCount: 1, Items: []model.PeerItem{{State: "active", Resources: model.Resources{Memory: 1024}, Expires: &expiry}, {State: "queued", Resources: model.Resources{Memory: 2048}, QueuePosition: 2, EstimatedStart: &estimate, RequestedTTL: time.Minute}}}}}}
	if err := a.peersHuman(report); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Peer-reported held", "Peer-reported queued", "expires", "queue #2", "requested for 1m0s", "estimated start", `peer\u001b[2J`} {
		if !strings.Contains(out.String(), expected) {
			t.Fatalf("missing %q: %s", expected, out.String())
		}
	}
	if strings.Contains(out.String(), "\x1b") {
		t.Fatal("untrusted peer text reached terminal control channel")
	}
}
