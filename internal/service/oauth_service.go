package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"su-server/internal/model"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// The Google Workspace domain every account must belong to. Named once so the
// email-suffix check and the hosted-domain check cannot drift apart.
const mfuDomain = "lamduan.mfu.ac.th"

// isTrue reads a JSON value that may be either `true` or the string "true".
// Google's tokeninfo endpoint returns the latter; a locally-verified ID token
// yields the former.
func isTrue(raw json.RawMessage) bool {
	switch string(raw) {
	case "true", `"true"`:
		return true
	default:
		return false
	}
}

type OAuthService struct {
	userService  *UserService
	oauthConfig  *oauth2.Config
}

type GoogleUserInfo struct {
	Sub        string `json:"sub"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	Picture    string `json:"picture"`
}

func NewOAuthService(userService *UserService) *OAuthService {
	config := &oauth2.Config{
		ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		RedirectURL:  os.Getenv("GOOGLE_REDIRECT_URL"),
		Scopes: []string{
			"https://www.googleapis.com/auth/userinfo.email",
			"https://www.googleapis.com/auth/userinfo.profile",
		},
		Endpoint: google.Endpoint,
	}

	return &OAuthService{
		userService: userService,
		oauthConfig: config,
	}
}

// GetAuthURL returns the Google OAuth2 login URL
func (s *OAuthService) GetAuthURL(state string) string {
	return s.oauthConfig.AuthCodeURL(state, oauth2.AccessTypeOffline)
}

// ExchangeCode exchanges the OAuth2 code for a Google token
// then fetches user info and upserts the user in the DB
func (s *OAuthService) ExchangeCode(ctx context.Context, code string) (*model.User, error) {
	// exchange code for token
	token, err := s.oauthConfig.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("code exchange failed: %w", err)
	}

	// fetch user info from Google
	userInfo, err := s.fetchGoogleUserInfo(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user info: %w", err)
	}

	// validate MFU domain
	if !strings.HasSuffix(userInfo.Email, "@lamduan.mfu.ac.th") {
		return nil, fmt.Errorf("only MFU students are allowed")
	}

	// extract student id from email prefix
	studentID, _, _ := strings.Cut(userInfo.Email, "@")

	// upsert user
	user, err := s.userService.UpsertUser(ctx, model.User{
		UserType:     model.UserTypeStudent,
		Name:         userInfo.Name,
		Email:        userInfo.Email,
		AvatarURL:    &userInfo.Picture,
		StudentID:    &studentID,
		OAuthSubject: userInfo.Sub,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to upsert user: %w", err)
	}

	return user, nil
}

// VerifyIDToken verifies a Google ID token from Flutter mobile
func (s *OAuthService) VerifyIDToken(ctx context.Context, idToken string) (*model.User, error) {
	// fetch Google's public keys and verify token
	resp, err := http.Get("https://oauth2.googleapis.com/tokeninfo?id_token=" + idToken)
	if err != nil {
		return nil, fmt.Errorf("failed to verify token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("invalid token")
	}

	// Every field here is already inside the ID token — reading more of them
	// costs no extra OAuth scope and does not change what the user is asked to
	// consent to.
	var info struct {
		Sub     string `json:"sub"`
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
		Aud     string `json:"aud"`

		// tokeninfo renders booleans as the strings "true"/"false", while a
		// locally-parsed ID token carries a real bool. RawMessage accepts both
		// so this keeps working if the verification method ever changes.
		EmailVerified json.RawMessage `json:"email_verified"`
		// Google Workspace hosted domain. Absent on consumer accounts, which is
		// what makes it a stronger signal than the email suffix: the suffix is
		// just text in a claim, while hd states that Google itself considers
		// this account to belong to the domain.
		HD string `json:"hd"`

		GivenName  string `json:"given_name"`
		FamilyName string `json:"family_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("failed to decode token info: %w", err)
	}

	// verify the token was issued for your app
	if info.Aud != os.Getenv("GOOGLE_CLIENT_ID") {
		return nil, fmt.Errorf("token audience mismatch")
	}

	// An unverified address can be set to anything, so the domain check below
	// would be meaningless without this.
	if !isTrue(info.EmailVerified) {
		return nil, fmt.Errorf("email address is not verified")
	}

	// validate MFU domain
	if !strings.HasSuffix(info.Email, "@"+mfuDomain) {
		return nil, fmt.Errorf("only MFU students are allowed")
	}

	// When Google tells us which domain owns the account, hold it to that.
	// Empty means a consumer account, which the suffix check above has already
	// rejected — so this only ever tightens, never locks out an account the
	// previous rule allowed.
	if info.HD != "" && info.HD != mfuDomain {
		return nil, fmt.Errorf("only MFU students are allowed")
	}

	studentID, _, _ := strings.Cut(info.Email, "@")

	// `name` is populated for Workspace accounts, but it is not guaranteed —
	// a profile with only the two parts set leaves it empty, and an empty
	// display name is worse than a rebuilt one.
	displayName := info.Name
	if displayName == "" {
		displayName = strings.TrimSpace(info.GivenName + " " + info.FamilyName)
	}

	user, err := s.userService.UpsertUser(ctx, model.User{
		UserType:     model.UserTypeStudent,
		Name:         displayName,
		Email:        info.Email,
		AvatarURL:    &info.Picture,
		StudentID:    &studentID,
		OAuthSubject: info.Sub,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to upsert user: %w", err)
	}

	return user, nil
}

// fetchGoogleUserInfo fetches user info from Google using the OAuth2 token
func (s *OAuthService) fetchGoogleUserInfo(ctx context.Context, token *oauth2.Token) (*GoogleUserInfo, error) {
	client := s.oauthConfig.Client(ctx, token)
	resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var userInfo GoogleUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&userInfo); err != nil {
		return nil, err
	}

	return &userInfo, nil
}
