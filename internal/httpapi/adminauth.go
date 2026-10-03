package httpapi

import "net/http"

// RequireAdminToken is a deliberately simple bearer-token gate on
// control-api: the console (and anyone curling it) must send
// "Authorization: Bearer <token>" matching the server's configured admin
// token. This is a v1 simplification, not the real identity model —
// control-api is operator-facing, single-tenant, and meant to run behind
// a trusted network boundary (same posture as Keycloak running locally in
// milestone 3); a real deployment would put the console behind the same
// OAuth/OIDC flow the gateway already enforces on agent traffic. See
// DECISIONS.md.
func RequireAdminToken(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		got := r.Header.Get("Authorization")
		if got != "Bearer "+token {
			http.Error(w, "missing or invalid admin token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
