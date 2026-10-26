package mesh

import (
	"github.com/inferal-oss/arm/internal/model"
	"testing"
	"time"
)

func TestSSHGrantIsSeparateAndRequiresExplicitLeaseCeilings(t *testing.T) {
	for _, g := range []Grant{{Permissions: []string{"ssh-exec"}}, {Permissions: []string{"lease-own", "ssh-exec"}}, {Permissions: []string{"lease-own", "ssh-exec"}, Limits: model.Resources{CPU: 1}}, {Permissions: []string{"lease-own", "ssh-exec", "ssh-exec"}, Limits: model.Resources{CPU: 1}, MaxTTL: time.Minute}} {
		if g.Validate() == nil {
			t.Fatal("unbounded SSH grant accepted", g)
		}
	}
	g := Grant{Permissions: []string{"lease-own", "ssh-exec"}, Limits: model.Resources{CPU: 1}, MaxTTL: time.Minute}
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, permissions := range [][]string{{"observe-host"}, {"lease-own", "run"}, {"message", "contact-read"}} {
		g.Permissions = permissions
		if err := g.Validate(); err != nil {
			t.Fatal(err)
		}
		if g.Has("ssh-exec") {
			t.Fatal("existing permission implied SSH")
		}
	}
}
