package repository

import (
	"context"
	"errors"

	"su-server/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type WBWAdminRepository struct {
	db *pgxpool.Pool
}

func NewWBWAdminRepository(db *pgxpool.Pool) *WBWAdminRepository {
	return &WBWAdminRepository{db: db}
}

// projection เดียวกับ PARTICIPANT_SELECT ของ Express — alias id/bib/created ต้องตรง
const participantSelect = `
	SELECT u.user_id::text AS id,
	       u.student_id,
	       to_char(u.created_at, 'YYYY-MM-DD') AS created,
	       p.bib_number AS bib,
	       p.first_name, p.last_name, p.contact_phone,
	       p.school_id, s.name AS school_name, p.major, p.sex::text,
	       p.group_id, g.group_number,
	       COALESCE(p.checked_in, FALSE) AS checked_in,
	       h.blood_type::text
	  FROM app_user u
	  LEFT JOIN participant_profile p ON p.user_id = u.user_id
	  LEFT JOIN school            s ON s.school_id = p.school_id
	  LEFT JOIN participant_group g ON g.group_id  = p.group_id
	  LEFT JOIN health_details    h ON h.user_id   = u.user_id`

func scanParticipant(row pgx.Row) (*model.Participant, error) {
	var p model.Participant
	err := row.Scan(&p.ID, &p.StudentID, &p.Created, &p.Bib, &p.FirstName, &p.LastName,
		&p.ContactPhone, &p.SchoolID, &p.SchoolName, &p.Major, &p.Sex,
		&p.GroupID, &p.GroupNumber, &p.CheckedIn, &p.BloodType)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

/* ---------- schools ---------- */

func (r *WBWAdminRepository) ListSchools(ctx context.Context) ([]model.School, error) {
	rows, err := r.db.Query(ctx, `SELECT school_id, name FROM school ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	schools := []model.School{}
	for rows.Next() {
		var s model.School
		if err := rows.Scan(&s.SchoolID, &s.Name); err != nil {
			return nil, err
		}
		schools = append(schools, s)
	}
	return schools, rows.Err()
}

/* ---------- dashboard ---------- */

func (r *WBWAdminRepository) Dashboard(ctx context.Context) (*model.DashboardStats, error) {
	var s model.DashboardStats
	err := r.db.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM app_user WHERE role = 'participant'),
		       (SELECT count(*) FROM check_in),
		       (SELECT count(*) FROM sos_event WHERE resolved = FALSE),
		       (SELECT count(*) FROM participant_group WHERE member_count >= capacity)
	`).Scan(&s.Participants, &s.TotalCheckins, &s.OpenSOS, &s.FullGroups)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

/* ---------- participants ---------- */

func (r *WBWAdminRepository) ListParticipants(ctx context.Context) ([]model.Participant, error) {
	rows, err := r.db.Query(ctx, participantSelect+`
		WHERE u.role = 'participant'
		ORDER BY p.bib_number`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []model.Participant{}
	for rows.Next() {
		p, err := scanParticipant(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *p)
	}
	return list, rows.Err()
}

func (r *WBWAdminRepository) GetParticipant(ctx context.Context, id string) (*model.Participant, error) {
	p, err := scanParticipant(r.db.QueryRow(ctx, participantSelect+` WHERE u.user_id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

func (r *WBWAdminRepository) ParticipantDetail(ctx context.Context, id string) (*model.ParticipantDetail, error) {
	var d model.ParticipantDetail
	err := r.db.QueryRow(ctx, `
		SELECT u.user_id::text, u.student_id, u.created_at::text,
		       p.bib_number, p.first_name, p.last_name, p.sex::text,
		       to_char(p.date_of_birth, 'YYYY-MM-DD'),
		       p.contact_phone, p.school_id, s.name, p.major,
		       p.group_id, g.group_number, p.photo_url,
		       COALESCE(p.checked_in, FALSE),
		       p.emergency_contact_name, p.emergency_contact_phone,
		       h.blood_type::text, h.weight_kg, h.height_cm,
		       c.consent_health_data, c.consent_emergency_treatment, c.waiver_accepted
		  FROM app_user u
		  LEFT JOIN participant_profile p ON p.user_id = u.user_id
		  LEFT JOIN school            s ON s.school_id = p.school_id
		  LEFT JOIN participant_group g ON g.group_id  = p.group_id
		  LEFT JOIN health_details    h ON h.user_id   = u.user_id
		  LEFT JOIN consent           c ON c.user_id   = u.user_id
		 WHERE u.user_id = $1 AND u.role = 'participant'`, id,
	).Scan(&d.ID, &d.StudentID, &d.Created, &d.Bib, &d.FirstName, &d.LastName, &d.Sex,
		&d.DateOfBirth, &d.ContactPhone, &d.SchoolID, &d.SchoolName, &d.Major,
		&d.GroupID, &d.GroupNumber, &d.PhotoURL, &d.CheckedIn,
		&d.EmergencyContactName, &d.EmergencyContactPhone,
		&d.BloodType, &d.WeightKg, &d.HeightCm,
		&d.ConsentHealthData, &d.ConsentEmergencyTreatment, &d.WaiverAccepted)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// UpdateParticipant ใช้ COALESCE — field ที่ไม่ส่งมา (nil) คงค่าเดิม ลบค่าไม่ได้ (ตามของเดิม)
func (r *WBWAdminRepository) UpdateParticipant(ctx context.Context, id string, patch model.ParticipantPatch) (*model.Participant, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var exists string
	err = tx.QueryRow(ctx, `SELECT user_id::text FROM app_user WHERE user_id = $1 AND role = 'participant'`, id).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	// student_id เปลี่ยนทั้ง username และ student_id พร้อมกัน
	if patch.StudentID != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE app_user SET username = $2, student_id = $2 WHERE user_id = $1`,
			id, *patch.StudentID); err != nil {
			return nil, err
		}
	}

	var sex *string
	if patch.Sex != nil && isValidSex(*patch.Sex) {
		sex = patch.Sex
	}

	if _, err := tx.Exec(ctx, `
		UPDATE participant_profile SET
		  first_name              = COALESCE($2, first_name),
		  last_name               = COALESCE($3, last_name),
		  contact_phone           = COALESCE($4, contact_phone),
		  major                   = COALESCE($5, major),
		  school_id               = COALESCE($6, school_id),
		  group_id                = COALESCE($7, group_id),
		  sex                     = COALESCE($8::sex_type, sex),
		  date_of_birth           = COALESCE($9::date, date_of_birth),
		  emergency_contact_name  = COALESCE($10, emergency_contact_name),
		  emergency_contact_phone = COALESCE($11, emergency_contact_phone),
		  checked_in              = COALESCE($12, checked_in),
		  updated_at              = now()
		WHERE user_id = $1`,
		id, patch.FirstName, patch.LastName, patch.ContactPhone, patch.Major,
		patch.SchoolID, patch.GroupID, sex, patch.DateOfBirth,
		patch.EmergencyContactName, patch.EmergencyContactPhone, patch.CheckedIn,
	); err != nil {
		return nil, err
	}

	// health_details แตะเฉพาะเมื่อ body มี key ด้านสุขภาพ (ตาม Express)
	if patch.HasHealthFields() {
		var blood *string
		if patch.BloodType != nil && isValidBlood(*patch.BloodType) {
			blood = patch.BloodType
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO health_details (user_id, blood_type, weight_kg, height_cm)
			VALUES ($1, $2::blood_type, $3, $4)
			ON CONFLICT (user_id) DO UPDATE SET
			  blood_type = COALESCE(EXCLUDED.blood_type, health_details.blood_type),
			  weight_kg  = COALESCE(EXCLUDED.weight_kg,  health_details.weight_kg),
			  height_cm  = COALESCE(EXCLUDED.height_cm,  health_details.height_cm),
			  updated_at = now()`,
			id, blood, patch.WeightKg, patch.HeightCm,
		); err != nil {
			return nil, err
		}
	}

	p, err := scanParticipant(tx.QueryRow(ctx, participantSelect+` WHERE u.user_id = $1`, id))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

func (r *WBWAdminRepository) ResetParticipantPassword(ctx context.Context, id, hash string) (string, error) {
	var studentID *string
	err := r.db.QueryRow(ctx,
		`UPDATE app_user SET password_hash = $2
		 WHERE user_id = $1 AND role = 'participant'
		 RETURNING student_id`, id, hash).Scan(&studentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if studentID == nil {
		return "", nil
	}
	return *studentID, nil
}

// DeleteParticipant ลบข้อมูลที่อ้างถึง user ก่อน แล้วค่อยลบ app_user (ที่เหลือ cascade เอง)
func (r *WBWAdminRepository) DeleteParticipant(ctx context.Context, id string) (string, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var studentID *string
	var groupID *int
	err = tx.QueryRow(ctx, `
		SELECT u.student_id, p.group_id
		  FROM app_user u
		  LEFT JOIN participant_profile p ON p.user_id = u.user_id
		 WHERE u.user_id = $1 AND u.role = 'participant'`, id).Scan(&studentID, &groupID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}

	stmts := []string{
		`DELETE FROM check_in      WHERE participant_id = $1 OR staff_id = $1`,
		`DELETE FROM track_point   WHERE participant_id = $1`,
		`DELETE FROM sos_event     WHERE participant_id = $1`,
		`DELETE FROM group_message WHERE sender_id = $1`,
		`UPDATE sos_event    SET resolved_by = NULL WHERE resolved_by = $1`,
		`UPDATE notification SET created_by  = NULL WHERE created_by  = $1`,
	}
	for _, q := range stmts {
		if _, err := tx.Exec(ctx, q, id); err != nil {
			return "", err
		}
	}

	// trigger sync_group_count ทำงานเฉพาะ INSERT/UPDATE ไม่ทำตอน DELETE
	// ของเดิมจึงเกิด member_count ค้าง — ที่นี่ลดเองให้ถูกต้อง
	if groupID != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE participant_group SET member_count = GREATEST(member_count - 1, 0) WHERE group_id = $1`,
			*groupID); err != nil {
			return "", err
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM app_user WHERE user_id = $1`, id); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	if studentID == nil {
		return "", nil
	}
	return *studentID, nil
}

/* ---------- groups ---------- */

func (r *WBWAdminRepository) ListGroups(ctx context.Context) ([]model.Group, error) {
	rows, err := r.db.Query(ctx, `
		SELECT group_id, group_number, capacity, member_count,
		       capacity - member_count AS seats_left
		  FROM participant_group ORDER BY group_number`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	groups := []model.Group{}
	for rows.Next() {
		var g model.Group
		if err := rows.Scan(&g.GroupID, &g.GroupNumber, &g.Capacity, &g.MemberCount, &g.SeatsLeft); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

/* ---------- logs ---------- */

func (r *WBWAdminRepository) ListLogs(ctx context.Context) ([]model.AdminLog, error) {
	rows, err := r.db.Query(ctx, `
		SELECT log_id, actor_name, action, detail, created_at::text
		  FROM admin_log ORDER BY log_id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	logs := []model.AdminLog{}
	for rows.Next() {
		var l model.AdminLog
		if err := rows.Scan(&l.ID, &l.ActorName, &l.Action, &l.Detail, &l.CreatedAt); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

// LogAction บันทึก audit — กลืน error เองเหมือนของเดิม (audit ล้มไม่ควรทำ request พัง)
func (r *WBWAdminRepository) LogAction(ctx context.Context, actorID, actorName, action, detail string) {
	_, _ = r.db.Exec(ctx,
		`INSERT INTO admin_log (actor_id, actor_name, action, detail) VALUES ($1, $2, $3, $4)`,
		actorID, actorName, action, detail)
}
