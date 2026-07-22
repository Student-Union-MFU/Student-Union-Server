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

/* ---------- admin ---------- */

type School struct {
	SchoolID int    `json:"school_id"`
	Name     string `json:"name"`
}

type DashboardStats struct {
	Participants   int `json:"participants"`
	TotalCheckins  int `json:"total_checkins"`
	OpenSOS        int `json:"open_sos"`
	FullGroups     int `json:"full_groups"`
}

// Participant คือ PARTICIPANT_SELECT ของ Express — สังเกต alias: id/bib/created
type Participant struct {
	ID           string   `json:"id"`
	StudentID    *string  `json:"student_id"`
	Created      *string  `json:"created"`
	Bib          *int     `json:"bib"`
	FirstName    *string  `json:"first_name"`
	LastName     *string  `json:"last_name"`
	ContactPhone *string  `json:"contact_phone"`
	SchoolID     *int     `json:"school_id"`
	SchoolName   *string  `json:"school_name"`
	Major        *string  `json:"major"`
	Sex          *string  `json:"sex"`
	GroupID      *int     `json:"group_id"`
	GroupNumber  *int     `json:"group_number"`
	CheckedIn    bool     `json:"checked_in"`
	BloodType    *string  `json:"blood_type"`
}

type ParticipantDetail struct {
	ID                        string   `json:"id"`
	StudentID                 *string  `json:"student_id"`
	Created                   *string  `json:"created"`
	Bib                       *int     `json:"bib"`
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
	CreatedBy  *string `json:"created_by"`
	CreatedAt  string  `json:"created_at"`
	ExpiresAt  *string `json:"expires_at"`
	ReadAt     *string `json:"read_at,omitempty"`
}

// NotificationPublic — ประกาศสาธารณะ (audience='all') สำหรับหน้า /announcements
// ที่เปิดดูได้โดยไม่ต้องล็อกอิน · ไม่มีข้อมูลผู้รับ/การอ่าน (เป็นของ staff เท่านั้น)
type NotificationPublic struct {
	ID          int64   `json:"id"`
	Type        string  `json:"type"`
	Title       string  `json:"title"`
	Body        *string `json:"body"`
	Level       string  `json:"level"`
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
