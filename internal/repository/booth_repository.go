package repository

import (
	"context"
	"su-server/internal/model"

	"github.com/jackc/pgx/v5"
)

type BoothRepository struct {
	db *pgx.Conn
}

func NewBoothRepository(db *pgx.Conn) *BoothRepository {
	return &BoothRepository{db: db}
}

// GetAllBooths returns every booth, secret included — the caller decides what
// reaches the wire. Project C needs the secret here to verify an HMAC, which
// is why the guard lives at the response boundary rather than in this query.
func (r *BoothRepository) GetAllBooths(ctx context.Context) ([]model.Booth, error) {
	rows, err := r.db.Query(ctx,
		"SELECT id, event_id, name, category, secret, created_at FROM booth ORDER BY id")
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
			&booth.Secret,
			&booth.CreatedAt,
		); err != nil {
			return nil, err
		}
		booths = append(booths, booth)
	}
	return booths, rows.Err()
}
