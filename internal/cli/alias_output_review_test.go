package cli

import (
	"bytes"
	"strings"
	"testing"

	"getarm.org/internal/model"
	"getarm.org/internal/readablename"
)

func TestAliasHumanViewsKeepPrimaryNamesAndSecondaryIDs(t *testing.T) {
	var out bytes.Buffer
	a := &app{out: &out, outputTTY: true, commandName: "status"}
	hostID := strings.Repeat("1", 32)
	resourceID := hostID + "/cpu"
	value := map[string]any{
		"host": hostID, "display_name": "operator-name\nline",
		"resources": []any{map[string]any{"id": resourceID, "kind": "cpu", "unit": "cores"}},
		"leases":    []any{},
	}
	if err := a.human(value); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, `Host: operator-name\nline (`+hostID+")") || !strings.Contains(text, "cpu (live) ("+resourceID+")") {
		t.Fatal(text)
	}
	if strings.Contains(text, "operator-name\nline") {
		t.Fatal("supplied display name reached terminal as control text")
	}
	out.Reset()
	a.resourceTable(&out, []any{
		map[string]any{"id": "pool_22222222222222222222222222222222", "kind": "custom", "display_name": "reported-generated-pool-label"},
		map[string]any{"id": "port-resource-id", "kind": "port", "port": map[string]any{"name": "operator-port-name", "network": "tcp4", "address": "127.0.0.1", "port": 1234}},
	})
	if !strings.Contains(out.String(), "reported-generated-pool-label (pool_22222222222222222222222222222222)") || !strings.Contains(out.String(), "operator-port-name (port-resource-id)") {
		t.Fatal(out.String())
	}
	out.Reset()
	if err := a.peersHuman(model.PeerOverview{Instances: []model.PeerInstance{{Host: hostID, UID: 501, URL: "http://127.0.0.1:1234"}}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "UID 501 · "+readablename.FromID(readablename.Host, hostID)+" ("+hostID+")") {
		t.Fatal(out.String())
	}
}

func TestAliasHelpCarriesInstalledVocabularyCredit(t *testing.T) {
	var out, errout bytes.Buffer
	if code := Main([]string{"--help"}, strings.NewReader(""), &out, &errout); code != 0 || errout.Len() != 0 {
		t.Fatal(code, out.String(), errout.String())
	}
	for _, text := range []string{"EFF's Long Wordlist", "Joseph Bonneau and EFF", "CC BY 4.0", "https://www.eff.org/dice", "https://creativecommons.org/licenses/by/4.0/", "ambiguous names require a stable ID"} {
		if !strings.Contains(out.String(), text) {
			t.Fatal("installed help omitted vocabulary credit/selector contract", text, out.String())
		}
	}
}
