package router

import (
	"context"
	"testing"

	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/provider"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/provider/mock"
)

func TestFallsBackWhenPrimaryErrors(t *testing.T) {
	primary := mock.New("primary")
	secondary := mock.New("secondary")
	primary.SetUnhealthy(true)

	r := New()
	r.RegisterProvider(primary)
	r.RegisterProvider(secondary)
	r.AddRoute(Route{ModelAlias: "test-model", Providers: []string{"primary", "secondary"}})

	resp, servedBy, err := r.Chat(context.Background(), provider.ChatRequest{
		Model:    "test-model",
		Messages: []provider.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("expected fallback to succeed, got error: %v", err)
	}
	if servedBy != "secondary" {
		t.Fatalf("expected secondary to serve the request, got %q", servedBy)
	}
	if resp.Content == "" {
		t.Fatal("expected non-empty response content")
	}
}

func TestErrorsWhenAllProvidersDown(t *testing.T) {
	primary := mock.New("primary")
	primary.SetUnhealthy(true)

	r := New()
	r.RegisterProvider(primary)
	r.AddRoute(Route{ModelAlias: "test-model", Providers: []string{"primary"}})

	_, _, err := r.Chat(context.Background(), provider.ChatRequest{
		Model:    "test-model",
		Messages: []provider.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected an error when the only provider is down")
	}
}
