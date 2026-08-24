// Package repository is for uh sql queries? i think
package repository

import (
	"context"
	"su-server/internal/model"

	"github.com/jackc/pgx/v5/pgxpool"
)

type LeaderboardRepository struct {
	db *pgxpool.Pool
}

func NewLeaderboardRepository(db *pgxpool.Pool) *LeaderboardRepository {
	return &LeaderboardRepository{db: db}
}

// Upsert inserts or updates a user's step count in the leaderboard
func (r *LeaderboardRepository) Upsert(ctx context.Context, userID int, stepCount int) (*model.Leaderboard, error) {
	var result model.Leaderboard
	err := r.db.QueryRow(ctx,
		`INSERT INTO leaderboard (user_id, step_count)
		 VALUES ($1, $2)
		 ON CONFLICT (user_id) DO UPDATE
		 SET step_count = EXCLUDED.step_count,
		     updated_at = NOW()
		 RETURNING id, user_id, step_count, created_at, updated_at`,
		userID,
		stepCount,
	).Scan(
		&result.ID,
		&result.UserID,
		&result.StepCount,
		&result.CreatedAt,
		&result.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// GetRanked returns all leaderboard entries ranked by step count with user info
func (r *LeaderboardRepository) GetRanked(ctx context.Context) ([]model.LeaderboardEntry, error) {
	rows, err := r.db.Query(ctx,
		`SELECT
		    RANK() OVER (ORDER BY l.step_count DESC) as rank,
		    l.user_id,
		    u.name,
		    COALESCE(u.avatar_url, '') as avatar_url,
		    l.step_count
		 FROM leaderboard l
		 JOIN users u ON u.id = l.user_id
		 WHERE u.is_flagged = false
		 ORDER BY l.step_count DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []model.LeaderboardEntry
	for rows.Next() {
		var entry model.LeaderboardEntry
		if err := rows.Scan(
			&entry.Rank,
			&entry.UserID,
			&entry.Name,
			&entry.AvatarURL,
			&entry.StepCount,
		); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// GetUserRank returns a single user's rank and step count
func (r *LeaderboardRepository) GetUserRank(ctx context.Context, userID int) (*model.LeaderboardEntry, error) {
	var entry model.LeaderboardEntry
	err := r.db.QueryRow(ctx,
		`SELECT rank, user_id, name, avatar_url, step_count FROM (
		    SELECT
		        RANK() OVER (ORDER BY l.step_count DESC) as rank,
		        l.user_id,
		        u.name,
		        COALESCE(u.avatar_url, '') as avatar_url,
		        l.step_count
		    FROM leaderboard l
		    JOIN users u ON u.id = l.user_id
		    WHERE u.is_flagged = false
		) ranked
		WHERE user_id = $1`,
		userID,
	).Scan(
		&entry.Rank,
		&entry.UserID,
		&entry.Name,
		&entry.AvatarURL,
		&entry.StepCount,
	)
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

// Reset clears all leaderboard entries (for weekly reset)
func (r *LeaderboardRepository) Reset(ctx context.Context) error {
	_, err := r.db.Exec(ctx, "TRUNCATE leaderboard")
	return err
}
