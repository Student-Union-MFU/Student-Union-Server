package handler

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"su-server/internal/service"
)

type OAuthHandler struct {
	oauthService *service.OAuthService
	jwtService   *service.JWTService
}

func NewOAuthHandler(oauthService *service.OAuthService, jwtService *service.JWTService) *OAuthHandler {
	return &OAuthHandler{
		oauthService: oauthService,
		jwtService:   jwtService,
	}
}

/*
Where to send the browser after a successful callback, keyed by the value of
`?redirect=` on /auth/google.

⚠ An ALLOWLIST, deliberately, and not a general `?next=<url>`.

What comes back from the callback is a signed staff JWT. A `next` parameter that
accepts arbitrary destinations is therefore not merely an open redirect — it
hands that token to whoever wrote the link, and `?next=https://evil.com` becomes
credential theft that looks exactly like a normal sign-in to the person
clicking. Validating the string does not rescue it either: "must start with /"
still admits `//evil.com`, which browsers read as protocol-relative and resolve
to another origin.

Adding a destination is a code change and a review, which is the correct amount
of friction for something that decides where a credential gets delivered.

The keys:

  - "stats" — the page this binary serves itself, at /su-server/stats. A
    root-relative path, so it cannot leave this origin whatever host the server
    is reached on.

  - "web" — the Next.js dashboard in the su-server-stats-dashboard repo, which
    runs as its own app on its own origin and therefore needs an ABSOLUTE URL.
    Registered only when STATS_WEB_ORIGIN is set and parses as an http(s) origin.

⚠ "web" is the one entry that can send a token off this origin, and it is safe
for a reason worth stating: the destination comes from the SERVER'S
ENVIRONMENT, never from the request. An operator who sets STATS_WEB_ORIGIN has
already decided to trust that origin. What makes `?next=` dangerous is not that
the target is remote, it is that the *caller* chooses it — and no caller chooses
anything here beyond a key that either is or is not in this map.

Unset STATS_WEB_ORIGIN and the key simply does not resolve: the flow falls
through to the JSON body, which is what the Next app's paste-a-token fallback
expects. An unconfigured server degrades rather than breaks.
*/
var oauthRedirectTargets = buildRedirectTargets()

func buildRedirectTargets() map[string]string {
	out := map[string]string{
		"stats": "/su-server/stats",
	}

	origin := strings.TrimRight(os.Getenv("STATS_WEB_ORIGIN"), "/")
	if origin == "" {
		return out
	}

	/*
	   Parsed rather than trusted, because a malformed value here fails in the
	   worst possible way: a Location header that is not a URL sends the
	   operator somewhere unpredictable while holding a live staff token. A
	   scheme and a host are the whole requirement — anything else and the entry
	   is dropped with a warning, leaving the JSON fallback in place.
	*/
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		slog.Error("STATS_WEB_ORIGIN is not an http(s) origin — the web dashboard redirect is disabled", "value", origin)
		return out
	}

	out["web"] = origin + "/signin/callback"
	return out
}

// GoogleLogin handles GET /auth/google
// redirects the user to Google's OAuth2 login page
func (h *OAuthHandler) GoogleLogin(w http.ResponseWriter, r *http.Request) {
	state := generateState()

	// store state in cookie to verify later in callback
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		HttpOnly: true,
		Secure:   os.Getenv("ENV") == "production",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   300, // 5 minutes
	})

	/*
	   Remember the caller's destination across the trip to Google, in a cookie
	   of its own.

	   It could ride inside `state` instead, since that is already round-tripped
	   by Google — but `state` is the CSRF check, compared byte-for-byte against
	   its cookie in the callback. Making it carry a payload means changing that
	   comparison, and the CSRF defence of every SU sign-in is not worth
	   reworking to save one cookie.

	   Only a key that is actually in the allowlist is stored, so the callback
	   never has to trust what it reads back out.
	*/
	if _, ok := oauthRedirectTargets[r.URL.Query().Get("redirect")]; ok {
		http.SetCookie(w, &http.Cookie{
			Name:     "oauth_redirect",
			Value:    r.URL.Query().Get("redirect"),
			Path:     "/",
			HttpOnly: true,
			Secure:   os.Getenv("ENV") == "production",
			SameSite: http.SameSiteLaxMode,
			MaxAge:   300, // same 5 minutes as the state it travels with
		})
	}

	url := h.oauthService.GetAuthURL(state)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

// GoogleCallback handles GET /auth/google/callback
// Google redirects here after the user logs in
func (h *OAuthHandler) GoogleCallback(w http.ResponseWriter, r *http.Request) {
	// verify state to prevent CSRF
	cookie, err := r.Cookie("oauth_state")
	if err != nil {
		http.Error(w, "missing state cookie", http.StatusBadRequest)
		return
	}

	if r.URL.Query().Get("state") != cookie.Value {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}

	// exchange code for user info + upsert user
	code := r.URL.Query().Get("code")
	user, err := h.oauthService.ExchangeCode(r.Context(), code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	// generate JWT
	token, err := h.jwtService.Generate(user.ID, user.UserType)
	if err != nil {
		http.Error(w, "failed to generate token", http.StatusInternalServerError)
		return
	}

	/*
	   If the flow began with a known ?redirect= key, hand the token back by
	   redirecting instead of printing JSON at the operator.

	   ⚠ The token goes in the FRAGMENT, never the query string. A fragment is
	   not sent to the server by any browser, so it stays out of
	   middleware.Logger, out of any access log or proxy log in front of this,
	   and out of the Referer header of whatever the page loads next. The same
	   value in `?token=` would be written to disk on every hop it passes.

	   It is still in the address bar and in history at this point, which is why
	   the page clears it with history.replaceState the moment it has read it.

	   No cookie, or a key that is no longer in the allowlist, falls through to
	   the JSON body below — byte-identical to what this endpoint has always
	   returned, so every existing caller is untouched.
	*/
	if c, err := r.Cookie("oauth_redirect"); err == nil {
		if dest, ok := oauthRedirectTargets[c.Value]; ok {
			// Consume it: a stale redirect cookie outliving its flow would
			// bounce a later plain sign-in somewhere it never asked to go.
			http.SetCookie(w, &http.Cookie{
				Name:     "oauth_redirect",
				Value:    "",
				Path:     "/",
				HttpOnly: true,
				Secure:   os.Getenv("ENV") == "production",
				SameSite: http.SameSiteLaxMode,
				MaxAge:   -1,
			})
			http.Redirect(w, r, dest+"#token="+url.QueryEscape(token), http.StatusSeeOther)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"token": token,
		"user":  user,
	})
}

// GoogleVerify handles POST /auth/google/verify
// for Flutter mobile — Flutter does the Google login itself
// and sends the ID token here to verify
func (h *OAuthHandler) GoogleVerify(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDToken string `json:"id_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if body.IDToken == "" {
		http.Error(w, "id_token is required", http.StatusBadRequest)
		return
	}

	user, err := h.oauthService.VerifyIDToken(r.Context(), body.IDToken)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	token, err := h.jwtService.Generate(user.ID, user.UserType)
	if err != nil {
		http.Error(w, "failed to generate token", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"token": token,
		"user":  user,
	})
}

// generateState generates a random state string for CSRF protection
func generateState() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}
