package model

import (
	"strings"
	"time"
)

// ClubFairUser is a row of clubfair_users.
//
// Separate from [User] for the reason migration 000014 spells out: `users`
// requires a Google `oauth_subject` on every row, and Club Fair has to hold an
// account that signed up with a password instead. The two tables therefore have
// two ids for the same student, which is why Club Fair tokens carry their own
// audience — see ClubFairTokenService.
type ClubFairUser struct {
	ID        int    `json:"id"`
	Role      string `json:"role"`
	FirstName string `json:"first_name"`
	Surname   string `json:"surname"`
	Email     string `json:"email"`

	Phone     *string `json:"phone"`
	StudentID *string `json:"student_id"`
	School    *string `json:"school"`
	Major     *string `json:"major"`
	AvatarURL *string `json:"avatar_url"`

	OAuthSubject *string `json:"oauth_subject"`

	// bcrypt, and tagged out of JSON the way Booth.Secret is: responses are
	// built from PublicClubFairUser, but a direct marshal of this type must not
	// leak the hash either.
	PasswordHash *string `json:"-"`

	IsFlagged bool      `json:"is_flagged"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// HasPassword reports whether this account can sign in with one.
func (u ClubFairUser) HasPassword() bool {
	return u.PasswordHash != nil && *u.PasswordHash != ""
}

// ProfileComplete reports whether sign-up actually finished.
//
// A Google sign-in creates the row — see UpsertGoogleIdentity — so "the account
// exists" is not the same question as "the student has finished signing up".
// Everything below is asked for on the sign-up form and cannot come from Google:
// a password (so the account has a second way in), a phone number (which is what
// the password login takes), and the school and major.
//
// Computed here rather than in the client so the rule has one home. The app
// gates on the flag; it does not re-derive it from which fields came back null.
func (u ClubFairUser) ProfileComplete() bool {
	return u.HasPassword() &&
		filled(u.Phone) && filled(u.School) && filled(u.Major)
}

func filled(s *string) bool { return s != nil && strings.TrimSpace(*s) != "" }

// PublicClubFairUser is what the API returns for "who am I".
//
// No password field at all, following PublicBooth: the hash cannot escape by
// someone forgetting to blank it, because there is nothing to blank.
// `oauth_subject` is also absent — the client has no use for Google's internal
// id, and it is a stable cross-service identifier.
type PublicClubFairUser struct {
	ID        int     `json:"id"`
	Role      string  `json:"role"`
	FirstName string  `json:"first_name"`
	Surname   string  `json:"surname"`
	Email     string  `json:"email"`
	Phone     *string `json:"phone"`
	StudentID *string `json:"student_id"`
	School    *string `json:"school"`
	Major     *string `json:"major"`
	AvatarURL *string `json:"avatar_url"`

	// Whether this account has a password set. The app needs to know so it can
	// prompt a Google-only student to add one, without being told the hash.
	HasPassword bool `json:"has_password"`

	// False while a Google sign-in has created the account but sign-up has not
	// been finished — see [ClubFairUser.ProfileComplete]. The app sends such a
	// student to the sign-up form instead of the fair.
	ProfileComplete bool `json:"profile_complete"`
}

func NewPublicClubFairUser(u ClubFairUser) PublicClubFairUser {
	return PublicClubFairUser{
		ID:          u.ID,
		Role:        u.Role,
		FirstName:   u.FirstName,
		Surname:     u.Surname,
		Email:       u.Email,
		Phone:       u.Phone,
		StudentID:   u.StudentID,
		School:      u.School,
		Major:       u.Major,
		AvatarURL:   u.AvatarURL,
		HasPassword: u.HasPassword(),

		ProfileComplete: u.ProfileComplete(),
	}
}

// ClubFairCheckIn is one stamp: this student collected this booth.
type ClubFairCheckIn struct {
	ID       int64  `json:"id"`
	ClientID string `json:"client_id"`
	UserID   int    `json:"-"`
	BoothID  int    `json:"booth_id"`

	// What the phone said, and what the server heard. Both are returned: the
	// client reconciles its own queue against the first and displays the second.
	DeviceTime       time.Time `json:"device_time"`
	ServerReceivedAt time.Time `json:"server_received_at"`
}

// ClubFairAnnouncement is one post in the channel, with its reactions rolled up.
type ClubFairAnnouncement struct {
	ID       int64  `json:"id"`
	AuthorID *int   `json:"author_id"`
	Author   string `json:"author"`
	Body     string `json:"body"`

	// An instant, never a formatted string — the client renders it in the
	// reader's own locale and against the current clock.
	PostedAt time.Time `json:"posted_at"`

	Reactions []ClubFairReaction `json:"reactions"`
}

// ClubFairReaction is one emoji bucket under a post.
//
// `Count` is everyone; `Mine` is whether the requesting student is one of them.
// Two fields because a chip has to show both at once, and only the caller's own
// token can decide the second.
type ClubFairReaction struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
	Mine  bool   `json:"mine"`
}

// ClubFairPrizeTier is a stamp count that earns something.
type ClubFairPrizeTier struct {
	ID          int     `json:"id"`
	Threshold   int     `json:"threshold"`
	Name        string  `json:"name"`
	Description *string `json:"description"`

	// Derived per-caller, not stored on the tier: whether this student has
	// reached it, and whether they have already collected it.
	Reached bool `json:"reached"`
	Claimed bool `json:"claimed"`
}

// ClubFairDashboardStats is the fair at a glance, for the admin console.
//
// Same shape as WBW's DashboardStats: four counts, one query, one screen.
// FullSweeps counts students who have stamped every booth — the number the
// prize table needs to know how many "Full sweep" tiers to have on hand.
type ClubFairDashboardStats struct {
	Students      int `json:"students"`
	TotalCheckins int `json:"total_checkins"`
	PrizesClaimed int `json:"prizes_claimed"`
	FullSweeps    int `json:"full_sweeps"`
}

// ClubFairProgress is the whole of what Home renders, in one response.
//
// One call rather than four, because Home shows the count, the percentage, the
// grid and the prize tiles together — and four calls means four chances for the
// screen to render half a state.
type ClubFairProgress struct {
	Visited int `json:"visited"`
	Total   int `json:"total"`

	// The booth ids collected, so the checkpoint grid can light the right cells
	// rather than the first N of them.
	VisitedBoothIDs []int `json:"visited_booth_ids"`

	// Standing among all students. A pointer because it is genuinely unknown
	// until the leaderboard query runs, and the client renders an em dash for
	// null rather than inventing a number.
	Rank *int `json:"rank"`

	Prizes []ClubFairPrizeTier `json:"prizes"`
}

// PublicPrizeTier is a tier with nothing caller-specific on it.
//
// [ClubFairPrizeTier] carries `reached` and `claimed`, which only mean anything
// against a particular student's stamps. Serving that shape on a public endpoint
// would answer both as false for everyone — not absent, but *wrong*, and wrong
// in the direction a reader would believe. Same reasoning as PublicBooth: the
// field cannot be misread if the type does not have it.
type PublicPrizeTier struct {
	ID          int     `json:"id"`
	Threshold   int     `json:"threshold"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
}

// ClubFairInfo is the single row of clubfair_fair_info: when and where the fair
// is.
//
// It exists so the dates stop being a constant in two clients that have to be
// edited together — see migration 000023. `starts_at` and `ends_at` are instants;
// every client formats them itself, because a date rendered on the server is a
// date rendered in the server's locale.
type ClubFairInfo struct {
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`

	// Thai original, English translation, null falling back to Thai.
	Venue    *string `json:"venue"`
	VenueEN  *string `json:"venue_en"`
	Notice   *string `json:"notice"`
	NoticeEN *string `json:"notice_en"`

	UpdatedAt time.Time `json:"updated_at"`
}

// ClubFairProgramEntry is one item in the running order.
//
// `EndsAt` is nullable because plenty of what happens at a fair has a start and
// no announced finish, and inventing one puts a closing time on the page that
// nobody promised.
type ClubFairProgramEntry struct {
	ID       int        `json:"id"`
	StartsAt time.Time  `json:"starts_at"`
	EndsAt   *time.Time `json:"ends_at"`

	Title    string  `json:"title"`
	TitleEN  *string `json:"title_en"`
	Detail   *string `json:"detail"`
	DetailEN *string `json:"detail_en"`

	// Free text — most of these happen on a stage or at a doorway rather than in
	// an area of the floor. `Zone` is the structured half, for the ones that do.
	Location   *string `json:"location"`
	LocationEN *string `json:"location_en"`
	Zone       *string `json:"zone"`

	// Drafts are invisible on the public endpoint. Staff see it so the dashboard
	// can show what is not live yet.
	IsPublished bool `json:"is_published"`
}

// ClubFairParticipant is a student as the staff dashboard sees them.
//
// Built the way PublicClubFairUser is: there is no password field to forget to
// blank, and no `oauth_subject` — a stable cross-service identifier that a
// roster screen has no use for. What is here is what someone at a prize table
// needs to identify the person in front of them, plus the two things staff can
// actually change.
type ClubFairParticipant struct {
	ID        int     `json:"id"`
	FirstName string  `json:"first_name"`
	Surname   string  `json:"surname"`
	Email     string  `json:"email"`
	StudentID *string `json:"student_id"`
	Phone     *string `json:"phone"`
	School    *string `json:"school"`
	Major     *string `json:"major"`

	Role      string `json:"role"`
	IsFlagged bool   `json:"is_flagged"`

	// How many booths they have collected. Joined in rather than fetched per
	// row: a roster screen sorted by progress is the one view of this data that
	// staff actually asked for, and 2,000 students is 2,000 extra queries.
	Visited int `json:"visited"`

	CreatedAt time.Time `json:"created_at"`
}

// ClubFairPrizeTierAdmin is a tier as the dashboard sees it.
//
// [PublicPrizeTier] plus the two things staff need before touching one: whether
// it is retired, and how many students have already collected it. The second is
// what makes the difference between a tier that can be deleted and one that
// cannot — `clubfair_prize_claim.tier_id` is ON DELETE RESTRICT precisely so a
// student holding a prize keeps a row that points somewhere, and a dashboard
// that offered Delete without showing the count would be offering an action
// that fails.
type ClubFairPrizeTierAdmin struct {
	PublicPrizeTier
	IsActive bool `json:"is_active"`
	Claims   int  `json:"claims"`
}
