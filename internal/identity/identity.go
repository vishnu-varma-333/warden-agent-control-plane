// Package identity verifies OAuth access tokens issued by Keycloak and
// resolves the two identities every Warden call carries: the agent making
// the call, and the human user it's acting on behalf of.
package identity

import (
	"context"
	"fmt"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// Identity is what the token itself proves: which agent it belongs to, and
// which users that agent is allowed to act on behalf of.
type Identity struct {
	AgentID         string
	ActingAsAllowed []string
}

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

// Verify checks the token's signature, expiry and issuer, then extracts the
// agent identity and its "acting as" allowlist.
//
// Simplification, recorded in DECISIONS.md: a full OAuth token-exchange flow
// (RFC 8693) would let an agent request a token scoped to one specific user
// per call. Instead, the agent's own client-credentials token carries a
// fixed allowlist of users it may act for (via a Keycloak protocol mapper),
// and the caller names which one for this call via a header. Good enough to
// prove the "two identities per call" requirement without standing up
// token exchange, which Keycloak makes considerably more involved to set up.
func (v *Verifier) Verify(tokenString string) (Identity, error) {
	token, err := jwt.Parse(tokenString, v.keyfunc.Keyfunc,
		jwt.WithIssuer(v.issuer),
		jwt.WithValidMethods([]string{"RS256"}),
	)
	if err != nil {
		return Identity{}, fmt.Errorf("identity: invalid token: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return Identity{}, fmt.Errorf("identity: unexpected claims shape")
	}

	// "azp" (authorized party) is the OIDC-standard claim for which client a
	// client-credentials token was issued to.
	agentID, _ := claims["azp"].(string)
	if agentID == "" {
		return Identity{}, fmt.Errorf("identity: token has no azp (client) claim")
	}

	var allowed []string
	if raw, ok := claims["acting_as_allowed"].(string); ok {
		for _, s := range strings.Split(raw, ",") {
			if s = strings.TrimSpace(s); s != "" {
				allowed = append(allowed, s)
			}
		}
	}

	return Identity{AgentID: agentID, ActingAsAllowed: allowed}, nil
}
