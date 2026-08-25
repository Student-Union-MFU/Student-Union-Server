package repository

import (
	"context"
	"errors"

	"su-server/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrParticipantNotFound — สแกน QR หรือกรอก BIB แล้วไม่เจอคน (handler แปลงเป็น 404)
var ErrParticipantNotFound = errors.New("participant not found")

type WBWStaffRepository struct {
	db *pgxpool.Pool
}

func NewWBWStaffRepository(db *pgxpool.Pool) *WBWStaffRepository {
	return &WBWStaffRepository{db: db}
}

// Checkpoints — ทุกฐานที่ต้องเช็คอิน · เจ้าหน้าที่ทุกคนเห็นเท่ากันหมด ไม่ว่าจะประจำฐานไหน
//
// เดิมกรองด้วย checkpoint_staff (staff เห็นเฉพาะฐานที่ตัวเองถูกกำหนดให้ประจำ) — เอาออก
// เพราะหน้างานจริงเจ้าหน้าที่สลับฐานกันตลอด และ "ไม่มีใครกำหนดฐานให้" ทำให้ picker ว่าง
// จนสแกนไม่ได้เลย ซึ่งแย่กว่าการเห็นฐานเกินความจำเป็น · การกำหนดฐาน (checkpoint_staff)
// ยังใช้อยู่ ไม่ได้ถูกทิ้ง — แต่ใช้สำหรับ "SOS ของฐานไหนเด้งหาใคร" เท่านั้น (ดู
// wbw_sos_repository.go) ไม่ใช่สำหรับสิทธิ์การเช็คอิน
//
// กรอง requires_checkin เพราะฐานที่ไม่ต้องเช็คอิน (ห้องน้ำ/สวัสดิการ) ไม่ควรโผล่ใน picker
func (r *WBWStaffRepository) Checkpoints(ctx context.Context, userID, role string) ([]model.StaffCheckpoint, error) {
	rows, err := r.db.Query(ctx, `
		SELECT checkpoint_id, name, sequence
		  FROM checkpoint WHERE requires_checkin
		 ORDER BY sequence NULLS LAST, checkpoint_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []model.StaffCheckpoint{}
	for rows.Next() {
		var c model.StaffCheckpoint
		if err := rows.Scan(&c.ID, &c.Name, &c.Sequence); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// Checkin — บันทึกเช็คอินที่ฐาน · ระบุคนด้วย qr_token หรือ bib
//
// AlreadyCheckedIn มาจาก unique (participant_id, checkpoint_id) ที่ชนกัน ไม่ใช่ error:
// staff ต้องเห็นชื่อคนอยู่ดี แค่บอกว่าเช็คอินฐานนี้ไปแล้ว
func (r *WBWStaffRepository) Checkin(ctx context.Context, staffID string, checkpointID int, qrToken *string, bib *int) (*model.CheckinResult, error) {
	var (
		userID string
		out    model.CheckinResult
	)

	find := `SELECT user_id::text, first_name, last_name, bib_number
	           FROM participant_profile WHERE `
	var err error
	if qrToken != nil {
		err = r.db.QueryRow(ctx, find+`qr_token = $1`, *qrToken).
			Scan(&userID, &out.FirstName, &out.LastName, &out.Bib)
	} else {
		err = r.db.QueryRow(ctx, find+`bib_number = $1`, *bib).
			Scan(&userID, &out.FirstName, &out.LastName, &out.Bib)
	}
	if err == pgx.ErrNoRows {
		return nil, ErrParticipantNotFound
	}
	if err != nil {
		return nil, err
	}
	out.ParticipantID = userID

	// ธงข้อมูลการแพทย์ — เตือนเจ้าหน้าที่หน้าฐาน · ไม่มีแถว health_details = ไม่มีธง
	if err := r.db.QueryRow(ctx,
		`SELECT COALESCE(has_medical_flag, false) FROM health_details WHERE user_id = $1`,
		userID).Scan(&out.HasMedicalFlag); err != nil && err != pgx.ErrNoRows {
		return nil, err
	}

	_, err = r.db.Exec(ctx, `
		INSERT INTO check_in (client_id, participant_id, checkpoint_id, staff_id, device_time)
		VALUES (gen_random_uuid(), $1, $2, $3, now())`,
		userID, checkpointID, staffID)
	if err != nil {
		if IsPGCode(err, "23505") {
			out.AlreadyCheckedIn = true // เช็คอินฐานนี้ไปแล้ว — ไม่ใช่ error
		} else {
			return nil, err
		}
	}
	return &out, nil
}

// CheckpointName — ชื่อฐานไว้ตั้งหัวข้อแจ้งเตือน · หาไม่เจอหรือ query พังคืนค่าว่าง
// ไม่ใช่ error: ชื่อหายไปจากหัวข้อแค่ทำให้ข้อความดูด้วน ไม่ควรทำให้ทั้งแจ้งเตือนหายไปด้วย
func (r *WBWStaffRepository) CheckpointName(ctx context.Context, checkpointID int) string {
	var name string
	if err := r.db.QueryRow(ctx,
		`SELECT name FROM checkpoint WHERE checkpoint_id = $1`, checkpointID).Scan(&name); err != nil {
		return ""
	}
	return name
}

/* ---------- device token (push) ---------- */

type WBWDeviceRepository struct {
	db *pgxpool.Pool
}

func NewWBWDeviceRepository(db *pgxpool.Pool) *WBWDeviceRepository {
	return &WBWDeviceRepository{db: db}
}

// Register — upsert เจ้าของ token · token เดิมย้ายเครื่อง/ย้ายบัญชีได้
func (r *WBWDeviceRepository) Register(ctx context.Context, userID, token, platform string) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO device_token (token, user_id, platform, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (token) DO UPDATE
		   SET user_id = EXCLUDED.user_id, platform = EXCLUDED.platform, updated_at = now()`,
		token, userID, platform)
	return err
}

// Unregister — ลบเฉพาะ token ของผู้เรียกเอง กันคนอื่นถอน token ของคนอื่นทิ้ง
func (r *WBWDeviceRepository) Unregister(ctx context.Context, userID, token string) error {
	_, err := r.db.Exec(ctx,
		`DELETE FROM device_token WHERE token = $1 AND user_id = $2`, token, userID)
	return err
}

// PushTarget — หนึ่งเครื่องที่ต้องได้ push พร้อมเลข badge ของคนนั้น
type PushTarget struct {
	Token string
	Badge int
}

// ChatPushTargets — ใครควรได้ push ของข้อความในกลุ่มนี้บ้าง
//
// เงื่อนไขสามข้อ:
//   - ตัดคนส่งออก (p.user_id <> senderID)
//   - ตัดคนที่กำลังเปิดจอแชทอยู่: /chat/read ทำหน้าที่ heartbeat ทุก ~10 วิ
//     ดังนั้น read_at ใหม่กว่า 25 วิ = ยังดูอยู่ ไม่ต้องเด้ง
//   - LEFT JOIN ไม่ใช่ INNER: คนที่ admin ย้ายกลุ่มให้ตรงๆ ไม่มีแถว group_chat_state
//     ถ้า INNER JOIN เขาจะไม่ได้รับ push ตลอดไปโดยไม่มีใครรู้
//
// badge = จำนวนข้อความในกลุ่มที่ id เกิน cursor ของคนนั้น
func (r *WBWDeviceRepository) ChatPushTargets(ctx context.Context, groupID int, senderID string) ([]PushTarget, error) {
	rows, err := r.db.Query(ctx, `
		SELECT d.token,
		       (SELECT count(*) FROM group_message m
		         WHERE m.group_id = p.group_id AND m.id > COALESCE(s.last_read_id, 0)) AS unread
		  FROM participant_profile p
		  JOIN device_token d ON d.user_id = p.user_id
		  LEFT JOIN group_chat_state s ON s.user_id = p.user_id AND s.group_id = p.group_id
		 WHERE p.group_id = $1
		   AND p.user_id <> $2
		   AND (s.read_at IS NULL OR now() - s.read_at > interval '25 seconds')`,
		groupID, senderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []PushTarget{}
	for rows.Next() {
		var t PushTarget
		if err := rows.Scan(&t.Token, &t.Badge); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

// UserPushTargets — เครื่องทั้งหมดของผู้ใช้คนเดียว
//
// badge เป็น 0 เสมอ: จำนวนที่ยังไม่อ่านของแจ้งเตือนคิดคนละทางกับแชท และแอปนับ
// badge กระดิ่งเองจาก /notifications อยู่แล้ว
func (r *WBWDeviceRepository) UserPushTargets(ctx context.Context, userID string) ([]PushTarget, error) {
	rows, err := r.db.Query(ctx,
		`SELECT token FROM device_token WHERE user_id = $1::uuid`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []PushTarget{}
	for rows.Next() {
		var t PushTarget
		if err := rows.Scan(&t.Token); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

// DeleteTokens — เก็บกวาด token ที่ FCM บอกว่าใช้ไม่ได้แล้ว (ถอนแอป/ติดตั้งใหม่)
// ไม่ลบทิ้ง = ยิง push ใส่เครื่องที่ไม่มีอยู่จริงไปเรื่อยๆ และ targets โตขึ้นทุกวัน
func (r *WBWDeviceRepository) DeleteTokens(ctx context.Context, tokens []string) error {
	if len(tokens) == 0 {
		return nil
	}
	_, err := r.db.Exec(ctx, `DELETE FROM device_token WHERE token = ANY($1)`, tokens)
	return err
}
