package presence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestNativePublishScanRefreshAndClose(t *testing.T) {
	d, err := Open()
	if err != nil {
		t.Skipf("shared native /tmp unavailable: %v", err)
	}
	defer d.Close()
	p, err := Publish(d, "ws://127.0.0.1:45671/v1/peers", strings.Repeat("a", 32), strings.Repeat("b", 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	c, err := d.Read(p.Name)
	if err != nil {
		t.Fatal(err)
	}
	if c.UID != uint32(os.Geteuid()) || c.Record.URL != p.Record.URL || len(c.Record.PublicKey) != 32 {
		t.Fatalf("wrong published identity: %+v", c)
	}
	raw, err := os.ReadFile(filepath.Join(d.path, p.Name))
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) || strings.Contains(string(raw), `"token"`) {
		t.Fatal("public record malformed or contains secret")
	}
	info, err := os.Stat(filepath.Join(d.path, p.Name))
	if err != nil || info.Mode().Perm() != 0644 {
		t.Fatalf("public record not readable across users: %v %v", info, err)
	}
	found := false
	for _, entry := range d.Scan().Candidates {
		if entry.Name == p.Name {
			found = true
		}
	}
	if !found {
		t.Fatal("published record undiscoverable")
	}
	if err = p.Refresh(); err != nil {
		t.Fatal(err)
	}
	p.Record.Expires = time.Now().Add(-time.Second)
	if err = d.write(p.Name, p.Record, true); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Read(p.Name); err == nil {
		t.Fatal("expired record remains discoverable")
	}
	if err = p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Read(p.Name); err == nil {
		t.Fatal("closed record remains")
	}
}
func TestPoisonedPresenceRecordsRefused(t *testing.T) {
	now := time.Now().UTC()
	good := Record{Version: 1, URL: "ws://127.0.0.1:8/v1/peers", Host: strings.Repeat("a", 32), Generation: strings.Repeat("b", 32), PublicKey: make([]byte, 32), Expires: now.Add(time.Second)}
	if !validRecord(good, now) {
		t.Fatal("valid record refused")
	}
	for _, url := range []string{"ws://example.com:8/v1/peers", "ws://127.0.0.1:8/v1/ws", "ws://127.0.0.1:8/v1/peers?token=x", "wss://127.0.0.1:8/v1/peers", "ws://127.0.0.1:0/v1/peers", "ws://127.0.0.1:99999/v1/peers"} {
		bad := good
		bad.URL = url
		if validRecord(bad, now) {
			t.Fatal("unsafe endpoint accepted", url)
		}
	}
	d, err := Open()
	if err != nil {
		t.Skipf("shared native /tmp unavailable: %v", err)
	}
	defer d.Close()
	name, err := randomName()
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err = os.WriteFile(target, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = unix.Symlinkat(target, d.fd, name); err != nil {
		t.Fatal(err)
	}
	defer unix.Unlinkat(d.fd, name, 0)
	if _, err = d.Read(name); err == nil {
		t.Fatal("symlink presence accepted")
	}
}
