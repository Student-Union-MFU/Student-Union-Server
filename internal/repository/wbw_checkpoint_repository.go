package repository

import (
	"context"
	"errors"
	"time"

	"su-server/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type WBWCheckpointRepository struct {
	db *pgxpool.Pool
}

func NewWBWCheckpointRepository(db *pgxpool.Pool) *WBWCheckpointRepository {
	return &WBWCheckpointRepository{db: db}
}

/* ---------- checkpoints ---------- */

// List — staff เป็น {id, username, display_name} (ต่างจาก bases-overview ที่ใช้ {id, name})
func (r *WBWCheckpointRepository) List(ctx context.Context) ([]model.Checkpoint, error) {
	rows, err := r.db.Query(ctx, `
		SELECT c.checkpoint_id, c.name, c.name_en, c.type::text, c.sequence,
		       COALESCE(json_agg(
		         json_build_object('id', u.user_id::text, 'username', u.username, 'display_name', u.display_name)
		         ORDER BY u.username
		       ) FILTER (WHERE u.user_id IS NOT NULL), '[]') AS staff,
		       -- subquery ไม่ใช่ JOIN โดยตั้งใจ: LEFT JOIN check_in เข้ามาตรงนี้จะคูณ
		       -- จำนวนแถวกับ checkpoint_staff ทำให้ยอดเช็คอินของฐานที่มีเจ้าหน้าที่
		       -- สองคนกลายเป็นสองเท่า ซึ่งเป็นบั๊กที่อ่านเหมือนข้อมูลจริงจนกว่าจะมีคน
		       -- เอาไปเทียบกับหน้าอื่น
		       (SELECT count(*)::int FROM check_in ci WHERE ci.checkpoint_id = c.checkpoint_id),
		       (SELECT count(*)::int FROM checkin_feedback f WHERE f.checkpoint_id = c.checkpoint_id),
		       (SELECT avg(f.rating)::float8 FROM checkin_feedback f WHERE f.checkpoint_id = c.checkpoint_id),
		       (SELECT count(*)::int FROM sos_event e WHERE e.checkpoint_id = c.checkpoint_id)
		  FROM checkpoint c
		  LEFT JOIN checkpoint_staff cs ON cs.checkpoint_id = c.checkpoint_id
		  LEFT JOIN wbw_user         u  ON u.user_id = cs.user_id
		 GROUP BY c.checkpoint_id
		 ORDER BY c.sequence NULLS LAST, c.checkpoint_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []model.Checkpoint{}
	for rows.Next() {
		var c model.Checkpoint
		if err := rows.Scan(&c.ID, &c.Name, &c.NameEn, &c.Type, &c.Sequence, &c.Staff,
			&c.CheckinCount, &c.FeedbackCount, &c.AvgRating, &c.SOSCount); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// ListForParticipant — ฐานทุกใบสำหรับผู้เข้าร่วม
//
// คืนทั้งฐานกิจกรรมและจุดบริการ ไม่กรองด้วย requires_checkin — แอปแยกเองจาก `type` กับ
// `sequence` และการคืนครบทำให้ไม่ต้องแก้ endpoint อีกรอบตอนอยากโชว์จุดห้องน้ำบนแผนที่
//
// เรียงเหมือน List ของแอดมิน (sequence ก่อน แล้วค่อย id) เพื่อให้ทั้งสองฝั่งเห็นลำดับเดียวกัน —
// จุดบริการไม่มี sequence จึงตกไปท้ายด้วย NULLS LAST
func (r *WBWCheckpointRepository) ListForParticipant(ctx context.Context) ([]model.ParticipantCheckpoint, error) {
	// จำนวนคนที่เช็คอินต่อฐาน นับสดจาก check_in ทุกครั้งที่ถาม ไม่ได้เก็บเป็นตัวนับไว้
	//
	// ตัวนับที่เก็บไว้เป็นสำเนาที่เพี้ยนได้ — เช็คอินที่เพิ่มแถวแต่ลืมบวก, แถวที่แอดมินลบทิ้ง,
	// retry ที่บวกสองครั้ง — แล้วไม่มีใครรู้ว่าเลขเริ่มผิดตั้งแต่เมื่อไร ส่วน check_in คือ
	// ตัวบันทึกจริง การนับมันจึงถูกเสมอตามนิยาม ไม่ใช่ถูกถ้าไม่มีอะไรพลาด
	//
	// ถูกด้วยว่าเป็น "จำนวนคน" ไม่ใช่ "จำนวนครั้ง": check_in มี UNIQUE (participant_id,
	// checkpoint_id) อยู่แล้ว คนหนึ่งจึงมีได้แถวเดียวต่อฐาน count(*) กับ
	// count(DISTINCT participant_id) ให้ผลเดียวกัน และตัวแรกใช้ index ตรง ๆ
	//
	// ราคา: idx_checkin_checkpoint คลุมคอลัมน์ที่กรอง งานจึงเป็นการนับ index ต่อฐาน ไม่ใช่
	// สแกนทั้งตาราง ที่ ~2,000 คน × ~9 ฐาน = ~18,000 แถว ถือว่าถูกกว่าการดูแลตัวนับให้ตรง
	rows, err := r.db.Query(ctx, `
		SELECT c.checkpoint_id, c.sequence, c.name, c.name_en, c.activity_name, c.activity_name_en,
		       c.type::text, c.requires_checkin, c.lat, c.lng,
		       (SELECT count(*)::int FROM check_in ci WHERE ci.checkpoint_id = c.checkpoint_id) AS checkin_count
		  FROM checkpoint c
		 ORDER BY c.sequence NULLS LAST, c.checkpoint_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// เริ่มจาก slice ว่าง ไม่ใช่ nil — nil เข้ารหัสเป็น `null` ไม่ใช่ `[]` แล้วตัวถอดรหัสฝั่งแอป
	// ที่รอ array จะพังทั้งก้อน (แพทเทิร์นเดียวกับ List ข้างบน)
	list := []model.ParticipantCheckpoint{}
	for rows.Next() {
		var c model.ParticipantCheckpoint
		if err := rows.Scan(&c.ID, &c.Sequence, &c.Name, &c.NameEn,
			&c.ActivityName, &c.ActivityNameEn, &c.Type, &c.RequiresCheckin,
			&c.Lat, &c.Lng, &c.CheckinCount); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// BasesOverview — เฉพาะฐานที่ต้องเช็คอิน พร้อมจำนวนคนที่เช็คอินแล้ว
func (r *WBWCheckpointRepository) BasesOverview(ctx context.Context) ([]model.BaseOverview, error) {
	rows, err := r.db.Query(ctx, `
		SELECT c.checkpoint_id, c.name, c.name_en, c.sequence, c.activity_name, c.activity_name_en,
		       (SELECT count(*)::int FROM check_in ci WHERE ci.checkpoint_id = c.checkpoint_id) AS checkin_count,
		       COALESCE(json_agg(
		         json_build_object('id', u.user_id::text, 'name', COALESCE(u.display_name, u.username))
		         ORDER BY u.username
		       ) FILTER (WHERE u.user_id IS NOT NULL), '[]') AS staff
		  FROM checkpoint c
		  LEFT JOIN checkpoint_staff cs ON cs.checkpoint_id = c.checkpoint_id
		  LEFT JOIN wbw_user         u  ON u.user_id = cs.user_id
		 WHERE c.requires_checkin = TRUE
		 GROUP BY c.checkpoint_id
		 ORDER BY c.sequence NULLS LAST, c.checkpoint_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []model.BaseOverview{}
	for rows.Next() {
		var b model.BaseOverview
		if err := rows.Scan(&b.ID, &b.Name, &b.NameEn, &b.Sequence, &b.ActivityName, &b.ActivityNameEn, &b.CheckinCount, &b.Staff); err != nil {
			return nil, err
		}
		list = append(list, b)
	}
	return list, rows.Err()
}

func (r *WBWCheckpointRepository) Create(ctx context.Context, name string, nameEn *string, cpType string, sequence *int) (*model.Checkpoint, error) {
	var c model.Checkpoint
	err := r.db.QueryRow(ctx,
		`INSERT INTO checkpoint (name, name_en, type, sequence) VALUES ($1, $2, $3::checkpoint_type, $4)
		 RETURNING checkpoint_id, name, name_en, type::text, sequence`,
		name, nameEn, cpType, sequence).Scan(&c.ID, &c.Name, &c.NameEn, &c.Type, &c.Sequence)
	if err != nil {
		return nil, err
	}
	c.Staff = []model.StaffRef{}
	return &c, nil
}

// Update — name/type ใช้ COALESCE แต่ sequence/name_en เซ็ตตรงๆ (ไม่ส่งมา = NULL)
// name_en เซ็ตตรง ๆ เพื่อให้ลบคำแปลออกได้ (ส่ง null = ล้าง) ต่างจาก name ที่ห้ามว่าง
func (r *WBWCheckpointRepository) Update(ctx context.Context, id int, name, nameEn, cpType *string, sequence *int) (*model.CheckpointPatched, error) {
	var c model.CheckpointPatched
	err := r.db.QueryRow(ctx, `
		UPDATE checkpoint SET
		  name     = COALESCE($2, name),
		  name_en  = $3,
		  type     = COALESCE($4::checkpoint_type, type),
		  sequence = $5
		WHERE checkpoint_id = $1
		RETURNING checkpoint_id, name, name_en, type::text, sequence`,
		id, name, nameEn, cpType, sequence).Scan(&c.ID, &c.Name, &c.NameEn, &c.Type, &c.Sequence)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *WBWCheckpointRepository) Delete(ctx context.Context, id int) (string, error) {
	var name string
	err := r.db.QueryRow(ctx, `DELETE FROM checkpoint WHERE checkpoint_id = $1 RETURNING name`, id).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return name, err
}

// AssignStaff — idempotent (ON CONFLICT DO NOTHING) และรับเฉพาะ staff/admin
// AssignGroupStaff — มอบหมายเจ้าหน้าที่ให้ประจำกลุ่มหนึ่ง
//
// รูปแบบเดียวกับ AssignStaff ของฐานทุกอย่าง รวมถึงการเช็คว่า user เป็น staff/admin จริง
// ก่อน INSERT — ไม่งั้นจะมอบหมายผู้เข้าร่วมให้ดูแลกลุ่มตัวเองได้ ซึ่ง FK ไม่ได้ห้ามไว้
func (r *WBWCheckpointRepository) AssignGroupStaff(ctx context.Context, groupID int, userID string) (string, error) {
	var username string
	err := r.db.QueryRow(ctx,
		`SELECT username FROM wbw_user WHERE user_id = $1 AND role IN ('staff','admin')`, userID).Scan(&username)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if _, err := r.db.Exec(ctx,
		`INSERT INTO group_staff (group_id, user_id) VALUES ($1, $2)
		 ON CONFLICT DO NOTHING`, groupID, userID); err != nil {
		return "", err
	}
	return username, nil
}

// RemoveGroupStaff — ถอนการมอบหมาย · false = ไม่มีแถวนั้นอยู่แล้ว
func (r *WBWCheckpointRepository) RemoveGroupStaff(ctx context.Context, groupID int, userID string) (bool, error) {
	tag, err := r.db.Exec(ctx,
		`DELETE FROM group_staff WHERE group_id = $1 AND user_id = $2`, groupID, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// GroupStaff — ใครประจำกลุ่มไหนบ้าง สำหรับหน้าแอดมิน
func (r *WBWCheckpointRepository) GroupStaff(ctx context.Context, groupID int) ([]model.StaffRef, error) {
	rows, err := r.db.Query(ctx, `
		SELECT u.user_id::text, u.username, u.display_name
		  FROM group_staff gs JOIN wbw_user u ON u.user_id = gs.user_id
		 WHERE gs.group_id = $1
		 ORDER BY u.username`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []model.StaffRef{}
	for rows.Next() {
		var ref model.StaffRef
		if err := rows.Scan(&ref.ID, &ref.Username, &ref.DisplayName); err != nil {
			return nil, err
		}
		list = append(list, ref)
	}
	return list, rows.Err()
}

func (r *WBWCheckpointRepository) AssignStaff(ctx context.Context, checkpointID int, userID string) (string, error) {
	var username string
	err := r.db.QueryRow(ctx,
		`SELECT username FROM wbw_user WHERE user_id = $1 AND role IN ('staff','admin')`, userID).Scan(&username)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}

	if _, err := r.db.Exec(ctx,
		`INSERT INTO checkpoint_staff (checkpoint_id, user_id) VALUES ($1, $2)
		 ON CONFLICT DO NOTHING`, checkpointID, userID); err != nil {
		return "", err
	}
	return username, nil
}

// RemoveStaff คืน true เมื่อมีแถวถูกลบจริง (ใช้ตัดสินใจว่าจะบันทึก audit ไหม)
func (r *WBWCheckpointRepository) RemoveStaff(ctx context.Context, checkpointID int, userID string) (bool, error) {
	tag, err := r.db.Exec(ctx,
		`DELETE FROM checkpoint_staff WHERE checkpoint_id = $1 AND user_id = $2`, checkpointID, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

/* ---------- staff / admin accounts ---------- */

func (r *WBWCheckpointRepository) ListUsers(ctx context.Context) ([]model.AdminUser, error) {
	// เฉพาะบัญชีที่อนุมัติแล้ว — ที่ยัง pending อยู่ในแท็บ "คำขอเจ้าหน้าที่" แทน
	rows, err := r.db.Query(ctx, `
		SELECT user_id::text, username, role::text, display_name, created_at::text
		  FROM wbw_user WHERE role IN ('staff','admin') AND status = 'approved'
		 ORDER BY role, username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := []model.AdminUser{}
	for rows.Next() {
		var u model.AdminUser
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.DisplayName, &u.Created); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (r *WBWCheckpointRepository) CreateUser(ctx context.Context, username, hash, role string, displayName *string) (*model.AdminUser, error) {
	var u model.AdminUser
	err := r.db.QueryRow(ctx,
		`INSERT INTO wbw_user (username, password_hash, role, display_name)
		 VALUES ($1, $2, $3::user_role, $4)
		 RETURNING user_id::text, username, role::text, display_name, created_at::text`,
		username, hash, role, displayName,
	).Scan(&u.ID, &u.Username, &u.Role, &u.DisplayName, &u.Created)
	if err != nil {
		if IsPGCode(err, "23505") {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	return &u, nil
}

func (r *WBWCheckpointRepository) UpdateUser(ctx context.Context, id string, displayName, role *string) (*model.AdminUser, error) {
	var u model.AdminUser
	err := r.db.QueryRow(ctx, `
		UPDATE wbw_user SET
		  display_name = COALESCE($2, display_name),
		  role         = COALESCE($3::user_role, role)
		WHERE user_id = $1 AND role IN ('staff','admin')
		RETURNING user_id::text, username, role::text, display_name, created_at::text`,
		id, displayName, role,
	).Scan(&u.ID, &u.Username, &u.Role, &u.DisplayName, &u.Created)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *WBWCheckpointRepository) SetUserPassword(ctx context.Context, id, hash string) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE wbw_user SET password_hash = $2 WHERE user_id = $1 AND role IN ('staff','admin')`, id, hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *WBWCheckpointRepository) DeleteUser(ctx context.Context, id string) (string, error) {
	var username string
	err := r.db.QueryRow(ctx,
		`DELETE FROM wbw_user WHERE user_id = $1 AND role IN ('staff','admin') RETURNING username`, id).Scan(&username)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return username, err
}

/* ---------- staff requests (สมัครเอง รออนุมัติ) ---------- */

// ListStaffRequests คืนเฉพาะบัญชี staff ที่สถานะ 'pending'
func (r *WBWCheckpointRepository) ListStaffRequests(ctx context.Context) ([]model.StaffRequest, error) {
	// LEFT JOIN: บัญชีที่ผู้ดูแลสร้างเองไม่มีแถวใน wbw_staff
	rows, err := r.db.Query(ctx, `
		SELECT u.user_id::text, u.username, u.role::text, u.display_name,
		       sp.school_id, s.name, sp.major, sp.staff_role::text,
		       u.status::text, u.created_at::text
		  FROM wbw_user u
		  LEFT JOIN wbw_staff sp ON sp.user_id = u.user_id
		  LEFT JOIN school s         ON s.school_id = sp.school_id
		 WHERE u.role IN ('staff','admin') AND u.status = 'pending'
		 ORDER BY u.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	reqs := []model.StaffRequest{}
	for rows.Next() {
		var s model.StaffRequest
		if err := rows.Scan(
			&s.ID, &s.Username, &s.Role, &s.DisplayName,
			&s.SchoolID, &s.SchoolName, &s.Major, &s.StaffRole,
			&s.Status, &s.Created,
		); err != nil {
			return nil, err
		}
		reqs = append(reqs, s)
	}
	return reqs, rows.Err()
}

// ApproveStaffRequest เปลี่ยนสถานะเป็น 'approved' (เฉพาะที่ยัง pending) คืน username ไว้ log
func (r *WBWCheckpointRepository) ApproveStaffRequest(ctx context.Context, id string) (string, error) {
	var username string
	err := r.db.QueryRow(ctx,
		`UPDATE wbw_user SET status = 'approved'
		 WHERE user_id = $1 AND role IN ('staff','admin') AND status = 'pending'
		 RETURNING username`, id).Scan(&username)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return username, err
}

// RejectStaffRequest ลบบัญชีที่ยัง pending ทิ้ง (ปฏิเสธ = คืน username ให้สมัครใหม่ได้)
func (r *WBWCheckpointRepository) RejectStaffRequest(ctx context.Context, id string) (string, error) {
	var username string
	err := r.db.QueryRow(ctx,
		`DELETE FROM wbw_user
		 WHERE user_id = $1 AND role IN ('staff','admin') AND status = 'pending'
		 RETURNING username`, id).Scan(&username)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return username, err
}

/* ---------- progress (ต้นไม้หน้า Home) ---------- */

// Progress — ฐานที่ผู้เข้าร่วมคนนี้เช็คอินแล้ว + จำนวนฐานทั้งหมดที่ต้องเช็คอิน
//
// นับ total แยกจากรายการ เพราะต้องได้เลขที่ถูกแม้ยังไม่เคยเช็คอินสักฐาน
// (COUNT บน join จะได้ 0 ทั้งคู่)
func (r *WBWCheckpointRepository) Progress(ctx context.Context, participantID string) (*model.CheckinProgress, error) {
	out := &model.CheckinProgress{CheckedIn: []model.CheckinProgressItem{}}

	if err := r.db.QueryRow(ctx,
		`SELECT count(*)::int FROM checkpoint WHERE requires_checkin`,
	).Scan(&out.Total); err != nil {
		return nil, err
	}

	// ตอบความเห็นต่อการเดินไปแล้วหรือยัง — คำถามคนละระดับกับ Answered ของแต่ละฐาน จึงถาม
	// แยก ไม่ได้ join เข้ามาในรายการด้านล่างซึ่งเป็นแถวต่อฐาน
	if err := r.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM event_feedback WHERE participant_id = $1::uuid)`,
		participantID,
	).Scan(&out.EventFeedbackAnswered); err != nil {
		return nil, err
	}

	rows, err := r.db.Query(ctx, `
		SELECT c.checkpoint_id, c.name, c.activity_name, c.sequence, ci.server_received_at,
		       (f.id IS NOT NULL) AS answered, f.rating, f.comment
		  FROM check_in ci
		  JOIN checkpoint c ON c.checkpoint_id = ci.checkpoint_id
		  LEFT JOIN checkin_feedback f
		         ON f.participant_id = ci.participant_id
		        AND f.checkpoint_id  = ci.checkpoint_id
		 WHERE ci.participant_id = $1::uuid AND c.requires_checkin
		 ORDER BY c.sequence NULLS LAST, c.checkpoint_id`, participantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var it model.CheckinProgressItem
		var at time.Time
		if err := rows.Scan(&it.CheckpointID, &it.Name, &it.ActivityName, &it.Sequence, &at,
			&it.Answered, &it.Rating, &it.Comment); err != nil {
			return nil, err
		}
		it.At = at.UTC().Format(time.RFC3339)
		out.CheckedIn = append(out.CheckedIn, it)
	}
	return out, rows.Err()
}
