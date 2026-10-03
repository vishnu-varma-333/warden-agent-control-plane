// Package httpapi exposes the gateway's OpenAI-compatible endpoint. Keeping
// it separate from main.go means the HTTP wire format (OpenAI's JSON shapes)
// is isolated from routing/caching logic, and this handler can be unit
// tested with httptest instead of a running server.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/budget"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/cache"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/identity"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/policy"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/provider"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/ratelimit"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/router"
)

// chatCompletionRequest mirrors the subset of OpenAI's /v1/chat/completions
// request body Warden actually needs. Callers' SDKs already speak this
// format, which is the entire point of being "OpenAI-compatible."
type chatCompletionRequest struct {
	Model    string                  `json:"model"`
	Messages []provider.ChatMessage  `json:"messages"`
	Stream   bool                    `json:"stream"`
}

type chatCompletionResponse struct {
	ID      string                   `json:"id"`
	Object  string                   `json:"object"`
	Model   string                   `json:"model"`
	Choices []chatCompletionChoice   `json:"choices"`
	Served  string                   `json:"-"` // which provider served it; used for logging, not sent to the caller
}

type chatCompletionChoice struct {
	Index        int                    `json:"index"`
	Message      provider.ChatMessage   `json:"message"`
	FinishReason string                 `json:"finish_reason"`
}

type chatCompletionChunk struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Model   string             `json:"model"`
	Choices []chunkChoice      `json:"choices"`
}

type chunkChoice struct {
	Index        int           `json:"index"`
	Delta        chunkDelta    `json:"delta"`
	FinishReason *string       `json:"finish_reason"`
}

type chunkDelta struct {
	Content string `json:"content"`
}

type ChatHandler struct {
	Router    *router.Router
	Cache     *cache.Cache
	RateLimit *ratelimit.Limiter
	Budget    *budget.Budget
	Policy    *policy.Engine
}

// costPerCall is a placeholder until milestone 2's real provider integration
// reports actual token usage; see DECISIONS.md. It makes the budget
// mechanism (atomic charge, correct across replicas) demonstrable now
// without pretending to know a real dollar cost yet.
const costPerCall = 1.0

// ServeHTTP's check order is deliberate, not incidental:
//
//  1. Parse the body first — the policy decision needs to know *which*
//     model is being requested, so it has to happen before any check that
//     depends on that.
//  2. Policy BEFORE rate limit/budget — a denied call shouldn't consume
//     either quota; only work Warden actually permits should count against
//     them.
//  3. Policy BEFORE the cache lookup — this one is a correctness issue,
//     not just efficiency: the response cache (internal/cache) is keyed
//     only on model+messages, not on who's asking, so it's shared across
//     every agent/user. If the cache were checked before authorization, an
//     agent denied access to a model could still receive another agent's
//     cached answer for it. Checking policy first closes that.
func (h *ChatHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := identity.FromContext(r.Context())
	if !ok {
		// Defensive only: identity.Middleware should already guarantee this.
		http.Error(w, "no authenticated principal", http.StatusUnauthorized)
		return
	}

	var req chatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if req.Model == "" || len(req.Messages) == 0 {
		http.Error(w, "model and messages are required", http.StatusBadRequest)
		return
	}

	decision, err := h.Policy.Authorize(r.Context(), principal.AgentID, principal.ActingAs, "CallModel", "Model", req.Model)
	if err != nil {
		slog.Error("policy check failed", "agent", principal.AgentID, "error", err)
		http.Error(w, "policy check failed", http.StatusServiceUnavailable)
		return
	}
	if !decision.Allow {
		http.Error(w, fmt.Sprintf("denied by policy (version %d): %v", decision.PolicyVersion, decision.Reasons), http.StatusForbidden)
		return
	}

	if allowed, err := h.RateLimit.Allow(r.Context(), principal.AgentID); err != nil {
		slog.Error("rate limit check failed, allowing request", "agent", principal.AgentID, "error", err)
	} else if !allowed {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	if allowed, total, err := h.Budget.Charge(r.Context(), principal.AgentID, costPerCall); err != nil {
		slog.Error("budget check failed, allowing request", "agent", principal.AgentID, "error", err)
	} else if !allowed {
		http.Error(w, fmt.Sprintf("budget exceeded (at %v)", total), http.StatusPaymentRequired)
		return
	}

	providerReq := provider.ChatRequest{Model: req.Model, Messages: req.Messages}

	if req.Stream {
		h.serveStream(w, r, providerReq)
		return
	}
	h.serveOnce(w, r, providerReq)
}

func (h *ChatHandler) serveOnce(w http.ResponseWriter, r *http.Request, req provider.ChatRequest) {
	ctx := r.Context()

	if cached, hit := h.Cache.Get(ctx, req); hit {
		slog.Info("chat cache hit", "model", req.Model)
		writeJSON(w, toResponse(req.Model, cached))
		return
	}

	resp, servedBy, err := h.Router.Chat(ctx, req)
	if err != nil {
		h.writeRouterError(w, err)
		return
	}
	slog.Info("chat served", "model", req.Model, "provider", servedBy)
	h.Cache.Set(ctx, req, resp)
	writeJSON(w, toResponse(req.Model, resp))
}

func (h *ChatHandler) serveStream(w http.ResponseWriter, r *http.Request, req provider.ChatRequest) {
	ctx := r.Context()

	// A cached hit is replayed as a single chunk rather than a real stream:
	// simpler than re-splitting cached text, and still correct — the caller
	// just sees the whole answer arrive in one SSE event instead of several.
	if cached, hit := h.Cache.Get(ctx, req); hit {
		slog.Info("chat cache hit (stream)", "model", req.Model)
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		setSSEHeaders(w)
		writeSSEChunk(w, req.Model, cached.Content, &cached.FinishReason)
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}

	stream, servedBy, err := h.Router.ChatStream(ctx, req)
	if err != nil {
		h.writeRouterError(w, err)
		return
	}
	slog.Info("chat stream served", "model", req.Model, "provider", servedBy)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	setSSEHeaders(w)

	var full string
	var finishReason string
	for chunk := range stream {
		full += chunk.Delta
		var fr *string
		if chunk.Done {
			finishReason = chunk.FinishReason
			fr = &finishReason
		}
		writeSSEChunk(w, req.Model, chunk.Delta, fr)
		flusher.Flush()
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()

	// Cache the fully-assembled response so a repeat of this exact request
	// gets the fast, non-streamed cache path next time.
	h.Cache.Set(ctx, req, provider.ChatResponse{Content: full, FinishReason: finishReason})
}

func (h *ChatHandler) writeRouterError(w http.ResponseWriter, err error) {
	if errors.Is(err, router.ErrNoProviderAvailable) {
		http.Error(w, "all providers unavailable", http.StatusServiceUnavailable)
		return
	}
	http.Error(w, err.Error(), http.StatusBadGateway)
}

func toResponse(model string, resp provider.ChatResponse) chatCompletionResponse {
	return chatCompletionResponse{
		ID:     "chatcmpl-mock",
		Object: "chat.completion",
		Model:  model,
		Choices: []chatCompletionChoice{{
			Index:        0,
			Message:      provider.ChatMessage{Role: "assistant", Content: resp.Content},
			FinishReason: resp.FinishReason,
		}},
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func setSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
}

func writeSSEChunk(w http.ResponseWriter, model, delta string, finishReason *string) {
	chunk := chatCompletionChunk{
		ID:     "chatcmpl-mock",
		Object: "chat.completion.chunk",
		Model:  model,
		Choices: []chunkChoice{{
			Index:        0,
			Delta:        chunkDelta{Content: delta},
			FinishReason: finishReason,
		}},
	}
	b, _ := json.Marshal(chunk)
	fmt.Fprintf(w, "data: %s\n\n", b)
}
