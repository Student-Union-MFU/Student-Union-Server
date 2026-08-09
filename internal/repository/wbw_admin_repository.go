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
	       h.blood_type::text,
	       -- role='participant' ที่ยังไม่มีแถว participant_profile ทำให้ p.leave_quota เป็น NULL
	       -- ผ่าน LEFT JOIN นี้ — scan ตรงเข้า int (ไม่ใช่ pointer) จะ error ทั้งคำสั่ง แล้วพังทั้งหน้ารายชื่อ
	       -- (เหตุผลเดียวกับ COALESCE(p.checked_in, FALSE) บรรทัดบน)
	       COALESCE(p.leave_quota, 0) AS leave_quota
	  FROM wbw_user u
	  LEFT JOIN participant_profile p ON p.user_id = u.user_id
	  LEFT JOIN school            s ON s.school_id = p.school_id
	  LEFT JOIN participant_group g ON g.group_id  = p.group_id
	  LEFT JOIN health_details    h ON h.user_id   = u.user_id`

func scanParticipant(row pgx.Row) (*model.Participant, error) {
	var p model.Participant
	err := row.Scan(&p.ID, &p.StudentID, &p.Created, &p.Bib, &p.FirstName, &p.LastName,
		&p.ContactPhone, &p.SchoolID, &p.SchoolName, &p.Major, &p.Sex,
		&p.GroupID, &p.GroupNumber, &p.CheckedIn, &p.BloodType, &p.LeaveQuota)
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
		SELECT (SELECT count(*) FROM wbw_user WHERE role = 'participant'),
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
		       c.consent_health_data, c.consent_emergency_treatment, c.waiver_accepted,
		       -- key ที่แอป iOS ต้องการ (contract §2) · user_id/bib_number ซ้ำค่ากับ
		       -- id/bib ที่ web-next ใช้อยู่ ตั้งใจส่งทั้งสองชื่อ ไม่เปลี่ยนของเดิม
		       u.user_id::text, u.username, u.role,
		       p.bib_number, p.qr_token, p.year,
		       h.food_allergies, h.chronic_disease, h.medications,
		       -- เหตุผลเดียวกับ participantSelect ด้านบน — LEFT JOIN นี้ให้ NULL ได้เมื่อไม่มี
		       -- participant_profile และ d.LeaveQuota เป็น int ธรรมดา ไม่ใช่ pointer
		       COALESCE(p.leave_quota, 0)
		  FROM wbw_user u
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
		&d.ConsentHealthData, &d.ConsentEmergencyTreatment, &d.WaiverAccepted,
		&d.UserID, &d.Username, &d.Role,
		&d.BibNumber, &d.QRToken, &d.Year,
		&d.FoodAllergies, &d.ChronicDisease, &d.Medications,
		&d.LeaveQuota)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	// 10 แถวล่าสุดพอสำหรับคำถามที่หน้านี้ตอบ ("คนนี้ออกกี่ครั้ง ตอนไหน ใครปรับให้") — index
	// idx_gml_user (user_id, log_id DESC) ทำให้เป็นการอ่าน 10 แถวแรกตรง ๆ ไม่ใช่การเรียงทั้งตาราง
	rows, err := r.db.Query(ctx, `
		SELECT l.action, l.group_id, g.group_number, l.quota_after, a.display_name, l.created_at::text
		  FROM group_membership_log l
		  LEFT JOIN participant_group g ON g.group_id = l.group_id
		  LEFT JOIN wbw_user          a ON a.user_id  = l.actor_id
		 WHERE l.user_id = $1
		 ORDER BY l.log_id DESC
		 LIMIT 10`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	d.MembershipLog = []model.MembershipLogEntry{}
	for rows.Next() {
		var e model.MembershipLogEntry
		if err := rows.Scan(&e.Action, &e.GroupID, &e.GroupNumber, &e.QuotaAfter,
			&e.ActorName, &e.CreatedAt); err != nil {
			return nil, err
		}
		d.MembershipLog = append(d.MembershipLog, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &d, nil
}

// UpdateParticipant ใช้ COALESCE — field ที่ไม่ส่งมา (nil) คงค่าเดิม ลบค่าไม่ได้ (ตามของเดิม)
// actorID มาจาก admin ที่เรียก PATCH — ใช้บันทึกแถว quota_adjust ว่าใครเป็นคนปรับให้
func (r *WBWAdminRepository) UpdateParticipant(ctx context.Context, id string, patch model.ParticipantPatch, actorID string) (*model.Participant, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var exists string
	err = tx.QueryRow(ctx, `SELECT user_id::text FROM wbw_user WHERE user_id = $1 AND role = 'participant'`, id).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	// student_id เปลี่ยนทั้ง username และ student_id พร้อมกัน
	if patch.StudentID != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE wbw_user SET username = $2, student_id = $2 WHERE user_id = $1`,
			id, *patch.StudentID); err != nil {
			return nil, err
		}
	}

	var sex *string
	if patch.Sex != nil && isValidSex(*patch.Sex) {
		sex = patch.Sex
	}

	// ต้องรู้ group_id "ก่อน" UPDATE เมื่อแอดมินอาจย้ายกลุ่ม — เทียบกับค่าใหม่ทีหลังเพื่อรู้ว่า
	// เปลี่ยนจริงไหม (ส่ง group_id เดิมซ้ำมาไม่ควรถูกนับเป็นการย้าย ไม่ควรเคลียร์แชทหรือเขียน log)
	//
	// FOR UPDATE ล็อกแถว participant_profile (แถว "user") ของคนนี้ก่อน — ปลอดภัยกับลำดับล็อกที่
	// Join/Leave วางไว้ (user ก่อน group เสมอ กัน deadlock) เพราะยังไม่มีจุดไหนแตะ participant_group
	// เลยในฟังก์ชันนี้จนกว่า UPDATE ด้านล่างจะทำให้ trigger trg_group_count ยิงทีหลัง จึงยังเป็น
	// user-ก่อน-group เหมือนเดิม ไม่ได้สลับลำดับ — กัน race ที่สอง PATCH ย้ายกลุ่มคนเดียวกันพร้อมกัน
	// อ่าน prevGroupID เป็นค่าเก่าตรงกันทั้งคู่ (stale read) แล้วเขียน log/เคลียร์แชทซ้ำซ้อนผิดจังหวะ
	var prevGroupID *int
	if patch.GroupID != nil {
		err := tx.QueryRow(ctx,
			`SELECT group_id FROM participant_profile WHERE user_id = $1 FOR UPDATE`, id,
		).Scan(&prevGroupID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		// ไม่มีแถว participant_profile เลย — เกิดกับ role='participant' ที่ยังไม่เคยถูกเติมโปรไฟล์
		// (ประชากรกลุ่มเดียวกับที่ COALESCE(p.leave_quota, 0) ใน participantSelect กันไว้อยู่แล้ว)
		// exists check ต้นฟังก์ชันเช็คแค่ wbw_user เท่านั้น ไม่เคยบังคับว่าต้องมี participant_profile
		// ด้วย ก่อนหน้านี้ PATCH เคสนี้จึงตอบ 200 มาตลอด (UPDATE/INSERT...SELECT ด้านล่างที่อ้าง
		// participant_profile ก็แค่โดน 0 แถวเงียบ ๆ ไม่ error) ถ้า return ErrNotFound ตรงนี้จะกลายเป็น
		// 404 ใหม่ที่ไม่เคยมีมาก่อนสำหรับ endpoint นี้ จึงเลือกตีความว่า "ไม่มีกลุ่มเดิม" แทน
		// (prevGroupID เป็น nil ตามค่าเริ่มต้นของตัวแปรอยู่แล้ว) แล้วปล่อยให้ทำงานต่อ ไม่ตัดจบ transaction
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
		  leave_quota             = COALESCE($13, leave_quota),
		  updated_at              = now()
		WHERE user_id = $1`,
		id, patch.FirstName, patch.LastName, patch.ContactPhone, patch.Major,
		patch.SchoolID, patch.GroupID, sex, patch.DateOfBirth,
		patch.EmergencyContactName, patch.EmergencyContactPhone, patch.CheckedIn,
		patch.LeaveQuota,
	); err != nil {
		return nil, err
	}

	// แอดมินย้ายกลุ่มให้ตรง ๆ ไม่ผ่าน Join — ก่อนหน้านี้ผู้เข้าร่วมย้ายกลุ่มได้ทางเดียวคือ Join
	// เอง ซึ่งเคลียร์ group_chat_state ให้เสมอ (จุดตัดประวัติแชทตั้งใหม่ทุกครั้งที่เข้ากลุ่ม) เส้นทาง
	// PATCH นี้ข้าม Join ไปเลย ถ้าไม่เคลียร์ตรงนี้ด้วย cursor เก่าจะยังชี้ไปกลุ่มเดิม แล้วคนที่ย้ายมาใหม่
	// จะอ่านแชทของกลุ่มปลายทางย้อนหลังไปถึงก่อนที่เขาจะย้ายเข้ามาได้ทั้งหมด
	//
	// เช็คว่า "เปลี่ยนจริง" ก่อนเสมอ — ส่ง group_id เดิมซ้ำมาไม่ควรไปรีเซ็ตจุดตัดแชทของคนที่ไม่ได้ย้ายไปไหน
	//
	// action ใช้ 'join' (ไม่ใช่ 'leave' เพราะเขายังมีกลุ่มอยู่ ไม่ได้ออกไปเฉย ๆ ไม่ใช่ 'quota_adjust'
	// เพราะการย้ายนี้ไม่แตะโควตาเลย) — เข้ากลุ่มใหม่คือ 'join' เหมือนตอนผู้ใช้ Join เอง และ group_id
	// ที่บันทึกคือกลุ่มปลายทาง (สมมาตรกับที่ Join บันทึกกลุ่มที่เพิ่งเข้า ไม่ใช่กลุ่มที่จากมา)
	// quota_after อ่านค่าปัจจุบันสด ๆ เพราะ PATCH เดียวกันอาจส่ง leave_quota มาด้วย
	if patch.GroupID != nil && (prevGroupID == nil || *prevGroupID != *patch.GroupID) {
		if _, err := tx.Exec(ctx,
			`DELETE FROM group_chat_state WHERE user_id = $1`, id); err != nil {
			return nil, err
		}
		// DELETE อย่างเดียวไม่พอ — SinceID (wbw_chat_repository.go) คืน COALESCE(MAX(since_id), 0)
		// เมื่อไม่มีแถวเลย นั่นคือจุดตัด = 0 แปลว่าเห็นประวัติแชท "ทั้งหมด" ของกลุ่มปลายทางย้อนไปถึง
		// ก่อนที่เขาจะย้ายเข้ามา ตรงกับ chat-backlog leak ที่ finding นี้พูดถึงเป๊ะ ต้อง INSERT
		// จุดตัดใหม่ทันทีในทรานแซกชันเดียวกัน เหมือนที่ Join ทำ (wbw_group_repository.go) —
		// since_id/last_read_id ตั้งเป็นข้อความล่าสุดของกลุ่มปลายทาง ณ ตอนนี้ ไม่ใช่ 0 ผู้ใช้จะเห็น
		// แชทเริ่มจากตอนที่ถูกย้ายเข้ามาเท่านั้น (ใช้ ON CONFLICT DO UPDATE เผื่อกรณีย้ายกลับเข้ากลุ่ม
		// เดิมที่เคยมีจุดตัดจริงอยู่ก่อน — DELETE ด้านบนลบไปแล้ว จุดตัดใหม่ตรงนี้เขียนทับให้ถูกอยู่ดี
		// ไม่ใช่ปล่อยว่างจนกลายเป็น 0 ที่กว้างขึ้นกว่าเดิม)
		if _, err := tx.Exec(ctx, `
			INSERT INTO group_chat_state (user_id, group_id, since_id, last_read_id, read_at)
			SELECT $1, $2, COALESCE(MAX(id), 0), COALESCE(MAX(id), 0), now()
			  FROM group_message WHERE group_id = $2
			ON CONFLICT (user_id, group_id) DO UPDATE
			   SET since_id = EXCLUDED.since_id,
			       last_read_id = EXCLUDED.since_id,
			       read_at = now()`,
			id, *patch.GroupID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO group_membership_log (user_id, group_id, action, quota_after, actor_id)
			SELECT $1, group_id, 'join', leave_quota, $2
			  FROM participant_profile WHERE user_id = $1`,
			id, actorID); err != nil {
			return nil, err
		}
	}

	// log เฉพาะตอนที่ค่านี้ถูกส่งมาจริง — PATCH ตัวอื่น (แก้ชื่อ, เช็คอิน) ไม่ควรสร้างแถวประวัติโควตาขึ้นมา
	// อ่าน group_id สดในทรานแซกชันเดียวกัน เพื่อให้ไทม์ไลน์บอกได้ว่าตอนถูกปรับ เขาอยู่กลุ่มไหน
	if patch.LeaveQuota != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO group_membership_log (user_id, group_id, action, quota_after, actor_id)
			SELECT $1, group_id, 'quota_adjust', $2, $3
			  FROM participant_profile WHERE user_id = $1`,
			id, *patch.LeaveQuota, actorID); err != nil {
			return nil, err
		}
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
		`UPDATE wbw_user SET password_hash = $2
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

// DeleteParticipant ลบข้อมูลที่อ้างถึง user ก่อน แล้วค่อยลบ wbw_user (ที่เหลือ cascade เอง)
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
		  FROM wbw_user u
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

	if _, err := tx.Exec(ctx, `DELETE FROM wbw_user WHERE user_id = $1`, id); err != nil {
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

/* ---------- โปรไฟล์ตัวเอง ---------- */

// UpdateOwnPhoto — ผู้เข้าร่วมเปลี่ยนรูปโปรไฟล์ตัวเอง (PATCH /wbw/me)
//
// จำกัดไว้แค่ photo_url โดยตั้งใจ · ฟิลด์อื่น (กลุ่ม, BIB, สำนักวิชา) เป็นของ admin
// เปิดให้ PATCH ทั้ง profile จากฝั่งผู้ใช้ = เขาย้ายกลุ่มตัวเองข้ามการเช็คที่นั่งได้
func (r *WBWAdminRepository) UpdateOwnPhoto(ctx context.Context, userID string, photoURL *string) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE participant_profile SET photo_url = $1, updated_at = now() WHERE user_id = $2`,
		photoURL, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// LogAction บันทึก audit — กลืน error เองเหมือนของเดิม (audit ล้มไม่ควรทำ request พัง)
func (r *WBWAdminRepository) LogAction(ctx context.Context, actorID, actorName, action, detail string) {
	_, _ = r.db.Exec(ctx,
		`INSERT INTO admin_log (actor_id, actor_name, action, detail) VALUES ($1, $2, $3, $4)`,
		actorID, actorName, action, detail)
}
