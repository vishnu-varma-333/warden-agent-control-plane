package identity

import (
	"context"
	"fmt"
	"net/http"
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

// ResolveFromHeader does the same Bearer-token resolution Middleware does,
// as a standalone function. It exists because the MCP proxy handler needs
// identity resolved per tool call (using headers the SDK hands it via
// RequestExtra), not just once when the HTTP connection to /mcp opened —
// a single MCP session can carry many tool calls, potentially acting as
// different users (each with its own already-exchanged token) across
// calls.
//
// X-Acting-As is optional now (it used to be required — see
// identity.go's doc comment on Verify for why): the token itself already
// proves who it's acting as, Keycloak-verified at exchange time. If a
// caller sends it anyway, it's checked as a consistency guard — catching
// "the caller attached the wrong token for this call" — not trusted as
// the source of truth.
func ResolveFromHeader(v *Verifier, h http.Header) (Principal, error) {
	tokenString, ok := strings.CutPrefix(h.Get("Authorization"), "Bearer ")
	if !ok || tokenString == "" {
		return Principal{}, fmt.Errorf("missing Authorization: Bearer <token>")
	}

	p, err := v.Verify(tokenString)
	if err != nil {
		return Principal{}, fmt.Errorf("invalid token: %w", err)
	}

	if hdr := h.Get("X-Acting-As"); hdr != "" && hdr != p.ActingAs {
		return Principal{}, fmt.Errorf("X-Acting-As header (%q) does not match the token's actual subject (%q) — wrong exchanged token attached to this call?", hdr, p.ActingAs)
	}

	return p, nil
}

// Middleware requires a Bearer token, verified against Keycloak, that has
// already been exchanged (RFC 8693) for the specific user it's acting as
// — see identity.go's Verify. Every downstream handler can then trust
// both identities are real and authorized, without re-checking anything
// itself.
func Middleware(v *Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, err := ResolveFromHeader(v, r.Header)
			if err != nil {
				status := http.StatusUnauthorized
				if strings.Contains(err.Error(), "does not match") {
					status = http.StatusBadRequest
				}
				http.Error(w, err.Error(), status)
				return
			}

			ctx := context.WithValue(r.Context(), principalKey{}, p)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
