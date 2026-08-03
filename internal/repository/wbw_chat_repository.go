package repository

import (
	"context"

	"su-server/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type WBWChatRepository struct {
	db *pgxpool.Pool
}

func NewWBWChatRepository(db *pgxpool.Pool) *WBWChatRepository {
	return &WBWChatRepository{db: db}
}

// คอลัมน์ชุดเดียวกันทุก query ที่คืน Message — first_name/last_name มาจาก LEFT JOIN
// เพราะ staff/admin ที่ไม่มี participant_profile ก็โพสต์ได้ (ชื่อจะเป็น null)
const chatMessageCols = `m.id, m.group_id, m.sender_id, m.client_id, m.body,
	                     m.device_time::text, m.created_at::text, p.first_name, p.last_name`

func scanMessages(rows pgx.Rows) ([]model.Message, error) {
	defer rows.Close()
	list := []model.Message{}
	for rows.Next() {
		var m model.Message
		if err := rows.Scan(&m.ID, &m.GroupID, &m.SenderID, &m.ClientID, &m.Body,
			&m.DeviceTime, &m.CreatedAt, &m.FirstName, &m.LastName); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// IsMember — อยู่ในกลุ่มนี้จริงไหม · ใช้กันทุก endpoint ของแชท
func (r *WBWChatRepository) IsMember(ctx context.Context, userID string, groupID int) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM participant_profile WHERE user_id = $1 AND group_id = $2)`,
		userID, groupID).Scan(&ok)
	return ok, err
}

// SinceID — จุดตัดประวัติของคนนี้ · ไม่มีแถว = 0 คือเห็นทั้งหมด
// (เกิดได้จริงเมื่อ admin ย้ายกลุ่มให้ตรงๆ ผ่านหน้า /admin ซึ่งไม่แตะ group_chat_state)
func (r *WBWChatRepository) SinceID(ctx context.Context, userID string, groupID int) (int64, error) {
	var since int64
	err := r.db.QueryRow(ctx,
		`SELECT COALESCE(MAX(since_id), 0) FROM group_chat_state WHERE user_id = $1 AND group_id = $2`,
		userID, groupID).Scan(&since)
	return since, err
}

// MessagesAfter — ข้อความหลัง afterID แต่ต้องไม่ต่ำกว่าจุดตัดประวัติของคนเรียก
func (r *WBWChatRepository) MessagesAfter(ctx context.Context, groupID int, afterID, sinceID int64, limit int) ([]model.Message, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+chatMessageCols+`
		  FROM group_message m
		  LEFT JOIN participant_profile p ON p.user_id = m.sender_id
		 WHERE m.group_id = $1 AND m.id > GREATEST($2::bigint, $3::bigint)
		 ORDER BY m.id ASC LIMIT $4`,
		groupID, afterID, sinceID, limit)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// MessagesLatest — N ข้อความล่าสุด (ไม่ส่ง after มา) เรียงกลับเป็น ascending
func (r *WBWChatRepository) MessagesLatest(ctx context.Context, groupID int, sinceID int64, limit int) ([]model.Message, error) {
	rows, err := r.db.Query(ctx, `
		SELECT * FROM (
			SELECT `+chatMessageCols+`
			  FROM group_message m
			  LEFT JOIN participant_profile p ON p.user_id = m.sender_id
			 WHERE m.group_id = $1 AND m.id > $2::bigint
			 ORDER BY m.id DESC LIMIT $3
		) t ORDER BY id ASC`,
		groupID, sinceID, limit)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// Cursors — คนอื่นในกลุ่มอ่านถึงไหน · JOIN participant_profile ตัดคนที่ย้าย/ออกกลุ่มไปแล้ว
// ไม่งั้น "อ่านแล้ว N" จะนับคนที่ไม่อยู่แล้วค้างอยู่ตลอด
func (r *WBWChatRepository) Cursors(ctx context.Context, groupID int, exceptUserID string) ([]model.ReadCursor, error) {
	rows, err := r.db.Query(ctx, `
		SELECT s.user_id::text, s.last_read_id
		  FROM group_chat_state s
		  JOIN participant_profile p ON p.user_id = s.user_id AND p.group_id = s.group_id
		 WHERE s.group_id = $1 AND s.user_id <> $2`,
		groupID, exceptUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []model.ReadCursor{}
	for rows.Next() {
		var c model.ReadCursor
		if err := rows.Scan(&c.UserID, &c.LastReadID); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

func (r *WBWChatRepository) MemberCount(ctx context.Context, groupID int) (int, error) {
	var n int
	err := r.db.QueryRow(ctx,
		`SELECT COALESCE(member_count, 0) FROM participant_group WHERE group_id = $1`,
		groupID).Scan(&n)
	if err == pgx.ErrNoRows {
		return 0, nil
	}
	return n, err
}

// MarkRead — upsert ไม่ใช่ UPDATE เฉยๆ
//
// แถวอาจไม่มี (admin ย้ายกลุ่มให้ตรงๆ ไม่ได้แตะตารางนี้) · UPDATE เฉยๆ จะแมตช์ 0 แถว
// เงียบๆ แล้วตอบสำเร็จโดยไม่ได้บันทึก · read_at ไม่ขยับ = push เข้าใจผิดว่าคนนี้ไม่เคยอ่าน
//
// ตอน INSERT ใหม่ตั้ง since_id = 0 ไม่ใช่ค่าที่โพสต์มา: /chat/read มีหน้าที่ขยับ cursor
// อ่านแล้วอย่างเดียว การตัดขอบเขตประวัติเป็นหน้าที่ของ /join · ถ้าตั้งเป็น lastReadID
// จะตัดประวัติที่คนนี้มีสิทธิ์เห็นอยู่แล้วทิ้งไปโดยไม่ตั้งใจ
func (r *WBWChatRepository) MarkRead(ctx context.Context, userID string, groupID int, lastReadID int64) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO group_chat_state (user_id, group_id, since_id, last_read_id, read_at)
		VALUES ($1, $2, 0, $3::bigint, now())
		ON CONFLICT (user_id, group_id) DO UPDATE
		   SET last_read_id = GREATEST(group_chat_state.last_read_id, EXCLUDED.last_read_id),
		       read_at = now()`,
		userID, groupID, lastReadID)
	return err
}

// InsertMessage — idempotent ด้วย client_id เพื่อให้ retry ตอนเน็ตแย่ได้ข้อความเดิม
//
// คืน (nil, nil) เมื่อ client_id ถูกใช้ไปแล้วโดย "คนอื่น" หรือ "กลุ่มอื่น" — handler
// แปลงเป็น 409 · ต่างจาก Node เดิมที่ fallback SELECT ไม่กรอง sender/group เลย ทำให้
// จงใจส่ง client_id ชนของคนอื่นแล้วได้ข้อความของเขากลับมาอ่านได้
func (r *WBWChatRepository) InsertMessage(ctx context.Context, groupID int, senderID string, req model.SendMessageRequest, deviceTime string) (*model.Message, error) {
	var m model.Message
	err := r.db.QueryRow(ctx, `
		WITH ins AS (
			INSERT INTO group_message (group_id, sender_id, client_id, body, device_time)
			VALUES ($1, $2, $3, $4, $5::timestamptz)
			ON CONFLICT (client_id) DO NOTHING
			RETURNING id, group_id, sender_id, client_id, body, device_time, created_at
		), row AS (
			SELECT * FROM ins
			UNION ALL
			SELECT id, group_id, sender_id, client_id, body, device_time, created_at
			  FROM group_message
			 WHERE client_id = $3 AND group_id = $1 AND sender_id = $2
			   AND NOT EXISTS (SELECT 1 FROM ins)
		)
		SELECT r.id, r.group_id, r.sender_id::text, r.client_id::text, r.body,
		       r.device_time::text, r.created_at::text, p.first_name, p.last_name
		  FROM row r LEFT JOIN participant_profile p ON p.user_id = r.sender_id`,
		groupID, senderID, req.ClientID, req.Body, deviceTime,
	).Scan(&m.ID, &m.GroupID, &m.SenderID, &m.ClientID, &m.Body,
		&m.DeviceTime, &m.CreatedAt, &m.FirstName, &m.LastName)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}
