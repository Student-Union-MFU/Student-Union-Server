package middleware

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"su-server/internal/service"
)

// Distinct from ctxKey (WBW) and suCtxKey (SU): same package, three claim types,
// and a context key collision would hand one system's claims to another's
// handler.
type clubFairCtxKey string

const clubFairClaimsKey clubFairCtxKey = "clubfair_claims"

// Club Fair roles, matching the CHECK on clubfair_users.role.
const (
	ClubFairRoleStudent = "student"
	ClubFairRoleStaff   = "staff"
	ClubFairRoleAdmin   = "admin"
)

// RequireClubFairAuth admits only a token minted by ClubFairTokenService.
//
// Error messages match the SU and WBW middleware: Thai, and identical for
// "no token" versus "bad token" versus "wrong audience", so a caller cannot
// learn which check it failed.
func RequireClubFairAuth(tokens *service.ClubFairTokenService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if !strings.HasPrefix(header, "Bearer ") {
				WriteError(w, http.StatusUnauthorized, "ต้องเข้าสู่ระบบก่อน")
				return
			}

			claims, err := tokens.Validate(header[len("Bearer "):])
			if err != nil {
				WriteError(w, http.StatusUnauthorized, "โทเคนไม่ถูกต้องหรือหมดอายุ")
				return
			}

			ctx := context.WithValue(r.Context(), clubFairClaimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClubFairClaimsFrom returns nil when RequireClubFairAuth did not run.
func ClubFairClaimsFrom(ctx context.Context) *service.ClubFairClaims {
	claims, _ := ctx.Value(clubFairClaimsKey).(*service.ClubFairClaims)
	return claims
}

// RequireClubFairRole gates on the signed-in student's role. Use after
// RequireClubFairAuth.
//
// This is what stands between a student and the announcements channel: posting
// is staff-only, and the role comes off a token this server signed rather than
// off a flag the client sends.
func RequireClubFairRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := ClubFairClaimsFrom(r.Context())
			if claims == nil || !slices.Contains(roles, claims.Role) {
				WriteError(w, http.StatusForbidden, "ไม่มีสิทธิ์เข้าถึง")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
