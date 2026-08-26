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
	// Avatar — คีย์รูปของผู้ส่ง ติดมากับข้อความเพื่อให้ห้องแชทวาดได้โดยไม่ต้องถามรายชื่อ
	// สมาชิกก่อน · ผู้ส่งที่ยังไม่เลือกจะเป็น NULL และฝั่งแอปวาดวงกลมสีเดิมให้
	Avatar *string `json:"avatar"`
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
	Avatar    *string `json:"avatar"`
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

/* ============================================================
   มุมมองผู้ดูแล — เห็นทุกห้อง และเห็นสิ่งที่ถูกจัดการไปแล้ว
   ============================================================ */

// ChatRoomSummary — หนึ่งห้องในรายการที่ผู้ดูแลเลือกเปิด
//
// DeletedCount/CensoredCount อยู่ในรายการห้อง ไม่ใช่แค่ในห้อง: คำถามแรกของคน
// เปิดหน้านี้คือ "ห้องไหนมีเรื่อง" ซึ่งตอบได้ก็ต่อเมื่อตัวเลขนั้นอยู่ในรายการ
type ChatRoomSummary struct {
	GroupID       int     `json:"group_id"`
	GroupNumber   int     `json:"group_number"`
	MemberCount   int     `json:"member_count"`
	MessageCount  int     `json:"message_count"`
	DeletedCount  int     `json:"deleted_count"`
	CensoredCount int     `json:"censored_count"`
	LastMessageAt *string `json:"last_message_at"`
}

// AdminMessage — ข้อความหนึ่งในสายตาผู้ดูแล
//
// ต่างจาก Message ที่แอปได้รับสองอย่าง: Body ตรงนี้เป็นค่าดิบ (ข้อความที่ถูกลบ
// ยังอ่านได้) และมี OriginalBody ของข้อความที่ถูกเซ็นเซอร์ · ทั้งสองอย่างคือสิ่ง
// ที่คนต้องเห็นก่อนตัดสินใจว่าจะกู้คืนไหม
type AdminMessage struct {
	ID         int64   `json:"id"`
	GroupID    int     `json:"group_id"`
	SenderID   string  `json:"sender_id"`
	ClientID   string  `json:"client_id"`
	Body       string  `json:"body"`
	CreatedAt  *string `json:"created_at"`
	FirstName  *string `json:"first_name"`
	LastName   *string `json:"last_name"`
	Username   *string `json:"username"`
	SenderRole *string `json:"sender_role"`
	// StudentID — ว่างสำหรับบัญชีเจ้าหน้าที่/ผู้ดูแลที่ไม่ได้ผูกกับรหัสนักศึกษา
	StudentID string  `json:"student_id"`
	Avatar    *string `json:"avatar"`

	DeletedAt  *string `json:"deleted_at"`
	DeletedBy  *string `json:"deleted_by"`
	CensoredAt *string `json:"censored_at"`
	CensoredBy *string `json:"censored_by"`
	// OriginalBody — ข้อความก่อนถูกเซ็นเซอร์ · null เมื่อไม่เคยถูกเซ็นเซอร์
	OriginalBody *string `json:"original_body"`
}

// ChatModerateRequest — สิ่งที่ผู้ดูแลสั่งกับข้อความหนึ่ง
//
// เป็น action เดียวต่อครั้ง ไม่ใช่ก้อน patch หลายฟิลด์: "ลบ" กับ "เซ็นเซอร์" เป็น
// การตัดสินใจคนละเรื่องที่บังเอิญแก้ตารางเดียวกัน การรวมเป็น patch เดียวเปิดทางให้
// ส่งคำสั่งที่ขัดกันเองมาในคำขอเดียว (ลบและกู้คืนพร้อมกัน) ซึ่งไม่มีคำตอบที่ถูก
type ChatModerateRequest struct {
	Action string `json:"action"` // delete | restore | censor | uncensor
	// Replacement — ข้อความที่จะให้ผู้เข้าร่วมเห็นแทน · ว่างได้ ระบบใส่ค่าตั้งต้นให้
	Replacement string `json:"replacement"`
}
