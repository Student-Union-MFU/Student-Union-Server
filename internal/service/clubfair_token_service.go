package service

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

/*
Club Fair tokens are signed with their OWN secret, and that is the whole point
of this file existing rather than reusing JWTService.

The hazard is specific. JWTService's claims are {user_id int, user_type string}
where user_id is a row in `users`. Club Fair's users are `clubfair_users` — a
different table with its own id sequence, so id 5 is two different people. Had
Club Fair minted JWTService-shaped tokens under the shared JWT_SECRET, a Club
Fair token would have been *indistinguishable* from an SU token: RequireSUAuth
checks only that the signature verifies and user_id > 0, so Club Fair student 5
would have been admitted to SU user 5's profile and step history.

su_auth.go already guards the WBW direction of this — it rejects UserID <= 0
because a WBW token decodes into JWTClaims with no user_id — but that guard works
only because the two claim shapes differ. Two systems sharing both a secret and
a claim shape cannot be told apart at all.

So: a separate secret, which makes cross-verification fail at the signature
rather than at a claims check. Belt and braces on top of that — distinct JSON
field names (`cf_uid`, not `user_id`) and a required audience — so that even if
someone later points both at the same secret, nothing decodes usefully in either
direction.
*/

// The audience every Club Fair token carries and every Club Fair route demands.
const ClubFairAudience = "clubfair"

const clubFairTokenTTL = 30 * 24 * time.Hour

// ErrClubFairTokensDisabled means CLUBFAIR_JWT_SECRET was not set, so this
// service cannot sign or verify anything. Routes check this at wiring time.
var ErrClubFairTokensDisabled = errors.New("clubfair: CLUBFAIR_JWT_SECRET is not set")

// ClubFairClaims deliberately shares no JSON field name with JWTClaims or
// WBWClaims. `cf_uid` is a clubfair_users.id; `cf_role` is that row's role.
type ClubFairClaims struct {
	UserID int    `json:"cf_uid"`
	Role   string `json:"cf_role"`
	jwt.RegisteredClaims
}

type ClubFairTokenService struct {
	secret []byte
}

// NewClubFairTokenService returns a disabled service, not an error, when the
// secret is missing.
//
// This diverges from JWTService, which calls os.Exit(1) on an empty secret, and
// the divergence is deliberate. JWTService gates routes that are already live,
// so refusing to start is the correct response to a key that would be
// forgeable. Club Fair is new: a deploy that forgets one new variable would
// otherwise take down Walk-Bike-Week and the SU app with it. The cryptographic
// outcome is identical either way — no Club Fair token is ever signed or
// accepted — but the blast radius is one feature instead of three.
//
// [IsEnabled] is false in that case and main.go leaves the /clubfair routes
// unregistered, so the failure is a 404 on a new surface rather than a working
// endpoint with no security.
func NewClubFairTokenService() *ClubFairTokenService {
	secret := os.Getenv("CLUBFAIR_JWT_SECRET")
	if secret == "" {
		slog.Error("CLUBFAIR_JWT_SECRET is empty — /clubfair routes will NOT be registered")
		return &ClubFairTokenService{}
	}
	if secret == os.Getenv("JWT_SECRET") {
		// Not fatal — the claim shapes and the audience check still keep the
		// two apart — but it throws away the strongest of the three barriers,
		// and it is almost certainly a copy-paste rather than an intention.
		slog.Warn("CLUBFAIR_JWT_SECRET is the same as JWT_SECRET — use a different key")
	}
	return &ClubFairTokenService{secret: []byte(secret)}
}

func (s *ClubFairTokenService) IsEnabled() bool { return len(s.secret) > 0 }

// Sign issues a token for a clubfair_users row.
func (s *ClubFairTokenService) Sign(userID int, role string) (string, error) {
	if !s.IsEnabled() {
		return "", ErrClubFairTokensDisabled
	}
	if userID <= 0 {
		// Signing a zero id would mint exactly the token RequireClubFairAuth
		// treats as forged. Refuse at the source.
		return "", fmt.Errorf("clubfair: refusing to sign a token for user id %d", userID)
	}

	now := time.Now()
	claims := ClubFairClaims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{ClubFairAudience},
			Subject:   fmt.Sprintf("%s:%d", ClubFairAudience, userID),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(clubFairTokenTTL)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
}

// Validate accepts only a token this service signed, for this audience.
func (s *ClubFairTokenService) Validate(tokenString string) (*ClubFairClaims, error) {
	if !s.IsEnabled() {
		return nil, ErrClubFairTokensDisabled
	}

	token, err := jwt.ParseWithClaims(
		tokenString,
		&ClubFairClaims{},
		func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return s.secret, nil
		},
		// Checked by the parser rather than by hand afterwards, so a token
		// minted for another audience is rejected before its claims are ever
		// read.
		jwt.WithAudience(ClubFairAudience),
	)
	if err != nil {
		return nil, fmt.Errorf("clubfair: invalid token: %w", err)
	}

	claims, ok := token.Claims.(*ClubFairClaims)
	if !ok || !token.Valid {
		return nil, errors.New("clubfair: invalid token claims")
	}

	// The same guard su_auth.go applies in the other direction: JSON decoding
	// ignores fields it does not recognise, so anything foreign that somehow
	// verified would arrive here as UserID 0.
	if claims.UserID <= 0 {
		return nil, errors.New("clubfair: token carries no user id")
	}

	return claims, nil
}
