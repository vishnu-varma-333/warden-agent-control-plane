// Package provider defines the interface every model backend (mock, OpenAI,
// Anthropic, ...) implements, so the router and HTTP layer never depend on a
// specific vendor's SDK or wire format.
package provider

import "context"

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
}

type ChatResponse struct {
	Content      string `json:"content"`
	FinishReason string `json:"finish_reason"`
}

// ChatChunk is one piece of a streamed response. Done is true on the final
// chunk (which carries no further Delta).
type ChatChunk struct {
	Delta        string
	FinishReason string
	Done         bool
}

type Provider interface {
	Name() string
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
	// ChatStream returns a channel of chunks. The channel is closed when the
	// stream ends (normally or via ctx cancellation); a mid-stream error is
	// reported on the final chunk's FinishReason being "error".
	ChatStream(ctx context.Context, req ChatRequest) (<-chan ChatChunk, error)
}
