package service

import (
	"context"
	"errors"
	"strings"
	"su-server/internal/model"
	"su-server/internal/repository"

	"golang.org/x/crypto/bcrypt"
)

/*
The staff dashboard's rules: who may change what about a participant, and what
makes a prize tier valid.

The participant rules are the interesting half, and all three exist because the
dashboard is a web page that staff will have open on a laptop at a fair:

  - **Only an admin may move a role.** Staff can flag an account; promoting one
    to staff is what hands out the ability to post announcements to two thousand
    students and to read the code that mints check-ins. That is an admin's
    decision.
  - **Nobody may act on their own account.** Flagging yourself locks you out of
    the dashboard you are standing in, and demoting yourself is the same thing
    one step slower. Neither is recoverable from the web — it needs
    `cmd/createclubfairstaff` and a terminal.
  - **The last admin cannot be demoted or flagged.** An event whose admin list is
    empty has nobody who can refill it. Checked in the service rather than as a
    constraint because "how many admins are left" is a question about a set, and
    the answer changes between the count and the write — see the note on that
    method for why that race is acceptable here and would not be for a seat cap.
*/

var (
	ErrClubFairRoleUnknown = errors.New("clubfair: not a role")
	ErrClubFairNotAdmin    = errors.New("clubfair: only an admin may change a role")
	ErrClubFairSelfEdit    = errors.New("clubfair: cannot change your own account here")
	ErrClubFairLastAdmin   = errors.New("clubfair: this is the last admin")
	ErrPrizeNameRequired   = errors.New("clubfair: a prize tier needs a name")
	ErrPrizeThresholdRange = errors.New("clubfair: a threshold must be at least one booth")
)

// How many participants one page of the roster returns.
//
// Capped rather than trusted from the query string: `?limit=100000` on a table
// with every student at the university in it is a way to make the server build a
// very large JSON document, and no screen renders that many rows anyway.
const (
	participantPageDefault = 50
	participantPageMax     = 200
)

type ClubFairAdminService struct {
	repo *repository.ClubFairAdminRepository
}

func NewClubFairAdminService(repo *repository.ClubFairAdminRepository) *ClubFairAdminService {
	return &ClubFairAdminService{repo: repo}
}

// ---- Participants --------------------------------------------------------

func (s *ClubFairAdminService) ListParticipants(
	ctx context.Context, query string, roles []string, limit, offset int,
) ([]model.ClubFairParticipant, int, error) {
	if limit <= 0 {
		limit = participantPageDefault
	}
	if limit > participantPageMax {
		limit = participantPageMax
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.ListParticipants(ctx, query, roles, limit, offset)
}

// UpdateParticipant applies the three rules above.
//
// [actorID] and [actorRole] come off the caller's token, never off the request
// body: the whole point of the role rule is that it cannot be asserted by the
// client it is restricting.
func (s *ClubFairAdminService) UpdateParticipant(
	ctx context.Context,
	id int,
	role *string,
	isFlagged *bool,
	actorID int,
	actorRole string,
) (*model.ClubFairParticipant, error) {
	if id == actorID {
		return nil, ErrClubFairSelfEdit
	}

	if role != nil {
		normalised := strings.TrimSpace(*role)
		switch normalised {
		case ClubFairRoleStudent,
			ClubFairRoleStaff,
			ClubFairRoleAdmin,
			ClubFairRoleBoothOwner:
		default:
			// Caught here as well as by the CHECK on the column, so an unknown
			// role is "not a role" rather than a 23514 the handler has to guess
			// the meaning of.
			return nil, ErrClubFairRoleUnknown
		}
		if actorRole != ClubFairRoleAdmin {
			return nil, ErrClubFairNotAdmin
		}
		role = &normalised
	}

	// Whether this edit would leave the fair with no admin. Only asked when the
	// edit could actually do that — a flag or a demotion — because it is a count
	// over the users table and most edits are neither.
	demoting := role != nil && *role != ClubFairRoleAdmin
	flagging := isFlagged != nil && *isFlagged
	if demoting || flagging {
		lastAdmin, err := s.repo.IsLastAdmin(ctx, id)
		if err != nil {
			return nil, err
		}
		if lastAdmin {
			return nil, ErrClubFairLastAdmin
		}
	}

	updated, err := s.repo.UpdateParticipant(ctx, id, role, isFlagged)
	if err != nil {
		return nil, err
	}

	/*
	 * Moving off booth_owner drops every booth assignment, and that is a
	 * revocation rather than tidying up.
	 *
	 * The role rides in a 30-day JWT this server has no way to recall, so a
	 * demoted account goes on presenting a token whose cf_role still says
	 * booth_owner until it expires, and the middleware believes it. The only
	 * half of the check that reads live state is the assignment row — so if the
	 * rows survive a demotion, the demotion does nothing whatsoever for up to a
	 * month.
	 *
	 * An earlier version of this kept them, on the reasoning that a demotion
	 * made by mistake should be one click to undo. It was measured rather than
	 * argued: the old token still opened the booth's code after the demotion had
	 * returned 200. Rebuilding a checkbox list is a minute's work; a revocation
	 * that quietly does not revoke is not discovered until it matters.
	 */
	if role != nil && *role != ClubFairRoleBoothOwner {
		if err := s.repo.ClearOwnedBooths(ctx, id); err != nil {
			return nil, err
		}
	}

	return updated, nil
}

// ---- Prize tiers ---------------------------------------------------------

func (s *ClubFairAdminService) ActivePrizeTiers(ctx context.Context) ([]model.PublicPrizeTier, error) {
	return s.repo.ListActivePrizeTiers(ctx)
}

func (s *ClubFairAdminService) PrizeTiersForAdmin(ctx context.Context) ([]model.ClubFairPrizeTierAdmin, error) {
	return s.repo.ListPrizeTiersForAdmin(ctx)
}

// checkTier is the whole of what makes a tier valid.
//
// No upper bound on the threshold, deliberately. A tier above the booth count is
// unreachable rather than invalid, and it is a legitimate thing to enter the day
// before three more clubs sign up — refusing it would mean the dashboard
// enforcing an ordering between two edits that has no reason to exist.
func checkTier(threshold int, name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", ErrPrizeNameRequired
	}
	if threshold < 1 {
		return "", ErrPrizeThresholdRange
	}
	return trimmed, nil
}

func (s *ClubFairAdminService) CreatePrizeTier(
	ctx context.Context, threshold int, name string, description *string,
) (*model.ClubFairPrizeTierAdmin, error) {
	trimmed, err := checkTier(threshold, name)
	if err != nil {
		return nil, err
	}
	return s.repo.CreatePrizeTier(ctx, threshold, trimmed, blankToNil(description))
}

func (s *ClubFairAdminService) UpdatePrizeTier(
	ctx context.Context, id, threshold int, name string, description *string, isActive bool,
) (*model.ClubFairPrizeTierAdmin, error) {
	trimmed, err := checkTier(threshold, name)
	if err != nil {
		return nil, err
	}
	return s.repo.UpdatePrizeTier(ctx, id, threshold, trimmed, blankToNil(description), isActive)
}

func (s *ClubFairAdminService) DeletePrizeTier(ctx context.Context, id int) error {
	return s.repo.DeletePrizeTier(ctx, id)
}

// ---- Making an account, and reading one ----------------------------------

var (
	// ErrClubFairEmailRequired covers both "no address" and "not an MFU one".
	ErrClubFairEmailRequired = errors.New("clubfair: an MFU email address is required")

	// ErrClubFairNotBoothOwner is an attempt to assign booths to an account
	// that is not a booth owner. Assignments are meaningless without the role —
	// the check-in endpoint tests both — so this refuses rather than writing
	// rows that grant nothing.
	ErrClubFairNotBoothOwner = errors.New("clubfair: only a booth owner can be assigned booths")
)

// ClubFairNewParticipant is what an admin fills in to create an account.
//
// Everything optional is a pointer, so "left blank" and "set to empty" are
// different — the same convention PATCH /clubfair/me uses.
type ClubFairNewParticipant struct {
	FirstName string
	Surname   string
	Email     string
	Phone     *string
	StudentID *string
	School    *string
	Major     *string
	Role      string
	Password  string
}

// CreateParticipant makes an account that nobody has signed in to yet.
//
// ## Why this exists at all
//
// Every other Club Fair account is created by its owner signing in, and that is
// still the path students take. It does not work for the people running the
// fair: a booth owner has to exist in the table before they can be assigned a
// booth, and setup day is not the moment to talk twenty-eight volunteers
// through a registration form and then find each of their rows by hand.
//
// ## What it deliberately does not relax
//
//   - **The MFU domain.** The address is the identity every path here joins on —
//     Google sign-in finds a row by it, and the student id is its local part.
//     An account on some other domain would be one its owner's Google sign-in
//     could never match, so they would end up with two.
//   - **The password policy.** The account gets a real credential, checked by
//     the same rule a self-registered one is.
//
// And one it does: **the intake window.** `eligibleIntake` gates who may *open*
// a student account, because the fair is for intakes 67–69. A staff member or a
// booth owner is not collecting stamps, and refusing to create one because their
// student id starts with 64 would be applying a rule about prizes to a rule
// about employment.
//
// [actorRole] gates the role being handed out: an admin may create any account,
// staff may create students only. Same rule as UpdateParticipant, and for the
// same reason — creating a staff account and promoting one to staff give away
// exactly the same thing.
func (s *ClubFairAdminService) CreateParticipant(
	ctx context.Context, draft ClubFairNewParticipant, actorRole string,
) (*model.ClubFairParticipant, error) {
	email := strings.TrimSpace(strings.ToLower(draft.Email))
	if !strings.HasSuffix(email, "@"+MFUDomain) {
		return nil, ErrClubFairEmailRequired
	}
	if strings.TrimSpace(draft.FirstName) == "" || strings.TrimSpace(draft.Surname) == "" {
		return nil, ErrClubFairNameRequired
	}

	role := strings.TrimSpace(draft.Role)
	if role == "" {
		role = ClubFairRoleStudent
	}
	switch role {
	case ClubFairRoleStudent,
		ClubFairRoleStaff,
		ClubFairRoleAdmin,
		ClubFairRoleBoothOwner:
	default:
		return nil, ErrClubFairRoleUnknown
	}
	if role != ClubFairRoleStudent && actorRole != ClubFairRoleAdmin {
		return nil, ErrClubFairNotAdmin
	}

	if err := checkPasswordPolicy(draft.Password); err != nil {
		return nil, err
	}

	// The student id defaults to the address's local part, which for an MFU
	// address *is* the student id. Derived rather than asked for twice: two
	// fields carrying the same fact is two fields that can disagree.
	studentID := draft.StudentID
	if studentID == nil || strings.TrimSpace(*studentID) == "" {
		local, _, _ := strings.Cut(email, "@")
		studentID = &local
	}

	// Only validated when given. Phone is nullable and UNIQUE, and an admin
	// creating a booth owner may not have it to hand — but a wrong one is worse
	// than none, because it is one of the three fields sign-in accepts.
	phone := draft.Phone
	if phone != nil {
		normalised := NormalisePhone(*phone)
		if normalised == "" {
			phone = nil
		} else if !thaiMobile.MatchString(normalised) {
			return nil, ErrClubFairBadPhone
		} else {
			phone = &normalised
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(draft.Password), clubFairBcryptCost)
	if err != nil {
		return nil, err
	}

	return s.repo.CreateParticipant(
		ctx,
		strings.TrimSpace(draft.FirstName), strings.TrimSpace(draft.Surname), email,
		phone, studentID, trimmedOrNil(draft.School), trimmedOrNil(draft.Major),
		role, string(hash),
	)
}

// trimmedOrNil turns a blank optional field into a NULL rather than an empty
// string, so "not recorded" is one value in the column instead of two.
func trimmedOrNil(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// Participant is one row of the roster, for the detail screen.
func (s *ClubFairAdminService) Participant(
	ctx context.Context, id int,
) (*model.ClubFairParticipant, error) {
	return s.repo.GetParticipant(ctx, id)
}

// ---- Booth ownership -----------------------------------------------------

// OwnedBoothIDs is which booths an account may display a code for.
func (s *ClubFairAdminService) OwnedBoothIDs(ctx context.Context, userID int) ([]int, error) {
	return s.repo.ListOwnedBoothIDs(ctx, userID)
}

// OwnedBooths is the same set as whole rows, for the owner's own screen.
func (s *ClubFairAdminService) OwnedBooths(ctx context.Context, userID int) ([]model.Booth, error) {
	return s.repo.ListOwnedBooths(ctx, userID)
}

// SetOwnedBooths replaces an owner's assignments.
//
// Staff and admin both, unlike a role change. Granting the role is the decision
// that matters and is an admin's; deciding that the person who already runs a
// booth screen runs A5 as well as A4 is the sort of thing that happens twice an
// hour during setup, and routing it through the one admin in the building is how
// a screen ends up dark.
//
// The account has to already be a booth owner. Assignments against any other
// role grant nothing — the check-in endpoint tests the role *and* the
// assignment — so writing them would leave the dashboard showing booths against
// a student's name that they cannot open. The reverse move, demoting an owner,
// deletes their assignments; see UpdateParticipant for why that is the only
// revocation available.
func (s *ClubFairAdminService) SetOwnedBooths(
	ctx context.Context, userID int, boothIDs []int, actorID int,
) error {
	person, err := s.repo.GetParticipant(ctx, userID)
	if err != nil {
		return err
	}
	if person.Role != ClubFairRoleBoothOwner {
		return ErrClubFairNotBoothOwner
	}
	return s.repo.SetOwnedBooths(ctx, userID, boothIDs, actorID)
}

// SetParticipantPassword gives an account a new password on an admin's say-so.
//
// ## Why this exists
//
// Creating an account is not the same as the person being able to use it. There
// is no invitation email in this system, so an admin types a password and passes
// it on by voice or on a slip of paper — and when that goes wrong, which it does,
// the account was until now **unrecoverable**: `PUT /clubfair/me/password` needs
// a token, and getting a token is precisely what the person cannot do.
//
// ## Admin only, and never your own
//
// Setting someone's password is taking over their account outright — a stricter
// thing than changing their role, which is already admin-only. And an admin
// cannot use this on themselves: their own password goes through
// `PUT /clubfair/me/password`, which is the endpoint that knows this is the
// account holder rather than somebody with the same rights over it.
//
// ⚠ **It does not sign the account out.** Club Fair tokens are stateless with no
// revocation list, so a session already issued survives its own password being
// changed, for up to thirty days. That makes this a recovery tool, not a way to
// eject somebody — suspending the account is not that either, for the same
// reason. There is currently no way to end a live Club Fair session, and this
// should not be mistaken for one.
func (s *ClubFairAdminService) SetParticipantPassword(
	ctx context.Context, id int, password string, actorID int, actorRole string,
) error {
	if id == actorID {
		return ErrClubFairSelfEdit
	}
	if actorRole != ClubFairRoleAdmin {
		return ErrClubFairNotAdmin
	}
	if err := checkPasswordPolicy(password); err != nil {
		return err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), clubFairBcryptCost)
	if err != nil {
		return err
	}
	return s.repo.SetParticipantPassword(ctx, id, string(hash))
}

// ---- The fair at a glance ------------------------------------------------

// Dashboard is a straight passthrough: four counts with no rule attached to
// them. It stays on this service rather than its own because the console that
// reads it is the same console as the roster above.
func (s *ClubFairAdminService) Dashboard(ctx context.Context) (*model.ClubFairDashboardStats, error) {
	return s.repo.Dashboard(ctx)
}
