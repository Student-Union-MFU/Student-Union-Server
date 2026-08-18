package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"su-server/internal/model"
	"su-server/internal/repository"
)

type BoothService struct {
	repo *repository.BoothRepository
}

func NewBoothService(repo *repository.BoothRepository) *BoothService {
	return &BoothService{repo: repo}
}

func (s *BoothService) GetAllBooths(ctx context.Context) ([]model.Booth, error) {
	return s.repo.GetAllBooths(ctx)
}

/*
Booth writes.

Three rules, and the third is the one that is not obvious.

`category` is a CHECK on the column, five values from club.pdf. Checked here as
well so a typo comes back as "not a category" rather than as a 23514 whose
constraint name the handler would have to interpret.

`name` is NOT NULL and a booth with a blank name is a row nobody can find.

**A booth code must start with its zone's letter**, when both are given. That is
not a database constraint and it is documented as a fact rather than enforced
anywhere — migration 000019 calls the zone code "the letter on the signage, and
the prefix of every booth code in the area", and the app, the website and the
printed plan all rely on it. A booth in zone B labelled A3 sorts into zone A on
every screen that groups by code, and stands on a floor where the sign above it
says B. It is a data entry slip with no visible failure until someone is walking
the hall looking for it, so it is caught at the point of entry.

Only when both are present: a booth can legitimately have a zone and no code yet
(the floor is not laid out), or neither.
*/

var (
	ErrBoothNameRequired     = errors.New("clubfair: a booth needs a name")
	ErrBoothCategoryUnknown  = errors.New("clubfair: not a booth category")
	ErrBoothCodeZoneMismatch = errors.New("clubfair: the booth code does not start with its zone letter")
)

// BoothCategories is the CHECK on booth.category, in the order the seed used.
//
// Exported because the dashboard has to offer them as a fixed list — adding a
// sixth needs a migration, so a form that let staff type one would be a form
// whose submit fails.
var BoothCategories = []string{
	"sports", "student_relations", "volunteer", "religion_and_culture", "academic",
}

// normaliseBooth trims, upper-cases the two floor-plan fields and applies the
// three rules.
//
// Zone and code are upper-cased rather than rejected for case: the signage is
// upper case, "b7" is unambiguously B7, and refusing it would be pedantry at a
// form someone is filling in at speed.
func normaliseBooth(b model.PublicBooth) (model.PublicBooth, error) {
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" {
		return b, ErrBoothNameRequired
	}

	b.Category = strings.TrimSpace(b.Category)
	if !slices.Contains(BoothCategories, b.Category) {
		return b, ErrBoothCategoryUnknown
	}

	b.NameEN = blankToNil(b.NameEN)
	b.About = blankToNil(b.About)
	// Not validated against a known set. The token is neutral by design so the
	// server can name art a client does not have yet — an unrecognised one
	// renders the fallback glyph, which is exactly the behaviour that makes
	// adding one safe.
	b.Icon = blankToNil(b.Icon)

	if zone := blankToNil(b.Zone); zone != nil {
		upper := strings.ToUpper(*zone)
		b.Zone = &upper
	} else {
		b.Zone = nil
	}
	if code := blankToNil(b.BoothCode); code != nil {
		upper := strings.ToUpper(*code)
		b.BoothCode = &upper
	} else {
		b.BoothCode = nil
	}

	if b.Zone != nil && b.BoothCode != nil && !strings.HasPrefix(*b.BoothCode, *b.Zone) {
		return b, ErrBoothCodeZoneMismatch
	}

	return b, nil
}

func (s *BoothService) CreateBooth(ctx context.Context, b model.PublicBooth) (*model.PublicBooth, error) {
	b, err := normaliseBooth(b)
	if err != nil {
		return nil, err
	}
	return s.repo.CreateBooth(ctx, b)
}

func (s *BoothService) UpdateBooth(ctx context.Context, id int, b model.PublicBooth) (*model.PublicBooth, error) {
	b, err := normaliseBooth(b)
	if err != nil {
		return nil, err
	}
	return s.repo.UpdateBooth(ctx, id, b)
}

func (s *BoothService) DeleteBooth(ctx context.Context, id int) error {
	return s.repo.DeleteBooth(ctx, id)
}
