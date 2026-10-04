// Package identity verifies OAuth access tokens issued by Keycloak and
// resolves the two identities every Warden call carries: the agent making
// the call, and the human user it's acting on behalf of.
package identity

import (
	"context"
	"fmt"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

type Verifier struct {
	keyfunc keyfunc.Keyfunc
	issuer  string
}

// NewVerifier fetches the signing keys from issuer+"/protocol/openid-connect/certs"
// (Keycloak's JWKS endpoint) and keeps them refreshed in the background.
func NewVerifier(ctx context.Context, issuer string) (*Verifier, error) {
	jwksURL := issuer + "/protocol/openid-connect/certs"
	kf, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("identity: fetch JWKS from %s: %w", jwksURL, err)
	}
	return &Verifier{keyfunc: kf, issuer: issuer}, nil
}

// Verify checks the token's signature, expiry and issuer, then extracts
// both identities the token itself proves: "azp" (the OIDC-standard
// "authorized party" claim) is the agent — the client that originally
// authenticated, and stays the exchanging client's ID even after a real
// RFC 8693 token exchange, which is exactly the property this needs.
// "sub"/"preferred_username" is the acting-as user.
//
// This is deliberately NOT "verify the agent, then trust a caller-
// supplied header naming who it acts for" — that was last year's
// simplification (see DECISIONS.md for the full history). A real token
// exchange already happened before this token ever reached Warden (see
// deploy/bench/get_token.sh): the agent asked Keycloak to exchange its
// own client-credentials token for one scoped to a specific user, and
// Keycloak checked ITS OWN impersonation permission/policy
// (deploy/docker/keycloak-realm.json's "agent-demo-can-impersonate"
// client policy) before agreeing. By the time this function runs, "is
// this agent allowed to act as this user" has already been answered, by
// Keycloak, not by Warden re-checking a claim the agent could otherwise
// have asserted about itself.
func (v *Verifier) Verify(tokenString string) (Principal, error) {
	token, err := jwt.Parse(tokenString, v.keyfunc.Keyfunc,
		jwt.WithIssuer(v.issuer),
		jwt.WithValidMethods([]string{"RS256"}),
	)
	if err != nil {
		return Principal{}, fmt.Errorf("identity: invalid token: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return Principal{}, fmt.Errorf("identity: unexpected claims shape")
	}

	agentID, _ := claims["azp"].(string)
	if agentID == "" {
		return Principal{}, fmt.Errorf("identity: token has no azp (client) claim")
	}

	// preferred_username over raw "sub" (a UUID): everywhere else in
	// Warden (policy, approvals, the console) a human user is named by
	// its readable username ("user-1"), not its Keycloak-internal ID —
	// changing that now would mean every Cedar policy, approval row and
	// console view would need to switch to matching on UUIDs instead.
	actingAs, _ := claims["preferred_username"].(string)
	if actingAs == "" {
		return Principal{}, fmt.Errorf("identity: token has no preferred_username claim — was it actually exchanged for a user, not just a plain client-credentials token?")
	}

	// Found live wiring this up: a PLAIN, never-exchanged client-credentials
	// token also carries a preferred_username — Keycloak gives every
	// service-account client its own hidden user, always named
	// "service-account-<clientId>" by Keycloak's own fixed, documented
	// convention, and that counts as a perfectly valid preferred_username
	// claim. Without this check, a plain token (which should be rejected —
	// it was never exchanged for anyone, so no impersonation permission
	// was ever checked) would silently be accepted as "acting as the
	// service account's own user," defeating the entire point of
	// requiring a real exchange.
	if actingAs == "service-account-"+agentID {
		return Principal{}, fmt.Errorf("identity: token was never exchanged for a user (got the agent's own service-account identity) — fetch a token via RFC 8693 token exchange first, see deploy/bench/get_token.sh")
	}

	// Optional: a client without a "team" claim just doesn't get
	// team-scoped budget/rate-limit enforcement — see
	// internal/httpapi.ChatHandler, which only charges a team scope when
	// this is non-empty.
	team, _ := claims["team"].(string)

	return Principal{AgentID: agentID, ActingAs: actingAs, Team: team}, nil
}
