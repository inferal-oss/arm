package agentstatus

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCurrentWorkPreviewPreservesSourceAndByteBound(t *testing.T) {
	current := "\x1b\u202e  Reported\n work " + strings.Repeat("界", 90)
	a := &Account{Current: []string{current, "second item is not projected"}}
	preview := CurrentWorkPreview(a)
	if len(preview) > 160 || !utf8.ValidString(preview) || !strings.HasSuffix(preview, "…") || !strings.HasPrefix(preview, "Reported work ") {
		t.Fatal("unbounded or malformed preview", preview)
	}
	if a.Current[0] != current || a.Current[1] != "second item is not projected" || strings.ContainsAny(preview, "\x1b\u202e\n") {
		t.Fatal("source changed or control characters retained")
	}
	if CurrentWorkPreview(nil) != "" || CurrentWorkPreview(&Account{}) != "" || CurrentWorkPreview(&Account{Current: []string{" first ", "second"}}) != "first" {
		t.Fatal("missing or multiple account items invented a preview")
	}
}
