package service

import (
	"testing"
)

// The whole reason ClubFairTokenService exists: clubfair_users.id and users.id
// are different people, so a token from one system must be worthless in the
// other. These tests assert that in both directions.

func newTestTokens(t *testing.T, secret string) *ClubFairTokenService {
	t.Helper()
	t.Setenv("CLUBFAIR_JWT_SECRET", secret)
	tokens := NewClubFairTokenService()
	if !tokens.IsEnabled() {
		t.Fatal("service should be enabled with a secret set")
	}
	return tokens
}

func TestSignAndValidateRoundTrip(t *testing.T) {
	tokens := newTestTokens(t, "clubfair-test-secret")

	signed, err := tokens.Sign(42, "student")
	if err != nil {
		t.Fatalf("sign failed: %v", err)
	}

	claims, err := tokens.Validate(signed)
	if err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if claims.UserID != 42 {
		t.Errorf("user id came back as %d, want 42", claims.UserID)
	}
	if claims.Role != "student" {
		t.Errorf("role came back as %q, want student", claims.Role)
	}
}

// An SU token is signed with JWT_SECRET. Even if both services were pointed at
// the same key, the audience keeps them apart — but the key alone should be
// enough, and this proves it is.
func TestATokenFromAnotherSecretDoesNotVerify(t *testing.T) {
	minted := newTestTokens(t, "clubfair-secret-one")
	signed, err := minted.Sign(42, "student")
	if err != nil {
		t.Fatalf("sign failed: %v", err)
	}

	other := newTestTokens(t, "a-completely-different-secret")
	if _, err := other.Validate(signed); err == nil {
		t.Error("a token signed with a different secret verified")
	}
}

// The SU middleware's guard works because a foreign token decodes with no
// user_id. The same has to hold here: nothing without cf_uid may be admitted.
func TestATokenWithNoClubFairUserIDIsRejected(t *testing.T) {
	secret := "shared-by-mistake"
	tokens := newTestTokens(t, secret)

	// An SU-shaped token: {user_id, user_type}, no cf_uid, no audience. This is
	// exactly what would arrive if someone set both secrets to the same value.
	t.Setenv("JWT_SECRET", secret)
	t.Setenv("JWT_EXPIRY", "24h")
	su := NewJWTService()
	suToken, err := su.Generate(42, "student")
	if err != nil {
		t.Fatalf("could not mint an SU token: %v", err)
	}

	if _, err := tokens.Validate(suToken); err == nil {
		t.Error("an SU token was accepted as a Club Fair token")
	}
}

// And the reverse: a Club Fair token must not satisfy the SU service, which
// would map cf_uid onto a users.id row belonging to someone else.
func TestAClubFairTokenIsNotAValidSUToken(t *testing.T) {
	secret := "shared-by-mistake"
	tokens := newTestTokens(t, secret)
	t.Setenv("JWT_SECRET", secret)
	t.Setenv("JWT_EXPIRY", "24h")

	cfToken, err := tokens.Sign(42, "student")
	if err != nil {
		t.Fatalf("sign failed: %v", err)
	}

	su := NewJWTService()
	claims, err := su.Validate(cfToken)
	if err != nil {
		// Ideal: it does not even parse.
		return
	}
	// Otherwise it must at least carry no usable SU user id — which is the
	// condition RequireSUAuth rejects on.
	if claims.UserID > 0 {
		t.Errorf("a Club Fair token yielded SU user id %d", claims.UserID)
	}
}

// Signing a zero id would mint precisely the token Validate treats as forged.
func TestRefusesToSignAnEmptyUserID(t *testing.T) {
	tokens := newTestTokens(t, "clubfair-test-secret")

	for _, id := range []int{0, -1} {
		if _, err := tokens.Sign(id, "student"); err == nil {
			t.Errorf("signed a token for user id %d", id)
		}
	}
}

// With no secret the service must be inert rather than silently signing under a
// zero-length key, which golang-jwt's HMAC signer would happily accept.
func TestDisabledWithoutASecret(t *testing.T) {
	t.Setenv("CLUBFAIR_JWT_SECRET", "")
	tokens := NewClubFairTokenService()

	if tokens.IsEnabled() {
		t.Fatal("service reports enabled with no secret")
	}
	if _, err := tokens.Sign(42, "student"); err == nil {
		t.Error("signed a token with no secret configured")
	}
	if _, err := tokens.Validate("anything"); err == nil {
		t.Error("validated a token with no secret configured")
	}
}

func TestGarbageDoesNotValidate(t *testing.T) {
	tokens := newTestTokens(t, "clubfair-test-secret")

	for _, junk := range []string{"", "not-a-token", "a.b.c", "Bearer something"} {
		if _, err := tokens.Validate(junk); err == nil {
			t.Errorf("%q validated", junk)
		}
	}
}
