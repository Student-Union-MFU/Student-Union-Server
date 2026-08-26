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
	// Severity — ระดับที่เจ้าหน้าที่ประเมินหลังไปถึง: minor / major / urgent · nil = ยังไม่ประเมิน
	//
	// เคสที่เป็น major หรือ urgent ยัง "เปิด" อยู่ ไม่ได้ปิด — ต่างจาก ResolveReason ที่มีค่า
	// ก็ต่อเมื่อเคสจบแล้ว · แอปใช้ค่านี้เปลี่ยนสีการ์ดและลำดับความสำคัญในรายการ
	Severity *string `json:"severity"`

	// Escalated — ผ่านการยืนยันจากเจ้าหน้าที่ประจำกลุ่มแล้วหรือยัง
	//
	// false = ขั้นแรก ยังอยู่กับเจ้าหน้าที่ประจำกลุ่ม (+แอดมิน) เท่านั้น
	// true  = SOS จริง เจ้าหน้าที่ทุกคนเห็น
	Escalated bool `json:"escalated"`
}

type SOSResolveRequest struct {
	Reason string `json:"reason"`
}

// SOSReportRequest — ผลที่เจ้าหน้าที่รายงานหลังไปถึงเคส
//
// false_alarm / minor / major / urgent · สองอันแรกปิดเคส สองอันหลังยกระดับโดยไม่ปิด
type SOSReportRequest struct {
	Outcome string `json:"outcome"`
}

/* ---------- มุมมองแอดมิน ---------- */

// SOSAdminPatch — สิ่งที่แอดมินแก้ได้ด้วยมือจากแผงผู้ดูแล
//
// ทุกช่องเป็น pointer เพื่อแยก "ไม่ได้ส่งมา" ออกจาก "ส่งมาเป็นค่าว่าง" ให้ได้จริง:
// Severity = ตัวชี้ไปยัง "" แปลว่า "ล้างระดับที่เคยประเมินไว้" ส่วน nil แปลว่า
// ไม่แตะ · ถ้าใช้ string ธรรมดาสองอย่างนี้จะเป็นค่าเดียวกันและล้างไม่ได้เลย
type SOSAdminPatch struct {
	Severity  *string `json:"severity"`
	Escalated *bool   `json:"escalated"`
	Resolved  *bool   `json:"resolved"`
	Reason    *string `json:"reason"`
}

// SOSAdminCreate — แอดมินเปิดเคสแทนผู้เข้าร่วม
//
// มีไว้สำหรับกรณีที่คนแจ้งทางอื่น: วิทยุ โทรศัพท์ หรือเดินมาบอกที่จุดอำนวยการ
// เคสพวกนี้ต้องอยู่ในระบบเดียวกับเคสที่กดจากแอป ไม่งั้นยอดรวมของทั้งงานจะนับ
// เฉพาะคนที่มีมือถือใช้ได้ตอนนั้น ซึ่งเป็นกลุ่มที่มีปัญหาน้อยที่สุดโดยนิยาม
//
// ไม่มี ClientID/DeviceTime ให้ส่ง — ฝั่งเซิร์ฟเวอร์เป็นคนสร้าง เพราะต้นทางคือ
// แผงเว็บที่ไม่มี outbox ให้ retry ด้วย id เดิมอยู่แล้ว
type SOSAdminCreate struct {
	ParticipantID string   `json:"participant_id"`
	Message       *string  `json:"message"`
	Severity      *string  `json:"severity"`
	ForOther      bool     `json:"for_other"`
	Lat           *float64 `json:"lat"`
	Lng           *float64 `json:"lng"`
}
