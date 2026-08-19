package service

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"su-server/internal/model"
	"su-server/internal/repository"

	"golang.org/x/crypto/bcrypt"
)

/*
Club Fair sign-in, two ways into one account.

Google is the primary path: the address is @lamduan.mfu.ac.th, so it establishes
both who the student is and their student id (the email local part) without
either being typed. A password is the fallback, for a student whose Google
session is not available on the phone in their hand at the fair.

Both land on the same clubfair_users row, keyed on email — see
UpsertGoogleIdentity. A student who signed up with a password and later taps
"continue with Google" is linked, not duplicated.
*/

// bcrypt cost 10, matching cmd/createadmin and the note on
// clubfair_users.password_hash. Cost is the one parameter that must agree
// everywhere a hash in this column is produced.
const clubFairBcryptCost = 10

var (
	ErrClubFairNoAccount      = errors.New("clubfair: no account for those details")
	ErrClubFairWrongPassword  = errors.New("clubfair: wrong password")
	ErrClubFairNoPasswordSet  = errors.New("clubfair: account has no password set")
	ErrClubFairAccountFlagged = errors.New("clubfair: account is flagged")
	ErrClubFairBadPhone       = errors.New("clubfair: not a Thai mobile number")
	ErrClubFairWeakPassword   = errors.New("clubfair: password does not meet the policy")
	ErrClubFairEmailTaken     = errors.New("clubfair: that email already has an account")
	// Not an authentication failure — a registration policy one. Its own value so
	// the handler can answer 403 with a message that says what is wrong, rather
	// than the generic 401 every Google verification failure lands on.
	ErrClubFairNotMFU       = errors.New("clubfair: only MFU students are allowed")
	ErrClubFairNameRequired = errors.New("clubfair: first name and surname are required")
	// Also a policy error rather than an authentication one: the token verified,
	// the address is MFU's, the student simply is not in an intake the fair is
	// for. See eligibleIntake.
	ErrClubFairIntakeNotEligible = errors.New("clubfair: student id is not in an eligible intake")
)

// The intakes the fair is run for, as the leading digits of a student id.
//
// `6831503029` begins with the Buddhist-era year of entry, so "69" is the 2569
// intake. It was 69 alone on the theory that a club fair is for first-years;
// in practice the years above them come too, and turning a second-year away at
// the door of an event they are standing outside is the wrong failure. 66-69
// is the four intakes currently on campus for a four-year degree — the whole
// of the undergraduate body, which is who the fair is actually for.
//
// Read from CLUBFAIR_INTAKE_PREFIXES rather than compiled in because the answer
// changes every year and a redeploy is cheaper than a release; comma-separated,
// and `*` to turn the rule off entirely.
const defaultIntakePrefixes = "66,67,68,69"

// intakePrefixes reads the env on every call.
//
// A package-level var initialised once would be marginally cheaper and could not
// be exercised by a test with t.Setenv, which matters more: this is the one rule
// here that decides whether a real student gets in, so it has to be testable.
// The cost is a map lookup on a path that already does a network round trip to
// Google and a bcrypt compare.
func intakePrefixes() []string {
	raw := strings.TrimSpace(os.Getenv("CLUBFAIR_INTAKE_PREFIXES"))
	if raw == "" {
		raw = defaultIntakePrefixes
	}
	if raw == "*" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// AllowedIntakeLabel is the configured intakes, for the handler's message.
// A student told "you are not eligible" without being told what is eligible has
// been given a dead end rather than an answer.
func AllowedIntakeLabel() string {
	return strings.Join(intakePrefixes(), ", ")
}

// eligibleIntake reports whether a student id may open a NEW account.
//
// Deliberately not consulted for an account that already exists — see
// SignInWithGoogle. The rule is about who the fair is for, and applying it to
// every sign-in would lock out staff, admins and anyone who registered before
// it existed, on their next login, with no way back in.
func eligibleIntake(studentID string) bool {
	prefixes := intakePrefixes()
	if len(prefixes) == 0 {
		return true
	}
	id := strings.TrimSpace(studentID)
	for _, prefix := range prefixes {
		if strings.HasPrefix(id, prefix) {
			return true
		}
	}
	return false
}

// Thai mobile numbers, stored in the national 0XXXXXXXXX form. Mirrors the
// client's own rule so a number accepted on the form is accepted here.
var thaiMobile = regexp.MustCompile(`^0[689]\d{8}$`)

// NormalisePhone strips punctuation and folds a +66 prefix back to a leading 0.
//
// Both sides of a login comparison go through this. People type their own number
// with spaces one day and dashes the next, and being told your own number is
// unknown because of a space is indefensible.
func NormalisePhone(raw string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, raw)

	// A national number is ten digits, so eleven starting 66 is +66 with the
	// leading zero dropped — how the number is written everywhere outside Thailand.
	if len(digits) == 11 && strings.HasPrefix(digits, "66") {
		return "0" + digits[2:]
	}
	return digits
}

func IsValidThaiMobile(raw string) bool {
	return thaiMobile.MatchString(NormalisePhone(raw))
}

// checkPasswordPolicy mirrors the client's rule: eight characters, at least one
// letter and one digit. Enforced here as well because the client is a phone.
func checkPasswordPolicy(password string) error {
	if len(password) < 8 {
		return ErrClubFairWeakPassword
	}
	var hasLetter, hasDigit bool
	for _, r := range password {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			hasLetter = true
		}
	}
	if !hasLetter || !hasDigit {
		return ErrClubFairWeakPassword
	}
	return nil
}

type ClubFairAuthService struct {
	repo   *repository.ClubFairAuthRepository
	tokens *ClubFairTokenService
}

func NewClubFairAuthService(
	repo *repository.ClubFairAuthRepository,
	tokens *ClubFairTokenService,
) *ClubFairAuthService {
	return &ClubFairAuthService{repo: repo, tokens: tokens}
}

// Session is what every successful sign-in returns.
type ClubFairSession struct {
	Token string                   `json:"token"`
	User  model.PublicClubFairUser `json:"user"`
}

func (s *ClubFairAuthService) session(user *model.ClubFairUser) (*ClubFairSession, error) {
	token, err := s.tokens.Sign(user.ID, user.Role)
	if err != nil {
		return nil, err
	}
	return &ClubFairSession{Token: token, User: model.NewPublicClubFairUser(*user)}, nil
}

// SignInWithGoogle verifies a Google ID token and returns a Club Fair session.
//
// The verification is [VerifyGoogleIdentity], shared with the SU app's own
// Google path — the MFU domain check, the hosted-domain check and the audience
// check are the security boundary for both, and two copies of them would be one
// copy too many.
func (s *ClubFairAuthService) SignInWithGoogle(ctx context.Context, idToken string) (*ClubFairSession, error) {
	identity, err := VerifyGoogleIdentity(ctx, idToken)
	if err != nil {
		return nil, err
	}

	// The intake rule applies to the account this sign-in would CREATE, not to
	// the person signing in. UpsertGoogleIdentity inserts on a first sign-in, so
	// "is there a row for this address already" is exactly the question — and
	// asking it here rather than inside the upsert keeps existing students,
	// staff and admins signing in untouched however the rule is configured.
	if _, err := s.repo.FindByEmail(ctx, identity.Email); err != nil {
		if !errors.Is(err, repository.ErrClubFairUserNotFound) {
			return nil, err
		}
		if !eligibleIntake(identity.StudentID) {
			return nil, ErrClubFairIntakeNotEligible
		}
	}

	// Google gives the two name parts separately for Workspace accounts. Falling
	// back to splitting the display name is better than storing an empty
	// surname, which the column forbids anyway.
	firstName, surname := identity.GivenName, identity.FamilyName
	if firstName == "" && surname == "" {
		firstName, surname = splitDisplayName(identity.DisplayName())
	}
	if firstName == "" {
		firstName = identity.StudentID
	}
	if surname == "" {
		// NOT NULL, and a single space is visibly a gap the student can correct
		// rather than a fabricated name.
		surname = "-"
	}

	user, err := s.repo.UpsertGoogleIdentity(
		ctx, firstName, surname, identity.Email,
		identity.StudentID, identity.Picture, identity.Sub,
	)
	if err != nil {
		return nil, err
	}
	if user.IsFlagged {
		return nil, ErrClubFairAccountFlagged
	}
	return s.session(user)
}

// splitDisplayName is a last resort — see SignInWithGoogle.
func splitDisplayName(name string) (first, last string) {
	parts := strings.Fields(name)
	switch len(parts) {
	case 0:
		return "", ""
	case 1:
		return parts[0], ""
	default:
		return parts[0], strings.Join(parts[1:], " ")
	}
}

// A student id: ten digits, the first two being the Buddhist-era year of entry.
//
// Matched on shape rather than on a prefix list, deliberately. The intake rule
// (see eligibleIntake) decides who may *open* an account; this only decides how
// to read what someone typed into one box, and applying the intake rule here
// would lock out staff, admins and anyone who registered before it existed.
var studentIDShape = regexp.MustCompile(`^\d{8,12}$`)

// SignInWithPassword matches on a phone number, a student id, or an email.
//
// One box, three things people know about themselves. It was phone-only, which
// is what the sign-up form collects — but the identifier a student actually has
// memorised is their student id, it is printed on the card in their pocket, and
// it is what they type into every other university system.
//
// The student id needs no lookup of its own: for an MFU address the local part
// *is* the student id, which is why RegisterWithPassword derives one from the
// other. So `6831503029` is resolved to `6831503029@lamduan.mfu.ac.th` and found
// by email, on the same unique index the Google path joins on.
//
// Deliberately answers the same error for "no such account" and "wrong
// password". Distinguishing them turns this endpoint into a way to test which
// student ids are registered, and a student id is guessable in a way a password
// is not — the whole roster is a contiguous range of numbers.
func (s *ClubFairAuthService) SignInWithPassword(ctx context.Context, identifier, password string) (*ClubFairSession, error) {
	user, err := s.findByIdentifier(ctx, identifier)

	if errors.Is(err, repository.ErrClubFairUserNotFound) {
		// Compare against a dummy hash anyway, so a missing account and a wrong
		// password take about the same time.
		bcrypt.CompareHashAndPassword([]byte(dummyBcryptHash), []byte(password))
		return nil, ErrClubFairNoAccount
	}
	if err != nil {
		return nil, err
	}
	if user.IsFlagged {
		return nil, ErrClubFairAccountFlagged
	}
	if !user.HasPassword() {
		return nil, ErrClubFairNoPasswordSet
	}
	if bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(password)) != nil {
		return nil, ErrClubFairWrongPassword
	}
	return s.session(user)
}

// findByIdentifier reads what the caller typed and looks it up accordingly.
//
// The order matters. A phone number is checked first because it is the most
// constrained shape — `^0[689]\d{8}$` — and a Thai mobile cannot be mistaken for
// anything else here. A student id is the looser digit shape, so it has to come
// second or `0624202483` would be read as a student id and never found.
func (s *ClubFairAuthService) findByIdentifier(
	ctx context.Context, identifier string,
) (*model.ClubFairUser, error) {
	trimmed := strings.TrimSpace(identifier)

	if phone := NormalisePhone(trimmed); thaiMobile.MatchString(phone) {
		return s.repo.FindByPhone(ctx, phone)
	}

	if strings.Contains(trimmed, "@") {
		return s.repo.FindByEmail(ctx, strings.ToLower(trimmed))
	}

	if studentIDShape.MatchString(trimmed) {
		return s.repo.FindByEmail(ctx, trimmed+"@"+MFUDomain)
	}

	// Not a shape this can look up. Reported as "no account" rather than as a
	// format error, for the same reason the two failures above share a message:
	// "that is not a valid student id" is a probe telling the caller which
	// shapes exist.
	return nil, repository.ErrClubFairUserNotFound
}

// A valid bcrypt hash of a value nothing will ever submit. Its only job is to
// make the "no such account" path cost a real bcrypt comparison.
const dummyBcryptHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

// RegisterWithPassword creates an account without Google.
//
// Email is still required and still has to be an MFU address: it is the key both
// credential paths join on, and without the domain rule this endpoint would let
// anyone at all create a Club Fair account.
func (s *ClubFairAuthService) RegisterWithPassword(
	ctx context.Context,
	firstName, surname, email, phone, studentID, school, major, password string,
) (*ClubFairSession, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if !strings.HasSuffix(email, "@"+MFUDomain) {
		return nil, ErrClubFairNotMFU
	}
	if strings.TrimSpace(firstName) == "" || strings.TrimSpace(surname) == "" {
		return nil, ErrClubFairNameRequired
	}

	normalisedPhone := NormalisePhone(phone)
	if !thaiMobile.MatchString(normalisedPhone) {
		return nil, ErrClubFairBadPhone
	}
	if err := checkPasswordPolicy(password); err != nil {
		return nil, err
	}

	// Derived from the address rather than taken from the form: the local part of
	// an MFU email *is* the student id, so asking for it separately invites a
	// mismatch between the two.
	if studentID == "" {
		studentID, _, _ = strings.Cut(email, "@")
	}

	// Unconditional here, unlike the Google path: this endpoint only ever
	// creates an account, so there is no existing row to grandfather in.
	if !eligibleIntake(studentID) {
		return nil, ErrClubFairIntakeNotEligible
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), clubFairBcryptCost)
	if err != nil {
		return nil, err
	}

	user, err := s.repo.CreateWithPassword(
		ctx, strings.TrimSpace(firstName), strings.TrimSpace(surname),
		email, normalisedPhone, studentID,
		strings.TrimSpace(school), strings.TrimSpace(major), string(hash),
	)
	if err != nil {
		// 23505 is unique_violation: the email, phone or student id is taken.
		if strings.Contains(err.Error(), "23505") {
			return nil, ErrClubFairEmailTaken
		}
		return nil, err
	}
	return s.session(user)
}

// SetPassword gives a Google-only account the password fallback, or replaces an
// existing one.
func (s *ClubFairAuthService) SetPassword(ctx context.Context, userID int, password string) error {
	if err := checkPasswordPolicy(password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), clubFairBcryptCost)
	if err != nil {
		return err
	}
	return s.repo.SetPassword(ctx, userID, string(hash))
}

func (s *ClubFairAuthService) GetByID(ctx context.Context, id int) (*model.ClubFairUser, error) {
	return s.repo.GetByID(ctx, id)
}

// Delete removes the authenticated student's own account. The cascade lives in
// the schema, so there is nothing to unwind here.
func (s *ClubFairAuthService) Delete(ctx context.Context, userID int) error {
	return s.repo.Delete(ctx, userID)
}

// UpdateProfile writes the fields the student owns. A nil argument leaves that
// column alone.
func (s *ClubFairAuthService) UpdateProfile(
	ctx context.Context,
	id int,
	phone, school, major *string,
) (*model.ClubFairUser, error) {
	if phone != nil {
		normalised := NormalisePhone(*phone)
		if !thaiMobile.MatchString(normalised) {
			return nil, ErrClubFairBadPhone
		}
		phone = &normalised
	}
	return s.repo.UpdateProfile(ctx, id, phone, school, major)
}
