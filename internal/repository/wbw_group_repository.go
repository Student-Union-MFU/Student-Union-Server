package repository

import (
	"context"
	"errors"

	"su-server/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrGroupFull — ที่นั่งเต็มแล้ว (handler แปลงเป็น 409)
	ErrGroupFull = errors.New("group full")
	// ErrNoGroup — ยังไม่ได้อยู่กลุ่มไหนเลย ตอนสั่งออกจากกลุ่ม
	ErrNoGroup = errors.New("not in a group")
	// ErrNoQuota — สิทธิ์ออกจากกลุ่มหมดแล้ว (handler แปลงเป็น 409)
	ErrNoQuota = errors.New("no leave quota")
)

type WBWGroupRepository struct {
	db *pgxpool.Pool
}

func NewWBWGroupRepository(db *pgxpool.Pool) *WBWGroupRepository {
	return &WBWGroupRepository{db: db}
}

// Members — สมาชิกในกลุ่ม พร้อมรูปกับสำนักวิชา สำหรับจอรายชื่อ
func (r *WBWGroupRepository) Members(ctx context.Context, groupID int) ([]model.GroupMember, error) {
	rows, err := r.db.Query(ctx, `
		SELECT p.user_id::text, p.first_name, p.last_name, p.photo_url, p.bib_number, s.name
		  FROM participant_profile p
		  LEFT JOIN school s ON s.school_id = p.school_id
		 WHERE p.group_id = $1
		 ORDER BY p.first_name`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []model.GroupMember{}
	for rows.Next() {
		var m model.GroupMember
		if err := rows.Scan(&m.UserID, &m.FirstName, &m.LastName, &m.PhotoURL, &m.Bib, &m.School); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// MembersIndex — รายชื่อทุกคนทุกกลุ่ม ไม่มีรูป (เบาพอให้แอปโหลดทีเดียวแล้ว cache)
func (r *WBWGroupRepository) MembersIndex(ctx context.Context) ([]model.GroupMemberIndex, error) {
	rows, err := r.db.Query(ctx, `
		SELECT p.user_id::text, p.first_name, p.last_name, p.group_id, g.group_number
		  FROM participant_profile p
		  JOIN participant_group g ON g.group_id = p.group_id
		 WHERE p.group_id IS NOT NULL
		 ORDER BY g.group_number`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []model.GroupMemberIndex{}
	for rows.Next() {
		var m model.GroupMemberIndex
		if err := rows.Scan(&m.UserID, &m.FirstName, &m.LastName, &m.GroupID, &m.GroupNumber); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// Join — เข้ากลุ่มแบบ atomic กันที่นั่งเกิน แล้วตั้งจุดตัดประวัติแชทใหม่
//
// เข้ากลุ่มเดิมซ้ำก็ตัดประวัติใหม่ทุกครั้ง ตามที่ออกแบบไว้ (อธิบายง่ายและเป็นส่วนตัวที่สุด)
// คืน ErrNotFound เมื่อไม่มีกลุ่มนี้ · ErrGroupFull เมื่อเต็ม
func (r *WBWGroupRepository) Join(ctx context.Context, userID string, groupID int) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// ล็อกแถวกลุ่มปลายทางก่อน กันสองคนแย่งที่นั่งสุดท้ายพร้อมกัน
	var capacity, memberCount int
	err = tx.QueryRow(ctx,
		`SELECT capacity, member_count FROM participant_group WHERE group_id = $1 FOR UPDATE`,
		groupID).Scan(&capacity, &memberCount)
	if err == pgx.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	// อยู่กลุ่มนี้อยู่แล้วไหม — ถ้าใช่ ข้ามการเช็คที่นั่ง เพราะเขานั่งอยู่ในนั้นแล้ว
	// (ไม่ข้าม = กลุ่มที่เต็มพอดีจะกดเข้าซ้ำเพื่อตัดประวัติไม่ได้เลย)
	var current *int
	if err := tx.QueryRow(ctx,
		`SELECT group_id FROM participant_profile WHERE user_id = $1`,
		userID).Scan(&current); err != nil {
		if err == pgx.ErrNoRows {
			return ErrNotFound
		}
		return err
	}
	alreadyHere := current != nil && *current == groupID
	if !alreadyHere && memberCount >= capacity {
		return ErrGroupFull
	}

	// trigger trg_group_count ปรับ member_count ให้ทั้งกลุ่มเก่าและใหม่เอง
	if _, err := tx.Exec(ctx,
		`UPDATE participant_profile SET group_id = $1, updated_at = now() WHERE user_id = $2`,
		groupID, userID); err != nil {
		return err
	}

	// ย้ายกลุ่ม = ทิ้ง state ของกลุ่มเก่า ไม่งั้น cursor เก่าค้างอยู่ในตารางตลอด
	if _, err := tx.Exec(ctx,
		`DELETE FROM group_chat_state WHERE user_id = $1 AND group_id <> $2`,
		userID, groupID); err != nil {
		return err
	}

	// จุดตัด = ข้อความล่าสุดของกลุ่ม ณ ตอนนี้ · last_read_id เท่ากัน เพราะประวัติก่อนหน้า
	// ไม่ใช่ของเขา จะนับเป็น "ยังไม่อ่าน" ไม่ได้
	if _, err := tx.Exec(ctx, `
		INSERT INTO group_chat_state (user_id, group_id, since_id, last_read_id, read_at)
		SELECT $1, $2, COALESCE(MAX(id), 0), COALESCE(MAX(id), 0), now()
		  FROM group_message WHERE group_id = $2
		ON CONFLICT (user_id, group_id) DO UPDATE
		   SET since_id = EXCLUDED.since_id,
		       last_read_id = EXCLUDED.since_id,
		       read_at = now()`,
		userID, groupID); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// Leave — ออกจากกลุ่ม + หักโควตา 1 ครั้ง ในทรานแซกชันเดียว
//
// เงื่อนไข leave_quota > 0 อยู่ใน WHERE ของ UPDATE ตัวเดียวกับที่เคลียร์ group_id ตั้งใจ —
// ถ้าแยกเป็น SELECT เช็คก่อนแล้วค่อย UPDATE สองคำขอที่มาพร้อมกันจะอ่านเห็น quota = 1 ทั้งคู่
// แล้วหักคนละครั้งจน quota ติดลบและออกได้สองรอบ
//
// กลุ่มเดิมดึงมาจาก CTE ไม่ใช่ subquery ใน RETURNING — subquery ใน RETURNING ให้ค่าตาม
// snapshot semantics ซึ่งอ่านแล้วเดาไม่ออกว่าได้ค่าก่อนหรือหลัง UPDATE (เหตุผลเดียวกับที่โค้ดเดิม
// เลี่ยงไว้) · ลบ chat state ด้วย ไม่งั้นคนที่ออกไปแล้วยังถูกนับใน "อ่านแล้ว N"
func (r *WBWGroupRepository) Leave(ctx context.Context, userID string) (int, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var prevGroup, quotaAfter int
	err = tx.QueryRow(ctx, `
		WITH target AS (
		  SELECT user_id, group_id
		    FROM participant_profile
		   WHERE user_id = $1 AND group_id IS NOT NULL AND leave_quota > 0
		     FOR UPDATE
		)
		UPDATE participant_profile p
		   SET group_id = NULL, leave_quota = p.leave_quota - 1, updated_at = now()
		  FROM target t
		 WHERE p.user_id = t.user_id
		RETURNING t.group_id, p.leave_quota`, userID).Scan(&prevGroup, &quotaAfter)
	if errors.Is(err, pgx.ErrNoRows) {
		// ไม่โดนแถวไหนเลย — ต้องแยกว่าเพราะไม่มีกลุ่ม (ผลลัพธ์ที่ต้องการเกิดแล้ว) หรือสิทธิ์หมด (ปฏิเสธ)
		return 0, leaveBlockedReason(ctx, tx, userID)
	}
	if err != nil {
		return 0, err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM group_chat_state WHERE user_id = $1`, userID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO group_membership_log (user_id, group_id, action, quota_after)
		VALUES ($1, $2, 'leave', $3)`, userID, prevGroup, quotaAfter); err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return prevGroup, nil
}

// leaveBlockedReason — อ่านสถานะจริงในทรานแซกชันเดียวกัน เพื่อบอกสาเหตุที่ UPDATE ไม่โดนแถวไหน
func leaveBlockedReason(ctx context.Context, tx pgx.Tx, userID string) error {
	var groupID *int
	var quota int
	err := tx.QueryRow(ctx,
		`SELECT group_id, leave_quota FROM participant_profile WHERE user_id = $1`,
		userID).Scan(&groupID, &quota)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if groupID == nil {
		return ErrNoGroup
	}
	return ErrNoQuota
}
