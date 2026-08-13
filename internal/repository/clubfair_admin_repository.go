package repository

import (
	"context"

	"su-server/internal/model"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ClubFairAdminRepository struct {
	db *pgxpool.Pool
}

func NewClubFairAdminRepository(db *pgxpool.Pool) *ClubFairAdminRepository {
	return &ClubFairAdminRepository{db: db}
}

// Dashboard reads the four counts in one round trip, the way WBW's does.
//
// full_sweeps compares each student's stamp count to the live booth count
// rather than a hardcoded 28: the top prize tier already floats the same way
// (a booth pulling out mid-fair moves both together, with no release).
func (r *ClubFairAdminRepository) Dashboard(ctx context.Context) (*model.ClubFairDashboardStats, error) {
	var s model.ClubFairDashboardStats
	err := r.db.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM clubfair_users WHERE role = 'student'),
		       (SELECT count(*) FROM clubfair_checkin),
		       (SELECT count(*) FROM clubfair_prize_claim),
		       (SELECT count(*) FROM (
		            SELECT user_id FROM clubfair_checkin
		             GROUP BY user_id
		            HAVING count(*) >= (SELECT count(*) FROM booth)
		        ) full_sweep)
	`).Scan(&s.Students, &s.TotalCheckins, &s.PrizesClaimed, &s.FullSweeps)
	if err != nil {
		return nil, err
	}
	return &s, nil
}
