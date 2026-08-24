// Package repository is for uh sql queries? i think
package repository

import (
	"context"
	"su-server/internal/model"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type StepsRepository struct {
	db *pgxpool.Pool
}

func NewStepsRepository(db *pgxpool.Pool) *StepsRepository {
	return &StepsRepository{db: db}
}

// Upsert inserts or updates a step count for a user on a specific date
func (r *StepsRepository) Upsert(ctx context.Context, steps model.Steps) (*model.Steps, error) {
	var result model.Steps
	err := r.db.QueryRow(ctx,
		`INSERT INTO steps (user_id, step_count, recorded_date)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (user_id, recorded_date) DO UPDATE
		 SET step_count = EXCLUDED.step_count,
		     synced_at  = NOW()
		 RETURNING id, user_id, step_count, recorded_date, synced_at`,
		steps.UserID,
		steps.StepCount,
		steps.RecordedDate,
	).Scan(
		&result.ID,
		&result.UserID,
		&result.StepCount,
		&result.RecordedDate,
		&result.SyncedAt,
	)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// UpsertMany inserts or updates multiple step records at once (bulk sync)
func (r *StepsRepository) UpsertMany(ctx context.Context, stepsList []model.Steps) ([]model.Steps, error) {
	var results []model.Steps
	for _, steps := range stepsList {
		result, err := r.Upsert(ctx, steps)
		if err != nil {
			return nil, err
		}
		results = append(results, *result)
	}
	return results, nil
}

// GetByUserID returns all step records for a user
func (r *StepsRepository) GetByUserID(ctx context.Context, userID int) ([]model.Steps, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, user_id, step_count, recorded_date, synced_at
		 FROM steps WHERE user_id = $1
		 ORDER BY recorded_date DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []model.Steps
	for rows.Next() {
		var steps model.Steps
		if err := rows.Scan(
			&steps.ID,
			&steps.UserID,
			&steps.StepCount,
			&steps.RecordedDate,
			&steps.SyncedAt,
		); err != nil {
			return nil, err
		}
		results = append(results, steps)
	}
	return results, nil
}

// GetByDateRange returns step records for a user within a date range
func (r *StepsRepository) GetByDateRange(ctx context.Context, userID int, from, to time.Time) ([]model.Steps, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, user_id, step_count, recorded_date, synced_at
		 FROM steps
		 WHERE user_id = $1
		 AND recorded_date BETWEEN $2 AND $3
		 ORDER BY recorded_date DESC`,
		userID, from, to,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []model.Steps
	for rows.Next() {
		var steps model.Steps
		if err := rows.Scan(
			&steps.ID,
			&steps.UserID,
			&steps.StepCount,
			&steps.RecordedDate,
			&steps.SyncedAt,
		); err != nil {
			return nil, err
		}
		results = append(results, steps)
	}
	return results, nil
}
