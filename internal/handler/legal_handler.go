package handler

import (
	_ "embed"
	"net/http"
)

// The App Store listing needs a privacy-policy URL and a support URL that
// resolve publicly. Embedded like the dashboard so the single-binary build
// stays true — no static directory to COPY and no path to get wrong at runtime.
//
//go:embed legal_privacy.html
var legalPrivacyHTML []byte

//go:embed legal_support.html
var legalSupportHTML []byte

// GET /privacy — the Club Fair privacy policy, Thai first with an English
// section below. Same text as the in-app policy screen; when one changes the
// other must change with it (SUKit/Sources/SUStrings, legal.privacy.*).
func LegalPrivacyPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(legalPrivacyHTML)
}

// GET /support — contact details and the handful of answers reviewers and
// students actually need (sign-in, offline stamps, prizes, account deletion).
func LegalSupportPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(legalSupportHTML)
}
