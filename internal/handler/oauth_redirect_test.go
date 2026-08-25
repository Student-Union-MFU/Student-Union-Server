package handler

import (
	"net/url"
	"strings"
	"testing"
)

/*
The allowlist is the whole security boundary of the redirect flow. What travels
back through it is a signed staff JWT, so a key that resolves to somewhere
unintended does not merely redirect a browser — it delivers a credential.

These test the map and the destinations it can produce, which is the part that
decides where a token goes. The cookie plumbing around it is exercised by
actually running the flow.
*/

// The one destination that exists must stay same-origin and absolute-from-root.
func TestOAuthRedirectTargetIsARootRelativePath(t *testing.T) {
	dest, ok := oauthRedirectTargets["stats"]
	if !ok {
		t.Fatal("the stats destination is gone — the dashboard's sign-in link points at a key that no longer resolves")
	}
	if !strings.HasPrefix(dest, "/") {
		t.Errorf("destination must be root-relative, got %q", dest)
	}
	if strings.HasPrefix(dest, "//") {
		t.Errorf("%q is protocol-relative: a browser resolves it to another origin, which sends the token off-site", dest)
	}
}

/*
With nothing configured, every destination must be same-origin.

"web" is the one entry allowed to name another origin, and it only exists when
an operator sets STATS_WEB_ORIGIN. Absent that, an entry with a scheme and a
host is somebody having hardcoded a remote destination — the exact mistake the
allowlist exists to prevent.
*/
func TestNoDefaultRedirectTargetCanLeaveThisOrigin(t *testing.T) {
	t.Setenv("STATS_WEB_ORIGIN", "")

	for key, dest := range buildRedirectTargets() {
		u, err := url.Parse(dest)
		if err != nil {
			t.Errorf("%q: destination %q does not parse: %v", key, dest, err)
			continue
		}
		if u.Scheme != "" || u.Host != "" {
			t.Errorf("%q: destination %q names an origin (scheme=%q host=%q) — a staff token would be delivered off-site",
				key, dest, u.Scheme, u.Host)
		}
		if !strings.HasPrefix(dest, "/") || strings.HasPrefix(dest, "//") {
			t.Errorf("%q: destination %q is not an unambiguous same-origin path", key, dest)
		}
	}
}

// Unset means the key does not exist at all, so the flow falls through to the
// JSON body the paste-a-token fallback expects. An unconfigured server must
// degrade, not redirect somewhere empty.
func TestWebTargetIsAbsentUntilConfigured(t *testing.T) {
	t.Setenv("STATS_WEB_ORIGIN", "")

	if dest, ok := buildRedirectTargets()["web"]; ok {
		t.Errorf("web resolved to %q with STATS_WEB_ORIGIN unset", dest)
	}
}

func TestWebTargetPointsAtTheConfiguredOrigin(t *testing.T) {
	t.Setenv("STATS_WEB_ORIGIN", "http://localhost:3001")

	dest, ok := buildRedirectTargets()["web"]
	if !ok {
		t.Fatal("web did not resolve with STATS_WEB_ORIGIN set")
	}
	if want := "http://localhost:3001/signin/callback"; dest != want {
		t.Errorf("expected %q, got %q", want, dest)
	}
}

// A trailing slash in the environment must not become a double slash in the
// path. `//signin/callback` on some hosts is a different route, and on none of
// them is it the one intended.
func TestWebTargetToleratesATrailingSlash(t *testing.T) {
	t.Setenv("STATS_WEB_ORIGIN", "https://stats.example.org/")

	if dest := buildRedirectTargets()["web"]; dest != "https://stats.example.org/signin/callback" {
		t.Errorf("trailing slash was not trimmed: %q", dest)
	}
}

/*
A malformed origin must drop the entry rather than build a broken destination.

⚠ The failure this prevents is specific: a Location header that is not a URL
sends the operator somewhere unpredictable while the fragment carries a live
staff token. Refusing to register the key leaves the JSON fallback, which is
merely inconvenient.
*/
func TestMalformedWebOriginIsRefused(t *testing.T) {
	for _, bad := range []string{
		"localhost:3001",      // no scheme — parses, but as a path
		"ftp://stats.example", // not http(s)
		"https://",            // no host
		"://nonsense",         //
		"not a url at all",    //
		"javascript:alert(1)", // the classic
	} {
		t.Setenv("STATS_WEB_ORIGIN", bad)

		if dest, ok := buildRedirectTargets()["web"]; ok {
			t.Errorf("STATS_WEB_ORIGIN=%q was accepted and produced %q", bad, dest)
		}
	}
}

/*
Anything not in the map must miss. These are the values an attacker would try in
?redirect=, and the lookup — not a string check — is what refuses them.
*/
func TestUnknownRedirectKeysDoNotResolve(t *testing.T) {
	for _, key := range []string{
		"https://evil.com",
		"//evil.com",
		"/su-server/stats",       // the destination, not the key
		"stats/../../evil",       //
		"STATS",                  // the lookup is case-sensitive
		"stats ",                 // trailing space
		"",                       // no param at all
		"\\evil.com",             //
		"/\\evil.com",            //
		"http://localhost:8080/", //
	} {
		if dest, ok := oauthRedirectTargets[key]; ok {
			t.Errorf("%q resolved to %q — only exact allowlist keys may redirect", key, dest)
		}
	}
}

// One entry unconfigured, two with the web dashboard pointed at. If either
// count changes, someone widened where a credential can be delivered — which
// should be a deliberate, reviewed act rather than something noticed later.
func TestTheAllowlistStaysSmall(t *testing.T) {
	t.Setenv("STATS_WEB_ORIGIN", "")
	if n := len(buildRedirectTargets()); n != 1 {
		t.Errorf("unconfigured allowlist has %d entries, expected 1; each one is a place a staff token can be sent, so confirm the addition was intended and update this test", n)
	}

	t.Setenv("STATS_WEB_ORIGIN", "http://localhost:3001")
	if n := len(buildRedirectTargets()); n != 2 {
		t.Errorf("configured allowlist has %d entries, expected 2", n)
	}
}
