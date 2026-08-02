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
type StaffRegisterRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

// StaffRequest — คำขอเป็นเจ้าหน้าที่ที่ยังรออนุมัติ (แสดงในแผงผู้ดูแล)
type StaffRequest struct {
	ID          string  `json:"id"`
	Username    string  `json:"username"`
	Role        string  `json:"role"`
	DisplayName *string `json:"display_name"`
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
	UserID                    string   `json:"user_id"`
	Username                  string   `json:"username"`
	Role                      string   `json:"role"`
	BibNumber                 *int     `json:"bib_number"`
	QRToken                   *string  `json:"qr_token"`
	Year                      *int     `json:"year"`
	FirstName                 *string  `json:"first_name"`
	LastName                  *string  `json:"last_name"`
	Sex                       *string  `json:"sex"`
	DateOfBirth               *string  `json:"date_of_birth"`
	ContactPhone              *string  `json:"contact_phone"`
	SchoolID                  *int     `json:"school_id"`
	SchoolName                *string  `json:"school_name"`
	Major                     *string  `json:"major"`
	GroupID                   *int     `json:"group_id"`
	GroupNumber               *int     `json:"group_number"`
	PhotoURL                  *string  `json:"photo_url"`
	CheckedIn                 bool     `json:"checked_in"`
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
}

/* ---------- ความเห็นต่อฐาน ---------- */

// FeedbackRequest — สิ่งที่แอปส่งมาตอนกดส่งความเห็น
//
// ClientID ทำให้ส่งซ้ำตอนเน็ตหลุดไม่เกิดแถวซ้ำ (unique ใน DB) — แอปสร้างเองก่อนยิง
// และใช้ค่าเดิมทุกครั้งที่ retry
type FeedbackRequest struct {
	ClientID     string  `json:"client_id"`
	CheckpointID int     `json:"checkpoint_id"`
	Rating       int     `json:"rating"` // 1 ไม่ชอบ · 2 เฉยๆ · 3 ชอบ
	Comment      *string `json:"comment"`
	DeviceTime   string  `json:"device_time"`
}

// CheckinFeedback — ความเห็นหนึ่งอันที่บันทึกแล้ว
type CheckinFeedback struct {
	ID           int64   `json:"id"`
	CheckpointID int     `json:"checkpoint_id"`
	Rating       int     `json:"rating"`
	Comment      *string `json:"comment"`
	CreatedAt    string  `json:"created_at"`
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
type FeedbackSummary struct {
	CheckpointID int    `json:"checkpoint_id"`
	Name         string `json:"name"`
	Dislike      int    `json:"dislike"`
	Neutral      int    `json:"neutral"`
	Like         int    `json:"like"`
}

// AdminFeedbackResponse — สิ่งที่ GET /wbw/admin/feedback คืน
type AdminFeedbackResponse struct {
	Items   []AdminFeedbackRow `json:"items"`
	Summary []FeedbackSummary  `json:"summary"`
}
