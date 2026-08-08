package model

// SOSRequest — สิ่งที่แอปส่งมาตอนกด SOS
//
// lat/lng/accuracy เป็น pointer ทั้งหมดเพราะ "ยังไม่มีพิกัด" เป็นสถานะปกติ ไม่ใช่ error:
// แอปยิงทันทีที่กดครบ 3 วิ โดยไม่รอ GPS แล้วค่อยยิงซ้ำด้วย client_id เดิมเมื่อได้ fix
type SOSRequest struct {
	ClientID   string   `json:"client_id"`
	DeviceTime string   `json:"device_time"`
	ForOther   bool     `json:"for_other"`
	Lat        *float64 `json:"lat"`
	Lng        *float64 `json:"lng"`
	AccuracyM  *float64 `json:"accuracy_m"`
	Message    *string  `json:"message"`
}

// SOSCase — เคสที่คนกดเห็นของตัวเอง (และเพื่อนในกลุ่มเห็นได้)
type SOSCase struct {
	ID             int64    `json:"id"`
	ForOther       bool     `json:"for_other"`
	Lat            *float64 `json:"lat"`
	Lng            *float64 `json:"lng"`
	AccuracyM      *float64 `json:"accuracy_m"`
	LocSource      *string  `json:"loc_source"`
	CheckpointID   *int     `json:"checkpoint_id"`
	CheckpointName *string  `json:"checkpoint_name"`
	Message        *string  `json:"message"`
	Resolved       bool     `json:"resolved"`
	ResolveReason  *string  `json:"resolve_reason"`
	AckedAt        *string  `json:"acked_at"`
	AckedByName    *string  `json:"acked_by_name"`
	CreatedAt      string   `json:"created_at"`
	// EmergencyPhone — เบอร์กลางงาน แนบมากับทุกคำตอบเพื่อให้แอปอัปเดตค่าที่ cache ไว้
	EmergencyPhone string `json:"emergency_phone"`
	// GroupID — กลุ่มของคนกด · `json:"-"` เพราะไม่ใช่ข้อมูลที่แอปต้องใช้ ไม่ควรเปลี่ยนรูป
	// JSON ที่ฝั่ง iOS พึ่งพาอยู่ เป็นค่าที่ service ต้องใช้ "ภายใน" เท่านั้น: notifyGroup ต้องรู้
	// ว่าจะยิงแถวแจ้งเตือนไปที่กลุ่มไหน (notification.audience_id) — ก่อนหน้านี้ SOSCase ไม่มี
	// ค่านี้เลย AudienceID จึงถูกปล่อยว่าง แถวที่สร้างได้ไม่มีวันถูก ListForUser หยิบขึ้นมาให้ใคร
	// (เงื่อนไขคือ n.audience_id = p.group_id::text ซึ่ง NULL ไม่มีวันเท่ากับอะไร) · nil ได้จริง
	// เมื่อคนกดยังไม่ถูกจัดกลุ่ม — ในกรณีนั้นไม่มีกลุ่มให้แจ้ง ก็ไม่ต้องสร้างแถว
	GroupID *int `json:"-"`
}

// SOSStaffCase — เคสหนึ่งอันในสายตาเจ้าหน้าที่ ข้อมูลมากกว่าที่คนกดเห็น
//
// HealthNotes มีค่าก็ต่อเมื่อ consent.consent_health_data = TRUE และเคสเปิดอยู่และ
// for_other = false — ไม่ใช่ข้อมูลที่ staff เปิดดูใครก็ได้ตลอดเวลา
type SOSStaffCase struct {
	SOSCase
	ParticipantID string  `json:"participant_id"`
	FirstName     string  `json:"first_name"`
	LastName      string  `json:"last_name"`
	Bib           *int    `json:"bib"`
	GroupNumber   *int    `json:"group_number"`
	ContactPhone  *string `json:"contact_phone"`
	EmergencyName *string `json:"emergency_contact_name"`
	EmergencyPh   *string `json:"emergency_contact_phone"`
	BloodType     *string `json:"blood_type"`
	HealthNotes   *string `json:"health_notes"`
	UpdatedAt     string  `json:"updated_at"`
}

type SOSResolveRequest struct {
	Reason string `json:"reason"`
}
