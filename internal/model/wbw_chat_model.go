package model

// โครงสร้างของ WBW ฝั่งแอป iOS — แชท กลุ่ม staff และ device token
// json tag ต้องตรงกับที่ iOS decode เป๊ะๆ (APIClient ตั้ง keyDecodingStrategy
// = .convertFromSnakeCase ไว้ ดังนั้น snake_case ที่นี่ = camelCase ฝั่ง Swift)

/* ---------- แชทกลุ่ม ---------- */

// Message — ตรงกับ MessageDTO ฝั่ง iOS (WBW/Models.swift:195)
// ID เป็น int64 ไม่ใช่ string: iOS ครอบด้วย @FlexibleString ซึ่งรับได้ทั้งเลขและ string
type Message struct {
	ID         int64   `json:"id"`
	GroupID    int     `json:"group_id"`
	SenderID   string  `json:"sender_id"`
	ClientID   string  `json:"client_id"`
	Body       string  `json:"body"`
	DeviceTime *string `json:"device_time"`
	CreatedAt  *string `json:"created_at"`
	FirstName  *string `json:"first_name"`
	LastName   *string `json:"last_name"`
}

// ReadCursor — คนอื่นในกลุ่มอ่านถึง id ไหน (server ตัดตัวผู้เรียกออกให้แล้ว)
type ReadCursor struct {
	UserID     string `json:"user_id"`
	LastReadID int64  `json:"last_read_id"`
}

// ChatSyncResponse — ข้อความ + สถานะอ่าน ในคำขอเดียว (ตรงกับ ChatSyncResponse ฝั่ง iOS)
type ChatSyncResponse struct {
	SinceID     int64        `json:"since_id"`
	MemberCount int          `json:"member_count"`
	Messages    []Message    `json:"messages"`
	Cursors     []ReadCursor `json:"cursors"`
}

type ChatReadRequest struct {
	LastReadID *int64 `json:"last_read_id"`
}

// SendMessageRequest — client_id ทำให้ส่งซ้ำตอนเน็ตแย่ได้ข้อความเดิม ไม่เกิดซ้ำ
type SendMessageRequest struct {
	ClientID   string  `json:"client_id"`
	Body       string  `json:"body"`
	DeviceTime *string `json:"device_time"`
}

/* ---------- สมาชิกกลุ่ม ---------- */

// GroupMember — ตรงกับ GroupMember ฝั่ง iOS (WBW/Models.swift:175)
type GroupMember struct {
	UserID    string  `json:"user_id"`
	FirstName *string `json:"first_name"`
	LastName  *string `json:"last_name"`
	PhotoURL  *string `json:"photo_url"`
	Bib       *int    `json:"bib"`
	School    *string `json:"school"`
}

type GroupMembersResponse struct {
	Members []GroupMember `json:"members"`
	Count   int           `json:"count"`
}

// GroupMemberIndex — รายชื่อทุกคนทุกกลุ่มในคำขอเดียว ใช้ทำ index ฝั่งแอป
type GroupMemberIndex struct {
	UserID      string  `json:"user_id"`
	FirstName   *string `json:"first_name"`
	LastName    *string `json:"last_name"`
	GroupID     int     `json:"group_id"`
	GroupNumber int     `json:"group_number"`
}

/* ---------- device token (push) ---------- */

type DeviceRegisterRequest struct {
	Token    string `json:"token"`
	Platform string `json:"platform"`
}

type DeviceUnregisterRequest struct {
	Token string `json:"token"`
}

/* ---------- staff เช็คอินหน้าฐาน ---------- */

// StaffCheckpoint — ตรงกับ StaffCheckpoint ฝั่ง iOS: key ชื่อ "id" ไม่ใช่ "checkpoint_id"
type StaffCheckpoint struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Sequence *int   `json:"sequence"`
}

// StaffCheckinRequest — ระบุคนด้วย qr_token หรือ bib อย่างใดอย่างหนึ่ง
type StaffCheckinRequest struct {
	CheckpointID *int    `json:"checkpoint_id"`
	QRToken      *string `json:"qr_token"`
	Bib          *int    `json:"bib"`
}

// CheckinResult — ตรงกับ CheckinResult ฝั่ง iOS (WBW/Models.swift:107)
// AlreadyCheckedIn = true คือเช็คอินซ้ำ ตอบ 200 ไม่ใช่ error (staff จะได้เห็นชื่อคนอยู่ดี)
type CheckinResult struct {
	FirstName        *string `json:"first_name"`
	LastName         *string `json:"last_name"`
	Bib              *int    `json:"bib"`
	HasMedicalFlag   bool    `json:"has_medical_flag"`
	AlreadyCheckedIn bool    `json:"already_checked_in"`

	// ParticipantID — ไม่ส่งออกใน JSON (`-`) เพราะจอเจ้าหน้าที่ไม่ได้ใช้ และ id ผู้ใช้
	// ไม่ควรรั่วไปอยู่ในมือเครื่องอื่นโดยไม่จำเป็น · service ใช้ยิงแจ้งเตือนให้ถูกคน
	ParticipantID string `json:"-"`
}

/* ---------- โปรไฟล์ตัวเอง ---------- */

type MePatchRequest struct {
	PhotoURL *string `json:"photo_url"`
}
