package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"su-server/internal/service"
)

func newJWT(t *testing.T) *service.JWTService {
	t.Helper()
	t.Setenv("JWT_SECRET", "test-secret-for-middleware")
	t.Setenv("JWT_EXPIRY", "1h")
	return service.NewJWTService()
}

func tokenFor(t *testing.T, jwt *service.JWTService, id int, userType string) string {
	t.Helper()
	signed, err := jwt.Generate(id, userType)
	if err != nil {
		t.Fatalf("signing failed: %v", err)
	}
	return signed
}

// Records whether the wrapped handler was reached at all — the thing every
// one of these tests is really asking.
func passthrough(reached *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
	})
}

func TestRequireSUAuthRejectsAMissingHeader(t *testing.T) {
	jwt := newJWT(t)
	var reached bool
	rec := httptest.NewRecorder()

	RequireSUAuth(jwt)(passthrough(&reached)).ServeHTTP(
		rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rec.Code)
	}
	if reached {
		t.Error("the handler ran without a token")
	}
}

func TestRequireSUAuthRejectsANonBearerHeader(t *testing.T) {
	jwt := newJWT(t)
	var reached bool
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic abc123")
	rec := httptest.NewRecorder()

	RequireSUAuth(jwt)(passthrough(&reached)).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized || reached {
		t.Errorf("got %d reached=%v, want 401 and no handler", rec.Code, reached)
	}
}

// The check that matters most: a token this server did not sign must not work.
func TestRequireSUAuthRejectsAForeignSignature(t *testing.T) {
	jwt := newJWT(t)

	t.Setenv("JWT_SECRET", "a-completely-different-secret")
	other := service.NewJWTService()
	foreign := tokenFor(t, other, 7, "student")

	var reached bool
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+foreign)
	rec := httptest.NewRecorder()

	RequireSUAuth(jwt)(passthrough(&reached)).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized || reached {
		t.Errorf("got %d reached=%v, want 401 and no handler", rec.Code, reached)
	}
}

func TestRequireSUAuthRejectsAnExpiredToken(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-for-middleware")
	// Negative expiry: issued already stale, so no test has to wait.
	t.Setenv("JWT_EXPIRY", "-1h")
	jwt := service.NewJWTService()
	stale := tokenFor(t, jwt, 7, "student")

	var reached bool
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+stale)
	rec := httptest.NewRecorder()

	RequireSUAuth(jwt)(passthrough(&reached)).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized || reached {
		t.Errorf("got %d reached=%v, want 401 and no handler", rec.Code, reached)
	}
}

// The finding this exists for: a WBW token carries role and username but no
// user_id, and JSON decoding silently ignores fields JWTClaims doesn't
// recognize — so it parses into UserID: 0 with a valid signature and an
// unexpired exp. Sign one for real, with the same secret both services read
// from JWT_SECRET, and confirm it no longer opens an SU route.
func TestRequireSUAuthRejectsAWBWToken(t *testing.T) {
	jwt := newJWT(t)

	wbwTokens := service.NewWBWTokenService()
	foreign, err := wbwTokens.Sign("some-uuid", "staff", "somebody")
	if err != nil {
		t.Fatalf("signing the WBW token failed: %v", err)
	}

	var reached bool
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+foreign)
	rec := httptest.NewRecorder()

	RequireSUAuth(jwt)(passthrough(&reached)).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized || reached {
		t.Errorf("got %d reached=%v, want 401 and no handler", rec.Code, reached)
	}
}

// Same shape, minimal case: whatever produced it, UserID: 0 is never a real
// account, so it must be rejected the same way an invalid token is — same
// status, same message, so the caller can't tell which check failed.
func TestRequireSUAuthRejectsAZeroUserID(t *testing.T) {
	jwt := newJWT(t)
	zero := tokenFor(t, jwt, 0, "student")

	var reached bool
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+zero)
	rec := httptest.NewRecorder()

	RequireSUAuth(jwt)(passthrough(&reached)).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized || reached {
		t.Errorf("got %d reached=%v, want 401 and no handler", rec.Code, reached)
	}
}

// The other half: a real, positive user id must still pass. Guards against
// a fix that overcorrects into rejecting every token.
func TestRequireSUAuthPassesAPositiveUserID(t *testing.T) {
	jwt := newJWT(t)
	var reached bool

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tokenFor(t, jwt, 1, "student"))
	rec := httptest.NewRecorder()

	RequireSUAuth(jwt)(passthrough(&reached)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !reached {
		t.Errorf("got %d reached=%v, want 200 and the handler", rec.Code, reached)
	}
}

func TestRequireSUAuthPassesAValidTokenAndCarriesTheClaims(t *testing.T) {
	jwt := newJWT(t)
	var seen *service.JWTClaims

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = SUClaimsFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tokenFor(t, jwt, 42, "student"))
	rec := httptest.NewRecorder()

	RequireSUAuth(jwt)(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if seen == nil || seen.UserID != 42 || seen.UserType != "student" {
		t.Errorf("claims did not reach the handler: %+v", seen)
	}
}

// `RequireSelfOrStaff` reads a chi URL parameter, so the request has to go
// through a router for that parameter to exist.
func selfOrStaffRequest(t *testing.T, jwt *service.JWTService, id int, userType, path string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	var reached bool

	router := chi.NewRouter()
	router.Route("/users", func(r chi.Router) {
		r.Use(RequireSUAuth(jwt))
		r.With(RequireSelfOrStaff("id")).Get("/{id}", passthrough(&reached).ServeHTTP)
	})

	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+tokenFor(t, jwt, id, userType))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec, reached
}

func TestSelfOrStaffAllowsAStudentTheirOwnRecord(t *testing.T) {
	jwt := newJWT(t)
	rec, reached := selfOrStaffRequest(t, jwt, 42, "student", "/users/42")

	if rec.Code != http.StatusOK || !reached {
		t.Errorf("got %d reached=%v, want 200 and the handler", rec.Code, reached)
	}
}

// The whole reason a bare token is not enough: five thousand ids is a loop.
func TestSelfOrStaffRefusesAStudentSomeoneElsesRecord(t *testing.T) {
	jwt := newJWT(t)
	rec, reached := selfOrStaffRequest(t, jwt, 42, "student", "/users/43")

	if rec.Code != http.StatusForbidden || reached {
		t.Errorf("got %d reached=%v, want 403 and no handler", rec.Code, reached)
	}
}

func TestSelfOrStaffAllowsStaffAnyRecord(t *testing.T) {
	jwt := newJWT(t)
	rec, reached := selfOrStaffRequest(t, jwt, 1, "staff", "/users/43")

	if rec.Code != http.StatusOK || !reached {
		t.Errorf("got %d reached=%v, want 200 and the handler", rec.Code, reached)
	}
}

func TestSelfOrStaffAllowsAdminAnyRecord(t *testing.T) {
	jwt := newJWT(t)
	rec, reached := selfOrStaffRequest(t, jwt, 1, "admin", "/users/43")

	if rec.Code != http.StatusOK || !reached {
		t.Errorf("got %d reached=%v, want 200 and the handler", rec.Code, reached)
	}
}

// 403 rather than 400: a different answer for a malformed id tells someone
// walking the id space which ids exist.
func TestSelfOrStaffTreatsAnUnparseableIdAsForbidden(t *testing.T) {
	jwt := newJWT(t)
	rec, reached := selfOrStaffRequest(t, jwt, 42, "student", "/users/abc")

	if rec.Code != http.StatusForbidden || reached {
		t.Errorf("got %d reached=%v, want 403 and no handler", rec.Code, reached)
	}
}

func TestRequireSUStaffRefusesAStudentAndAllowsStaff(t *testing.T) {
	jwt := newJWT(t)

	for _, tc := range []struct {
		userType string
		want     int
	}{
		{"student", http.StatusForbidden},
		{"staff", http.StatusOK},
		{"admin", http.StatusOK},
	} {
		var reached bool
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+tokenFor(t, jwt, 1, tc.userType))
		rec := httptest.NewRecorder()

		chain := RequireSUAuth(jwt)(RequireSUStaff()(passthrough(&reached)))
		chain.ServeHTTP(rec, req)

		if rec.Code != tc.want {
			t.Errorf("%s: got %d, want %d", tc.userType, rec.Code, tc.want)
		}
	}
}

// Nothing should reach a claims lookup without the middleware, but a nil
// return is what callers are entitled to assume if it happens.
func TestSUClaimsFromReturnsNilWithoutTheMiddleware(t *testing.T) {
	if SUClaimsFrom(context.Background()) != nil {
		t.Error("claims appeared from an untouched context")
	}
}
