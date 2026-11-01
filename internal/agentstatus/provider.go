package agentstatus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const MaxPromptBytes = 12 << 10
const OutputTokens = 2048

// Provider executes one stateless tool-free summarizer turn, never an owning
// agent's session. The only production destinations are fixed official APIs.
type Provider struct{ client *http.Client }

func (p *Provider) Close() { p.client.CloseIdleConnections() }

func NewProvider() *Provider {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxResponseHeaderBytes = 16 << 10
	transport.DisableCompression = true
	return &Provider{client: &http.Client{Transport: transport, Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (p *Provider) Generate(ctx context.Context, c Config, credential, prompt string) (string, *Usage, error) {
	if err := c.Validate(); err != nil {
		return "", nil, err
	}
	if !c.Enabled || credential == "" {
		return "", nil, fmt.Errorf("summary provider is disabled or its explicit credential is unavailable")
	}
	if len(prompt) > MaxPromptBytes {
		return "", nil, fmt.Errorf("summary input exceeds 12 KiB")
	}
	inputRate, outputRate := 1.0, 5.0
	if c.Provider == "anthropic" {
		inputRate, outputRate = 2, 10
	}
	// This is an estimate, not a guaranteed provider billing cap. Count every
	// input byte as a token conservatively; output includes provider reasoning.
	estimate := (float64(len(prompt))*inputRate + OutputTokens*outputRate) / 1e6
	if estimate > c.EstimatedAttemptUSD {
		return "", nil, fmt.Errorf("estimated attempt budget exceeded; no provider request made")
	}
	endpoint := "https://api.openai.com/v1/responses"
	body := map[string]any{"model": c.Model, "input": prompt, "max_output_tokens": OutputTokens, "store": false, "tools": []any{}, "tool_choice": "none", "reasoning": map[string]string{"effort": "low"}}
	if c.Provider == "anthropic" {
		endpoint = "https://api.anthropic.com/v1/messages"
		body = map[string]any{"model": c.Model, "max_tokens": OutputTokens, "messages": []any{map[string]string{"role": "user", "content": prompt}}, "tools": []any{}}
	}
	raw, _ := json.Marshal(body)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return "", nil, fmt.Errorf("summary request could not be constructed")
	}
	request.Header.Set("Content-Type", "application/json")
	if c.Provider == "openai" {
		request.Header.Set("Authorization", "Bearer "+credential)
	} else {
		request.Header.Set("x-api-key", credential)
		request.Header.Set("anthropic-version", "2023-06-01")
	}
	response, err := p.client.Do(request)
	if err != nil {
		return "", &Usage{Billing: "unknown; provider may have processed the request"}, fmt.Errorf("summary provider transport failed or was cancelled; billing outcome unknown")
	}
	defer response.Body.Close()
	usage := &Usage{Billing: "unknown; response not verified"}
	raw, err = io.ReadAll(io.LimitReader(response.Body, (128<<10)+1))
	if err != nil || len(raw) > 128<<10 {
		return "", usage, fmt.Errorf("summary provider response exceeded its bound or could not be read")
	}
	if response.StatusCode != http.StatusOK {
		return "", usage, fmt.Errorf("summary provider refused the request (HTTP %d); response body withheld", response.StatusCode)
	}
	var result struct {
		Model      string `json:"model"`
		Status     string `json:"status"`
		StopReason string `json:"stop_reason"`
		Usage      *struct {
			Input  *int `json:"input_tokens"`
			Output *int `json:"output_tokens"`
		} `json:"usage"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Model != c.Model {
		return "", usage, fmt.Errorf("summary response model or framing did not match the selected model")
	}
	if result.Usage == nil || result.Usage.Input == nil || result.Usage.Output == nil {
		return "", usage, fmt.Errorf("summary response omitted usage evidence; billing remains unknown")
	}
	input, output := *result.Usage.Input, *result.Usage.Output
	if input < 0 || input > MaxPromptBytes*2 || output < 0 || output > OutputTokens {
		return "", usage, fmt.Errorf("summary usage exceeded its declared bound")
	}
	usage = &Usage{Input: input, Output: output, EstimatedUSD: (float64(input)*inputRate + float64(output)*outputRate) / 1e6, Billing: "provider-reported tokens; USD estimate, not a billing receipt"}
	var text bytes.Buffer
	if c.Provider == "openai" {
		if result.Status != "completed" {
			return "", usage, fmt.Errorf("summary generation did not complete")
		}
		for _, item := range result.Output {
			if item.Type == "message" {
				for _, part := range item.Content {
					if part.Type == "output_text" {
						text.WriteString(part.Text)
					} else {
						return "", usage, fmt.Errorf("summary provider returned unsupported content")
					}
				}
			} else if item.Type != "reasoning" {
				return "", usage, fmt.Errorf("summary provider returned a forbidden tool or content item")
			}
		}
	} else {
		if result.StopReason != "end_turn" {
			return "", usage, fmt.Errorf("summary generation did not complete")
		}
		for _, part := range result.Content {
			if part.Type == "text" {
				text.WriteString(part.Text)
			} else if part.Type != "thinking" && part.Type != "redacted_thinking" {
				return "", usage, fmt.Errorf("summary provider returned a forbidden tool or content item")
			}
		}
	}
	document, err := Document(text.String())
	if err != nil {
		return "", usage, fmt.Errorf("summary HTML failed the static presentation policy")
	}
	return document, usage, nil
}
