package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type recordLine struct {
	label string
	path  string
	value any
	depth int
	more  bool
}

type recordTree struct {
	value    any
	label    string
	expanded map[string]bool
	limits   map[string]int
	lines    []recordLine
	selected int
}

func newRecord(value any, label string) (*recordTree, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(raw) > 4<<20 {
		return nil, fmt.Errorf("record exceeds the 4 MiB display boundary")
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	t := &recordTree{value: decoded, label: label, expanded: map[string]bool{"[]": true}, limits: map[string]int{}}
	t.rebuild()
	return t, nil
}

func recordChildCount(value any) int {
	switch value := value.(type) {
	case map[string]any:
		return len(value)
	case []any:
		return len(value)
	}
	return 0
}

func recordChildren(value any, limit int) []struct {
	key string
	val any
} {
	var children []struct {
		key string
		val any
	}
	switch value := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys[:min(limit, len(keys))] {
			children = append(children, struct {
				key string
				val any
			}{key, value[key]})
		}
	case []any:
		for i, item := range value[:min(limit, len(value))] {
			children = append(children, struct {
				key string
				val any
			}{strconv.Itoa(i), item})
		}
	}
	return children
}

func (t *recordTree) rebuild() {
	t.lines = nil
	var walk func(any, string, []string, int)
	walk = func(value any, label string, path []string, depth int) {
		if len(t.lines) >= 5000 || depth > 64 {
			return
		}
		key, _ := json.Marshal(path)
		line := recordLine{label: label, path: string(key), value: value, depth: depth}
		t.lines = append(t.lines, line)
		if !t.expanded[line.path] {
			return
		}
		limit := t.limits[line.path]
		if limit == 0 {
			limit = 100
		}
		for _, child := range recordChildren(value, limit) {
			p := append(append([]string{}, path...), child.key)
			walk(child.val, child.key, p, depth+1)
		}
		if count := recordChildCount(value); count > limit && len(t.lines) < 5000 {
			t.lines = append(t.lines, recordLine{label: fmt.Sprintf("Show next %d of %d remaining entries", min(100, count-limit), count-limit), path: line.path, depth: depth + 1, more: true})
		}
	}
	walk(t.value, t.label, []string{}, 0)
	t.selected = max(0, min(t.selected, len(t.lines)-1))
}

func (t *recordTree) toggle() {
	if len(t.lines) == 0 {
		return
	}
	line := t.lines[t.selected]
	if line.more {
		limit := t.limits[line.path]
		if limit == 0 {
			limit = 100
		}
		t.limits[line.path] = limit + 100
	} else {
		t.expanded[line.path] = !t.expanded[line.path]
	}
	t.rebuild()
}

func (t *recordTree) text(width int) string {
	var out []string
	for i, line := range t.lines {
		prefix := "  "
		if i == t.selected {
			prefix = "> "
		}
		indent := strings.Repeat("  ", min(20, line.depth))
		var value string
		switch item := line.value.(type) {
		case map[string]any:
			value = fmt.Sprintf("{…} %d fields", len(item))
		case []any:
			value = fmt.Sprintf("[…] %d items", len(item))
		case string:
			value = strconv.Quote(item)
		case nil:
			value = "null"
		default:
			value = fmt.Sprint(item)
		}
		if line.more {
			value = ""
		}
		label := line.label
		if recordChildCount(line.value) > 0 {
			if t.expanded[line.path] {
				label = "▾ " + label
			} else {
				label = "▸ " + label
			}
		}
		out = append(out, short(prefix+indent+label+": "+value, width))
	}
	if len(t.lines) >= 5000 {
		out = append(out, short("Display limit reached; inspect the full record with arm inspect --json.", width))
	}
	return strings.Join(out, "\n")
}
