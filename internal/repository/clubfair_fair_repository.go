package repository

import (
	"context"
	"errors"
	"su-server/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrBoothNotFound distinguishes "no such booth" from a broken query, so the
	// handler can answer 404 rather than 500.
	ErrBoothNotFound = errors.New("clubfair: booth not found")

	// ErrCheckInNotFound is only ever an internal inconsistency: a conflicting
	// insert whose conflicting row then cannot be found.
	ErrCheckInNotFound = errors.New("clubfair: check-in not found")
)

type ClubFairFairRepository struct {
	db *pgxpool.Pool
}

func NewClubFairFairRepository(db *pgxpool.Pool) *ClubFairFairRepository {
	return &ClubFairFairRepository{db: db}
}

// ---- Zones ---------------------------------------------------------------

func (r *ClubFairFairRepository) ListZones(ctx context.Context) ([]model.Zone, error) {
	rows, err := r.db.Query(ctx,
		`SELECT code, name, name_en, intent, sort_order
		   FROM clubfair_zone ORDER BY sort_order`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// `make`, not `var`: a nil slice marshals as `null`, which the clients read
	// as a broken response.
	zones := make([]model.Zone, 0, 3)
	for rows.Next() {
		var z model.Zone
		if err := rows.Scan(&z.Code, &z.Name, &z.NameEN, &z.Intent, &z.SortOrder); err != nil {
			return nil, err
		}
		zones = append(zones, z)
	}
	return zones, rows.Err()
}

// ---- Booth secret --------------------------------------------------------

// BoothSecret reads one booth's HMAC key.
//
// The only query in the codebase that selects this column, and it takes a single
// id — never a list. Whoever holds a booth's secret can mint valid check-in
// codes for it for the rest of the event, so it leaves the database one row at a
// time, for one purpose: computing or verifying that booth's current code.
func (r *ClubFairFairRepository) BoothSecret(ctx context.Context, boothID int) (string, error) {
	var secret string
	err := r.db.QueryRow(ctx, `SELECT secret FROM booth WHERE id = $1`, boothID).Scan(&secret)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrBoothNotFound
	}
	return secret, err
}

func (r *ClubFairFairRepository) BoothExists(ctx context.Context, boothID int) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM booth WHERE id = $1)`, boothID).Scan(&exists)
	return exists, err
}

func (r *ClubFairFairRepository) BoothCount(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM booth`).Scan(&n)
	return n, err
}

// ---- Check-ins -----------------------------------------------------------

const checkInColumns = `id, client_id, booth_id, device_time, server_received_at`

func scanCheckIn(row pgx.Row) (*model.ClubFairCheckIn, error) {
	var c model.ClubFairCheckIn
	err := row.Scan(&c.ID, &c.ClientID, &c.BoothID, &c.DeviceTime, &c.ServerReceivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCheckInNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// RecordCheckIn writes a stamp and reports whether it was new.
//
// `ON CONFLICT DO NOTHING` covers both unique constraints at once, which is what
// makes this safe to call repeatedly:
//
//   - the same client_id again is the app replaying its offline queue after a
//     timeout, and must be an idempotent success
//   - a different client_id for a booth already stamped is the student scanning
//     the same booth twice, and must be reported as such
//
// Both come back as `created == false`; the service tells them apart by which
// row it finds, because only the second is worth saying out loud.
func (r *ClubFairFairRepository) RecordCheckIn(
	ctx context.Context,
	clientID string,
	userID, boothID int,
	deviceTimeRFC3339 string,
) (checkIn *model.ClubFairCheckIn, created bool, err error) {
	row := r.db.QueryRow(ctx,
		`INSERT INTO clubfair_checkin (client_id, user_id, booth_id, device_time)
		 VALUES ($1, $2, $3, $4::timestamptz)
		 ON CONFLICT DO NOTHING
		 RETURNING `+checkInColumns,
		clientID, userID, boothID, deviceTimeRFC3339,
	)

	inserted, err := scanCheckIn(row)
	if err == nil {
		return inserted, true, nil
	}
	if !errors.Is(err, ErrCheckInNotFound) {
		return nil, false, err
	}

	// Nothing inserted. Find out which rule stopped it — the client's own
	// replay, or this booth already being collected.
	existing, err := scanCheckIn(r.db.QueryRow(ctx,
		`SELECT `+checkInColumns+` FROM clubfair_checkin WHERE client_id = $1`, clientID))
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, ErrCheckInNotFound) {
		return nil, false, err
	}

	existing, err = scanCheckIn(r.db.QueryRow(ctx,
		`SELECT `+checkInColumns+` FROM clubfair_checkin
		  WHERE user_id = $1 AND booth_id = $2`, userID, boothID))
	if err != nil {
		return nil, false, err
	}
	return existing, false, nil
}

// ListCheckIns returns a student's stamps oldest first — the order they walked
// the floor, and the order a sync backlog goes up in.
func (r *ClubFairFairRepository) ListCheckIns(ctx context.Context, userID int) ([]model.ClubFairCheckIn, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+checkInColumns+` FROM clubfair_checkin
		  WHERE user_id = $1 ORDER BY server_received_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.ClubFairCheckIn, 0)
	for rows.Next() {
		var c model.ClubFairCheckIn
		if err := rows.Scan(&c.ID, &c.ClientID, &c.BoothID, &c.DeviceTime, &c.ServerReceivedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---- Progress ------------------------------------------------------------

// Progress is everything Home renders, in one round trip.
//
// One query per fact but one call from the client: the screen shows the count,
// the percentage, the grid and the prize tiles together, and four separate
// requests means four chances to render half a state.
func (r *ClubFairFairRepository) Progress(ctx context.Context, userID int) (*model.ClubFairProgress, error) {
	total, err := r.BoothCount(ctx)
	if err != nil {
		return nil, err
	}

	boothIDs, err := r.visitedBoothIDs(ctx, userID)
	if err != nil {
		return nil, err
	}

	rank, err := r.rank(ctx, userID)
	if err != nil {
		return nil, err
	}

	prizes, err := r.prizeTiers(ctx, userID, len(boothIDs))
	if err != nil {
		return nil, err
	}

	return &model.ClubFairProgress{
		Visited:         len(boothIDs),
		Total:           total,
		VisitedBoothIDs: boothIDs,
		Rank:            rank,
		Prizes:          prizes,
	}, nil
}

func (r *ClubFairFairRepository) visitedBoothIDs(ctx context.Context, userID int) ([]int, error) {
	rows, err := r.db.Query(ctx,
		`SELECT booth_id FROM clubfair_checkin WHERE user_id = $1 ORDER BY booth_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := make([]int, 0)
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// rank returns nil for a student with no stamps yet.
//
// Nil rather than "last": a student who has scanned nothing is not in the
// running, and the app renders an em dash. RANK() rather than ROW_NUMBER() so
// two students on the same count share a position instead of one of them being
// arbitrarily ahead.
func (r *ClubFairFairRepository) rank(ctx context.Context, userID int) (*int, error) {
	var rank *int
	err := r.db.QueryRow(ctx,
		`SELECT rank FROM (
		     SELECT user_id, RANK() OVER (ORDER BY count(*) DESC) AS rank
		       FROM clubfair_checkin GROUP BY user_id
		 ) ranked WHERE user_id = $1`, userID).Scan(&rank)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return rank, nil
}

func (r *ClubFairFairRepository) prizeTiers(ctx context.Context, userID, visited int) ([]model.ClubFairPrizeTier, error) {
	rows, err := r.db.Query(ctx,
		`SELECT t.id, t.threshold, t.name, t.description,
		        ($2 >= t.threshold)   AS reached,
		        (c.id IS NOT NULL)    AS claimed
		   FROM clubfair_prize_tier t
		   LEFT JOIN clubfair_prize_claim c
		          ON c.tier_id = t.id AND c.user_id = $1
		  WHERE t.is_active
		  ORDER BY t.threshold`, userID, visited)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tiers := make([]model.ClubFairPrizeTier, 0)
	for rows.Next() {
		var t model.ClubFairPrizeTier
		if err := rows.Scan(&t.ID, &t.Threshold, &t.Name, &t.Description, &t.Reached, &t.Claimed); err != nil {
			return nil, err
		}
		tiers = append(tiers, t)
	}
	return tiers, rows.Err()
}

// ClaimPrize records a prize handed over, and reports whether it was new.
//
// `false` means this student already collected this tier — the case the UNIQUE
// constraint exists for, and the answer staff at a prize table need.
func (r *ClubFairFairRepository) ClaimPrize(ctx context.Context, userID, tierID, issuedBy int) (bool, error) {
	tag, err := r.db.Exec(ctx,
		`INSERT INTO clubfair_prize_claim (user_id, tier_id, issued_by)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (user_id, tier_id) DO NOTHING`,
		userID, tierID, issuedBy)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// TierThreshold is what a claim is checked against before it is written.
func (r *ClubFairFairRepository) TierThreshold(ctx context.Context, tierID int) (int, error) {
	var threshold int
	err := r.db.QueryRow(ctx,
		`SELECT threshold FROM clubfair_prize_tier WHERE id = $1 AND is_active`, tierID).Scan(&threshold)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errors.New("clubfair: no such prize tier")
	}
	return threshold, err
}
