package repository

import (
	"context"
	"errors"
	"time"

	"su-server/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrDuplicate = PG 23505 (unique violation)
var ErrDuplicate = errors.New("duplicate")

// ErrDuplicateEmail = อีเมลนี้มีบัญชีอื่นถืออยู่แล้ว (ยูนีค wbw_user_email_key)
// แยกจาก ErrDuplicate ที่หมายถึง username/student_id ซ้ำ
var ErrDuplicateEmail = errors.New("duplicate email")

// ErrFull = ที่นั่งเต็ม — CHECK taken_within_max บน wbw_capacity ไม่ผ่าน (PG 23514)
// ดู db/migrations/000021_wbw_capacity.up.sql
var ErrFull = errors.New("capacity full")

// capacityConstraint — ชื่อ constraint ที่บอกว่า "เต็ม" · เช็คชื่อด้วย ไม่ใช่ดูแค่รหัส 23514
// เพราะ CHECK ตัวอื่นบนตารางอื่นก็คืนรหัสเดียวกัน แต่แปลว่าคนละเรื่อง
const capacityConstraint = "taken_within_max"

func isCapacityFull(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514" && pgErr.ConstraintName == capacityConstraint
}

// isUniqueOn — 23505 ของ constraint/index ชื่อที่ระบุ · ตารางที่มียูนีคมากกว่าหนึ่ง
// ตัวต้องใช้ตัวนี้ ไม่ใช่ IsPGCode เปล่า ๆ เพราะ "ซ้ำ" ของแต่ละคอลัมน์คนละเรื่องกัน
// และผู้ใช้ต้องแก้คนละอย่าง
func isUniqueOn(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

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

// Register สร้าง wbw_user + participant_profile + health_details + consent ใน transaction เดียว
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
		`INSERT INTO wbw_user (username, password_hash, role, student_id, display_name)
		 VALUES ($1, $2, 'participant', $3, $4)
		 RETURNING user_id::text, username, role::text`,
		studentID, passwordHash, studentID,
		nullableName(req.Profile.FirstName, req.Profile.LastName),
	).Scan(&user.UserID, &user.Username, &user.Role)
	if err != nil {
		if IsPGCode(err, "23505") {
			return nil, ErrDuplicate
		}
		// trigger trg_participant_count เพิ่ม wbw_capacity.taken ตอน INSERT นี้ —
		// ถ้าเต็มแล้ว CHECK จะพังตรงนี้ ทั้ง transaction ถูก rollback จึงไม่มีเศษข้อมูลค้าง
		if isCapacityFull(err) {
			return nil, ErrFull
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

// Capacity อ่านโควตาทั้งงาน — หน้าสมัครเรียกก่อนให้กรอกฟอร์ม จะได้ไม่ปล่อยให้กรอกจนจบ
// แล้วค่อยบอกว่าเต็ม · อ่านแถวเดียว ไม่ได้ count ทั้งตาราง
func (r *WBWAuthRepository) Capacity(ctx context.Context) (*model.Capacity, error) {
	var c model.Capacity
	err := r.db.QueryRow(ctx,
		`SELECT max_participants, taken, GREATEST(max_participants - taken, 0)
		 FROM wbw_capacity WHERE id`,
	).Scan(&c.Max, &c.Taken, &c.SeatsLeft)
	if err != nil {
		return nil, err
	}
	c.Full = c.SeatsLeft == 0
	return &c, nil
}

// FindByUsername คืน user + hash + status สำหรับตรวจรหัสผ่านและ gate การอนุมัติ
func (r *WBWAuthRepository) FindByUsername(ctx context.Context, username string) (*model.AuthUser, string, string, error) {
	var u model.AuthUser
	var hash, status string
	err := r.db.QueryRow(ctx,
		`SELECT user_id::text, username, role::text, password_hash, status::text
		 FROM wbw_user WHERE username = $1`, username,
	).Scan(&u.UserID, &u.Username, &u.Role, &hash, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", "", nil // ไม่เจอ user — handler จะตอบ 401 เหมือนรหัสผิด (ไม่บอกใบ้)
	}
	if err != nil {
		return nil, "", "", err
	}
	return &u, hash, status, nil
}

// RegisterStaff สร้างบัญชี staff สถานะ 'pending' + wbw_staff ใน transaction เดียว
// — ล็อกอินไม่ได้จนกว่าแอดมินจะอนุมัติ
//
// email บังคับตั้งแต่ migration 000036: เจ้าหน้าที่ไม่มี student_id จึงคำนวณอีเมล
// ย้อนหลังแบบ participant ไม่ได้ ถ้าไม่ถามตอนสมัครก็ไม่มีวันได้ แล้วคนคนนั้นจะ
// รีเซ็ตรหัสผ่านเองไม่ได้ตลอดไป
func (r *WBWAuthRepository) RegisterStaff(
	ctx context.Context,
	username, passwordHash, email string,
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
		`INSERT INTO wbw_user (username, password_hash, role, status, email)
		 VALUES ($1, $2, 'staff', 'pending', $3)
		 RETURNING user_id::text, username, role::text`,
		username, passwordHash, email,
	).Scan(&u.UserID, &u.Username, &u.Role)
	if err != nil {
		// สองยูนีคบนตารางเดียวกัน — ต้องแยกให้ออก ไม่งั้นคนที่กรอกอีเมลซ้ำจะได้
		// ข้อความว่า "ชื่อผู้ใช้นี้มีอยู่แล้ว" แล้วนั่งเปลี่ยนชื่อผู้ใช้ไปเรื่อย ๆ โดยไม่มีวันผ่าน
		if isUniqueOn(err, "wbw_user_email_key") {
			return nil, ErrDuplicateEmail
		}
		if IsPGCode(err, "23505") {
			return nil, ErrDuplicate
		}
		return nil, err
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO wbw_staff (user_id, school_id, major, staff_role)
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

/* ---------- ลืมรหัสผ่าน (migration 000036) ---------- */

// ErrResetTokenInvalid — ตั๋วรีเซ็ตใช้ไม่ได้: ไม่มีจริง / หมดอายุ / ถูกใช้ไปแล้ว
//
// สามกรณีนี้ตอบเป็นอันเดียวกันโดยตั้งใจ ทั้งฝั่ง error และฝั่ง HTTP — คนที่ถือ
// ตั๋วปลอมไม่ควรรู้ว่าเดาใกล้แล้วหรือแค่ช้าไป
var ErrResetTokenInvalid = errors.New("reset token invalid")

// FindResetTarget หาปลายทางของลิงก์รีเซ็ตจาก username ที่กรอกมา
//
// อีเมลมาจาก COALESCE(email, student_id || '@lamduan.mfu.ac.th') — participant
// ทุกคนจึงมีปลายทางโดยไม่ต้อง backfill (username = student_id ตั้งแต่ตอนสมัคร)
// ส่วนเจ้าหน้าที่รุ่นก่อน migration 000036 ได้ค่าว่าง เพราะไม่มีทั้งสองอย่าง
//
// คืน userID ว่าง = ไม่มีบัญชีชื่อนี้ · ไม่ใช่ error เพราะฝั่งเรียกตอบ 204 เหมือนกัน
// หมดไม่ว่าเจอหรือไม่เจอ (กัน enumeration แบบเดียวกับ FindByUsername ข้างบน)
func (r *WBWAuthRepository) FindResetTarget(ctx context.Context, username string) (userID, email, status string, err error) {
	err = r.db.QueryRow(ctx,
		`SELECT user_id::text,
		        COALESCE(email, student_id || '@lamduan.mfu.ac.th', ''),
		        status::text
		   FROM wbw_user WHERE username = $1`, username,
	).Scan(&userID, &email, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", nil
	}
	if err != nil {
		return "", "", "", err
	}
	return userID, email, status, nil
}

// CountRecentResets นับตั๋วที่ออกให้บัญชีนี้ไปตั้งแต่ since — ฝั่ง service เอาไปตัดสิน
// ว่าเกินโควตาหรือยัง (นับรวมใบที่ใช้ไปแล้ว ดูเหตุผลใน migration 000036)
func (r *WBWAuthRepository) CountRecentResets(ctx context.Context, userID string, since time.Time) (int, error) {
	var n int
	err := r.db.QueryRow(ctx,
		`SELECT count(*)::int FROM wbw_password_reset
		  WHERE user_id = $1 AND created_at > $2`, userID, since,
	).Scan(&n)
	return n, err
}

// InsertPasswordReset บันทึกตั๋วใบใหม่ · tokenHash เป็น sha256 ของ token จริง
// ตัว token ไม่เคยผ่านมาถึงชั้นนี้
func (r *WBWAuthRepository) InsertPasswordReset(ctx context.Context, userID string, tokenHash []byte, expiresAt time.Time) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO wbw_password_reset (token_hash, user_id, expires_at)
		 VALUES ($1, $2, $3)`, tokenHash, userID, expiresAt)
	return err
}

// ConsumePasswordReset แลกตั๋วเป็นรหัสผ่านใหม่ · คืน username ของเจ้าของตั๋ว
//
// ⚠ UPDATE ที่มีเงื่อนไขครบในตัวเอง ไม่ใช่ SELECT แล้วค่อยเช็คใน Go — สองคำสั่งแยกกัน
// เปิดช่องให้ยิงลิงก์เดิมพร้อมกันสองครั้งแล้วผ่านทั้งคู่ (ทั้งคู่อ่านเจอ used_at เป็น
// NULL ก่อนที่ฝั่งไหนจะเขียน) ที่นี่แถวถูกล็อกโดย UPDATE เอง คนที่สองจึงได้ 0 แถว
//
// ใบอื่นที่ยังไม่ถูกใช้ของคนเดียวกันถูกลบทิ้งท้าย transaction: คนที่กด "ลืมรหัสผ่าน"
// ซ้ำสามรอบแล้วใช้ใบล่าสุด ไม่ควรเหลือใบเก่าที่ยังเปิดประตูได้อีกครึ่งชั่วโมง
func (r *WBWAuthRepository) ConsumePasswordReset(ctx context.Context, tokenHash []byte, passwordHash string) (string, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var userID string
	err = tx.QueryRow(ctx,
		`UPDATE wbw_password_reset SET used_at = now()
		  WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
		 RETURNING user_id::text`, tokenHash,
	).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrResetTokenInvalid
	}
	if err != nil {
		return "", err
	}

	var username string
	err = tx.QueryRow(ctx,
		`UPDATE wbw_user SET password_hash = $2 WHERE user_id = $1
		 RETURNING username`, userID, passwordHash,
	).Scan(&username)
	if err != nil {
		return "", err
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM wbw_password_reset
		  WHERE user_id = $1 AND used_at IS NULL`, userID); err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return username, nil
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
