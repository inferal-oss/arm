// Package knowledge ships explanatory workload cards. Detection, execution and
// ownership checks remain implemented by the code registered for each adapter.
package knowledge

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yrashk/arm/internal/manager"
)

//go:embed cards/*.md
var embedded embed.FS

//go:generate go run ../scripts/check-knowledge --write-index index.json
//go:embed index.json
var embeddedIndex []byte

type Metadata struct {
	SchemaVersion int      `json:"schema_version"`
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Tags          []string `json:"tags"`
	Resources     []string `json:"resources"`
	Adapter       string   `json:"adapter"`
}

type Card struct {
	Metadata
	Markdown string `json:"markdown"`
}

var identifier = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

// Load validates the embedded documentation and implementation bindings. It is
// also run by the build gate; retrieving a card never starts a backend.
func Load() ([]Card, error) {
	cards, err := load(embedded, manager.SupportsProfile)
	if err != nil {
		return nil, err
	}
	if err := validateIndex(cards, embeddedIndex); err != nil {
		return nil, err
	}
	return cards, nil
}

// SourceIndex generates metadata from card sources without trusting a previous
// index. The development generator uses it; ordinary builds only validate.
func SourceIndex() ([]byte, error) {
	cards, err := load(embedded, manager.SupportsProfile)
	if err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(metadata(cards), "", "  ")
	return append(data, '\n'), err
}

func metadata(cards []Card) []Metadata {
	index := make([]Metadata, 0, len(cards))
	for _, card := range cards {
		index = append(index, card.Metadata)
	}
	return index
}

func validateIndex(cards []Card, data []byte) error {
	var index []Metadata
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&index); err != nil {
		return fmt.Errorf("invalid embedded knowledge index: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("embedded knowledge index contains trailing content")
	}
	expected, _ := json.Marshal(metadata(cards))
	actual, _ := json.Marshal(index)
	if string(actual) != string(expected) {
		return fmt.Errorf("embedded knowledge index is stale; run go generate ./knowledge")
	}
	return nil
}

func load(files fs.FS, supports func(string) bool) ([]Card, error) {
	paths, err := fs.Glob(files, "cards/*.md")
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("knowledge catalog contains no cards")
	}
	cards := make([]Card, 0, len(paths))
	seen := make(map[string]bool)
	for _, path := range paths {
		data, err := fs.ReadFile(files, path)
		if err != nil {
			return nil, err
		}
		card, err := parse(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if seen[card.ID] {
			return nil, fmt.Errorf("%s: duplicate card ID %q", path, card.ID)
		}
		if !supports(card.Adapter) {
			return nil, fmt.Errorf("%s: adapter %q has no registered implementation", path, card.Adapter)
		}
		seen[card.ID] = true
		cards = append(cards, card)
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].ID < cards[j].ID })
	return cards, nil
}

// The frontmatter intentionally accepts a small YAML subset: top-level string
// scalars (plain, JSON double-quoted or YAML single-quoted), schema_version: 1,
// and flow sequences of string scalars. No anchors, tags, executable values,
// implicit types, nested mappings or multiline scalars are interpreted.
func parse(data []byte) (Card, error) {
	var card Card
	if len(data) > 64<<10 || !utf8.Valid(data) {
		return card, fmt.Errorf("card must be UTF-8 and at most 64 KiB")
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return card, fmt.Errorf("YAML frontmatter is required")
	}
	header, body, ok := strings.Cut(text[4:], "\n---\n")
	if !ok || strings.TrimSpace(body) == "" {
		return card, fmt.Errorf("frontmatter needs a closing delimiter and Markdown body")
	}
	fields := make(map[string]string)
	for _, line := range strings.Split(header, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found || key != strings.TrimSpace(key) || fields[key] != "" {
			return card, fmt.Errorf("invalid or duplicate metadata key %q", key)
		}
		switch key {
		case "schema_version", "id", "title", "tags", "resources", "adapter":
		default:
			return card, fmt.Errorf("unknown metadata key %q", key)
		}
		if value = strings.TrimSpace(value); value == "" {
			return card, fmt.Errorf("metadata %s cannot be empty", key)
		}
		fields[key] = value
	}
	if len(fields) != 6 || fields["schema_version"] != "1" {
		return card, fmt.Errorf("schema_version must be 1 and all six metadata fields are required")
	}
	card.SchemaVersion = 1
	var err error
	if card.ID, err = scalar(fields["id"]); err != nil || !validID(card.ID) {
		return card, fmt.Errorf("invalid card ID")
	}
	if card.ID == "list" {
		return card, fmt.Errorf("card ID %q is reserved for the arm agents card list command", card.ID)
	}
	if card.Title, err = scalar(fields["title"]); err != nil || len(card.Title) > 200 {
		return card, fmt.Errorf("invalid title; use a nonempty string up to 200 bytes")
	}
	if card.Adapter, err = scalar(fields["adapter"]); err != nil || !validID(card.Adapter) {
		return card, fmt.Errorf("invalid adapter ID")
	}
	if card.Tags, err = sequence(fields["tags"]); err != nil {
		return card, fmt.Errorf("invalid tags: %w", err)
	}
	if card.Resources, err = sequence(fields["resources"]); err != nil {
		return card, fmt.Errorf("invalid resources: %w", err)
	}
	for _, resource := range card.Resources {
		switch resource {
		case "cpu", "memory", "storage", "gpu", "network", "ports", "custom":
		default:
			return card, fmt.Errorf("unknown resource %q", resource)
		}
	}
	for _, r := range body {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return card, fmt.Errorf("Markdown body contains control characters")
		}
	}
	card.Markdown = strings.Trim(body, "\n") + "\n"
	return card, nil
}

func validID(s string) bool {
	return len(s) <= 64 && identifier.MatchString(s)
}

func scalar(s string) (string, error) {
	if strings.HasPrefix(s, `"`) {
		if err := json.Unmarshal([]byte(s), &s); err != nil {
			return "", fmt.Errorf("invalid quoted string")
		}
	} else if strings.HasPrefix(s, "'") {
		if len(s) < 2 || !strings.HasSuffix(s, "'") {
			return "", fmt.Errorf("unterminated quoted string")
		}
		value := s[1 : len(s)-1]
		if strings.Contains(strings.ReplaceAll(value, "''", ""), "'") {
			return "", fmt.Errorf("single quotes inside a string must be doubled")
		}
		s = strings.ReplaceAll(value, "''", "'")
	} else {
		first, _ := utf8.DecodeRuneInString(s)
		if !unicode.IsLetter(first) || strings.ContainsAny(s, "[]{}&*!|>#\"'`,") || strings.Contains(s, ": ") {
			return "", fmt.Errorf("use a quoted string for YAML punctuation or a non-letter prefix")
		}
		switch strings.ToLower(s) {
		case "true", "false", "null":
			return "", fmt.Errorf("quote strings that resemble YAML booleans or null")
		}
	}
	if strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("empty string")
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return "", fmt.Errorf("metadata contains control characters")
		}
	}
	return s, nil
}

func sequence(s string) ([]string, error) {
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return nil, fmt.Errorf("use a flow sequence such as [rust, cargo]")
	}
	// IDs never contain commas; accepting quoted IDs needs no general YAML
	// tokenization and prevents nested structures from becoming metadata.
	values := strings.Split(s[1:len(s)-1], ",")
	if len(values) > 16 {
		return nil, fmt.Errorf("at most 16 values are supported")
	}
	seen := make(map[string]bool)
	for i, value := range values {
		value, err := scalar(strings.TrimSpace(value))
		if err != nil || !validID(value) || seen[value] {
			return nil, fmt.Errorf("values must be unique nonempty lowercase identifiers")
		}
		seen[value] = true
		values[i] = value
	}
	return values, nil
}
