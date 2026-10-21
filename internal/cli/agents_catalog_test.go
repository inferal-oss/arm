package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yrashk/arm/knowledge"
)

func TestAgentCardCatalogOfflineAliasesAndRetrieval(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	home := filepath.Join(t.TempDir(), "absent")
	var first agentCardCatalog
	for i, args := range [][]string{
		{"agents", "card", "--list"},
		{"agents", "card", "--list", "--json"},
		{"agents", "card", "list"},
		{"agents", "card", "list", "--json"},
		{"--json", "agents", "card", "--list"},
	} {
		var out, stderr bytes.Buffer
		code := Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &stderr)
		var catalog agentCardCatalog
		if code != 0 || stderr.Len() != 0 || json.Unmarshal(out.Bytes(), &catalog) != nil {
			t.Fatal(args, code, out.String(), stderr.String())
		}
		if len(catalog.Cards) == 0 || catalog.CardCommand != "arm agents card NAME --json" || catalog.ListCommand != "arm agents card --list --json" || strings.Contains(out.String(), `"markdown"`) {
			t.Fatal("catalog must expose discovery metadata without card bodies", catalog, out.String())
		}
		if i == 0 {
			first = catalog
		} else if !reflect.DeepEqual(first, catalog) {
			t.Fatal("list spellings differ", first, catalog)
		}
	}
	for _, metadata := range first.Cards {
		var out, stderr bytes.Buffer
		code := Main([]string{"--home", home, "agents", "card", metadata.ID, "--json"}, strings.NewReader(""), &out, &stderr)
		var card knowledge.Card
		if code != 0 || stderr.Len() != 0 || json.Unmarshal(out.Bytes(), &card) != nil || !reflect.DeepEqual(card.Metadata, metadata) || card.Markdown == "" {
			t.Fatal("listed card cannot be retrieved consistently", metadata.ID, code, out.String(), stderr.String())
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("offline catalog created backend state", err)
	}
}

func TestAgentCardCatalogTerminalAndForcedJSON(t *testing.T) {
	for _, args := range [][]string{{"--list"}, {"list"}} {
		var out bytes.Buffer
		a := app{home: filepath.Join(t.TempDir(), "absent"), out: &out, outputTTY: true, colors: true}
		if err := a.agentsCard(args); err != nil || !strings.Contains(out.String(), "Workload knowledge cards") || !strings.Contains(out.String(), "rust-build") || !strings.Contains(out.String(), "\x1b[1;36m") || strings.Contains(out.String(), "# Rust builds") {
			t.Fatal("terminal catalog is not readable discovery output", err, out.String())
		}
		out.Reset()
		if err := a.agentsCard(append(args, "--json")); err != nil || !json.Valid(out.Bytes()) || strings.Contains(out.String(), "\x1b") {
			t.Fatal("terminal forced JSON failed", err, out.String())
		}
	}
}

func TestAgentCardCatalogRejectsMixedSelectorsAndMarkdownWithoutState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	for _, args := range [][]string{
		{"--list", "rust-build"}, {"--list", "list"}, {"--list", "--markdown"},
		{"list", "--markdown"}, {"list", "rust-build"}, {"--list=false"}, {"--list", "--cpu", "1"},
	} {
		var out, stderr bytes.Buffer
		code := Main(append([]string{"--home", home, "agents", "card"}, args...), strings.NewReader(""), &out, &stderr)
		if code == 0 || out.Len() != 0 || !json.Valid(stderr.Bytes()) {
			t.Fatal("invalid catalog invocation did not fail cleanly", args, code, out.String(), stderr.String())
		}
	}
	for _, args := range [][]string{{"agents", "card", "--help"}, {"help", "agents", "card"}} {
		var out, stderr bytes.Buffer
		if code := Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &stderr); code != 0 || stderr.Len() != 0 || !strings.Contains(out.String(), "--list") || !strings.Contains(out.String(), "card list") {
			t.Fatal("offline card help omits discovery", args, code, out.String(), stderr.String())
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("catalog errors/help created state", err)
	}
}

func TestAgentGuideReflectsCurrentAuthorityAndStorageBoundaries(t *testing.T) {
	var out, stderr bytes.Buffer
	if code := Main([]string{"--home", filepath.Join(t.TempDir(), "absent"), "agents", "--json"}, strings.NewReader(""), &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	var guide agentGuide
	if err := json.Unmarshal(out.Bytes(), &guide); err != nil {
		t.Fatal(err)
	}
	if guide.CardListCommand != "arm agents card --list --json" {
		t.Fatal("guide omits canonical catalog command", guide.CardListCommand)
	}
	sections := map[string]string{}
	for _, section := range guide.Sections {
		sections[section.ID] = strings.Join(section.Guidance, "\n") + "\n" + strings.Join(section.Commands, "\n")
	}
	for id, fragments := range map[string][]string{
		"orientation": {"current OS headroom", "not kernel hard limits", "stale/unknown"},
		"leases":      {"lost response does not prove failure", "--key OPERATION_ID", "Remote single-lease"},
		"storage":     {"independent explicit export", "shorter TTL", "does not release its source", "storage exports", "shared caches"},
		"pools":       {"mesh refuses remote pool bundles", "does not replicate global capacity"},
		"remote":      {"reverse permissions default to none", "not a sandbox", "absolute destination-local", "Remote broadcasts are refused", "mesh message-lookup", "not as local operator policy"},
		"inbox":       {"watcher does not guarantee live injection", "Reading/watching is not receipt or acknowledgement", "--after CURSOR"},
	} {
		for _, fragment := range fragments {
			if !strings.Contains(sections[id], fragment) {
				t.Errorf("section %s omits %q", id, fragment)
			}
		}
	}
	if strings.Contains(out.String(), "multi-host transport is planned") || strings.Contains(out.String(), "This build supports one local host authority") || strings.Contains(sections["storage"], "target/release/") {
		t.Fatal("guide advertises obsolete boundaries or embeds detailed Rust export policy")
	}
}
