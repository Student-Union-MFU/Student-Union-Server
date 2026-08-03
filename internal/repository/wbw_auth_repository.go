package repository

import (
	"context"
	"errors"

	"su-server/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrDuplicate = PG 23505 (unique violation)
var ErrDuplicate = errors.New("duplicate")

// IsPGCode ช่วยเช็ค SQLSTATE เช่น "23505" (unique) "23503" (fk) "22P02" (invalid text repr)
func IsPGCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == code
	}
	return false
}

type WBWAuthRepository struct {
	db *pgxpool.Pool
}

func NewWBWAuthRepository(db *pgxpool.Pool) *WBWAuthRepository {
	return &WBWAuthRepository{db: db}
}

// Register สร้าง app_user + participant_profile + health_details + consent ใน transaction เดียว
// username = student_id และ role ถูกบังคับเป็น 'participant' เสมอ (client กำหนดเองไม่ได้)
func (r *WBWAuthRepository) Register(
	ctx context.Context,
	req model.RegisterRequest,
	studentID, passwordHash string,
	birthdate *string,
) (*model.AuthUser, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var user model.AuthUser
	err = tx.QueryRow(ctx,
		`INSERT INTO app_user (username, password_hash, role, student_id, display_name)
		 VALUES ($1, $2, 'participant', $3, $4)
		 RETURNING user_id::text, username, role::text`,
		studentID, passwordHash, studentID,
		nullableName(req.Profile.FirstName, req.Profile.LastName),
	).Scan(&user.UserID, &user.Username, &user.Role)
	if err != nil {
		if IsPGCode(err, "23505") {
			return nil, ErrDuplicate
		}
		return nil, err
	}

	// sex ที่ไม่อยู่ใน enum ให้เป็น NULL (ตาม Express เดิม)
	var sex *string
	if isValidSex(req.Profile.Sex) {
		s := req.Profile.Sex
		sex = &s
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO participant_profile
		   (user_id, first_name, last_name, date_of_birth, sex, contact_phone,
		    school_id, major, photo_url, emergency_contact_name, emergency_contact_phone)
		 VALUES ($1,$2,$3,$4::date,$5::sex_type,$6,$7,$8,$9,$10,$11)`,
		user.UserID, req.Profile.FirstName, req.Profile.LastName, birthdate, sex,
		req.Profile.ContactPhone, req.Profile.SchoolID, req.Profile.Major,
		req.Profile.PhotoURL, req.Profile.EmergencyContactName, req.Profile.EmergencyContactPhone,
	)
	if err != nil {
		return nil, err
	}

	// blood_type นอก enum ให้เป็น NULL
	var blood *string
	if req.Medical.BloodType != nil && isValidBlood(*req.Medical.BloodType) {
		blood = req.Medical.BloodType
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO health_details (user_id, blood_type, weight_kg, height_cm)
		 VALUES ($1, $2::blood_type, $3, $4)`,
		user.UserID, blood, req.Medical.WeightKg, req.Medical.HeightCm,
	)
	if err != nil {
		return nil, err
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO consent
		   (user_id, consent_health_data, consent_health_data_at,
		    consent_emergency_treatment, waiver_accepted)
		 VALUES ($1, $2, CASE WHEN $2 THEN now() END, $3, $4)`,
		user.UserID, req.Consent.ConsentHealthData,
		req.Consent.ConsentEmergencyTreatment, req.Consent.WaiverAccepted,
	)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &user, nil
}

// FindByUsername คืน user + hash + status สำหรับตรวจรหัสผ่านและ gate การอนุมัติ
func (r *WBWAuthRepository) FindByUsername(ctx context.Context, username string) (*model.AuthUser, string, string, error) {
	var u model.AuthUser
	var hash, status string
	err := r.db.QueryRow(ctx,
		`SELECT user_id::text, username, role::text, password_hash, status::text
		 FROM app_user WHERE username = $1`, username,
	).Scan(&u.UserID, &u.Username, &u.Role, &hash, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", "", nil // ไม่เจอ user — handler จะตอบ 401 เหมือนรหัสผิด (ไม่บอกใบ้)
	}
	if err != nil {
		return nil, "", "", err
	}
	return &u, hash, status, nil
}

// RegisterStaff สร้างบัญชี staff สถานะ 'pending' + staff_profile ใน transaction เดียว
// — ล็อกอินไม่ได้จนกว่าแอดมินจะอนุมัติ
func (r *WBWAuthRepository) RegisterStaff(
	ctx context.Context,
	username, passwordHash string,
	schoolID int,
	major *string,
	staffRole string,
) (*model.AuthUser, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var u model.AuthUser
	err = tx.QueryRow(ctx,
		`INSERT INTO app_user (username, password_hash, role, status)
		 VALUES ($1, $2, 'staff', 'pending')
		 RETURNING user_id::text, username, role::text`,
		username, passwordHash,
	).Scan(&u.UserID, &u.Username, &u.Role)
	if err != nil {
		if IsPGCode(err, "23505") {
			return nil, ErrDuplicate
		}
		return nil, err
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO staff_profile (user_id, school_id, major, staff_role)
		 VALUES ($1, $2, $3, $4::staff_role)`,
		u.UserID, schoolID, major, staffRole,
	)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &u, nil
}

func isValidSex(s string) bool {
	return s == "male" || s == "female" || s == "unspecified"
}

func isValidBlood(b string) bool {
	switch b {
	case "O-", "O+", "A-", "A+", "B-", "B+", "AB-", "AB+":
		return true
	}
	return false
}

func nullableName(first, last string) *string {
	name := first
	if last != "" {
		if name != "" {
			name += " "
		}
		name += last
	}
	if name == "" {
		return nil
	}
	return &name
}
