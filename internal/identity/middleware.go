package identity

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// Principal is the resolved "who" for a request: the agent that
// authenticated, and the user it's acting on behalf of for this call.
type Principal struct {
	AgentID  string
	ActingAs string
}

type principalKey struct{}

func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// ResolveFromHeader does the same Bearer-token + X-Acting-As resolution
// Middleware does, as a standalone function. It exists because the MCP
// proxy handler needs identity resolved per tool call (using headers the
// SDK hands it via RequestExtra), not just once when the HTTP connection to
// /mcp opened — a single MCP session can carry many tool calls, potentially
// claiming different acting-as users across calls.
func ResolveFromHeader(v *Verifier, h http.Header) (Principal, error) {
	tokenString, ok := strings.CutPrefix(h.Get("Authorization"), "Bearer ")
	if !ok || tokenString == "" {
		return Principal{}, fmt.Errorf("missing Authorization: Bearer <token>")
	}

	id, err := v.Verify(tokenString)
	if err != nil {
		return Principal{}, fmt.Errorf("invalid token: %w", err)
	}

	actingAs := h.Get("X-Acting-As")
	if actingAs == "" {
		return Principal{}, fmt.Errorf("missing X-Acting-As header")
	}
	if !slices.Contains(id.ActingAsAllowed, actingAs) {
		return Principal{}, fmt.Errorf("agent %q may not act as %q", id.AgentID, actingAs)
	}

	return Principal{AgentID: id.AgentID, ActingAs: actingAs}, nil
}

// Middleware requires a Bearer token (verified against Keycloak) and an
// X-Acting-As header naming a user the token's agent is allowed to act for.
// Every downstream handler can then trust that both identities are real and
// authorized, without re-checking anything itself.
func Middleware(v *Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, err := ResolveFromHeader(v, r.Header)
			if err != nil {
				status := http.StatusUnauthorized
				if strings.Contains(err.Error(), "X-Acting-As") {
					status = http.StatusBadRequest
				} else if strings.Contains(err.Error(), "may not act as") {
					status = http.StatusForbidden
				}
				http.Error(w, err.Error(), status)
				return
			}

			ctx := context.WithValue(r.Context(), principalKey{}, p)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
