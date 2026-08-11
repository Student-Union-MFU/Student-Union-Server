package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"su-server/internal/model"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// The Google Workspace domain every account must belong to. Named once so the
// email-suffix check and the hosted-domain check cannot drift apart.
// Kept as an alias: MFUDomain in google_identity.go is now the single
// definition, and ExchangeCode below still reads this name.
const mfuDomain = MFUDomain

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
	userService *UserService
	oauthConfig *oauth2.Config
}

type GoogleUserInfo struct {
	Sub     string `json:"sub"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Picture string `json:"picture"`
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

// VerifyIDToken verifies a Google ID token from a mobile client and upserts
// the matching `users` row.
//
// The verification itself moved to VerifyGoogleIdentity, which Club Fair's auth
// also calls. That is deliberate: the checks are the security boundary for both
// apps, and two copies of them means one copy eventually misses a fix. What
// stays here is the only part that is specific to this table — turning a
// verified identity into a `users` row.
func (s *OAuthService) VerifyIDToken(ctx context.Context, idToken string) (*model.User, error) {
	identity, err := VerifyGoogleIdentity(ctx, idToken)
	if err != nil {
		return nil, err
	}

	studentID := identity.StudentID
	picture := identity.Picture

	user, err := s.userService.UpsertUser(ctx, model.User{
		UserType:     model.UserTypeStudent,
		Name:         identity.DisplayName(),
		Email:        identity.Email,
		AvatarURL:    &picture,
		StudentID:    &studentID,
		OAuthSubject: identity.Sub,
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
