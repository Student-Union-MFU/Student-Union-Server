package repository

import (
	"context"
	"errors"
	"strings"

	"su-server/internal/model"

	"github.com/jackc/pgx/v5"
)

/* ============================================================
   แชทในมุมผู้ดูแล — เห็นทุกห้อง และเห็นสิ่งที่ถูกจัดการไปแล้ว

   ต่างจาก query ฝั่งผู้ใช้สองข้อ และทั้งสองข้อคือเหตุผลที่ต้องเป็นไฟล์แยก:

   1) ไม่มี CanUseGroupChat — ผู้ดูแลอ่านได้ทุกห้องโดยไม่ต้องเป็นสมาชิก
   2) คืน original_body กับสถานะการจัดการมาด้วย — คนที่ต้องตัดสินใจว่าจะกู้คืน
      ข้อความที่เซ็นเซอร์ไปไหม ต้องเห็นว่าของเดิมเขียนว่าอะไร ไม่งั้นการกู้คืน
      กลายเป็นการเดา

   ข้อ 2 คือเหตุผลที่ห้ามใช้ chatMessageCols ตัวเดิมซ้ำ: มันตั้งใจซ่อนของเดิม
   ============================================================ */

var ErrMessageNotFound = errors.New("message not found")

// ChatRoomSummary — หนึ่งห้องในรายการที่ผู้ดูแลเลือกเปิด
type ChatRoomSummary = model.ChatRoomSummary

// Rooms — ทุกกลุ่มที่มีข้อความ เรียงตามความเคลื่อนไหวล่าสุด
//
// กลุ่มที่ไม่เคยมีใครพิมพ์ก็อยู่ในรายการด้วย (LEFT JOIN) แต่ตกไปท้าย — ผู้ดูแล
// ต้องเลือกห้องได้ทุกห้องแม้ห้องที่เงียบ ไม่ใช่เฉพาะห้องที่มีเรื่อง
func (r *WBWChatRepository) Rooms(ctx context.Context) ([]model.ChatRoomSummary, error) {
	rows, err := r.db.Query(ctx, `
		SELECT g.group_id, g.group_number, g.member_count,
		       count(m.id)::int,
		       count(*) FILTER (WHERE m.deleted_at  IS NOT NULL)::int,
		       count(*) FILTER (WHERE m.censored_at IS NOT NULL)::int,
		       max(m.created_at)::text
		  FROM participant_group g
		  LEFT JOIN group_message m ON m.group_id = g.group_id
		 GROUP BY g.group_id, g.group_number, g.member_count
		 ORDER BY max(m.created_at) DESC NULLS LAST, g.group_number`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []model.ChatRoomSummary{}
	for rows.Next() {
		var s model.ChatRoomSummary
		if err := rows.Scan(&s.GroupID, &s.GroupNumber, &s.MemberCount,
			&s.MessageCount, &s.DeletedCount, &s.CensoredCount, &s.LastMessageAt); err != nil {
			return nil, err
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

// adminMessageCols — เหมือน chatMessageCols แต่เปิดทุกอย่างให้ผู้ดูแลเห็น
//
// body ตรงนี้เป็น m.body ดิบ ไม่ผ่าน chatVisibleBody: ข้อความที่ถูกลบยังต้องอ่าน
// ได้ในหน้าผู้ดูแล ไม่งั้นหน้าจอจะบอกว่า "มีบางอย่างถูกลบ" โดยไม่บอกว่าอะไร
// ซึ่งทำให้ตัดสินใจกู้คืนไม่ได้ และทำให้บันทึกหลักฐานไร้ประโยชน์
// student_id แยกจาก username แม้ผู้เข้าร่วมจะใช้รหัสนักศึกษาเป็น username —
// เจ้าหน้าที่กับผู้ดูแลใช้ชื่อผู้ใช้ที่ตั้งเอง ถ้าอ่าน username เป็นรหัสนักศึกษา
// หน้าจอจะขึ้น "yionstaff" ในช่องรหัสนักศึกษาซึ่งผิดและดูเหมือนข้อมูลปนกัน
const adminMessageCols = `m.id, m.group_id, m.sender_id, m.client_id, m.body,
	                      m.created_at::text, p.first_name, p.last_name, u.username, u.role::text,
	                      COALESCE(u.student_id, ''), p.avatar,
	                      m.deleted_at::text, du.display_name,
	                      m.censored_at::text, cu.display_name, m.original_body`

const adminMessageFrom = `
	  FROM group_message m
	  LEFT JOIN participant_profile p ON p.user_id = m.sender_id
	  LEFT JOIN wbw_user u  ON u.user_id  = m.sender_id
	  LEFT JOIN wbw_user du ON du.user_id = m.deleted_by
	  LEFT JOIN wbw_user cu ON cu.user_id = m.censored_by`

func scanAdminMessages(rows pgx.Rows) ([]model.AdminMessage, error) {
	defer rows.Close()
	list := []model.AdminMessage{}
	for rows.Next() {
		var m model.AdminMessage
		if err := rows.Scan(&m.ID, &m.GroupID, &m.SenderID, &m.ClientID, &m.Body,
			&m.CreatedAt, &m.FirstName, &m.LastName, &m.Username, &m.SenderRole,
			&m.StudentID, &m.Avatar,
			&m.DeletedAt, &m.DeletedBy,
			&m.CensoredAt, &m.CensoredBy, &m.OriginalBody); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// RoomMessages — ข้อความทั้งห้อง เก่าไปใหม่ (อ่านบทสนทนาตามลำดับที่เกิดขึ้นจริง)
func (r *WBWChatRepository) RoomMessages(ctx context.Context, groupID, limit int) ([]model.AdminMessage, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	// เอา N ตัวล่าสุดก่อนแล้วค่อยกลับลำดับ — ห้องที่มีข้อความหลักพัน ผู้ดูแลต้อง
	// เห็นท้ายห้องเป็นอันดับแรก แต่ต้องอ่านจากบนลงล่างตามเวลาเหมือนแชททั่วไป
	rows, err := r.db.Query(ctx, `
		SELECT * FROM (
		  SELECT `+adminMessageCols+adminMessageFrom+`
		   WHERE m.group_id = $1
		   ORDER BY m.id DESC LIMIT $2
		) t ORDER BY id`, groupID, limit)
	if err != nil {
		return nil, err
	}
	return scanAdminMessages(rows)
}

// SearchMessages — ค้นข้ามทุกห้อง · ใช้ตอนผู้ดูแลรู้คำแต่ไม่รู้ว่าอยู่ห้องไหน
//
// ค้นทั้ง body และ original_body: ข้อความที่ถูกเซ็นเซอร์ไปแล้วต้องยังค้นเจอด้วย
// คำเดิมของมัน ไม่งั้นการเซ็นเซอร์กลายเป็นการซ่อนหลักฐานจากคนที่ต้องตรวจสอบเอง
func (r *WBWChatRepository) SearchMessages(ctx context.Context, q string, limit int) ([]model.AdminMessage, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := r.db.Query(ctx, `
		SELECT `+adminMessageCols+adminMessageFrom+`
		 WHERE m.body ILIKE '%' || $1 || '%'
		    OR m.original_body ILIKE '%' || $1 || '%'
		 ORDER BY m.id DESC LIMIT $2`, strings.TrimSpace(q), limit)
	if err != nil {
		return nil, err
	}
	return scanAdminMessages(rows)
}

func (r *WBWChatRepository) AdminGetMessage(ctx context.Context, id int64) (*model.AdminMessage, error) {
	rows, err := r.db.Query(ctx, `SELECT `+adminMessageCols+adminMessageFrom+` WHERE m.id = $1`, id)
	if err != nil {
		return nil, err
	}
	list, err := scanAdminMessages(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrMessageNotFound
	}
	return &list[0], nil
}

/* ---------- การกระทำของผู้ดูแล ---------- */

// Delete — ซ่อนข้อความจากผู้เข้าร่วม โดยยังเก็บตัวข้อความไว้
func (r *WBWChatRepository) DeleteMessage(ctx context.Context, id int64, actorID string) (*model.AdminMessage, error) {
	return r.exec(ctx, id, `
		UPDATE group_message
		   SET deleted_at = now(), deleted_by = $2::uuid
		 WHERE id = $1`, actorID)
}

// Restore — ยกเลิกการลบ
//
// ไม่รับ actorID ต่างจาก DeleteMessage โดยตั้งใจ: การกู้คืนล้าง deleted_by ทิ้ง
// จึงไม่มีที่ให้เก็บว่าใครกู้ · ร่องรอยของการกู้คืนอยู่ใน admin_log ซึ่งเป็นที่
// ที่ถูกต้องสำหรับ "ประวัติการกระทำ" อยู่แล้ว
//
// (เวอร์ชันแรกรับ actorID แล้วส่งเข้าไปเป็น argument ที่ SQL ไม่มี $2 ให้ —
//
//	pgx ปฏิเสธทั้ง query ด้วย "mismatched param and argument count" ทำให้ปุ่ม
//	กู้คืนพังทั้งปุ่ม ทั้งที่ตัว UPDATE ถูกต้องทุกตัวอักษร)
func (r *WBWChatRepository) RestoreMessage(ctx context.Context, id int64) (*model.AdminMessage, error) {
	return r.exec(ctx, id, `
		UPDATE group_message
		   SET deleted_at = NULL, deleted_by = NULL
		 WHERE id = $1`)
}

// Censor — เก็บของเดิมไว้แล้วเขียนข้อความแทนที่ลงใน body
//
// COALESCE(original_body, body) กันการเซ็นเซอร์ซ้ำจากการกลืนของเดิมทิ้ง: เซ็นเซอร์
// รอบที่สองจะเอา "ข้อความแทนที่ของรอบแรก" ไปเก็บเป็นของเดิม แล้วต้นฉบับจริงหายไป
// ตลอดกาล · เขียน original_body เฉพาะครั้งแรกเท่านั้น
func (r *WBWChatRepository) CensorMessage(ctx context.Context, id int64, replacement, actorID string) (*model.AdminMessage, error) {
	return r.exec(ctx, id, `
		UPDATE group_message
		   SET original_body = COALESCE(original_body, body),
		       body          = $3,
		       censored_at   = now(),
		       censored_by   = $2::uuid
		 WHERE id = $1`, actorID, replacement)
}

// Uncensor — คืนข้อความเดิม · ข้อความที่ไม่เคยถูกเซ็นเซอร์ไม่มี original_body
// ให้คืน จึงปล่อย body ไว้ตามเดิมแทนที่จะเขียน NULL ทับ (body เป็น NOT NULL)
func (r *WBWChatRepository) UncensorMessage(ctx context.Context, id int64) (*model.AdminMessage, error) {
	return r.exec(ctx, id, `
		UPDATE group_message
		   SET body          = COALESCE(original_body, body),
		       original_body = NULL,
		       censored_at   = NULL,
		       censored_by   = NULL
		 WHERE id = $1`)
}

func (r *WBWChatRepository) exec(ctx context.Context, id int64, sql string, args ...any) (*model.AdminMessage, error) {
	tag, err := r.db.Exec(ctx, sql, append([]any{id}, args...)...)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrMessageNotFound
	}
	return r.AdminGetMessage(ctx, id)
}
