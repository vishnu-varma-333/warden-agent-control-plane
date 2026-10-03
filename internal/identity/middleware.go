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

// Middleware requires a Bearer token (verified against Keycloak) and an
// X-Acting-As header naming a user the token's agent is allowed to act for.
// Every downstream handler can then trust that both identities are real and
// authorized, without re-checking anything itself.
func Middleware(v *Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenString, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || tokenString == "" {
				http.Error(w, "missing Authorization: Bearer <token>", http.StatusUnauthorized)
				return
			}

			id, err := v.Verify(tokenString)
			if err != nil {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}

			actingAs := r.Header.Get("X-Acting-As")
			if actingAs == "" {
				http.Error(w, "missing X-Acting-As header", http.StatusBadRequest)
				return
			}
			if !slices.Contains(id.ActingAsAllowed, actingAs) {
				http.Error(w, fmt.Sprintf("agent %q may not act as %q", id.AgentID, actingAs), http.StatusForbidden)
				return
			}

			ctx := context.WithValue(r.Context(), principalKey{}, Principal{AgentID: id.AgentID, ActingAs: actingAs})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
