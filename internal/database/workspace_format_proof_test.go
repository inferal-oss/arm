package database_test

import (
	"strings"
	"testing"
)

func TestWorkspaceFormatSettledProofAcceptsOrdinaryAndRefusesFuture(t *testing.T) {
	for _, format := range []string{"1", "2"} {
		t.Run(format, func(t *testing.T) {
			home := proofFixture(t, false)
			edit(t, home, `INSERT INTO metadata VALUES('workspace_format',?)`, format)
			p, err := prove(t, home)
			if format == "1" {
				if err != nil || !p.Complete || !p.Settled {
					t.Fatal("ordinary format lost settled proof support", p, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "workspace_format_unsupported") || p.Complete || p.Settled {
				t.Fatal("future format acquired settled proof", p, err)
			}
		})
	}
}

func TestWorkspaceFormatSettledProofRefusesBlobBeforeInterpretation(t *testing.T) {
	home := proofFixture(t, false)
	edit(t, home, `UPDATE leases SET data=CAST(data AS BLOB)`)
	p, err := prove(t, home)
	if err == nil || !strings.Contains(err.Error(), "workspace_format_unsupported") || p.Complete || p.Settled {
		t.Fatal("BLOB ledger escaped the early format check", p, err)
	}
}
