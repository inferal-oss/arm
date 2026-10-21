package knowledge

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
)

const fixture = `---
schema_version: 1
id: rust-build
title: "Rust builds and temporary artifacts"
tags: [rust, cargo]
resources: [cpu, storage]
adapter: rust-build
---
# Rust builds

Only explain implemented behavior.
`

func TestEmbeddedCatalogMatchesRegisteredImplementations(t *testing.T) {
	cards, err := Load()
	if err != nil || len(cards) == 0 {
		t.Fatal(cards, err)
	}
	found := false
	for _, card := range cards {
		if card.ID == "rust-build" {
			found = true
			for _, requirement := range []string{"export", "short", "--dry-run", "shared", "not implemented", "CARGO_BUILD_JOBS"} {
				if !strings.Contains(card.Markdown, requirement) {
					t.Errorf("Rust card omits cleanup/implementation boundary %q", requirement)
				}
			}
		}
	}
	if !found {
		t.Fatal("missing Rust knowledge card")
	}
}

func TestGeneratedIndexCannotDriftFromCardSources(t *testing.T) {
	cards, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	data, err := SourceIndex()
	if err != nil || validateIndex(cards, data) != nil {
		t.Fatal("generator does not produce a valid index", err)
	}
	for _, bad := range []string{"[]", "null", "{}", string(data) + " []", strings.Replace(string(data), `"id": "rust-build"`, `"id": "not-the-card"`, 1)} {
		if err := validateIndex(cards, []byte(bad)); err == nil {
			t.Fatal("stale/invalid index accepted", bad)
		}
	}
	var unknown []map[string]any
	if err := json.Unmarshal(data, &unknown); err != nil {
		t.Fatal(err)
	}
	unknown[0]["executable"] = "not metadata"
	bad, _ := json.Marshal(unknown)
	if err := validateIndex(cards, bad); err == nil {
		t.Fatal("unknown index fields accepted")
	}
}

func TestCatalogRejectsInvalidMetadataAndUnboundCards(t *testing.T) {
	cases := map[string]string{
		"missing frontmatter": strings.TrimPrefix(fixture, "---\n"),
		"missing body":        strings.Split(fixture, "# Rust builds")[0],
		"missing field":       strings.Replace(fixture, "adapter: rust-build\n", "", 1),
		"unknown field":       strings.Replace(fixture, "adapter: rust-build", "adapter: rust-build\nexecute: rm", 1),
		"duplicate field":     strings.Replace(fixture, "id: rust-build", "id: rust-build\nid: rust-build", 1),
		"schema mismatch":     strings.Replace(fixture, "schema_version: 1", "schema_version: 2", 1),
		"title type":          strings.Replace(fixture, `title: "Rust builds and temporary artifacts"`, "title: [rust]", 1),
		"title boolean":       strings.Replace(fixture, `title: "Rust builds and temporary artifacts"`, "title: true", 1),
		"title null":          strings.Replace(fixture, `title: "Rust builds and temporary artifacts"`, "title: null", 1),
		"title number":        strings.Replace(fixture, `title: "Rust builds and temporary artifacts"`, "title: 42", 1),
		"empty title":         strings.Replace(fixture, `title: "Rust builds and temporary artifacts"`, `title: ""`, 1),
		"unknown resource":    strings.Replace(fixture, "[cpu, storage]", "[cpu, unicorns]", 1),
		"duplicate resource":  strings.Replace(fixture, "[cpu, storage]", "[cpu, cpu]", 1),
		"invalid resource":    strings.Replace(fixture, "[cpu, storage]", "[[cpu], storage]", 1),
		"empty tags":          strings.Replace(fixture, "[rust, cargo]", "[]", 1),
		"duplicate tags":      strings.Replace(fixture, "[rust, cargo]", "[rust, rust]", 1),
		"invalid tag type":    strings.Replace(fixture, "[rust, cargo]", "[rust, 123]", 1),
		"invalid id":          strings.Replace(fixture, "id: rust-build", "id: ../rust", 1),
		"quoted newline":      strings.Replace(fixture, `title: "Rust builds and temporary artifacts"`, `title: "Rust\nBuild"`, 1),
		"control body":        fixture + "\x1b[2J",
		"invalid utf8":        fixture + "\xff",
		"oversize":            fixture + strings.Repeat("x", 64<<10),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := load(fstest.MapFS{"cards/rust.md": &fstest.MapFile{Data: []byte(data)}}, func(string) bool { return true }); err == nil {
				t.Fatal("invalid metadata accepted")
			}
		})
	}
	files := fstest.MapFS{"cards/rust.md": &fstest.MapFile{Data: []byte(fixture)}}
	if _, err := load(files, func(string) bool { return false }); err == nil || !strings.Contains(err.Error(), "registered implementation") {
		t.Fatal("missing code binding accepted", err)
	}
	files["cards/copy.md"] = &fstest.MapFile{Data: []byte(fixture)}
	if _, err := load(files, func(string) bool { return true }); err == nil || !strings.Contains(err.Error(), "duplicate card") {
		t.Fatal("duplicate ID accepted", err)
	}
	if _, err := load(fstest.MapFS{}, func(string) bool { return true }); err == nil {
		t.Fatal("empty catalog accepted")
	}
}

func TestCatalogSupportsDocumentedQuotedMetadataAndStableOrdering(t *testing.T) {
	first := strings.Replace(fixture, "id: rust-build", "id: alpha", 1)
	first = strings.Replace(first, `title: "Rust builds and temporary artifacts"`, "title: 'Cargo''s build artifacts'", 1)
	first = strings.Replace(first, "[rust, cargo]", `['rust', "cargo"]`, 1)
	cards, err := load(fstest.MapFS{
		"cards/z.md": &fstest.MapFile{Data: []byte(first)},
		"cards/a.md": &fstest.MapFile{Data: []byte(strings.ReplaceAll(fixture, "\n", "\r\n"))},
	}, func(s string) bool { return s == "rust-build" })
	if err != nil || len(cards) != 2 || cards[0].ID != "alpha" || cards[0].Title != "Cargo's build artifacts" || cards[1].ID != "rust-build" || strings.Contains(cards[1].Markdown, "\r") {
		t.Fatal("invalid stable/quoted catalog", cards, err)
	}
}
