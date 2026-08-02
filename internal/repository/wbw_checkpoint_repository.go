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
		       ) FILTER (WHERE u.user_id IS NOT NULL), '[]') AS staff
		  FROM checkpoint c
		  LEFT JOIN checkpoint_staff cs ON cs.checkpoint_id = c.checkpoint_id
		  LEFT JOIN app_user         u  ON u.user_id = cs.user_id
		 GROUP BY c.checkpoint_id
		 ORDER BY c.sequence NULLS LAST, c.checkpoint_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []model.Checkpoint{}
	for rows.Next() {
		var c model.Checkpoint
		if err := rows.Scan(&c.ID, &c.Name, &c.NameEn, &c.Type, &c.Sequence, &c.Staff); err != nil {
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
		  LEFT JOIN app_user         u  ON u.user_id = cs.user_id
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
func (r *WBWCheckpointRepository) AssignStaff(ctx context.Context, checkpointID int, userID string) (string, error) {
	var username string
	err := r.db.QueryRow(ctx,
		`SELECT username FROM app_user WHERE user_id = $1 AND role IN ('staff','admin')`, userID).Scan(&username)
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
		  FROM app_user WHERE role IN ('staff','admin') AND status = 'approved'
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
		`INSERT INTO app_user (username, password_hash, role, display_name)
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
		UPDATE app_user SET
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
		`UPDATE app_user SET password_hash = $2 WHERE user_id = $1 AND role IN ('staff','admin')`, id, hash)
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
		`DELETE FROM app_user WHERE user_id = $1 AND role IN ('staff','admin') RETURNING username`, id).Scan(&username)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return username, err
}

/* ---------- staff requests (สมัครเอง รออนุมัติ) ---------- */

// ListStaffRequests คืนเฉพาะบัญชี staff ที่สถานะ 'pending'
func (r *WBWCheckpointRepository) ListStaffRequests(ctx context.Context) ([]model.StaffRequest, error) {
	rows, err := r.db.Query(ctx, `
		SELECT user_id::text, username, role::text, display_name, status::text, created_at::text
		  FROM app_user
		 WHERE role IN ('staff','admin') AND status = 'pending'
		 ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	reqs := []model.StaffRequest{}
	for rows.Next() {
		var s model.StaffRequest
		if err := rows.Scan(&s.ID, &s.Username, &s.Role, &s.DisplayName, &s.Status, &s.Created); err != nil {
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
		`UPDATE app_user SET status = 'approved'
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
		`DELETE FROM app_user
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

	rows, err := r.db.Query(ctx, `
		SELECT c.checkpoint_id, c.name, c.sequence, ci.server_received_at
		  FROM check_in ci
		  JOIN checkpoint c ON c.checkpoint_id = ci.checkpoint_id
		 WHERE ci.participant_id = $1::uuid AND c.requires_checkin
		 ORDER BY c.sequence NULLS LAST, c.checkpoint_id`, participantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var it model.CheckinProgressItem
		var at time.Time
		if err := rows.Scan(&it.CheckpointID, &it.Name, &it.Sequence, &at); err != nil {
			return nil, err
		}
		it.At = at.UTC().Format(time.RFC3339)
		out.CheckedIn = append(out.CheckedIn, it)
	}
	return out, rows.Err()
}
