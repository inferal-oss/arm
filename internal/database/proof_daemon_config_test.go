package database_test

import (
	"testing"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/manager"
	"getarm.org/internal/model"
)

// The startup snapshot is audit evidence, not a new format or settlement
// obligation. Existing generic event validation must still accept its producer.
func TestSettledProofAcceptsDaemonConfigurationAuditWithoutSchemaChange(t *testing.T) {
	home := proofFixture(t, false)
	s := daemonconfig.Settings{Resources: model.Resources{CPU: 2, Memory: 4096, Disk: 4096}, Listen: "127.0.0.1:0"}
	m, e := manager.OpenConfigured(home, s)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	p, e := prove(t, home)
	if e != nil || !p.Complete || !p.Settled {
		t.Fatal("supported startup event broke settled proof", p, e)
	}
}
