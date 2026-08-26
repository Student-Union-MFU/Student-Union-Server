package model

/* ============================================================
   Analytics — ก้อนสรุปสำหรับแผงผู้ดูแล (GET /wbw/admin/analytics)

   แยกจาก DashboardStats เดิมโดยตั้งใจ ไม่ได้ไปเพิ่มฟิลด์ในนั้น:
   DashboardStats เป็นตัวเลขสี่ตัวที่หน้าแรกของแผงเรียกทุกครั้งที่เปิด และแอป
   มือถือก็อ่านอยู่ — ทำให้มันใหญ่ขึ้นเรื่อย ๆ แปลว่าทุกคนจ่ายค่า query ของกราฟ
   ที่ตัวเองไม่ได้เปิดดู · ตัวนี้เป็น endpoint แยกที่หนักกว่า เรียกเมื่อเปิดแท็บ
   "วิเคราะห์" เท่านั้น

   ทุกค่าที่ "ยังไม่มีข้อมูลพอจะคำนวณ" เป็น pointer แล้วส่ง null ไม่ใช่ 0 —
   ค่าเฉลี่ยของศูนย์คำตอบไม่ใช่ 0 ดาว และกราฟที่วาด 0 ให้ดูเหมือนคะแนนแย่
   ทั้งที่ยังไม่มีใครตอบ คือการโกหกด้วยภาพ
   ============================================================ */

// Analytics — คำตอบทั้งก้อนของ GET /wbw/admin/analytics
type Analytics struct {
	GeneratedAt   string            `json:"generated_at"`
	Capacity      AnalyticsCapacity `json:"capacity"`
	Registration  []RegistrationDay `json:"registration"`
	Demographics  Demographics      `json:"demographics"`
	Groups        GroupStats        `json:"groups"`
	Checkins      CheckinStats      `json:"checkins"`
	SOS           SOSStats          `json:"sos"`
	Feedback      FeedbackStats     `json:"feedback"`
	Staff         StaffCoverage     `json:"staff"`
	Notifications NotificationStats `json:"notifications"`
}

// CountByKey — "หมวด → จำนวน" รูปเดียวใช้ซ้ำได้ทุกกราฟแท่ง/โดนัท
// Key เป็น string เสมอแม้ค่าจริงจะเป็นตัวเลข (เช่น ชั้นปี) เพื่อให้ฝั่งเว็บ
// เขียน component เดียวรับได้ทุกชุด ไม่ต้องแยก type ตามชนิดของแกน
type CountByKey struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

/* ---------- ที่นั่ง / ยอดสมัคร ---------- */

// AnalyticsCapacity — เหมือน Capacity ของหน้าสมัคร แต่แนบยอดที่เช็คอินแล้วมาด้วย
// (หน้าสมัครไม่ต้องรู้ว่ามีใครมาถึงงานแล้วบ้าง แผงผู้ดูแลต้องรู้)
type AnalyticsCapacity struct {
	Max       int `json:"max"`
	Taken     int `json:"taken"`
	SeatsLeft int `json:"seats_left"`
	CheckedIn int `json:"checked_in"`
}

// RegistrationDay — ยอดสมัครของหนึ่งวัน พร้อมยอดสะสมถึงวันนั้น
// Cumulative คำนวณฝั่ง SQL (window function) ไม่ใช่ฝั่งเว็บ เพราะถ้าเว็บสะสมเอง
// การกรองแถวใด ๆ บนหน้าจะทำให้เส้นสะสมผิดโดยไม่มีใครสังเกต
type RegistrationDay struct {
	Day        string `json:"day"`
	Count      int    `json:"count"`
	Cumulative int    `json:"cumulative"`
}

/* ---------- ประชากรผู้เข้าร่วม ---------- */

// Demographics — ภาพรวมว่าคนที่สมัครมาเป็นใคร
//
// Profiled = จำนวนคนที่มีแถว participant_profile จริง ซึ่งน้อยกว่ายอด role=participant
// ได้ (สมัครค้างกลางคัน) · ทุกสัดส่วนในบล็อกนี้ต้องหารด้วยตัวนี้ ไม่ใช่ยอดสมัครรวม
type Demographics struct {
	Profiled int               `json:"profiled"`
	Sex      []CountByKey      `json:"sex"`
	Year     []CountByKey      `json:"year"`
	Blood    []CountByKey      `json:"blood"`
	School   []SchoolBreakdown `json:"school"`
}

// SchoolBreakdown — ยอดสมัครต่อสำนักวิชา แยกว่ามาถึงงานแล้วกี่คน
type SchoolBreakdown struct {
	SchoolID  *int   `json:"school_id"`
	Name      string `json:"name"`
	Count     int    `json:"count"`
	CheckedIn int    `json:"checked_in"`
}

/* ---------- กลุ่ม ---------- */

// GroupStats — ความแน่นของกลุ่มทั้งงาน
type GroupStats struct {
	Total      int         `json:"total"`
	Full       int         `json:"full"`
	Empty      int         `json:"empty"`
	Assigned   int         `json:"assigned"`   // ผู้เข้าร่วมที่มีกลุ่มแล้ว
	Unassigned int         `json:"unassigned"` // สมัครแล้วแต่ยังไม่ถูกจัดกลุ่ม
	Seats      int         `json:"seats"`      // ที่นั่งรวมทุกกลุ่ม
	Items      []GroupFill `json:"items"`
}

// GroupFill — หนึ่งกลุ่มบนกราฟความแน่น · StaffCount = เจ้าหน้าที่ประจำกลุ่ม
// (คนที่เห็น SOS ขั้นแรกของกลุ่มนั้น) — กลุ่มที่คนเต็มแต่ไม่มีเจ้าหน้าที่คือสิ่งที่
// กราฟนี้มีไว้ให้เห็น
type GroupFill struct {
	GroupID     int `json:"group_id"`
	GroupNumber int `json:"group_number"`
	Capacity    int `json:"capacity"`
	MemberCount int `json:"member_count"`
	StaffCount  int `json:"staff_count"`
}

/* ---------- เช็คอิน ---------- */

// CheckinStats — ความคืบหน้าบนเส้นทาง
type CheckinStats struct {
	Total    int              `json:"total"`   // จำนวนครั้งทั้งหมด
	Walkers  int              `json:"walkers"` // จำนวน "คน" ที่เช็คอินอย่างน้อยหนึ่งฐาน
	Funnel   []CheckpointStep `json:"funnel"`
	Timeline []TimeBucket     `json:"timeline"`
	// Completion — กี่คนเดินได้กี่ฐาน (BasesDone 0 = สมัครแล้วแต่ยังไม่เริ่ม)
	Completion []CompletionBucket `json:"completion"`
	ByStaff    []CountByKey       `json:"by_staff"`

	// Pace — เวลาที่ใช้เดินจากฐานหนึ่งไปเช็คอินอีกฐานหนึ่ง
	Pace []LegPace `json:"pace"`
	// เวลารวมบนเส้นทางต่อคน = เช็คอินครั้งสุดท้าย − ครั้งแรก · nil เมื่อยังไม่มีใคร
	// เช็คอินถึงสองฐาน (คนเดียวหนึ่งฐานไม่มี "ช่วงเวลา" ให้วัด)
	TotalMedianSec *float64 `json:"total_median_sec"`
	TotalP90Sec    *float64 `json:"total_p90_sec"`
}

// LegPace — หนึ่งช่วงระหว่างสองฐาน วัดจากเวลาเช็คอินจริงของผู้เข้าร่วมแต่ละคน
//
// คู่ฐานมาจาก "ลำดับที่คนคนนั้นเช็คอินจริง" ไม่ใช่ลำดับบนแผนที่ — คนที่ข้ามฐาน 3
// ไปฐาน 4 สร้างช่วง 2→4 ของตัวเอง ซึ่งเป็นสิ่งที่เกิดขึ้นจริงและควรเห็น การบังคับ
// จับคู่ตามลำดับแผนที่จะได้ตัวเลขของการเดินที่ไม่มีใครเดิน
//
// Walkers = จำนวนคนที่นับได้ในช่วงนี้ · ช่วงที่มีคนเดียวสองคนค่ามัธยฐานแทบไม่มี
// ความหมาย ฝั่งเว็บจึงแสดงจำนวนคนคู่กับเวลาเสมอ ไม่ให้อ่านเวลาลอย ๆ
type LegPace struct {
	FromID     int      `json:"from_id"`
	FromName   string   `json:"from_name"`
	FromNameEn *string  `json:"from_name_en"`
	ToID       int      `json:"to_id"`
	ToName     string   `json:"to_name"`
	ToNameEn   *string  `json:"to_name_en"`
	Walkers    int      `json:"walkers"`
	MedianSec  *float64 `json:"median_sec"`
	P90Sec     *float64 `json:"p90_sec"`
	FastestSec *float64 `json:"fastest_sec"`
	SlowestSec *float64 `json:"slowest_sec"`
}

// CheckpointStep — หนึ่งขั้นของ funnel เรียงตาม sequence
type CheckpointStep struct {
	CheckpointID int     `json:"checkpoint_id"`
	Sequence     *int    `json:"sequence"`
	Name         string  `json:"name"`
	NameEn       *string `json:"name_en"`
	Count        int     `json:"count"`
}

// TimeBucket — จำนวนเหตุการณ์ในหนึ่งช่วงเวลา (ปัดเป็นชั่วโมง)
type TimeBucket struct {
	Bucket string `json:"bucket"`
	Count  int    `json:"count"`
}

type CompletionBucket struct {
	BasesDone    int `json:"bases_done"`
	Participants int `json:"participants"`
}

/* ---------- SOS ---------- */

// SOSStats — สรุปเคสฉุกเฉินทั้งงาน
//
// ค่าเวลาตอบสนองเป็นวินาที และเป็น pointer เพราะ "ยังไม่มีเคสที่ถูกรับ" ต่างจาก
// "รับภายใน 0 วินาที" — ดู header ของไฟล์
type SOSStats struct {
	Total       int          `json:"total"`
	Open        int          `json:"open"`
	Resolved    int          `json:"resolved"`
	Escalated   int          `json:"escalated"`
	ForOther    int          `json:"for_other"`
	Acked       int          `json:"acked"`
	OpenUnacked int          `json:"open_unacked"`
	WithGPS     int          `json:"with_gps"`
	BySeverity  []CountByKey `json:"by_severity"`
	ByReason    []CountByKey `json:"by_reason"`
	ByBase      []CountByKey `json:"by_base"`
	Timeline    []TimeBucket `json:"timeline"`

	AckMedianSec     *float64 `json:"ack_median_sec"`
	AckP90Sec        *float64 `json:"ack_p90_sec"`
	ResolveMedianSec *float64 `json:"resolve_median_sec"`
	ResolveP90Sec    *float64 `json:"resolve_p90_sec"`
}

/* ---------- ความเห็นต่อฐาน ---------- */

// FeedbackStats — คะแนนที่ผู้เข้าร่วมให้แต่ละฐาน
//
// ต่างจาก AdminFeedbackResponse ที่มีอยู่แล้ว (ซึ่งเป็นรายการดิบ + นับ 1/2/3 ตาม
// สเกลเก่า) — ตัวนี้เป็นค่าเฉลี่ยแยกรายมิติบนสเกล 1–5 ที่ migration 000031
// เพิ่มเข้ามา และเว็บยังไม่เคยแสดงเลยสักช่อง
type FeedbackStats struct {
	Responses    int              `json:"responses"`
	Respondents  int              `json:"respondents"`
	AvgOverall   *float64         `json:"avg_overall"`
	Distribution []int            `json:"distribution"` // ดัชนี 0..4 = 1..5 ดาว
	ByCheckpoint []FeedbackByBase `json:"by_checkpoint"`
	Recent       []FeedbackNote   `json:"recent"`
}

// FeedbackByBase — คะแนนเฉลี่ยของฐานเดียว แยกตามมิติที่ถาม
type FeedbackByBase struct {
	CheckpointID int      `json:"checkpoint_id"`
	Sequence     *int     `json:"sequence"`
	Name         string   `json:"name"`
	NameEn       *string  `json:"name_en"`
	Responses    int      `json:"responses"`
	AvgOverall   *float64 `json:"avg_overall"`
	AvgScenery   *float64 `json:"avg_scenery"`
	AvgActivity  *float64 `json:"avg_activity"`
	AvgStaff     *float64 `json:"avg_staff"`
}

// FeedbackNote — ความเห็นที่พิมพ์มาเป็นข้อความ (เอาเฉพาะที่ไม่ว่าง)
// ไม่แนบชื่อผู้ตอบ ต่างจาก AdminFeedbackRow โดยตั้งใจ: หน้านี้เป็นหน้าสถิติ
// ไม่ใช่หน้ารายบุคคล — ใครอยากรู้ว่าใครพูดเปิดที่ /admin/feedback ได้อยู่แล้ว
type FeedbackNote struct {
	CheckpointName string `json:"checkpoint_name"`
	Rating         int    `json:"rating"`
	Comment        string `json:"comment"`
	CreatedAt      string `json:"created_at"`
}

/* ---------- กำลังคน ---------- */

// StaffCoverage — เจ้าหน้าที่มีเท่าไหร่ และครอบคลุมงานครบไหม
type StaffCoverage struct {
	Total            int          `json:"total"`
	Pending          int          `json:"pending"`
	Admins           int          `json:"admins"`
	ByRole           []CountByKey `json:"by_role"`
	BasesTotal       int          `json:"bases_total"`
	BasesWithStaff   int          `json:"bases_with_staff"`
	GroupsTotal      int          `json:"groups_total"`
	GroupsWithStaff  int          `json:"groups_with_staff"`
	CheckedInByStaff int          `json:"checked_in_by_staff"`
}

/* ---------- ประกาศ ---------- */

// NotificationStats — ประกาศที่ส่งไปแล้ว และมีคนอ่านแค่ไหน
type NotificationStats struct {
	Total      int          `json:"total"`
	Active     int          `json:"active"` // ยังไม่หมดอายุ
	ByLevel    []CountByKey `json:"by_level"`
	ByAudience []CountByKey `json:"by_audience"`
	Delivered  int          `json:"delivered"`
	Read       int          `json:"read"`
	Timeline   []TimeBucket `json:"timeline"`
}
