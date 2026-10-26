package presence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReviewPresenceReadRefusesMismatchedNativeDirectory(t *testing.T) {
	d, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	other := t.TempDir()
	record := Record{Version: 1, URL: "ws://127.0.0.1:1/v1/peers", Host: strings.Repeat("a", 32), Generation: strings.Repeat("b", 32), PublicKey: make([]byte, 32), Expires: time.Now().Add(time.Second)}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	name := prefix + strings.Repeat("c", 32) + ".json"
	if err = os.WriteFile(filepath.Join(other, name), raw, 0600); err != nil {
		t.Fatal(err)
	}
	native, err := os.Open(other)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	// Fault-inject the native descriptor mismatch possible after a closed raw
	// descriptor is reused. Never overwrite or close another owner's descriptor.
	changed := &Directory{path: d.path, fd: int(native.Fd()), dev: d.dev, ino: d.ino}
	if _, err = changed.Read(name); err == nil {
		t.Fatal("presence read used a descriptor from another native directory")
	}
}

func TestReviewPresenceConcurrentCloseRetiresHandle(t *testing.T) {
	d, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	p, err := Publish(d, "ws://127.0.0.1:1/v1/peers", strings.Repeat("a", 32), strings.Repeat("b", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(filepath.Join(d.path, p.Name))
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 40; i++ {
			_, _ = d.Read(p.Name)
			_ = d.Scan()
		}
	}()
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	<-done
	if _, err = d.Read(p.Name); err == nil {
		t.Fatal("retired native handle accepted")
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestReviewRefreshPreservesReplacedPublication(t *testing.T) {
	d, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	p, err := Publish(d, "ws://127.0.0.1:1/v1/peers", strings.Repeat("a", 32), strings.Repeat("b", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(filepath.Join(d.path, p.Name))
	replacement := p.Record
	replacement.URL = "ws://127.0.0.1:2/v1/peers"
	if err = d.write(p.Name, replacement, true); err != nil {
		t.Fatal(err)
	}
	if err = p.Refresh(); err == nil {
		t.Fatal("refresh overwrote replacement identity")
	}
	_ = p.Close()
	c, err := d.Read(p.Name)
	if err != nil || c.Record.URL != replacement.URL {
		t.Fatalf("replacement removed or changed: %+v %v", c, err)
	}
}
