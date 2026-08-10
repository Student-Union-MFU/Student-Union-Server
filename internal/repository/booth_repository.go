package repository

import (
	"context"
	"su-server/internal/model"

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
