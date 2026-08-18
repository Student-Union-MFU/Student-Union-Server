package repository

import (
	"context"
	"errors"
	"su-server/internal/model"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

/*
The fair's own details and its running order — migration 000023.

Both are read without a token and written by staff, which is why they are one
repository: the public website and the staff dashboard are looking at the same
two tables from opposite sides, and keeping the read and the write together is
what stops the published-only filter existing in one place and being forgotten
in the other.
*/

var (
	// ErrFairInfoMissing means the singleton row is gone. It is seeded by the
	// migration, so this is a database someone has edited by hand rather than
	// anything a client did — distinguished from a broken query so the handler
	// can say which.
	ErrFairInfoMissing = errors.New("clubfair: fair info row is missing")

	// ErrProgramEntryNotFound distinguishes "no such entry" from a broken query.
	ErrProgramEntryNotFound = errors.New("clubfair: programme entry not found")
)

type ClubFairContentRepository struct {
	db *pgxpool.Pool
}

func NewClubFairContentRepository(db *pgxpool.Pool) *ClubFairContentRepository {
	return &ClubFairContentRepository{db: db}
}

// ---- Fair info -----------------------------------------------------------

const fairInfoColumns = `starts_at, ends_at, venue, venue_en, notice, notice_en, updated_at`

func scanFairInfo(row pgx.Row) (*model.ClubFairInfo, error) {
	var info model.ClubFairInfo
	err := row.Scan(
		&info.StartsAt, &info.EndsAt,
		&info.Venue, &info.VenueEN,
		&info.Notice, &info.NoticeEN,
		&info.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrFairInfoMissing
	}
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// FairInfo reads the one row.
//
// `WHERE id = 1` rather than `LIMIT 1`: the CHECK on the table makes 1 the only
// id there can be, and naming it means this query cannot quietly start returning
// whichever row the planner reached first if that constraint is ever relaxed.
func (r *ClubFairContentRepository) FairInfo(ctx context.Context) (*model.ClubFairInfo, error) {
	return scanFairInfo(r.db.QueryRow(ctx,
		`SELECT `+fairInfoColumns+` FROM clubfair_fair_info WHERE id = 1`))
}

// SaveFairInfo writes the one row and returns what it now says.
//
// An UPSERT rather than an UPDATE, so a database whose seed was rolled back or
// never applied is repaired by the first save instead of silently accepting a
// write that changed nothing. `updated_by` is the staff account that made the
// change — a date that moves during a fair should say who moved it.
func (r *ClubFairContentRepository) SaveFairInfo(
	ctx context.Context,
	startsAt, endsAt time.Time,
	venue, venueEN, notice, noticeEN *string,
	updatedBy int,
) (*model.ClubFairInfo, error) {
	return scanFairInfo(r.db.QueryRow(ctx,
		`INSERT INTO clubfair_fair_info
		     (id, starts_at, ends_at, venue, venue_en, notice, notice_en, updated_at, updated_by)
		 VALUES (1, $1, $2, $3, $4, $5, $6, now(), $7)
		 ON CONFLICT (id) DO UPDATE SET
		     starts_at  = EXCLUDED.starts_at,
		     ends_at    = EXCLUDED.ends_at,
		     venue      = EXCLUDED.venue,
		     venue_en   = EXCLUDED.venue_en,
		     notice     = EXCLUDED.notice,
		     notice_en  = EXCLUDED.notice_en,
		     updated_at = now(),
		     updated_by = EXCLUDED.updated_by
		 RETURNING `+fairInfoColumns,
		startsAt, endsAt, venue, venueEN, notice, noticeEN, updatedBy))
}

// ---- Programme -----------------------------------------------------------

const programColumns = `id, starts_at, ends_at, title, title_en, detail, detail_en,
                        location, location_en, zone, is_published`

func scanProgramEntry(row pgx.Row) (*model.ClubFairProgramEntry, error) {
	var e model.ClubFairProgramEntry
	err := row.Scan(
		&e.ID, &e.StartsAt, &e.EndsAt,
		&e.Title, &e.TitleEN, &e.Detail, &e.DetailEN,
		&e.Location, &e.LocationEN, &e.Zone, &e.IsPublished,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrProgramEntryNotFound
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// ListProgram returns the running order.
//
// [publishedOnly] is the difference between the public endpoint and the
// dashboard, and it is a parameter rather than two methods so the column list
// and the ordering cannot drift between them — a draft that leaked onto the
// public page because one of two copies forgot the filter is exactly the bug
// this shape prevents.
//
// Ordered by start, then id: two things at 13:00 have no natural order and id is
// at least the order they were entered in, which is stable across reads.
func (r *ClubFairContentRepository) ListProgram(
	ctx context.Context, publishedOnly bool,
) ([]model.ClubFairProgramEntry, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+programColumns+`
		   FROM clubfair_program
		  WHERE (NOT $1 OR is_published)
		  ORDER BY starts_at, id`, publishedOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// `make`, not `var`: a nil slice marshals as `null`, which the clients read
	// as a broken response rather than as an empty programme.
	out := make([]model.ClubFairProgramEntry, 0)
	for rows.Next() {
		var e model.ClubFairProgramEntry
		if err := rows.Scan(
			&e.ID, &e.StartsAt, &e.EndsAt,
			&e.Title, &e.TitleEN, &e.Detail, &e.DetailEN,
			&e.Location, &e.LocationEN, &e.Zone, &e.IsPublished,
		); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *ClubFairContentRepository) CreateProgramEntry(
	ctx context.Context, e model.ClubFairProgramEntry,
) (*model.ClubFairProgramEntry, error) {
	return scanProgramEntry(r.db.QueryRow(ctx,
		`INSERT INTO clubfair_program
		     (starts_at, ends_at, title, title_en, detail, detail_en,
		      location, location_en, zone, is_published)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		 RETURNING `+programColumns,
		e.StartsAt, e.EndsAt, e.Title, e.TitleEN, e.Detail, e.DetailEN,
		e.Location, e.LocationEN, e.Zone, e.IsPublished))
}

// UpdateProgramEntry replaces every editable column.
//
// A whole-row write rather than a partial one, unlike UpdateProfile on a
// student: this is a form the staff member is looking at in full, so "not in the
// request" means "cleared", not "leave alone". A COALESCE-per-column update
// would make it impossible to remove a detail line once one had been typed.
func (r *ClubFairContentRepository) UpdateProgramEntry(
	ctx context.Context, id int, e model.ClubFairProgramEntry,
) (*model.ClubFairProgramEntry, error) {
	return scanProgramEntry(r.db.QueryRow(ctx,
		`UPDATE clubfair_program SET
		     starts_at    = $2,
		     ends_at      = $3,
		     title        = $4,
		     title_en     = $5,
		     detail       = $6,
		     detail_en    = $7,
		     location     = $8,
		     location_en  = $9,
		     zone         = $10,
		     is_published = $11,
		     updated_at   = now()
		 WHERE id = $1
		 RETURNING `+programColumns,
		id, e.StartsAt, e.EndsAt, e.Title, e.TitleEN, e.Detail, e.DetailEN,
		e.Location, e.LocationEN, e.Zone, e.IsPublished))
}

// DeleteProgramEntry removes an entry outright.
//
// A hard delete, unlike an announcement's. An announcement is a record of what
// the fair was told and is soft-deleted so it stops being shown without
// vanishing; a programme entry is a plan, and a plan that was cancelled has no
// history worth keeping — staff removing a slot mean it is not happening.
func (r *ClubFairContentRepository) DeleteProgramEntry(ctx context.Context, id int) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM clubfair_program WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrProgramEntryNotFound
	}
	return nil
}
