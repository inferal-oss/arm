package agentstatus

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDocumentIsolation(t *testing.T) {
	doc, err := Document(`<div class="grid evil" style="background:url(https://example.invalid)" onclick="alert(1)"><section class="card"><h2>Work</h2><p>Build &amp; verify</p><a href="javascript:alert(1)">Link text</a><svg><script>alert(1)</script></svg><iframe src="/v1"></iframe><form action="/cancel">Submit</form><img src="https://example.invalid"></section></div>`)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"onclick", "javascript:", "https://example.invalid", "<svg", "<iframe", "<form", "<img", "class=\"grid evil\"", "Submit"} {
		if strings.Contains(doc, bad) {
			t.Fatalf("unsafe content retained: %s", bad)
		}
	}
	for _, good := range []string{`class="grid"`, `class="card"`, "Build &amp; verify", "Link text", "script-src &#39;none&#39;", "style-src &#39;sha256-"} {
		if !strings.Contains(doc, good) {
			t.Fatalf("missing %s", good)
		}
	}
}

func TestDocumentBounds(t *testing.T) {
	for _, raw := range []string{"", strings.Repeat("x", MaxHTMLBytes+1), strings.Repeat("<div>", 35) + "deep" + strings.Repeat("</div>", 35), strings.Repeat("<br>", 2050), "<script>bad</script>"} {
		if _, err := Document(raw); err == nil {
			t.Fatal("unbounded/empty document accepted")
		}
	}
}

func TestDocumentExpansionAndCanonicalEnvelope(t *testing.T) {
	for _, raw := range []string{strings.Repeat("\"", MaxHTMLBytes), strings.Repeat("&", MaxHTMLBytes), strings.Repeat("'", MaxHTMLBytes)} {
		if _, err := Document(raw); err == nil {
			t.Fatal("expanded full-document limit bypassed")
		}
	}
	doc, err := Document(`<section class="card"><p>Question &amp; answer: &quot;unknown&quot;</p></section>`)
	if err != nil || len(doc) > MaxDocumentBytes || ValidateDocument(doc) != nil {
		t.Fatal("canonical document rejected", err)
	}
	encoded, _ := json.Marshal(doc)
	if len(encoded) > 6*MaxDocumentBytes+2 {
		t.Fatal("serialized HTML exceeded reservation proof")
	}
}

func TestAccountStrictness(t *testing.T) {
	for _, raw := range []string{`{"version":1,"current":["working"],"extra":true}`, `{"version":2}`, `{"version":1} {}`, `{"version":1,"notes":[""]}`, strings.Repeat("x", MaxAccountBytes+1)} {
		if _, err := DecodeAccount(raw); err == nil {
			t.Fatal("invalid account accepted")
		}
	}
	a, err := DecodeAccount(`{"version":1,"request_id":"11111111111111111111111111111111","current":["Investigating; completion unknown"],"planned":["Review"],"notes":[],"questions":[],"blockers":[]}`)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := SynthesisPrompt(a)
	if err != nil || !strings.Contains(prompt, "completion unknown") {
		t.Fatal("lost uncertainty", err)
	}
}
