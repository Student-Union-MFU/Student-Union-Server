package repository

import (
	"context"
	"errors"
	"su-server/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrClubFairUserNotFound is returned instead of pgx.ErrNoRows so the service
// layer can tell "no such account" from "the query broke" without importing pgx.
var ErrClubFairUserNotFound = errors.New("clubfair: user not found")

// ErrClubFairPhoneTaken is the unique index on clubfair_users.phone, named so
// the handler can answer 409 instead of letting a raw pgconn error fall through
// to the catch-all 401.
var ErrClubFairPhoneTaken = errors.New("clubfair: that phone already has an account")

// clubfair_users carries four UNIQUE columns — email, phone, student_id and
// oauth_subject — and Postgres reports all four as 23505. Matching the
// constraint name as well as the code is what keeps this from dressing an email
// or student-id collision up as a phone one; anything else still travels up as
// itself, which is a 500 the logs can be read on.
const clubFairPhoneConstraint = "clubfair_users_phone_key"

func translateClubFairPhoneConflict(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == clubFairPhoneConstraint {
		return ErrClubFairPhoneTaken
	}
	return err
}

type ClubFairAuthRepository struct {
	db *pgxpool.Pool
}

func NewClubFairAuthRepository(db *pgxpool.Pool) *ClubFairAuthRepository {
	return &ClubFairAuthRepository{db: db}
}

// Every read selects the same column list in the same order. Named once so a
// column added to one query cannot be forgotten in another.
const clubFairUserColumns = `
	id, role, first_name, surname, email, phone, student_id, school, major,
	avatar_url, oauth_subject, password_hash, is_flagged, created_at, updated_at`

func scanClubFairUser(row pgx.Row) (*model.ClubFairUser, error) {
	var u model.ClubFairUser
	err := row.Scan(
		&u.ID, &u.Role, &u.FirstName, &u.Surname, &u.Email, &u.Phone,
		&u.StudentID, &u.School, &u.Major, &u.AvatarURL, &u.OAuthSubject,
		&u.PasswordHash, &u.IsFlagged, &u.CreatedAt, &u.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrClubFairUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *ClubFairAuthRepository) GetByID(ctx context.Context, id int) (*model.ClubFairUser, error) {
	return scanClubFairUser(r.db.QueryRow(ctx,
		`SELECT `+clubFairUserColumns+` FROM clubfair_users WHERE id = $1`, id))
}

func (r *ClubFairAuthRepository) FindByEmail(ctx context.Context, email string) (*model.ClubFairUser, error) {
	return scanClubFairUser(r.db.QueryRow(ctx,
		`SELECT `+clubFairUserColumns+` FROM clubfair_users WHERE lower(email) = lower($1)`, email))
}

// FindByPhone matches on the stored national form. Normalisation happens in the
// service so both sides of the comparison are the same shape.
func (r *ClubFairAuthRepository) FindByPhone(ctx context.Context, phone string) (*model.ClubFairUser, error) {
	return scanClubFairUser(r.db.QueryRow(ctx,
		`SELECT `+clubFairUserColumns+` FROM clubfair_users WHERE phone = $1`, phone))
}

// UpsertGoogleIdentity signs a verified Google account in, creating the row on
// first sight.
//
// Conflict is on `email`, which is the key the two credential paths share: a
// student who signed up with a password and later taps "continue with Google"
// has their existing row linked rather than a second one created.
//
// What it deliberately does NOT overwrite on an existing row is the name. Google
// is the source of it once, at creation; after that the student may have
// corrected it, and a login should not silently revert an edit. Avatar and
// oauth_subject are Google's to keep current.
func (r *ClubFairAuthRepository) UpsertGoogleIdentity(
	ctx context.Context,
	firstName, surname, email, studentID, avatarURL, oauthSubject string,
) (*model.ClubFairUser, error) {
	return scanClubFairUser(r.db.QueryRow(ctx,
		`INSERT INTO clubfair_users
		     (role, first_name, surname, email, student_id, avatar_url, oauth_subject)
		 VALUES ('student', $1, $2, $3, $4, $5, $6)
		 ON CONFLICT (email) DO UPDATE SET
		     oauth_subject = EXCLUDED.oauth_subject,
		     avatar_url    = EXCLUDED.avatar_url,
		     student_id    = COALESCE(clubfair_users.student_id, EXCLUDED.student_id),
		     updated_at    = now()
		 RETURNING `+clubFairUserColumns,
		firstName, surname, email, studentID, avatarURL, oauthSubject,
	))
}

// CreateWithPassword registers an account with no Google identity behind it.
func (r *ClubFairAuthRepository) CreateWithPassword(
	ctx context.Context,
	firstName, surname, email, phone, studentID, school, major, passwordHash string,
) (*model.ClubFairUser, error) {
	return scanClubFairUser(r.db.QueryRow(ctx,
		`INSERT INTO clubfair_users
		     (role, first_name, surname, email, phone, student_id, school, major, password_hash)
		 VALUES ('student', $1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING `+clubFairUserColumns,
		firstName, surname, email, phone, studentID, school, major, passwordHash,
	))
}

// SetPassword adds or replaces the password on an existing account — the path a
// Google-only student takes to gain the phone-and-password login.
func (r *ClubFairAuthRepository) SetPassword(ctx context.Context, id int, hash string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE clubfair_users SET password_hash = $2, updated_at = now() WHERE id = $1`,
		id, hash)
	return err
}

// Delete removes the account. The ON DELETE CASCADE on clubfair_users(id) takes
// the student's check-ins, announcement reactions and prize claims with it;
// announcements they authored and prizes they issued survive with the author
// and issuer nulled (ON DELETE SET NULL), so there is nothing to clean up here.
// ErrClubFairUserNotFound when the row is already gone, so a stale token
// deleting twice reads as a 404 rather than a silent success.
func (r *ClubFairAuthRepository) Delete(ctx context.Context, id int) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM clubfair_users WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrClubFairUserNotFound
	}
	return nil
}

// UpdateProfile writes the fields the student owns. A nil argument leaves that
// column alone, so a screen that edits one field cannot blank the others.
func (r *ClubFairAuthRepository) UpdateProfile(
	ctx context.Context,
	id int,
	phone, school, major *string,
) (*model.ClubFairUser, error) {
	user, err := scanClubFairUser(r.db.QueryRow(ctx,
		`UPDATE clubfair_users SET
		     phone      = COALESCE($2, phone),
		     school     = COALESCE($3, school),
		     major      = COALESCE($4, major),
		     updated_at = now()
		 WHERE id = $1
		 RETURNING `+clubFairUserColumns,
		id, phone, school, major,
	))
	if err != nil {
		return nil, translateClubFairPhoneConflict(err)
	}
	return user, nil
}
