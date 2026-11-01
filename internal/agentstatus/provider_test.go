package agentstatus

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestProviderToolFreePinnedAndBounded(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			c := DefaultConfig()
			c.Enabled = true
			c.CredentialEnv = "TEST_SUMMARY_KEY"
			c.Provider = provider
			if provider == "anthropic" {
				c.Model = ClaudeModel
			}
			p := NewProvider()
			calls := 0
			p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				if strings.Contains(string(raw), "resume") || !strings.Contains(string(raw), `"tools":[]`) {
					t.Fatal("unsafe request")
				}
				if r.URL.Scheme != "https" || r.URL.Host != "api.openai.com" && r.URL.Host != "api.anthropic.com" {
					t.Fatal("unapproved endpoint")
				}
				body := `{"model":"gpt-6.1-terra","status":"completed","usage":{"input_tokens":100,"output_tokens":30},"output":[{"type":"message","content":[{"type":"output_text","text":"<h2>Work</h2><p>Reported investigation</p>"}]}]}`
				if provider == "anthropic" {
					body = `{"model":"claude-sonnet-5-5","stop_reason":"end_turn","usage":{"input_tokens":100,"output_tokens":30},"content":[{"type":"text","text":"<h2>Work</h2><p>Reported investigation</p>"}]}`
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			doc, usage, err := p.Generate(context.Background(), c, "synthetic-key", "synthetic account")
			if err != nil || !strings.Contains(doc, "Reported investigation") || usage.Output != 30 || calls != 1 {
				t.Fatal("generation failed", err)
			}
			if _, _, err = p.Generate(context.Background(), c, "synthetic-key", strings.Repeat("x", MaxPromptBytes+1)); err == nil || calls != 1 {
				t.Fatal("oversized input dispatched")
			}
		})
	}
}
func TestProviderDoesNotEchoErrorsOrRetry(t *testing.T) {
	p := NewProvider()
	calls := 0
	p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("private provider details"))}, nil
	})
	c := DefaultConfig()
	c.Enabled = true
	c.CredentialEnv = "TEST_SUMMARY_KEY"
	_, _, err := p.Generate(context.Background(), c, "synthetic-key", "account")
	if err == nil || strings.Contains(err.Error(), "private") || calls != 1 {
		t.Fatal("unsafe failure", err)
	}
}

func TestProviderRequiresUsageAndVisibleReport(t *testing.T) {
	for _, kind := range []string{"missing_usage", "missing_counter", "empty", "sanitized_empty", "tag_only"} {
		t.Run(kind, func(t *testing.T) {
			result := map[string]any{"model": CodexModel, "status": "completed", "usage": map[string]int{"input_tokens": 100, "output_tokens": 10}}
			text := "<p>Reported work</p>"
			switch kind {
			case "missing_usage":
				delete(result, "usage")
			case "missing_counter":
				result["usage"] = map[string]int{"input_tokens": 100}
			case "empty":
				text = ""
			case "sanitized_empty":
				text = "<script>untrusted</script>"
			case "tag_only":
				text = "<section class=\"card\"><p> </p></section>"
			}
			result["output"] = []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": text}}}}
			raw, _ := json.Marshal(result)
			p := NewProvider()
			p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
			})
			config := DefaultConfig()
			config.Enabled = true
			config.CredentialEnv = "SYNTHETIC_KEY"
			doc, usage, err := p.Generate(t.Context(), config, "synthetic-key", "synthetic account")
			if err == nil || doc != "" {
				t.Fatal("unsupported evidence produced a ready report")
			}
			if kind == "missing_usage" || kind == "missing_counter" {
				encoded, _ := json.Marshal(usage)
				if strings.Contains(string(encoded), "input_tokens") || strings.Contains(string(encoded), "estimated_usd") {
					t.Fatal("missing usage was represented as zero-cost evidence")
				}
			}
		})
	}
}
