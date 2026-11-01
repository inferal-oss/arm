package agentstatus

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"html"
	"strings"
	"unicode"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const reportCSS = `:root{color-scheme:light dark;font:16px/1.5 system-ui,sans-serif}body{margin:0;padding:1rem;overflow-wrap:anywhere}h1,h2,h3{line-height:1.2}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,16rem),1fr));gap:1rem}.card{border:1px solid #8886;border-radius:.6rem;padding:1rem}.timeline{border-inline-start:3px solid #8886;padding-inline-start:1.5rem}.badge{border:1px solid #8886;border-radius:1rem;padding:.15rem .5rem}.muted{opacity:.75}table{border-collapse:collapse;max-width:100%}th,td{padding:.4rem;border:1px solid #8886;text-align:start}pre{white-space:pre-wrap}`

func StyleHash() string {
	digest := sha256.Sum256([]byte(reportCSS))
	return "sha256-" + base64.StdEncoding.EncodeToString(digest[:])
}

const MaxDocumentBytes = 32768

// JSON string encoding expands an arbitrary UTF-8 byte by at most six bytes.
// The additional 8 KiB covers bounded usage/error fields and timestamp/keys.
const FinalizationReserve = 6*MaxDocumentBytes + 8192

type boundedHTML struct {
	bytes.Buffer
	limit int
	err   error
}

func (b *boundedHTML) WriteString(s string) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	if len(s) > b.limit-b.Len() {
		b.err = fmt.Errorf("full report document exceeds 32768 bytes")
		return 0, b.err
	}
	return b.Buffer.WriteString(s)
}
func documentShell() (string, string) {
	csp := "default-src 'none'; script-src 'none'; style-src '" + StyleHash() + "'; connect-src 'none'; img-src 'none'; font-src 'none'; media-src 'none'; object-src 'none'; frame-src 'none'; base-uri 'none'; form-action 'none'"
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="` + html.EscapeString(csp) + `"><meta name="referrer" content="no-referrer"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Agent contribution</title><style>` + reportCSS + `</style></head><body>`, `</body></html>`
}
func ValidateDocument(document string) error {
	prefix, suffix := documentShell()
	if len(document) > MaxDocumentBytes || !strings.HasPrefix(document, prefix) || !strings.HasSuffix(document, suffix) {
		return fmt.Errorf("invalid static report envelope")
	}
	rewritten, err := Document(document[len(prefix) : len(document)-len(suffix)])
	if err != nil || rewritten != document {
		return fmt.Errorf("noncanonical static report")
	}
	return nil
}

// Document parses and rewrites model HTML. Every original attribute except a
// finite class vocabulary is removed; SVG/MathML and active subtrees are dropped.
// Defense in depth: the caller MUST also use an iframe sandbox with no tokens.
func Document(fragment string) (string, error) {
	if len(fragment) == 0 || len(fragment) > MaxHTMLBytes || !utf8.ValidString(fragment) {
		return "", fmt.Errorf("report requires 1–32768 UTF-8 bytes")
	}
	root := &xhtml.Node{Type: xhtml.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := xhtml.ParseFragment(strings.NewReader(fragment), root)
	if err != nil {
		return "", err
	}
	allowed := map[string]bool{}
	for _, name := range strings.Fields("div section article header footer main h1 h2 h3 h4 p ul ol li dl dt dd table thead tbody tr th td caption strong em b i small span code pre blockquote hr br") {
		allowed[name] = true
	}
	drop := map[string]bool{}
	for _, name := range strings.Fields("script style iframe object embed template svg math form input button select textarea link meta base img video audio source canvas") {
		drop[name] = true
	}
	classes := map[string]bool{"grid": true, "card": true, "timeline": true, "badge": true, "muted": true}
	prefix, suffix := documentShell()
	out := boundedHTML{limit: MaxDocumentBytes - len(prefix) - len(suffix)}
	count := 0
	visible := false
	var walk func(*xhtml.Node, int) error
	walk = func(n *xhtml.Node, depth int) error {
		count++
		if count > 2048 || depth > 32 {
			return fmt.Errorf("report structure exceeds limit")
		}
		if n.Type == xhtml.TextNode {
			for _, r := range n.Data {
				if unicode.IsGraphic(r) && !unicode.IsSpace(r) {
					visible = true
					break
				}
			}
			out.WriteString(html.EscapeString(n.Data))
			return out.err
		}
		if n.Type != xhtml.ElementNode {
			return nil
		}
		if n.Namespace != "" || drop[n.Data] {
			return nil
		}
		ok := allowed[n.Data]
		if ok {
			out.WriteString("<" + n.Data)
			for _, attr := range n.Attr {
				if attr.Namespace == "" && attr.Key == "class" {
					var safe []string
					for _, v := range strings.Fields(attr.Val) {
						if classes[v] {
							safe = append(safe, v)
						}
					}
					if len(safe) > 0 {
						out.WriteString(` class="` + strings.Join(safe, " ") + `"`)
					}
					break
				}
			}
			out.WriteString(">")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if err := walk(c, depth+1); err != nil {
				return err
			}
		}
		if ok && n.Data != "br" && n.Data != "hr" {
			out.WriteString("</" + n.Data + ">")
		}
		return out.err
	}
	for _, n := range nodes {
		if err = walk(n, 0); err != nil {
			return "", err
		}
	}
	if !visible || strings.TrimSpace(out.String()) == "" {
		return "", fmt.Errorf("report has no safe content")
	}
	return prefix + out.String() + suffix, nil
}
