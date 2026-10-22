package database_test

import "testing"

func TestDirectoryProbeStrictProofActualRetiredWriter(t *testing.T) {
	_, home := graphProofFixtureWithOptions(t, false, true)
	p, e := prove(t, home)
	if e != nil || !p.Complete || !p.Settled {
		t.Fatal("valid recorded actual native directory path rejected", p.Blockers, e)
	}
}
func TestDirectoryProbeStrictProofRejectsCorruptRawHistory(t *testing.T) {
	for name, mutation := range map[string]string{
		"null":                  `json_set(data,'$.directory_probes',NULL)`,
		"empty":                 `json_set(data,'$.directory_probes',json('[]'))`,
		"alias":                 `replace(data,'"directory_probes":','"Directory_Probes":')`,
		"missing-required":      `json_remove(data,'$.directory_probes[0].source_pin')`,
		"unknown":               `json_set(data,'$.directory_probes[0].extra',1)`,
		"nonce":                 `json_set(data,'$.directory_probes[0].request_nonce','0000000000000000000000000000000000000000000000000000000000000000')`,
		"future-sequence":       `json_set(data,'$.directory_probes[0].source_report_sequence',9223372036854775807)`,
		"future-revision":       `json_set(data,'$.directory_probes[0].membership_revision',9223372036854775807)`,
		"future-observation":    `json_set(data,'$.directory_probes[0].observed_at','9999-01-01T00:00:00Z')`,
		"wrong-directory":       `json_set(data,'$.directory_probes[0].directory_host','00000000000000000000000000000000')`,
		"wrong-node-generation": `json_set(data,'$.directory_probes[0].nodes[0].membership_generation',2)`,
		"wrong-node-pin":        `json_set(data,'$.directory_probes[0].nodes[0].certificate_sha256','0000000000000000000000000000000000000000000000000000000000000000')`,
		"duplicate-node":        `json_set(data,'$.directory_probes[0].nodes[1]',json_extract(data,'$.directory_probes[0].nodes[0]'))`,
		"duplicate-path":        `json_insert(data,'$.directory_probes[#]',json_extract(data,'$.directory_probes[0]'))`,
	} {
		t.Run(name, func(t *testing.T) {
			_, home := graphProofFixtureWithOptions(t, false, true)
			edit(t, home, `UPDATE routing_graph_observations SET data=`+mutation)
			p, e := prove(t, home)
			if e == nil && p.Complete && p.Settled {
				t.Fatal("corrupt historical probe became complete settled reset evidence", name)
			}
		})
	}
}
