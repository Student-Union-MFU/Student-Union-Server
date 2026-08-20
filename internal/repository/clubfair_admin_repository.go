package repository

import (
	"context"
	"errors"
	"strings"
	"su-server/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

/*
The staff dashboard's reads and writes: the participant roster and the prize
tiers.

Both are things that existed as tables with no way to reach them. `is_flagged`
has been a column on clubfair_users since 000017 and nothing has ever set it;
`clubfair_prize_tier` has been edited by writing a migration (000022), which is
the right tool for a considered change and the wrong one at four o'clock on the
Sunday when the draw is undersubscribed.
*/

var (
	// ErrPrizeTierNotFound distinguishes "no such tier" from a broken query.
	ErrPrizeTierNotFound = errors.New("clubfair: prize tier not found")

	// ErrPrizeTierHasClaims is a delete refused because students are holding
	// prizes issued under this tier. The FK would refuse it anyway — this turns
	// a 23503 into something the handler can phrase.
	ErrPrizeTierHasClaims = errors.New("clubfair: prize tier has claims against it")

	// ErrPrizeThresholdTaken is the UNIQUE on `threshold`. Two tiers at the same
	// count is a data entry error, not a scheme.
	ErrPrizeThresholdTaken = errors.New("clubfair: another tier already has that threshold")
)

type ClubFairAdminRepository struct {
	db *pgxpool.Pool
}

func NewClubFairAdminRepository(db *pgxpool.Pool) *ClubFairAdminRepository {
	return &ClubFairAdminRepository{db: db}
}

// ---- Participants --------------------------------------------------------

const participantColumns = `u.id, u.first_name, u.surname, u.email, u.student_id,
                            u.phone, u.school, u.major, u.role, u.is_flagged, u.created_at`

// ListParticipants returns one page of the roster, newest first, with each
// student's stamp count.
//
// The count is a correlated subquery rather than a LEFT JOIN with a GROUP BY,
// because grouping the whole of clubfair_checkin to page twenty rows out of it
// is work proportional to the fair rather than to the page. Postgres turns this
// one into an index scan on clubfair_checkin's (user_id, booth_id) unique index,
// which already exists for the check-in constraint.
//
// [query] matches name, email, student id and phone at once — staff at a prize
// table have whichever of those the student can produce, and asking them to pick
// a field first is a worse counter than a single box.
//
// Returns the total matching count alongside the page, so the dashboard can show
// "20 of 412" rather than a page with no sense of scale.
//
// [roles] narrows to a set of roles, or returns everyone when it is empty. That
// is what makes a staff-only screen possible: filtering a *paged* roster in the
// client would show "the staff on page one of everybody", which is a different
// and mostly empty list.
func (r *ClubFairAdminRepository) ListParticipants(
	ctx context.Context, query string, roles []string, limit, offset int,
) ([]model.ClubFairParticipant, int, error) {
	// Wrapped in % here rather than by the caller, so a handler cannot forget
	// and quietly turn the search into an exact match. ILIKE, because a student
	// id typed in a hurry is not capitalised the same way twice.
	pattern := "%" + strings.TrimSpace(query) + "%"

	// An empty query needs no special case: `first_name` is NOT NULL, so
	// `ILIKE '%%'` is true for every row and the whole roster comes back.
	// student_id and phone are nullable, which is why they are not on their own
	// here — `NULL ILIKE …` is NULL, not false, and an OR chain that started
	// with one would drop every student who has not filled it in.
	//
	// The role clause is `$2::text[] IS NULL OR role = ANY($2)` rather than SQL
	// built by string concatenation: the roles arrive from a query string, and
	// one array parameter keeps this a static statement whatever is in it.
	const where = `
		WHERE (u.first_name ILIKE $1 OR u.surname ILIKE $1 OR u.email ILIKE $1
		   OR u.student_id ILIKE $1 OR u.phone ILIKE $1)
		  AND ($2::text[] IS NULL OR u.role = ANY($2))`

	// nil, not an empty array: `= ANY('{}')` is false for every row, which would
	// turn "no filter" into "nobody".
	var roleFilter any
	if len(roles) > 0 {
		roleFilter = roles
	}

	var total int
	if err := r.db.QueryRow(ctx,
		`SELECT count(*) FROM clubfair_users u`+where, pattern, roleFilter).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.db.Query(ctx,
		`SELECT `+participantColumns+`,
		        (SELECT count(*) FROM clubfair_checkin c WHERE c.user_id = u.id) AS visited
		   FROM clubfair_users u`+where+`
		  ORDER BY u.created_at DESC, u.id DESC
		  LIMIT $3 OFFSET $4`, pattern, roleFilter, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]model.ClubFairParticipant, 0, limit)
	for rows.Next() {
		var p model.ClubFairParticipant
		if err := rows.Scan(
			&p.ID, &p.FirstName, &p.Surname, &p.Email, &p.StudentID,
			&p.Phone, &p.School, &p.Major, &p.Role, &p.IsFlagged, &p.CreatedAt,
			&p.Visited,
		); err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}

// UpdateParticipant sets the role and the flag. A nil argument leaves that
// column alone.
//
// These two and nothing else. A staff member editing a student's name, email or
// student id would be editing the identity the Google sign-in joins on — the
// student would come back as a different person, or as nobody. The student owns
// those fields through PATCH /clubfair/me.
func (r *ClubFairAdminRepository) UpdateParticipant(
	ctx context.Context, id int, role *string, isFlagged *bool,
) (*model.ClubFairParticipant, error) {
	var p model.ClubFairParticipant
	err := r.db.QueryRow(ctx,
		`UPDATE clubfair_users u SET
		     role       = COALESCE($2, u.role),
		     is_flagged = COALESCE($3, u.is_flagged),
		     updated_at = now()
		 WHERE u.id = $1
		 RETURNING `+participantColumns+`,
		     (SELECT count(*) FROM clubfair_checkin c WHERE c.user_id = u.id)`,
		id, role, isFlagged,
	).Scan(
		&p.ID, &p.FirstName, &p.Surname, &p.Email, &p.StudentID,
		&p.Phone, &p.School, &p.Major, &p.Role, &p.IsFlagged, &p.CreatedAt,
		&p.Visited,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrClubFairUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// ---- Prize tiers ---------------------------------------------------------

// ListActivePrizeTiers is the public list: the tiers a student can still reach.
//
// Retired tiers are excluded here and included in [ListPrizeTiersForAdmin],
// which is the whole difference between the two. `is_active` is how 000022 took
// the old 20-booth draw out of circulation without destroying the record of who
// had collected it, so a public list that showed it would be advertising a prize
// that no longer exists.
func (r *ClubFairAdminRepository) ListActivePrizeTiers(ctx context.Context) ([]model.PublicPrizeTier, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, threshold, name, description
		   FROM clubfair_prize_tier
		  WHERE is_active
		  ORDER BY threshold`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.PublicPrizeTier, 0)
	for rows.Next() {
		var t model.PublicPrizeTier
		if err := rows.Scan(&t.ID, &t.Threshold, &t.Name, &t.Description); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *ClubFairAdminRepository) ListPrizeTiersForAdmin(ctx context.Context) ([]model.ClubFairPrizeTierAdmin, error) {
	rows, err := r.db.Query(ctx,
		`SELECT t.id, t.threshold, t.name, t.description, t.is_active,
		        (SELECT count(*) FROM clubfair_prize_claim c WHERE c.tier_id = t.id)
		   FROM clubfair_prize_tier t
		  ORDER BY t.threshold`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.ClubFairPrizeTierAdmin, 0)
	for rows.Next() {
		var t model.ClubFairPrizeTierAdmin
		if err := rows.Scan(
			&t.ID, &t.Threshold, &t.Name, &t.Description, &t.IsActive, &t.Claims,
		); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

const prizeTierAdminReturning = `id, threshold, name, description, is_active, 0`

func (r *ClubFairAdminRepository) CreatePrizeTier(
	ctx context.Context, threshold int, name string, description *string,
) (*model.ClubFairPrizeTierAdmin, error) {
	var t model.ClubFairPrizeTierAdmin
	err := r.db.QueryRow(ctx,
		`INSERT INTO clubfair_prize_tier (threshold, name, description)
		 VALUES ($1, $2, $3)
		 RETURNING `+prizeTierAdminReturning,
		threshold, name, description,
	).Scan(&t.ID, &t.Threshold, &t.Name, &t.Description, &t.IsActive, &t.Claims)

	if IsPGCode(err, "23505") {
		return nil, ErrPrizeThresholdTaken
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// UpdatePrizeTier moves a tier rather than replacing it.
//
// The same rule migration 000022 had to follow by hand: a tier's id is what
// `clubfair_prize_claim.tier_id` points at, so changing a threshold is an UPDATE
// and never a delete-and-reinsert. Retiring one is `is_active = false` for the
// same reason.
func (r *ClubFairAdminRepository) UpdatePrizeTier(
	ctx context.Context, id, threshold int, name string, description *string, isActive bool,
) (*model.ClubFairPrizeTierAdmin, error) {
	var t model.ClubFairPrizeTierAdmin
	err := r.db.QueryRow(ctx,
		`UPDATE clubfair_prize_tier SET
		     threshold   = $2,
		     name        = $3,
		     description = $4,
		     is_active   = $5
		 WHERE id = $1
		 RETURNING id, threshold, name, description, is_active,
		     (SELECT count(*) FROM clubfair_prize_claim c WHERE c.tier_id = $1)`,
		id, threshold, name, description, isActive,
	).Scan(&t.ID, &t.Threshold, &t.Name, &t.Description, &t.IsActive, &t.Claims)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPrizeTierNotFound
	}
	if IsPGCode(err, "23505") {
		return nil, ErrPrizeThresholdTaken
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// DeletePrizeTier removes a tier nobody has collected.
//
// Guarded rather than left to the FK, so the caller gets "students are holding
// this one" instead of a 23503. A tier with claims is retired, not deleted —
// which is exactly the dance 000022 had to do for the 20-booth draw.
func (r *ClubFairAdminRepository) DeletePrizeTier(ctx context.Context, id int) error {
	var claims int
	err := r.db.QueryRow(ctx,
		`SELECT count(*) FROM clubfair_prize_claim WHERE tier_id = $1`, id).Scan(&claims)
	if err != nil {
		return err
	}
	if claims > 0 {
		return ErrPrizeTierHasClaims
	}

	tag, err := r.db.Exec(ctx, `DELETE FROM clubfair_prize_tier WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrPrizeTierNotFound
	}
	return nil
}

// IsLastAdmin reports whether this account is the only admin left.
//
// A read-then-write race exists here: two admins demoting each other at the same
// instant could both pass this check and leave the fair with none. It is left as
// a service-level check rather than promoted to the one-row-plus-CHECK-plus-
// trigger pattern that `wbw_capacity` and `participant_group` use, and the
// difference is worth stating.
//
// Those two guard a ceiling that thousands of concurrent students push against
// during a registration window — the race is not hypothetical there, it is the
// normal case, and losing it oversells seats. This guards a handful of accounts
// edited by hand, one screen at a time, by people who work together. Losing this
// race needs two admins to demote each other within the same few milliseconds,
// and the recovery — `cmd/createclubfairstaff` — is a command that already has
// to exist for the first admin anyway.
//
// If the roster ever grows an operation that changes roles in bulk, this stops
// being true and the check belongs in the database.
func (r *ClubFairAdminRepository) IsLastAdmin(ctx context.Context, id int) (bool, error) {
	var last bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS (
		     SELECT 1 FROM clubfair_users WHERE id = $1 AND role = 'admin' AND NOT is_flagged
		 ) AND (
		     SELECT count(*) FROM clubfair_users WHERE role = 'admin' AND NOT is_flagged
		 ) <= 1`, id).Scan(&last)
	return last, err
}

// ---- Creating an account, and reading one --------------------------------

// ErrClubFairAccountTaken is the UNIQUE on email, phone or student_id. The
// three share one error because the handler says the same thing for all of
// them: somebody already has this account, go and find it rather than making a
// second one.
var ErrClubFairAccountTaken = errors.New("clubfair: that email, phone or student id is already registered")

// ErrClubFairParticipantNotFound distinguishes "no such person" from a broken
// query. Prefixed because WBW's staff repository has its own participants and
// its own not-found, in this same package.
var ErrClubFairParticipantNotFound = errors.New("clubfair: participant not found")

// CreateParticipant makes an account on an admin's behalf.
//
// This is the only path that creates a Club Fair row without the person being
// present, and it exists because the roster is not only students: the people
// staffing booths have to be in the table before they can be assigned to one,
// and asking each of them to self-register and then hunting for the row is not
// a thing that happens on setup day.
//
// [passwordHash] is required rather than optional. `clubfair_users_has_credential`
// demands an oauth_subject or a password_hash, and an admin cannot mint the
// former — it is Google's. An account with neither would be a row nobody can
// ever sign in to, which is worse than no row.
func (r *ClubFairAdminRepository) CreateParticipant(
	ctx context.Context,
	firstName, surname, email string,
	phone, studentID, school, major *string,
	role, passwordHash string,
) (*model.ClubFairParticipant, error) {
	var id int
	err := r.db.QueryRow(ctx,
		`INSERT INTO clubfair_users
		     (first_name, surname, email, phone, student_id, school, major, role, password_hash)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING id`,
		firstName, surname, email, phone, studentID, school, major, role, passwordHash,
	).Scan(&id)
	if err != nil {
		// 23505 is unique_violation. Same translation the register path does,
		// for the same three columns.
		if strings.Contains(err.Error(), "23505") {
			return nil, ErrClubFairAccountTaken
		}
		return nil, err
	}
	return r.GetParticipant(ctx, id)
}

// GetParticipant is one row of the roster, with the same shape the list uses.
//
// Sharing `participantColumns` with ListParticipants is the point: a detail
// screen that showed a different set of fields, or the same fields computed a
// second way, is how a stamp count comes to disagree with itself between two
// screens of the same dashboard.
func (r *ClubFairAdminRepository) GetParticipant(
	ctx context.Context, id int,
) (*model.ClubFairParticipant, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+participantColumns+`,
		        (SELECT count(*) FROM clubfair_checkin c WHERE c.user_id = u.id) AS visited
		   FROM clubfair_users u
		  WHERE u.id = $1`, id)

	var p model.ClubFairParticipant
	if err := row.Scan(
		&p.ID, &p.FirstName, &p.Surname, &p.Email, &p.StudentID,
		&p.Phone, &p.School, &p.Major, &p.Role, &p.IsFlagged, &p.CreatedAt,
		&p.Visited,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrClubFairParticipantNotFound
		}
		return nil, err
	}
	return &p, nil
}

// ---- Booth ownership -----------------------------------------------------

// ListOwnedBoothIDs is what the dashboard shows against an owner's name.
func (r *ClubFairAdminRepository) ListOwnedBoothIDs(ctx context.Context, userID int) ([]int, error) {
	rows, err := r.db.Query(ctx,
		`SELECT booth_id FROM clubfair_booth_owner WHERE user_id = $1 ORDER BY booth_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// `make`, not `var`: a nil slice marshals as `null`, and every client here
	// reads a null list as a broken response rather than as an empty one.
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

// ListOwnedBooths is the same set as whole booth rows, in floor-plan order.
//
// What the booth owner's own screen renders. The ordering matches
// BoothRepository.GetAllBooths exactly — zone sort order, then a natural sort on
// the booth code so B10 follows B2 — because an owner holding A4 and A5 should
// see them in the order they are standing in, and two orderings of one floor
// plan is one too many.
func (r *ClubFairAdminRepository) ListOwnedBooths(ctx context.Context, userID int) ([]model.Booth, error) {
	rows, err := r.db.Query(ctx,
		`SELECT b.id, b.event_id, b.name, b.name_en, b.category,
		        b.zone, b.booth_code, b.about, b.icon, b.created_at
		   FROM clubfair_booth_owner o
		   JOIN booth b ON b.id = o.booth_id
		   LEFT JOIN clubfair_zone z ON z.code = b.zone
		  WHERE o.user_id = $1
		  ORDER BY z.sort_order NULLS LAST,
		           lpad(substring(b.booth_code from 2), 3, '0') NULLS LAST,
		           b.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	booths := make([]model.Booth, 0)
	for rows.Next() {
		var b model.Booth
		if err := rows.Scan(
			&b.ID, &b.EventID, &b.Name, &b.NameEN, &b.Category,
			&b.Zone, &b.BoothCode, &b.About, &b.Icon, &b.CreatedAt,
		); err != nil {
			return nil, err
		}
		booths = append(booths, b)
	}
	return booths, rows.Err()
}

// SetOwnedBooths replaces an owner's assignments with exactly [boothIDs].
//
// Replace rather than add/remove, because the dashboard edits this as a set of
// checkboxes and a diff computed in the browser is a diff that can be computed
// against a stale list. Sending the whole intended set means the last writer
// wins on a screen someone is looking at, rather than two admins each toggling
// one booth and ending up with the union of their mistakes.
//
// In a transaction, because a delete that succeeds and an insert that fails
// would silently lock an owner out of every booth they had.
func (r *ClubFairAdminRepository) SetOwnedBooths(
	ctx context.Context, userID int, boothIDs []int, assignedBy int,
) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	// A no-op once Commit has run. This is the rollback for every path that
	// returns early below.
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`DELETE FROM clubfair_booth_owner WHERE user_id = $1`, userID); err != nil {
		return err
	}

	for _, boothID := range boothIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO clubfair_booth_owner (booth_id, user_id, assigned_by)
			 VALUES ($1, $2, $3)
			 ON CONFLICT (booth_id, user_id) DO NOTHING`,
			boothID, userID, assignedBy); err != nil {
			// 23503 is foreign_key_violation: a booth id that is not a booth.
			// Reported as "not found" rather than as a constraint name, because
			// the caller sent it and can fix it.
			if strings.Contains(err.Error(), "23503") {
				return ErrBoothNotFound
			}
			return err
		}
	}

	return tx.Commit(ctx)
}

// ClearOwnedBooths drops every assignment an account holds.
//
// Called when a role moves away from booth_owner, and it is the *only* thing
// that actually revokes a booth screen. The role travels in a 30-day JWT this
// server cannot recall, so a demoted account goes on presenting a token that
// says booth_owner until it expires — see ClubFairAdminService.UpdateParticipant.
// Deleting the rows is what makes the demotion take effect on the next poll.
func (r *ClubFairAdminRepository) ClearOwnedBooths(ctx context.Context, userID int) error {
	_, err := r.db.Exec(ctx, `DELETE FROM clubfair_booth_owner WHERE user_id = $1`, userID)
	return err
}

// SetParticipantPassword replaces one account's password hash.
//
// The recovery path for an account an admin created and whose owner cannot sign
// in — which, before this existed, was unrecoverable from anywhere: the account
// holder needs a token to use PUT /clubfair/me/password, and getting a token is
// the thing they cannot do. `cmd/createclubfairstaff` promotes but does not
// touch credentials.
func (r *ClubFairAdminRepository) SetParticipantPassword(
	ctx context.Context, id int, passwordHash string,
) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE clubfair_users SET password_hash = $2, updated_at = now() WHERE id = $1`,
		id, passwordHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrClubFairParticipantNotFound
	}
	return nil
}

// ---- A participant's stamps ----------------------------------------------

/*
Reading and editing somebody else's check-ins.

## Why these are here and not on ClubFairFairRepository

That one owns the student-facing side: RecordCheckIn verifies an HMAC payload
and ListCheckIns answers "my own stamps". Both are about the caller. These three
are about *somebody else*, are reachable only by an admin, and want a different
shape — the dashboard renders a table of booth names, so the read joins `booth`
rather than returning bare ids for the client to cross-reference.

Keeping them apart also keeps the student path honest: nothing in this file is
reachable without the admin gate in the service above it, so there is no route by
which a phone reaches an unverified insert.

## The insert does not go through the check-in scheme, and cannot

CLAUDE.md §6: a booth's 32-byte secret never leaves the server, the app posts a
scanned payload verbatim, and the server verifies it. An admin fixing a stamp has
no payload — the whole reason they are doing it is that the scan did not happen
or did not land. So this writes `booth_id` directly.

That is the point of the feature and also its risk, and the risk is worth being
explicit about: **this is the one path in the system that mints a stamp without
proof a student stood at a booth.** It is admin-only for that reason, not because
the data is sensitive.
*/

// BoothExists answers whether a booth id is real.
//
// Here rather than borrowed from ClubFairFairRepository so the service above can
// tell an admin *which* id was wrong. Without it the insert's foreign key does
// the refusing, and a constraint violation reaches the handler as an
// unclassified error — which is a 500 and the words "ดำเนินการไม่สำเร็จ" for
// what is simply a booth that does not exist. That was the actual behaviour
// until this was added, and the message told the admin nothing they could act
// on.
func (r *ClubFairAdminRepository) BoothExists(ctx context.Context, boothID int) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM booth WHERE id = $1)`, boothID).Scan(&exists)
	return exists, err
}

// ListParticipantCheckIns returns one student's stamps with their booths, oldest
// first — the order they walked the floor.
//
// LEFT JOIN, not JOIN. `clubfair_checkin.booth_id` is ON DELETE CASCADE so a
// deleted booth takes its stamps with it and the join can never miss... today.
// A LEFT JOIN costs nothing here and means a future where that changes shows the
// admin a row with no name rather than silently dropping a stamp the student
// still has.
func (r *ClubFairAdminRepository) ListParticipantCheckIns(
	ctx context.Context, userID int,
) ([]model.ClubFairAdminCheckIn, error) {
	rows, err := r.db.Query(ctx, `
		SELECT c.id, c.booth_id,
		       COALESCE(b.name, ''), b.name_en, b.booth_code, b.zone,
		       c.device_time, c.server_received_at
		  FROM clubfair_checkin c
		  LEFT JOIN booth b ON b.id = c.booth_id
		 WHERE c.user_id = $1
		 ORDER BY c.server_received_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.ClubFairAdminCheckIn, 0)
	for rows.Next() {
		var c model.ClubFairAdminCheckIn
		if err := rows.Scan(
			&c.ID, &c.BoothID,
			&c.BoothName, &c.BoothNameEn, &c.BoothCode, &c.Zone,
			&c.DeviceTime, &c.ServerReceivedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AddParticipantCheckIn stamps a booth for a student who did not scan it, and
// reports whether the row is new.
//
// `created == false` means the student already had that booth. That is not an
// error — an admin clicking a booth that is already stamped has asked for a
// state that already holds — so the caller reports it and moves on rather than
// failing. Same reasoning as RecordCheckIn's idempotency, arrived at from the
// other side.
//
// ⚠ **client_id is minted by Postgres, not by the caller.** The column is UNIQUE
// because it is the app's idempotency key, minted on the phone before a scan
// goes out; there is no phone here, so a value has to come from somewhere and
// `gen_random_uuid()` is the one source that cannot collide with a real one. Do
// not be tempted to derive it from the user and booth ids — a deterministic
// client_id would make a re-add after a delete collide with its own deleted row.
//
// device_time is set to now() alongside server_received_at, because the honest
// answer to "when did the phone say this happened" is "no phone said anything".
// They will be equal on every hand-entered row, which is as close to a marker as
// this schema has — see the note on ClubFairAdminCheckIn.
func (r *ClubFairAdminRepository) AddParticipantCheckIn(
	ctx context.Context, userID, boothID int,
) (created bool, err error) {
	tag, err := r.db.Exec(ctx, `
		INSERT INTO clubfair_checkin (client_id, user_id, booth_id, device_time)
		VALUES (gen_random_uuid(), $1, $2, now())
		ON CONFLICT DO NOTHING`, userID, boothID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// DeleteParticipantCheckIn removes one stamp and reports whether there was one.
//
// Keyed on (user_id, booth_id) rather than on the check-in's own id, matching
// the UNIQUE constraint the table is built around and the way the dashboard
// thinks: an admin unticks *a booth*, not a row they have the primary key of.
// It also makes the call idempotent — a second delete is `false`, not a 500.
func (r *ClubFairAdminRepository) DeleteParticipantCheckIn(
	ctx context.Context, userID, boothID int,
) (removed bool, err error) {
	tag, err := r.db.Exec(ctx,
		`DELETE FROM clubfair_checkin WHERE user_id = $1 AND booth_id = $2`,
		userID, boothID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ---- The fair at a glance ------------------------------------------------

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
