package middleware

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"su-server/internal/service"
)

// Distinct from wbw_auth.go's ctxKey/claimsKey: same package, and the two
// auth systems carry different claim types.
type suCtxKey string

const suClaimsKey suCtxKey = "su_claims"

// RequireSUAuth admits only a request carrying a bearer token this server
// signed, and puts its claims in the context for everything downstream.
func RequireSUAuth(jwt *service.JWTService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if !strings.HasPrefix(header, "Bearer ") {
				WriteError(w, http.StatusUnauthorized, "ต้องเข้าสู่ระบบก่อน")
				return
			}

			claims, err := jwt.Validate(header[len("Bearer "):])
			if err != nil {
				WriteError(w, http.StatusUnauthorized, "โทเคนไม่ถูกต้องหรือหมดอายุ")
				return
			}

			ctx := context.WithValue(r.Context(), suClaimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// SUClaimsFrom returns nil when RequireSUAuth did not run.
func SUClaimsFrom(ctx context.Context) *service.JWTClaims {
	claims, _ := ctx.Value(suClaimsKey).(*service.JWTClaims)
	return claims
}

// RequireSelfOrStaff admits the owner of the record named by a URL parameter,
// and any staff member. Use it after RequireSUAuth.
//
// A bare token is not enough on a route like this: it would turn "anyone on
// the internet" into "any signed-in student", and five thousand ids is a loop.
func RequireSelfOrStaff(param string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := SUClaimsFrom(r.Context())
			if claims == nil {
				WriteError(w, http.StatusUnauthorized, "ต้องเข้าสู่ระบบก่อน")
				return
			}

			if isStaff(claims.UserType) {
				next.ServeHTTP(w, r)
				return
			}

			// An unparseable id answers 403, not 400. Answering differently
			// would tell someone walking the id space which ids exist.
			id, err := strconv.Atoi(chi.URLParam(r, param))
			if err != nil || id != claims.UserID {
				WriteError(w, http.StatusForbidden, "ไม่มีสิทธิ์เข้าถึงข้อมูลนี้")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireSUStaff asks only who you are, for routes where no record ownership
// applies. Use it after RequireSUAuth.
func RequireSUStaff() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := SUClaimsFrom(r.Context())
			if claims == nil || !isStaff(claims.UserType) {
				WriteError(w, http.StatusForbidden, "ไม่มีสิทธิ์เข้าถึงข้อมูลนี้")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isStaff(userType string) bool {
	return userType == "staff" || userType == "admin"
}
