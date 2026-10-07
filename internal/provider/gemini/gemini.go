// Package gemini is a real model backend — Google's Gemini API, chosen
// specifically because it has a genuinely free tier (unlike OpenAI/
// Anthropic, which need a funded account), closing the "real provider
// integration" gap tracked since milestone 2 without asking for a paid
// key. Talks to the plain REST API directly (generativelanguage.
// googleapis.com), not the full Go SDK — this project needs chat +
// streaming only, not the SDK's much larger surface.
package gemini

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/provider"
)

const baseURL = "https://generativelanguage.googleapis.com/v1beta/models"

type Provider struct {
	name   string
	apiKey string
	model  string // e.g. "gemini-2.0-flash" — the free-tier-eligible model
	client *http.Client
}

func New(name, apiKey, model string) *Provider {
	return &Provider{name: name, apiKey: apiKey, model: model, client: http.DefaultClient}
}

func (p *Provider) Name() string { return p.name }

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiRequest struct {
	Contents []geminiContent `json:"contents"`
}

type geminiResponse struct {
	Candidates []struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// toContents maps Warden's role-neutral messages onto Gemini's "user"/
// "model" roles (Gemini has no separate "assistant" or "system" role in
// the basic contents array; a leading system-ish instruction is sent as
// just another user turn, which is adequate for this project's mock-
// replacement and judge-comparison use, not a full system-prompt
// integration).
func toContents(messages []provider.ChatMessage) []geminiContent {
	contents := make([]geminiContent, 0, len(messages))
	for _, m := range messages {
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		contents = append(contents, geminiContent{Role: role, Parts: []geminiPart{{Text: m.Content}}})
	}
	return contents
}

func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	body, err := json.Marshal(geminiRequest{Contents: toContents(req.Messages)})
	if err != nil {
		return provider.ChatResponse{}, fmt.Errorf("gemini: marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/%s:generateContent?key=%s", baseURL, p.model, p.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return provider.ChatResponse{}, fmt.Errorf("gemini: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return provider.ChatResponse{}, fmt.Errorf("gemini: request failed: %w", err)
	}
	defer resp.Body.Close()

	var gr geminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return provider.ChatResponse{}, fmt.Errorf("gemini: decode response: %w", err)
	}
	if gr.Error != nil {
		return provider.ChatResponse{}, fmt.Errorf("gemini: api error: %s", gr.Error.Message)
	}
	if len(gr.Candidates) == 0 {
		return provider.ChatResponse{}, fmt.Errorf("gemini: no candidates in response")
	}

	var text strings.Builder
	for _, part := range gr.Candidates[0].Content.Parts {
		text.WriteString(part.Text)
	}
	return provider.ChatResponse{
		Content:      text.String(),
		FinishReason: strings.ToLower(gr.Candidates[0].FinishReason),
	}, nil
}

// ChatStream uses Gemini's SSE streaming endpoint (alt=sse) — each event
// is a JSON object with the same shape as the non-streaming response's
// candidate, one incremental piece of text at a time.
func (p *Provider) ChatStream(ctx context.Context, req provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	body, err := json.Marshal(geminiRequest{Contents: toContents(req.Messages)})
	if err != nil {
		return nil, fmt.Errorf("gemini: marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/%s:streamGenerateContent?alt=sse&key=%s", baseURL, p.model, p.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gemini: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini: request failed: %w", err)
	}
	if resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("gemini: unexpected status %d", resp.StatusCode)
	}

	out := make(chan provider.ChatChunk)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var gr geminiResponse
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &gr); err != nil {
				continue
			}
			if len(gr.Candidates) == 0 {
				continue
			}
			var text strings.Builder
			for _, part := range gr.Candidates[0].Content.Parts {
				text.WriteString(part.Text)
			}
			done := gr.Candidates[0].FinishReason != ""
			chunk := provider.ChatChunk{Delta: text.String(), Done: done}
			if done {
				chunk.FinishReason = strings.ToLower(gr.Candidates[0].FinishReason)
			}
			select {
			case out <- chunk:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}
