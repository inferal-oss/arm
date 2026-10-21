package tui

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRecordTreeExpandsIndependentlyAndPreservesExactNumbers(t *testing.T) {
	tree, err := newRecord(map[string]any{"capacity": json.Number("9223372036854775807"), "nested": map[string]any{"literal": "\x1b]52;c;unsafe\a"}}, "Lease record")
	if err != nil {
		t.Fatal(err)
	}
	text := tree.text(120)
	if !strings.Contains(text, "9223372036854775807") || strings.Contains(text, "literal") {
		t.Fatal("record tree lost exact values or eagerly expanded nested data", text)
	}
	for i, line := range tree.lines {
		if line.label == "nested" {
			tree.selected = i
			break
		}
	}
	tree.toggle()
	text = tree.text(120)
	if !strings.Contains(text, "literal") || strings.ContainsRune(text, '\x1b') {
		t.Fatal("expanded record interpreted terminal commands", text)
	}
	tree.toggle()
	if strings.Contains(tree.text(120), "literal") {
		t.Fatal("collapse failed")
	}
}

func TestRecordTreePagesLargeArraysAndBoundsVisibleNodes(t *testing.T) {
	values := make([]int, 10000)
	tree, err := newRecord(values, "Large record")
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.lines) != 102 || !tree.lines[len(tree.lines)-1].more {
		t.Fatal("array was not lazy/paginated", len(tree.lines))
	}
	tree.selected = len(tree.lines) - 1
	tree.toggle()
	if len(tree.lines) != 202 {
		t.Fatal("next page failed", len(tree.lines))
	}
	tree.limits["[]"] = 10000
	tree.rebuild()
	if len(tree.lines) > 5000 || !strings.Contains(tree.text(120), "Display limit") {
		t.Fatal("visible record node bound was exceeded", len(tree.lines))
	}
}

func TestRecordPathsDoNotCollideForPunctuationOrNestedKeys(t *testing.T) {
	tree, err := newRecord(map[string]any{"a.b": map[string]any{"one": 1}, "a": map[string]any{"b": 2}}, "Record")
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, line := range tree.lines {
		if paths[line.path] {
			t.Fatal("record paths collided", line.path)
		}
		paths[line.path] = true
	}
}
