package model

// โครงสร้างข้อมูลฝั่ง WBW (เดินรอบดอย)
// ชื่อ json tag ต้องตรงกับที่ frontend อ่านเป๊ะๆ — web-next destructure ตรงจาก key เหล่านี้

/* ---------- auth ---------- */

// AuthUser คือ user object ที่ส่งกลับตอน register/login (3 field เท่านั้น)
type AuthUser struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

type AuthResponse struct {
	User  AuthUser `json:"user"`
	Token string   `json:"token"`
}

type RegisterRequest struct {
	StudentID string `json:"student_id"`
	Username  string `json:"username"` // fallback ถ้าไม่ส่ง student_id
	Password  string `json:"password"`
	Profile   struct {
		FirstName             string  `json:"first_name"`
		LastName              string  `json:"last_name"`
		Sex                   string  `json:"sex"`
		ContactPhone          *string `json:"contact_phone"`
		SchoolID              *int    `json:"school_id"`
		Major                 *string `json:"major"`
		PhotoURL              *string `json:"photo_url"`
		DateOfBirth           *string `json:"date_of_birth"`
		EmergencyContactName  *string `json:"emergency_contact_name"`
		EmergencyContactPhone *string `json:"emergency_contact_phone"`
	} `json:"profile"`
	Medical struct {
		Birthdate *string  `json:"birthdate"`
		WeightKg  *float64 `json:"weight_kg"`
		HeightCm  *float64 `json:"height_cm"`
		BloodType *string  `json:"blood_type"`
	} `json:"medical"`
	Health struct {
		ChronicConditions []string `json:"chronic_conditions"`
	} `json:"health"`
	Consent struct {
		ConsentHealthData         bool `json:"consent_health_data"`
		ConsentEmergencyTreatment bool `json:"consent_emergency_treatment"`
		WaiverAccepted            bool `json:"waiver_accepted"`
	} `json:"consent"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// StaffRegisterRequest — เจ้าหน้าที่สมัครเอง (ต้องรอแอดมินอนุมัติ)
// ไม่มี display_name แล้ว — ถามสำนักวิชา/สาขา/หน้าที่ในงานแทน
type StaffRegisterRequest struct {
	Username  string `json:"username"`
	Password  string `json:"password"`
	SchoolID  *int   `json:"school_id"`
	Major     string `json:"major"`
	StaffRole string `json:"staff_role"`
}

// StaffRequest — คำขอเป็นเจ้าหน้าที่ที่ยังรออนุมัติ (แสดงในแผงผู้ดูแล)
type StaffRequest struct {
	ID          string  `json:"id"`
	Username    string  `json:"username"`
	Role        string  `json:"role"`
	DisplayName *string `json:"display_name"`
	SchoolID    *int    `json:"school_id"`
	SchoolName  *string `json:"school_name"`
	Major       *string `json:"major"`
	StaffRole   *string `json:"staff_role"`
	Status      string  `json:"status"`
	Created     *string `json:"created"`
}

/* ---------- admin ---------- */

type School struct {
	SchoolID int    `json:"school_id"`
	Name     string `json:"name"`
}

type DashboardStats struct {
	Participants  int `json:"participants"`
	TotalCheckins int `json:"total_checkins"`
	OpenSOS       int `json:"open_sos"`
	FullGroups    int `json:"full_groups"`
}

// Participant คือ PARTICIPANT_SELECT ของ Express — สังเกต alias: id/bib/created
type Participant struct {
	ID           string  `json:"id"`
	StudentID    *string `json:"student_id"`
	Created      *string `json:"created"`
	Bib          *int    `json:"bib"`
	FirstName    *string `json:"first_name"`
	LastName     *string `json:"last_name"`
	ContactPhone *string `json:"contact_phone"`
	SchoolID     *int    `json:"school_id"`
	SchoolName   *string `json:"school_name"`
	Major        *string `json:"major"`
	Sex          *string `json:"sex"`
	GroupID      *int    `json:"group_id"`
	GroupNumber  *int    `json:"group_number"`
	CheckedIn    bool    `json:"checked_in"`
	BloodType    *string `json:"blood_type"`
	LeaveQuota   int     `json:"leave_quota"`
}

type ParticipantDetail struct {
	ID        string  `json:"id"`
	StudentID *string `json:"student_id"`
	Created   *string `json:"created"`
	Bib       *int    `json:"bib"`

	// แอป iOS ต้องการชื่อ key ตาม docs/backend-contract.md §2 ของ repo wbw-ios-fontend
	// ซึ่งไม่ตรงกับที่ web-next ใช้อยู่ (id, bib) · เพิ่มเป็น key ใหม่คู่ขนานแทนที่จะ
	// เปลี่ยนชื่อของเดิม — เปลี่ยนชื่อเมื่อไหร่ web-next พังทันที
	// user_id/username/role เป็น field บังคับฝั่ง iOS ไม่มีแล้ว decode ไม่ผ่านทั้งก้อน
	UserID       string  `json:"user_id"`
	Username     string  `json:"username"`
	Role         string  `json:"role"`
	BibNumber    *int    `json:"bib_number"`
	QRToken      *string `json:"qr_token"`
	Year         *int    `json:"year"`
	FirstName    *string `json:"first_name"`
	LastName     *string `json:"last_name"`
	Sex          *string `json:"sex"`
	DateOfBirth  *string `json:"date_of_birth"`
	ContactPhone *string `json:"contact_phone"`
	SchoolID     *int    `json:"school_id"`
	SchoolName   *string `json:"school_name"`
	Major        *string `json:"major"`
	GroupID      *int    `json:"group_id"`
	GroupNumber  *int    `json:"group_number"`
	PhotoURL     *string `json:"photo_url"`
	// Avatar — คีย์รูปประจำตัวจากชุดที่แอปกำหนด · NULL = ยังไม่ได้เลือก
	Avatar                    *string  `json:"avatar"`
	CheckedIn                 bool     `json:"checked_in"`
	LeaveQuota                int      `json:"leave_quota"`
	EmergencyContactName      *string  `json:"emergency_contact_name"`
	EmergencyContactPhone     *string  `json:"emergency_contact_phone"`
	BloodType                 *string  `json:"blood_type"`
	WeightKg                  *float64 `json:"weight_kg"`
	HeightCm                  *float64 `json:"height_cm"`
	ConsentHealthData         *bool    `json:"consent_health_data"`
	ConsentEmergencyTreatment *bool    `json:"consent_emergency_treatment"`
	WaiverAccepted            *bool    `json:"waiver_accepted"`

	// ข้อมูลสุขภาพที่ contract §2 ระบุว่า /me ต้องมี — จอโปรไฟล์ในแอปแสดงให้เจ้าตัวดู
	FoodAllergies  *string `json:"food_allergies"`
	ChronicDisease *string `json:"chronic_disease"`
	Medications    *string `json:"medications"`

	MembershipLog []MembershipLogEntry `json:"membership_log"`
}

// MembershipLogEntry — หนึ่งบรรทัดของประวัติเข้า/ออก/ปรับสิทธิ์ ในหน้ารายละเอียดผู้เข้าร่วม
// actor_name เป็น NULL แปลว่าผู้ใช้ทำเอง ไม่ใช่ admin ทำให้
type MembershipLogEntry struct {
	Action      string  `json:"action"`
	GroupID     *int    `json:"group_id"`
	GroupNumber *int    `json:"group_number"`
	QuotaAfter  int     `json:"quota_after"`
	ActorName   *string `json:"actor_name"`
	CreatedAt   string  `json:"created_at"`
}

// ParticipantPatch — ทุก field เป็น pointer เพื่อแยก "ไม่ได้ส่งมา" ออกจาก "ส่งค่าว่าง"
type ParticipantPatch struct {
	StudentID             *string  `json:"student_id"`
	FirstName             *string  `json:"first_name"`
	LastName              *string  `json:"last_name"`
	ContactPhone          *string  `json:"contact_phone"`
	Major                 *string  `json:"major"`
	SchoolID              *int     `json:"school_id"`
	GroupID               *int     `json:"group_id"`
	Sex                   *string  `json:"sex"`
	DateOfBirth           *string  `json:"date_of_birth"`
	EmergencyContactName  *string  `json:"emergency_contact_name"`
	EmergencyContactPhone *string  `json:"emergency_contact_phone"`
	CheckedIn             *bool    `json:"checked_in"`
	BloodType             *string  `json:"blood_type"`
	WeightKg              *float64 `json:"weight_kg"`
	HeightCm              *float64 `json:"height_cm"`
	// pointer เพื่อแยก "ไม่ได้ส่งมา" ออกจาก "ส่ง 0" — ไม่ส่ง key นี้ต้องไม่สร้างแถวประวัติ quota_adjust
	LeaveQuota *int `json:"leave_quota"`
}

// HasHealthFields บอกว่า body มี key ด้านสุขภาพไหม — Express จะแตะ health_details
// ก็ต่อเมื่อมีอย่างน้อย 1 ใน 3 key นี้ส่งมา
func (p ParticipantPatch) HasHealthFields() bool {
	return p.BloodType != nil || p.WeightKg != nil || p.HeightCm != nil
}

type StaffRef struct {
	ID          string  `json:"id"`
	Username    string  `json:"username"`
	DisplayName *string `json:"display_name"`
}

type Checkpoint struct {
	ID       int        `json:"id"`
	Name     string     `json:"name"`
	NameEn   *string    `json:"name_en"`
	Type     string     `json:"type"`
	Sequence *int       `json:"sequence"`
	Staff    []StaffRef `json:"staff"`

	// ตัวเลขหน้างานที่แท็บ "ฐาน" ต้องเห็นคู่กับรายชื่อเจ้าหน้าที่
	//
	// เดิมแท็บนั้นตอบได้แค่ "ฐานนี้ชื่ออะไร ใครดูแล" ซึ่งไม่พอสำหรับคำถามที่คนเปิด
	// แท็บนี้ถามจริงระหว่างงาน: ฐานไหนคนแน่น ฐานไหนยังไม่มีใครไปถึง ฐานไหนคนบ่น
	// ทั้งสามตัวอยู่ในฐานข้อมูลอยู่แล้ว แค่ไม่เคยถูกส่งมาพร้อมกัน
	//
	// AvgRating เป็น pointer — ฐานที่ยังไม่มีใครให้คะแนนคือ "ไม่มีข้อมูล" ไม่ใช่ 0 ดาว
	CheckinCount  int      `json:"checkin_count"`
	FeedbackCount int      `json:"feedback_count"`
	AvgRating     *float64 `json:"avg_rating"`
	SOSCount      int      `json:"sos_count"`
}

// ParticipantCheckpoint — ฐานหนึ่งใบตามที่ผู้เข้าร่วมเห็น (GET /wbw/checkpoints)
//
// ต่างจาก `Checkpoint` ของแอดมินตรงที่ไม่มีรายชื่อเจ้าหน้าที่ และมีชื่อกิจกรรมสองภาษามาด้วย —
// แอปเอาไปขึ้นบนการ์ดฐานในแท็บแผนที่ ซึ่งเดิมรู้ชื่อเฉพาะฐานที่เช็คอินไปแล้ว (จาก /me/progress
// ที่คืนแค่ checked_in) ฐานที่ยังไม่ไปถึงจึงขึ้นว่า "ฐานที่ N" ทั้งที่ชื่อมีอยู่ในตารางนี้ตลอด
//
// **lat/lng กลับมาแล้ว** — เดิมตั้งใจไม่ส่ง เพราะตำแหน่งหมุดบนแผนที่ 3D ของ iOS มาจาก
// ตัวไฟล์โมเดล การส่งพิกัดไปด้วยจึงมีแหล่งความจริงสองที่แข่งกัน (กับดักเดียวกับที่ทำให้พิกัด
// ในตารางนี้ผิดมา 8 แถว ดู migration 000026)
//
// เหตุผลนั้นยังจริงสำหรับ iOS และไม่ได้เปลี่ยน — แต่ใช้กับ Android ไม่ได้ เพราะแท็บแผนที่ของ
// Android เป็น Google Maps ที่วาดหมุดจากพิกัดจริง ไม่มีไฟล์โมเดลให้อ่านตำแหน่งจาก ก่อนหน้านี้
// แอปจึงวาดฐานไม่ได้เลยสักฐาน — เส้นทางขึ้น แต่ไม่มีอะไรบอกว่าฐานอยู่ตรงไหน
//
// เป็น pointer เพราะ lat/lng ใน DB เป็น NULL ได้ · ฝั่งแอปข้ามแถวที่ไม่มีพิกัดแทนที่จะ
// ปักหมุดที่ (0,0) กลางมหาสมุทรแอตแลนติก
type ParticipantCheckpoint struct {
	ID              int      `json:"id"`
	Sequence        *int     `json:"sequence"`
	Name            string   `json:"name"`
	NameEn          *string  `json:"name_en"`
	ActivityName    *string  `json:"activity_name"`
	ActivityNameEn  *string  `json:"activity_name_en"`
	Type            string   `json:"type"`
	RequiresCheckin bool     `json:"requires_checkin"`
	Lat             *float64 `json:"lat"`
	Lng             *float64 `json:"lng"`
	// CheckinCount — จำนวนผู้เข้าร่วมที่เช็คอินฐานนี้แล้ว ณ ตอนที่ถาม
	//
	// นับสดจาก check_in ทุกครั้ง ไม่ได้เก็บเป็นตัวเลขไว้ที่ไหน — ดูเหตุผลใน
	// ListForParticipant · จุดบริการที่ไม่ต้องเช็คอินจะได้ 0 ซึ่งเป็นความจริง ไม่ใช่ค่าว่าง
	CheckinCount int `json:"checkin_count"`
}

// CheckpointPatched — response ของ PATCH ไม่มี key staff (ตามของเดิม)
type CheckpointPatched struct {
	ID       int     `json:"id"`
	Name     string  `json:"name"`
	NameEn   *string `json:"name_en"`
	Type     string  `json:"type"`
	Sequence *int    `json:"sequence"`
}

// BaseStaffRef ใช้ {id,name} ไม่ใช่ {id,username,display_name} (ตาม /admin/bases-overview เดิม)
type BaseStaffRef struct {
	ID   string  `json:"id"`
	Name *string `json:"name"`
}

type BaseOverview struct {
	ID             int            `json:"id"`
	Name           string         `json:"name"`
	NameEn         *string        `json:"name_en"`
	Sequence       *int           `json:"sequence"`
	ActivityName   *string        `json:"activity_name"`
	ActivityNameEn *string        `json:"activity_name_en"`
	CheckinCount   int            `json:"checkin_count"`
	Staff          []BaseStaffRef `json:"staff"`
}

type AdminUser struct {
	ID          string  `json:"id"`
	Username    string  `json:"username"`
	Role        string  `json:"role"`
	DisplayName *string `json:"display_name"`
	Created     *string `json:"created"`
}

type AdminLog struct {
	ID        int64   `json:"id"`
	ActorName *string `json:"actor_name"`
	Action    string  `json:"action"`
	Detail    *string `json:"detail"`
	CreatedAt string  `json:"created_at"`
}

type Group struct {
	GroupID     int `json:"group_id"`
	GroupNumber int `json:"group_number"`
	Capacity    int `json:"capacity"`
	MemberCount int `json:"member_count"`
	SeatsLeft   int `json:"seats_left"`
}

/* ---------- notification ---------- */

type Notification struct {
	ID         int64   `json:"id"`
	Type       string  `json:"type"`
	Title      string  `json:"title"`
	Body       *string `json:"body"`
	Level      string  `json:"level"`
	Audience   string  `json:"audience"`
	AudienceID *string `json:"audience_id"`
	// RefID ชี้ไปแถวที่แจ้งเตือนนี้พูดถึง (เช่น checkpoint_id ของ checkin_feedback) —
	// ไม่มี omitempty เพื่อให้คีย์นี้ออกมาเสมอแม้เป็น null รักษารูปทรง JSON ให้คงที่ไว้ก่อน
	// สำหรับฝั่ง iOS ซึ่งยังไม่มี field นี้อยู่จริงตอนนี้ — กันไว้เชิงป้องกันไม่ให้ decoder ใน
	// อนาคตต้องเจอคีย์ที่โผล่บ้างหายบ้าง ไม่ใช่ข้อพิสูจน์ว่ามี decoder ตัวไหนพังอยู่ตอนนี้
	RefID     *string `json:"ref_id"`
	CreatedBy *string `json:"created_by"`
	CreatedAt string  `json:"created_at"`
	ExpiresAt *string `json:"expires_at"`
	ReadAt    *string `json:"read_at,omitempty"`
}

// NotificationPublic — ประกาศสาธารณะ (audience='all') สำหรับหน้า /announcements
// ที่เปิดดูได้โดยไม่ต้องล็อกอิน · ไม่มีข้อมูลผู้รับ/การอ่าน (เป็นของ staff เท่านั้น)
type NotificationPublic struct {
	ID          int64   `json:"id"`
	Type        string  `json:"type"`
	Title       string  `json:"title"`
	Body        *string `json:"body"`
	Level       string  `json:"level"`
	RefID       *string `json:"ref_id"`
	CreatedAt   string  `json:"created_at"`
	ExpiresAt   *string `json:"expires_at"`
	CreatorName *string `json:"creator_name"`
}

// NotificationSent — delivered_count/read_count เป็น string เพราะของเดิม (node-pg)
// ส่ง count(*) มาเป็น string และ frontend อ่านแบบนั้นอยู่
type NotificationSent struct {
	ID             int64   `json:"id"`
	Type           string  `json:"type"`
	Title          string  `json:"title"`
	Body           *string `json:"body"`
	Level          string  `json:"level"`
	Audience       string  `json:"audience"`
	AudienceID     *string `json:"audience_id"`
	RefID          *string `json:"ref_id"`
	CreatedAt      string  `json:"created_at"`
	ExpiresAt      *string `json:"expires_at"`
	CreatorName    *string `json:"creator_name"`
	DeliveredCount string  `json:"delivered_count"`
	ReadCount      string  `json:"read_count"`
}

type NotificationRequest struct {
	Type       *string `json:"type"`
	Title      string  `json:"title"`
	Body       *string `json:"body"`
	Level      *string `json:"level"`
	Audience   *string `json:"audience"`
	AudienceID *string `json:"audience_id"`
	RefID      *string `json:"ref_id"`
	ExpiresAt  *string `json:"expires_at"`
}

// Preset ใช้ได้ทั้ง preset และ draft (แยกด้วย kind)
type Preset struct {
	ID         int64   `json:"id"`
	Kind       string  `json:"kind"`
	Name       *string `json:"name"`
	Title      *string `json:"title"`
	Body       *string `json:"body"`
	Level      string  `json:"level"`
	Audience   string  `json:"audience"`
	AudienceID *string `json:"audience_id"`
	CreatedBy  *string `json:"created_by"`
	UpdatedAt  string  `json:"updated_at"`
	CreatedAt  string  `json:"created_at"`
}

type PresetRequest struct {
	Name       *string `json:"name"`
	Title      *string `json:"title"`
	Body       *string `json:"body"`
	Level      *string `json:"level"`
	Audience   *string `json:"audience"`
	AudienceID *string `json:"audience_id"`
}

/* ---------- admin user requests ---------- */

type CreateAdminUserRequest struct {
	Username    string  `json:"username"`
	Password    string  `json:"password"`
	Role        string  `json:"role"`
	DisplayName *string `json:"display_name"`
}

type UpdateAdminUserRequest struct {
	DisplayName *string `json:"display_name"`
	Role        *string `json:"role"`
}

type PasswordRequest struct {
	Password string `json:"password"`
}

type CheckpointRequest struct {
	Name     *string `json:"name"`
	NameEn   *string `json:"name_en"`
	Type     *string `json:"type"`
	Sequence *int    `json:"sequence"`
}

type AssignStaffRequest struct {
	UserID string `json:"user_id"`
}

/* ---------- progress (ต้นไม้หน้า Home) ---------- */

// CheckinProgressItem — ฐานหนึ่งที่ผู้เข้าร่วมเช็คอินไปแล้ว
type CheckinProgressItem struct {
	CheckpointID int     `json:"checkpoint_id"`
	Name         string  `json:"name"`
	ActivityName *string `json:"activity_name"`
	Sequence     *int    `json:"sequence"`
	At           string  `json:"at"`
	// Answered = มีแถวใน checkin_feedback แล้ว · ไม่ได้เก็บสถานะไว้ที่ไหน คำนวณจาก LEFT JOIN
	Answered bool    `json:"answered"`
	Rating   *int    `json:"rating"`
	Comment  *string `json:"comment"`
}

// CheckinProgress — ความคืบหน้าของผู้เข้าร่วมคนหนึ่ง
//
// Total นับจาก DB ทุกครั้ง ไม่ใช่ค่าคงที่ 8 — แอดมินเพิ่ม/ลบฐานได้ผ่าน
// /wbw/admin/checkpoints ถ้าฝังเลขไว้ ต้นไม้ในแอปจะเพี้ยนทันทีที่แก้ฐานวันงาน
type CheckinProgress struct {
	Total     int                   `json:"total"`
	CheckedIn []CheckinProgressItem `json:"checked_in"`
	// EmergencyPhone — เบอร์กลางงาน แนบมากับทุกครั้งที่ /me/progress ถูก poll (ทุก 60 วิ
	// ระหว่างเปิดแอป) ให้ปุ่มโทรสำรองของ SOS มีเบอร์แคชไว้ในเครื่องตั้งแต่ก่อนเกิดเหตุ —
	// ต่างจาก SOSCase.EmergencyPhone ที่มาถึงก็ต่อเมื่อส่ง SOS สำเร็จแล้วเท่านั้น
	// ว่างได้ตอน dev — แอปมีเบอร์ default ของตัวเองอยู่แล้ว
	EmergencyPhone string `json:"emergency_phone"`
	// EventFeedbackAnswered — ตอบความเห็นต่อการเดินทั้งงานไปแล้วหรือยัง (ตาราง event_feedback)
	//
	// อยู่ตรงนี้เพราะแอปต้องรู้ว่า "เคยตอบไปแล้วไหม" ก่อนจะเปิดฟอร์มปิดทางตอนเดินครบ และ
	// ที่เดียวที่รู้คือฐานข้อมูล ไม่ใช่เครื่อง — เครื่องลืมทุกครั้งที่ลงแอปใหม่ ล้างข้อมูล
	// หรือเปลี่ยนเครื่อง แล้วจะถามซ้ำกับคนที่ตอบไปแล้ว ทรงเดียวกับ Answered ของแต่ละฐาน
	// ที่มาจาก LEFT JOIN ไม่ใช่จาก flag ในแอป
	EventFeedbackAnswered bool `json:"event_feedback_answered"`
}

/* ---------- ความเห็นต่อฐาน ---------- */

// FeedbackRequest — สิ่งที่แอปส่งมาตอนกดส่งความเห็น
//
// ClientID ทำให้ส่งซ้ำตอนเน็ตหลุดไม่เกิดแถวซ้ำ (unique ใน DB) — แอปสร้างเองก่อนยิง
// และใช้ค่าเดิมทุกครั้งที่ retry
type FeedbackRequest struct {
	ClientID     string `json:"client_id"`
	CheckpointID int    `json:"checkpoint_id"`
	// Rating — ภาพรวม 1-5 · เดิมเป็น 1-3 (ไม่ชอบ/เฉยๆ/ชอบ) ดู migration 000031
	Rating  int     `json:"rating"`
	Comment *string `json:"comment"`

	// สามข้อย่อย 1-5 · เป็น pointer เพราะไคลเอนต์รุ่นเก่าส่งมาแค่ Rating ข้อเดียว
	// การบังคับให้ครบทุกข้อจะทำให้แอปเวอร์ชันก่อนหน้าส่งความเห็นไม่ได้เลย
	RatingScenery  *int `json:"rating_scenery"`
	RatingActivity *int `json:"rating_activity"`
	RatingStaff    *int `json:"rating_staff"`
	// RatingArea — พื้นที่ (ร่มเงา ที่นั่ง ที่ว่าง) แยกจาก RatingScenery ที่เป็นวิว · ดู migration 000032
	RatingArea *int `json:"rating_area"`

	DeviceTime string `json:"device_time"`
}

// CheckinFeedback — ความเห็นหนึ่งอันที่บันทึกแล้ว
type CheckinFeedback struct {
	ID             int64   `json:"id"`
	CheckpointID   int     `json:"checkpoint_id"`
	Rating         int     `json:"rating"`
	RatingScenery  *int    `json:"rating_scenery"`
	RatingActivity *int    `json:"rating_activity"`
	RatingStaff    *int    `json:"rating_staff"`
	RatingArea     *int    `json:"rating_area"`
	Comment        *string `json:"comment"`
	CreatedAt      string  `json:"created_at"`
}

// EventFeedbackRequest — ความเห็นต่อการเดินทั้งงาน ถามครั้งเดียวตอนครบทุกฐาน
//
// ไม่มี CheckpointID โดยตั้งใจ — นี่คือเหตุผลที่ต้องมีตารางแยก (ดู migration 000033)
// ไม่ใช่แถวใน checkin_feedback ที่บังคับให้ต้องชี้ไปฐานใดฐานหนึ่ง
type EventFeedbackRequest struct {
	ClientID string `json:"client_id"`
	// Rating — ภาพรวมของการเดิน 1-5 · ข้อเดียวที่บังคับ ทรงเดียวกับ FeedbackRequest
	Rating int `json:"rating"`
	// RatingActivity — กิจกรรมตลอดเส้นทาง · ชื่อเดียวกับคอลัมน์ใน checkin_feedback เพราะ
	// เป็นคำถามเดียวกันที่ย้ายที่ถาม ไม่ใช่คำถามใหม่
	RatingActivity *int    `json:"rating_activity"`
	Comment        *string `json:"comment"`
	DeviceTime     string  `json:"device_time"`
}

// EventFeedback — ความเห็นต่อการเดินหนึ่งแถวที่บันทึกแล้ว
type EventFeedback struct {
	ID             int64   `json:"id"`
	Rating         int     `json:"rating"`
	RatingActivity *int    `json:"rating_activity"`
	Comment        *string `json:"comment"`
	CreatedAt      string  `json:"created_at"`
}

// AdminFeedbackRow — ความเห็นหนึ่งแถวสำหรับแอดมิน (ผูกชื่อผู้ตอบ ตามที่ตกลงไว้ใน spec)
type AdminFeedbackRow struct {
	ID             int64   `json:"id"`
	CheckpointID   int     `json:"checkpoint_id"`
	CheckpointName string  `json:"checkpoint_name"`
	ActivityName   *string `json:"activity_name"`
	ParticipantID  string  `json:"participant_id"`
	FirstName      string  `json:"first_name"`
	LastName       string  `json:"last_name"`
	Bib            *int    `json:"bib"`
	Rating         int     `json:"rating"`
	Comment        *string `json:"comment"`
	CreatedAt      string  `json:"created_at"`
}

// FeedbackSummary — นับคะแนนต่อฐาน
//
// เดิมเป็นสามช่อง dislike/neutral/like ตามสเกล 1–3 ของ migration 000014 และไม่ได้ตามไปแก้
// ตอน 000031 ขยายเป็น 1–5 ผลคือทุกคะแนน 4 และ 5 หายไปจากยอดเงียบ ๆ — ผลรวมของสามช่อง
// น้อยกว่าจำนวนคนที่ตอบจริง โดยหน้าที่อ่านไม่มีทางรู้ว่าขาดไป
//
// Distribution ทรงเดียวกับ FeedbackStats.Distribution ตั้งใจให้เหมือนกัน: สองที่นี้นับของ
// อย่างเดียวกันจากตารางเดียวกัน ถ้าคนละทรงจะมีวันที่ตัวเลขสองหน้าไม่ตรงกันแล้วหาสาเหตุยาก
type FeedbackSummary struct {
	CheckpointID int    `json:"checkpoint_id"`
	Name         string `json:"name"`
	Distribution []int  `json:"distribution"` // ดัชนี 0..4 = 1..5
}

// AdminFeedbackResponse — สิ่งที่ GET /wbw/admin/feedback คืน
type AdminFeedbackResponse struct {
	Items   []AdminFeedbackRow `json:"items"`
	Summary []FeedbackSummary  `json:"summary"`
}

/* ---------- โควตาผู้เข้าร่วมทั้งงาน ---------- */

// Capacity — สถานะที่นั่งของงาน (นับเฉพาะ role = 'participant' · staff/admin ไม่นับ)
// ที่มาของตัวเลขคือตาราง wbw_capacity ที่ trigger ดูแลให้ตรงกับ wbw_user เสมอ
type Capacity struct {
	Max       int  `json:"max"`
	Taken     int  `json:"taken"`
	SeatsLeft int  `json:"seats_left"`
	Full      bool `json:"full"`
}
