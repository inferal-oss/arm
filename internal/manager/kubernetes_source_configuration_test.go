package manager

import (
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/kubesource"
	"getarm.org/internal/model"
)

func TestConfiguredKubernetesSourceEndJoinsAndRetainsStorageDebt(t *testing.T) {
	settings := daemonconfig.Settings{
		Resources: model.Resources{CPU: 1, Memory: 512 << 20, Disk: 512 << 20},
		Listen:    "127.0.0.1:0",
	}
	m, err := OpenConfigured(filepath.Join(t.TempDir(), "state"), settings)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	journal := daemonJournal(t, m)
	if len(journal) != 1 || !reflect.DeepEqual(journal[0].Settings, settings) || m.capacity != settings.Resources {
		t.Fatal("configured admission preceded its durable startup decision", journal, m.capacity)
	}
	// Use the existing controlled admission fixture. The files and their native
	// cleanup are real; this test makes no fresh host-headroom or sleep claim.
	cpuReading(m, 64, 64, 0, 64, 0)
	joined := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(joined) }) }
	owner, err := m.PrepareKubernetesSource(t.Context(), sourceCoreScope(kubesource.Ship), joined)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { finish(); _, _ = owner.FinishAfterJoin() })
	payload, err := owner.Files().NewPayload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = payload.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	events, err := m.Events(0)
	if err != nil {
		t.Fatal(err)
	}
	var configured, firstSource int64
	for _, event := range events {
		if event.Type == daemonConfigEvent {
			configured = event.Sequence
		}
		if event.Entity == owner.transit.id && firstSource == 0 {
			firstSource = event.Sequence
		}
	}
	if configured == 0 || firstSource <= configured {
		t.Fatal("source authority lacks a preceding configuration decision", configured, firstSource)
	}
	m.Start()
	if _, err = m.End(owner.parent.ID, "released"); err != nil {
		t.Fatal(err)
	}
	if owner.Context().Err() == nil {
		t.Fatal("configured parent End did not cancel the original source")
	}
	select {
	case <-owner.privateJoined:
		t.Fatal("cancellation substituted for original caller/file join")
	default:
	}
	if _, err = payload.Write([]byte("y")); err == nil {
		t.Fatal("cancelled original file retained write authority")
	}
	status, err := m.Status()
	if err != nil || status.Reserved.CPU != kubesource.SourceCPU || status.Reserved.Disk != owner.scope.DiskBytes() {
		t.Fatal("End released an unjoined original promise", status.Reserved, err)
	}
	finish()
	result, err := owner.FinishAfterJoin()
	if err != nil || !result.SourceClosed || !result.SourceSettled || !result.StorageRetained {
		t.Fatal(result, err)
	}
	parent := sourceCoreDisk(t, m, owner.parent.ID)
	if parent.State != "needs_attention" || parent.Artifact == nil || parent.Artifact.AllocatedBefore == nil || !parent.Artifact.ContentObserved {
		t.Fatal("configured source closure erased ordinary storage debt", parent)
	}
	status, err = m.Status()
	if err != nil || status.Reserved.CPU != 0 || status.Reserved.Memory != 0 || status.Reserved.Disk != owner.scope.DiskBytes() {
		t.Fatal("native settlement and retained disk were conflated", status.Reserved, err)
	}
}
