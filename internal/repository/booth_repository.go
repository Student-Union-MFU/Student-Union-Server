package repository

import (
	"context"
	"errors"
	"su-server/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type BoothRepository struct {
	db *pgxpool.Pool
}

func NewBoothRepository(db *pgxpool.Pool) *BoothRepository {
	return &BoothRepository{db: db}
}

// GetAllBooths returns every booth for the public directory. It deliberately
// does not read the secret column — this list is served on the endpoint
// every student hits, and the secret has no business leaving the database on
// that path (it is only ever needed one booth at a time, to verify an HMAC).
func (r *BoothRepository) GetAllBooths(ctx context.Context) ([]model.Booth, error) {
	// Ordered by the floor plan rather than by id: the clients render this as
	// three zones of stalls in signage order, and `zone, booth_code` IS that
	// order. The lpad is because booth_code is text — ordering it plainly puts
	// B10 before B2, which is wrong on a wall a student is walking along.
	//
	// NULLS LAST on the zone so a booth created before the floor is laid out
	// still appears, at the end, rather than sorting into the middle of zone A.
	rows, err := r.db.Query(ctx,
		`SELECT b.id, b.event_id, b.name, b.name_en, b.category,
		        b.zone, b.booth_code, b.about, b.icon, b.created_at
		   FROM booth b
		   LEFT JOIN clubfair_zone z ON z.code = b.zone
		  ORDER BY z.sort_order NULLS LAST,
		           lpad(substring(b.booth_code from 2), 3, '0') NULLS LAST,
		           b.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// `make`, not `var`: a nil slice marshals as `null`, and the iOS client
	// reads a null list as a broken response.
	booths := make([]model.Booth, 0)
	for rows.Next() {
		var booth model.Booth
		if err := rows.Scan(
			&booth.ID,
			&booth.EventID,
			&booth.Name,
			&booth.NameEN,
			&booth.Category,
			&booth.Zone,
			&booth.BoothCode,
			&booth.About,
			&booth.Icon,
			&booth.CreatedAt,
		); err != nil {
			return nil, err
		}
		booths = append(booths, booth)
	}
	return booths, rows.Err()
}

/*
Booth writes — the dashboard's half of this table.

Everything below returns a [model.PublicBooth] rather than a [model.Booth], and
that is the same rule GetAllBooths follows for the same reason: `secret` has no
business leaving the database on any path but the one that verifies an HMAC. A
create returns the row the staff member just made without ever putting its key
on the wire.
*/

var (
	// ErrBoothCodeTaken is the UNIQUE on booth_code. Two stalls cannot carry the
	// same sign, and the staff member needs to be told which rule they hit.
	ErrBoothCodeTaken = errors.New("clubfair: another booth already has that code")

	// ErrBoothHasCheckIns is a delete refused because students have collected
	// this booth. See DeleteBooth.
	ErrBoothHasCheckIns = errors.New("clubfair: booth has check-ins against it")
)

const publicBoothColumns = `id, event_id, name, name_en, category, zone, booth_code, about, icon`

func scanPublicBooth(row pgx.Row) (*model.PublicBooth, error) {
	var b model.PublicBooth
	err := row.Scan(&b.ID, &b.EventID, &b.Name, &b.NameEN, &b.Category,
		&b.Zone, &b.BoothCode, &b.About, &b.Icon)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBoothNotFound
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// CreateBooth adds a stall.
//
// `secret` is not a parameter and never will be. The column's DEFAULT is
// `encode(gen_random_bytes(32), 'base64')`, so a new booth's HMAC key is minted
// by Postgres at insert time and is never seen by the handler, the service, or
// whoever is holding the laptop. A create endpoint that accepted a secret would
// be an endpoint for setting a booth's key to something known.
func (r *BoothRepository) CreateBooth(ctx context.Context, b model.PublicBooth) (*model.PublicBooth, error) {
	booth, err := scanPublicBooth(r.db.QueryRow(ctx,
		`INSERT INTO booth (name, name_en, category, zone, booth_code, about, icon)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 RETURNING `+publicBoothColumns,
		b.Name, b.NameEN, b.Category, b.Zone, b.BoothCode, b.About, b.Icon))

	if IsPGCode(err, "23505") {
		return nil, ErrBoothCodeTaken
	}
	return booth, err
}

// UpdateBooth replaces every editable column.
//
// A whole-row write: the dashboard shows the staff member the complete booth, so
// a field they cleared means cleared. `event_id` is not editable here — it is
// null on all 28 and the events table it would point at is the SU-wide listing,
// a different product's data.
func (r *BoothRepository) UpdateBooth(ctx context.Context, id int, b model.PublicBooth) (*model.PublicBooth, error) {
	booth, err := scanPublicBooth(r.db.QueryRow(ctx,
		`UPDATE booth SET
		     name       = $2,
		     name_en    = $3,
		     category   = $4,
		     zone       = $5,
		     booth_code = $6,
		     about      = $7,
		     icon       = $8
		 WHERE id = $1
		 RETURNING `+publicBoothColumns,
		id, b.Name, b.NameEN, b.Category, b.Zone, b.BoothCode, b.About, b.Icon))

	if IsPGCode(err, "23505") {
		return nil, ErrBoothCodeTaken
	}
	return booth, err
}

// DeleteBooth removes a stall no student has collected.
//
// Guarded rather than left to the cascade, and this is the important one on this
// page. `clubfair_checkin.booth_id` is ON DELETE CASCADE — deleting a booth
// silently deletes every stamp anyone ever collected at it, which moves students
// down the ranking and can take them back under a prize threshold they had
// already reached. A club pulling out on the morning of the fair is a real thing
// that happens, and the answer to it is not to erase the afternoon of everyone
// who already visited them.
//
// A booth with check-ins has to stay. If the SU needs it off the floor, that is
// a change to its zone and code, not a delete.
func (r *BoothRepository) DeleteBooth(ctx context.Context, id int) error {
	var stamps int
	if err := r.db.QueryRow(ctx,
		`SELECT count(*) FROM clubfair_checkin WHERE booth_id = $1`, id).Scan(&stamps); err != nil {
		return err
	}
	if stamps > 0 {
		return ErrBoothHasCheckIns
	}

	tag, err := r.db.Exec(ctx, `DELETE FROM booth WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrBoothNotFound
	}
	return nil
}
