package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
)

/*
One implementation of "is this Google ID token real, and does it belong to an
MFU student".

This was inline in OAuthService.VerifyIDToken and served one table. Club Fair
needs the identical checks against a different table (clubfair_users), and the
one thing that must not happen is two copies of them: the copy that gets a fix
and the copy that does not. So the checks live here and the upsert lives with
whichever service owns the table.
*/

// The Google Workspace domain every account must belong to.
const MFUDomain = "lamduan.mfu.ac.th"

// GoogleIdentity is a verified Google account, after every check below passed.
type GoogleIdentity struct {
	Sub        string
	Email      string
	Name       string
	GivenName  string
	FamilyName string
	Picture    string

	// The email local part. For an MFU address that is the student id —
	// 6831503029@lamduan.mfu.ac.th — which is why it can be trusted here and
	// not when a student types it.
	StudentID string
}

// DisplayName is Name, or the two parts joined when Google left Name empty.
func (g GoogleIdentity) DisplayName() string {
	if g.Name != "" {
		return g.Name
	}
	return strings.TrimSpace(g.GivenName + " " + g.FamilyName)
}

// allowedAudiences is GOOGLE_CLIENT_ID plus anything in
// GOOGLE_ALLOWED_AUDIENCES (comma-separated).
//
// One app, several OAuth clients: the Android app, the iOS app and the web
// frontend each have their own, and an ID token's `aud` is whichever client
// asked for it. Additive so the existing single-client setup keeps working
// untouched.
//
// Android note: Credential Manager should request the token with the **web**
// client id as its server client id, in which case that is the `aud` and no new
// value is needed here.
func allowedAudiences() []string {
	out := []string{}
	if primary := os.Getenv("GOOGLE_CLIENT_ID"); primary != "" {
		out = append(out, primary)
	}
	for _, extra := range strings.Split(os.Getenv("GOOGLE_ALLOWED_AUDIENCES"), ",") {
		if extra = strings.TrimSpace(extra); extra != "" {
			out = append(out, extra)
		}
	}
	return out
}

// VerifyGoogleIdentity checks a Google ID token and returns who it belongs to.
//
// Every check here is load-bearing and the order matters:
//
//  1. Google's tokeninfo endpoint verifies the signature. A 200 means Google
//     itself vouches for the token's contents.
//  2. `aud` must be one of ours. Without this, a token minted for any other
//     Google app on the internet would be accepted — this is the check that
//     stops a token being replayed here from somewhere else.
//  3. `email_verified` must be true, or the domain check below is meaningless:
//     an unverified address can be set to anything.
//  4. The address must be @lamduan.mfu.ac.th, and if Google states a hosted
//     domain it must agree. `hd` is the stronger of the two — the suffix is
//     text in a claim, `hd` is Google asserting the account belongs to the
//     domain.
func VerifyGoogleIdentity(ctx context.Context, idToken string) (*GoogleIdentity, error) {
	if strings.TrimSpace(idToken) == "" {
		return nil, errors.New("no id token supplied")
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"https://oauth2.googleapis.com/tokeninfo?id_token="+idToken,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build verification request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to verify token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("invalid token")
	}

	var info struct {
		Sub     string `json:"sub"`
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
		Aud     string `json:"aud"`

		// tokeninfo renders booleans as the strings "true"/"false", while a
		// locally-parsed ID token carries a real bool. RawMessage accepts both.
		EmailVerified json.RawMessage `json:"email_verified"`
		HD            string          `json:"hd"`

		GivenName  string `json:"given_name"`
		FamilyName string `json:"family_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("failed to decode token info: %w", err)
	}

	audiences := allowedAudiences()
	if len(audiences) == 0 {
		// Refuse rather than accept everything. An unset GOOGLE_CLIENT_ID would
		// otherwise turn step 2 into a no-op.
		return nil, errors.New("no Google client id configured")
	}
	matched := false
	for _, aud := range audiences {
		if info.Aud == aud {
			matched = true
			break
		}
	}
	if !matched {
		return nil, errors.New("token audience mismatch")
	}

	if !isTrue(info.EmailVerified) {
		return nil, errors.New("email address is not verified")
	}

	if !strings.HasSuffix(info.Email, "@"+MFUDomain) {
		return nil, errors.New("only MFU students are allowed")
	}

	// Empty means a consumer account, which the suffix check has already
	// rejected — so this only tightens, never locks out an allowed account.
	if info.HD != "" && info.HD != MFUDomain {
		return nil, errors.New("only MFU students are allowed")
	}

	studentID, _, _ := strings.Cut(info.Email, "@")

	return &GoogleIdentity{
		Sub:        info.Sub,
		Email:      info.Email,
		Name:       info.Name,
		GivenName:  info.GivenName,
		FamilyName: info.FamilyName,
		Picture:    info.Picture,
		StudentID:  studentID,
	}, nil
}
