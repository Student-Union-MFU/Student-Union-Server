package service

import (
	"context"
	"errors"
	"strings"
	"su-server/internal/model"
	"su-server/internal/repository"
	"time"
)

/*
The fair's details and its running order.

The rules here are small and there are only two of them worth stating: a window
has to run forwards, and a thing on a schedule has to have a name. Both are also
CHECK constraints in migration 000023, and both are checked here anyway — not
because the database might miss one, but because a 23514 arrives as a SQLSTATE
with a constraint name on it, and what the staff member needs to read is which
of the two times they got the wrong way round.

Everything else the dashboard sends is text that either has content or does not.
[blankToNil] is the whole of that policy: an empty box means the field is absent,
not that it holds an empty string. The clients all treat NULL as "fall back to
Thai" or "hide the line", and an empty string is neither.
*/

var (
	ErrFairWindowBackwards    = errors.New("clubfair: the fair ends before it starts")
	ErrProgramTitleMissing    = errors.New("clubfair: a programme entry needs a title")
	ErrProgramWindowBackwards = errors.New("clubfair: the entry ends before it starts")
)

// blankToNil turns an empty or whitespace-only field into a NULL.
//
// A form posts "" for a box the staff member cleared. Stored as an empty string
// that is a value, and `name_en` holding "" means the English name is the empty
// string rather than absent — so the client renders a blank line where it would
// otherwise have fallen back to the Thai. NULL is what "there isn't one" is
// spelled as in this schema, everywhere.
func blankToNil(s *string) *string {
	if s == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*s)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

type ClubFairContentService struct {
	repo *repository.ClubFairContentRepository
}

func NewClubFairContentService(repo *repository.ClubFairContentRepository) *ClubFairContentService {
	return &ClubFairContentService{repo: repo}
}

// ---- Fair info -----------------------------------------------------------

func (s *ClubFairContentService) FairInfo(ctx context.Context) (*model.ClubFairInfo, error) {
	return s.repo.FairInfo(ctx)
}

// SaveFairInfo writes when and where the fair is.
//
// The window check is here rather than only in the CHECK constraint because this
// is the single most consequential edit in the dashboard: these two instants are
// what the countdown on the front page counts to, and what every client uses to
// decide whether the fair is open. Getting them backwards is a typo anyone can
// make at a date picker, and the reply has to say so in words.
func (s *ClubFairContentService) SaveFairInfo(
	ctx context.Context,
	startsAt, endsAt time.Time,
	venue, venueEN, notice, noticeEN *string,
	updatedBy int,
) (*model.ClubFairInfo, error) {
	if !endsAt.After(startsAt) {
		return nil, ErrFairWindowBackwards
	}
	return s.repo.SaveFairInfo(
		ctx, startsAt, endsAt,
		blankToNil(venue), blankToNil(venueEN),
		blankToNil(notice), blankToNil(noticeEN),
		updatedBy,
	)
}

// ---- Programme -----------------------------------------------------------

// PublishedProgram is what the public endpoint serves.
//
// A separate method rather than a boolean the handler passes, so the public
// route cannot be given the wrong argument. The filter is the only thing
// standing between a half-written running order and the front page.
func (s *ClubFairContentService) PublishedProgram(ctx context.Context) ([]model.ClubFairProgramEntry, error) {
	return s.repo.ListProgram(ctx, true)
}

// FullProgram is the dashboard's view: drafts included.
func (s *ClubFairContentService) FullProgram(ctx context.Context) ([]model.ClubFairProgramEntry, error) {
	return s.repo.ListProgram(ctx, false)
}

// normaliseEntry applies the two rules and cleans the text fields.
func normaliseEntry(e model.ClubFairProgramEntry) (model.ClubFairProgramEntry, error) {
	e.Title = strings.TrimSpace(e.Title)
	if e.Title == "" {
		return e, ErrProgramTitleMissing
	}
	if e.EndsAt != nil && !e.EndsAt.After(e.StartsAt) {
		return e, ErrProgramWindowBackwards
	}

	e.TitleEN = blankToNil(e.TitleEN)
	e.Detail = blankToNil(e.Detail)
	e.DetailEN = blankToNil(e.DetailEN)
	e.Location = blankToNil(e.Location)
	e.LocationEN = blankToNil(e.LocationEN)
	e.Zone = blankToNil(e.Zone)

	return e, nil
}

func (s *ClubFairContentService) CreateProgramEntry(
	ctx context.Context, e model.ClubFairProgramEntry,
) (*model.ClubFairProgramEntry, error) {
	e, err := normaliseEntry(e)
	if err != nil {
		return nil, err
	}
	return s.repo.CreateProgramEntry(ctx, e)
}

func (s *ClubFairContentService) UpdateProgramEntry(
	ctx context.Context, id int, e model.ClubFairProgramEntry,
) (*model.ClubFairProgramEntry, error) {
	e, err := normaliseEntry(e)
	if err != nil {
		return nil, err
	}
	return s.repo.UpdateProgramEntry(ctx, id, e)
}

func (s *ClubFairContentService) DeleteProgramEntry(ctx context.Context, id int) error {
	return s.repo.DeleteProgramEntry(ctx, id)
}

// ---- Staff contacts ------------------------------------------------------

// ErrStaffContactRoleMissing is the one rule this list has.
//
// A contact with no role is a phone number nobody can act on: the role is the
// column a staff member scans for. The name and the number are both optional and
// deliberately so — see migration 000025.
var ErrStaffContactRoleMissing = errors.New("clubfair: a staff contact needs a role")

// StaffContacts is the rota, for the staff screen and for the dashboard editor.
//
// One method for both, matching the repository. There is no published/draft
// split here, and a staff member calling a number the editor has already removed
// is exactly what two separate reads would eventually produce.
func (s *ClubFairContentService) StaffContacts(
	ctx context.Context,
) ([]model.ClubFairStaffContact, error) {
	return s.repo.ListStaffContacts(ctx)
}

// normaliseStaffContact applies the rule and cleans the text fields.
//
// The phone number is trimmed and otherwise left exactly as typed. No format
// normalising, no stripping of spaces or dashes: this column has to hold an
// internal extension, a mobile with dashes and whatever the Student Union
// actually writes on its rota, and a server that reformats one of those is a
// server that eventually mangles one. The website strips the separators itself
// when it builds the `tel:` link, which is the one place a machine reads it.
func normaliseStaffContact(c model.ClubFairStaffContact) (model.ClubFairStaffContact, error) {
	c.Role = strings.TrimSpace(c.Role)
	if c.Role == "" {
		return c, ErrStaffContactRoleMissing
	}

	c.RoleEN = blankToNil(c.RoleEN)
	c.Name = blankToNil(c.Name)
	c.Phone = blankToNil(c.Phone)
	c.Note = blankToNil(c.Note)
	c.NoteEN = blankToNil(c.NoteEN)

	return c, nil
}

func (s *ClubFairContentService) CreateStaffContact(
	ctx context.Context, c model.ClubFairStaffContact, updatedBy int,
) (*model.ClubFairStaffContact, error) {
	c, err := normaliseStaffContact(c)
	if err != nil {
		return nil, err
	}
	return s.repo.CreateStaffContact(ctx, c, updatedBy)
}

func (s *ClubFairContentService) UpdateStaffContact(
	ctx context.Context, id int, c model.ClubFairStaffContact, updatedBy int,
) (*model.ClubFairStaffContact, error) {
	c, err := normaliseStaffContact(c)
	if err != nil {
		return nil, err
	}
	return s.repo.UpdateStaffContact(ctx, id, c, updatedBy)
}

func (s *ClubFairContentService) DeleteStaffContact(ctx context.Context, id int) error {
	return s.repo.DeleteStaffContact(ctx, id)
}
