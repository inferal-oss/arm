package install

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// FileChange describes the exact byte transformation, including existence and
// final-newline changes. Installer edits affect one contiguous managed block.
type FileChange struct {
	BeforeExists bool   `json:"before_exists"`
	AfterExists  bool   `json:"after_exists"`
	BeforeHash   string `json:"before_sha256,omitempty"`
	AfterHash    string `json:"after_sha256,omitempty"`
	Added        int    `json:"added_lines"`
	Removed      int    `json:"removed_lines"`
	BeforeBytes  int    `json:"before_bytes"`
	AfterBytes   int    `json:"after_bytes"`
	Binary       bool   `json:"binary"`
	Diff         string `json:"diff"`
}

func fileChange(path string, before, after []byte, beforeExists, afterExists bool) *FileChange {
	if beforeExists == afterExists && bytes.Equal(before, after) {
		return nil
	}
	c := &FileChange{BeforeExists: beforeExists, AfterExists: afterExists, BeforeBytes: len(before), AfterBytes: len(after)}
	if beforeExists {
		c.BeforeHash = digest(before)
	}
	if afterExists {
		c.AfterHash = digest(after)
	}
	lines := func(b []byte) []string {
		if len(b) == 0 {
			return nil
		}
		v := strings.SplitAfter(string(b), "\n")
		if v[len(v)-1] == "" {
			v = v[:len(v)-1]
		}
		return v
	}
	a, b := lines(before), lines(after)
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	c.Removed = len(a) - prefix - suffix
	c.Added = len(b) - prefix - suffix
	var out strings.Builder
	oldName, newName := path, path
	if !beforeExists {
		oldName = "/dev/null"
	}
	if !afterExists {
		newName = "/dev/null"
	}
	quote := func(path string) string {
		if path == "/dev/null" {
			return path
		}
		return strconv.Quote(path)
	}
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", quote(oldName), quote(newName))
	if !utf8.Valid(before) || !utf8.Valid(after) {
		c.Binary = true
		out.WriteString("Non-UTF-8 content differs; textual hunks are unavailable. SHA-256 hashes identify the exact byte transformation.\n")
		c.Diff = out.String()
		return c
	}
	if len(a) == 0 && len(b) == 0 {
		c.Diff = out.String()
		return c
	}
	start := max(0, prefix-3)
	oldEnd := min(len(a), len(a)-suffix+3)
	newEnd := min(len(b), len(b)-suffix+3)
	oldStart, newStart := start+1, start+1
	if oldEnd == start {
		oldStart = start
	}
	if newEnd == start {
		newStart = start
	}
	fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", oldStart, oldEnd-start, newStart, newEnd-start)
	writeLine := func(marker byte, text string) {
		out.WriteByte(marker)
		out.WriteString(text)
		if !strings.HasSuffix(text, "\n") {
			out.WriteString("\n\\ No newline at end of file\n")
		}
	}
	for _, line := range a[start:prefix] {
		writeLine(' ', line)
	}
	for _, line := range a[prefix : len(a)-suffix] {
		writeLine('-', line)
	}
	for _, line := range b[prefix : len(b)-suffix] {
		writeLine('+', line)
	}
	for _, line := range a[len(a)-suffix : oldEnd] {
		writeLine(' ', line)
	}
	c.Diff = out.String()
	return c
}
