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
	rows, err := r.db.Query(ctx,
		"SELECT id, event_id, name, category, created_at FROM booth ORDER BY id")
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
			&booth.Category,
			&booth.CreatedAt,
		); err != nil {
			return nil, err
		}
		booths = append(booths, booth)
	}
	return booths, rows.Err()
}
