package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

/* ============================================================
   ข้อมูลสำหรับส่งออกเป็น CSV

   คืนเป็น [][]string ตรง ๆ ไม่ผ่าน struct โดยตั้งใจ: ปลายทางคือไฟล์ CSV ซึ่งเป็น
   ตารางข้อความล้วนอยู่แล้ว · การแปลงเป็น struct แล้วแปลงกลับเป็นแถวข้อความเพิ่ม
   ที่ให้พลาดโดยไม่ได้อะไรกลับมา — และทำให้ลำดับคอลัมน์ในหัวตารางกับในแถวหลุด
   จากกันได้ ซึ่งเป็นบั๊กที่อ่านไฟล์แล้วไม่มีทางรู้

   ⚠ ข้อมูลสุขภาพผูกกับความยินยอมเหมือนทุกที่ในระบบ: คนที่ไม่ได้กดยินยอมให้ใช้
   ข้อมูลสุขภาพจะได้ช่องว่างในคอลัมน์สุขภาพ ไม่ใช่ค่าจริง · การส่งออกเป็นไฟล์
   ไม่ใช่ข้อยกเว้นของกติกานั้น — ตรงกันข้าม ไฟล์ที่หลุดออกไปแล้วเรียกคืนไม่ได้
   ============================================================ */

type WBWExportRepository struct {
	db *pgxpool.Pool
}

func NewWBWExportRepository(db *pgxpool.Pool) *WBWExportRepository {
	return &WBWExportRepository{db: db}
}

// ParticipantHeader — ลำดับต้องตรงกับ SELECT ใน Participants() เป๊ะ
var ParticipantHeader = []string{
	"bib", "student_id", "email", "first_name", "last_name", "sex", "date_of_birth",
	"contact_phone", "school", "major", "year", "group_number",
	"checked_in", "leave_quota", "bases_checked_in",
	"blood_type", "weight_kg", "height_cm",
	"food_allergies", "chronic_disease", "medications",
	"emergency_contact_name", "emergency_contact_phone",
	"consent_health_data", "consent_emergency_treatment", "waiver_accepted",
	"registered_at",
}

// Participants — ทุกผู้เข้าร่วมพร้อมข้อมูลที่หน้าเว็บไม่ได้โหลดมาไว้ในตาราง
//
// รวมทุกอย่างไว้ใน query เดียว แทนที่จะให้ฝั่งเว็บยิง /detail ทีละคน — สองพันคน
// คือสองพัน request ซึ่งใช้เวลาเป็นนาทีและถล่ม DB ไปพร้อมกัน
func (r *WBWExportRepository) Participants(ctx context.Context) ([][]string, error) {
	rows, err := r.db.Query(ctx, `
		SELECT COALESCE(p.bib_number::text, ''),
		       COALESCE(u.student_id, ''),
		       -- อีเมลไม่ได้เก็บไว้ในฐานข้อมูล — ระบบไม่เคยถามผู้สมัคร · ที่อยู่ของ
		       -- นักศึกษา มฟล. คือรหัสนักศึกษาต่อท้ายด้วยโดเมนเดียวกันทุกคน ซึ่งเป็น
		       -- กติกาเดียวกับที่ MFUDomain (service/google_identity.go) ใช้ตรวจตอน
		       -- เข้าสู่ระบบด้วย Google อยู่แล้ว · ประกอบตรงนี้แทนการเพิ่มคอลัมน์ที่
		       -- จะต้องคอยดูแลให้ตรงกับรหัสนักศึกษาตลอดไป
		       -- คนที่ไม่มีรหัสนักศึกษาได้ช่องว่าง ไม่ใช่ "@lamduan.mfu.ac.th" ลอย ๆ
		       -- (NULL || text = NULL แล้ว COALESCE เก็บให้เป็น '')
		       COALESCE(u.student_id || '@lamduan.mfu.ac.th', ''),
		       COALESCE(p.first_name, ''), COALESCE(p.last_name, ''),
		       COALESCE(p.sex::text, ''),
		       COALESCE(to_char(p.date_of_birth, 'YYYY-MM-DD'), ''),
		       COALESCE(p.contact_phone, ''),
		       COALESCE(s.name, ''), COALESCE(p.major, ''),
		       COALESCE(p.year::text, ''),
		       COALESCE(g.group_number::text, ''),
		       CASE WHEN COALESCE(p.checked_in, FALSE) THEN 'yes' ELSE 'no' END,
		       COALESCE(p.leave_quota::text, ''),
		       (SELECT count(*)::text
		          FROM check_in ci
		          JOIN checkpoint c ON c.checkpoint_id = ci.checkpoint_id AND c.requires_checkin
		         WHERE ci.participant_id = u.user_id),
		       -- คอลัมน์สุขภาพเปิดเฉพาะคนที่ยินยอม · เงื่อนไขอยู่ใน SQL ไม่ใช่ใน Go
		       -- ด้วยเหตุผลเดียวกับ sosStaffSelect: ตรรกะความเป็นส่วนตัวควรอยู่ติดกับ
		       -- ข้อมูล ไม่ใช่กระจายอยู่ในโค้ดที่เรียกใช้
		       CASE WHEN co.consent_health_data THEN COALESCE(h.blood_type::text, '') ELSE '' END,
		       CASE WHEN co.consent_health_data THEN COALESCE(h.weight_kg::text, '') ELSE '' END,
		       CASE WHEN co.consent_health_data THEN COALESCE(h.height_cm::text, '') ELSE '' END,
		       CASE WHEN co.consent_health_data THEN COALESCE(h.food_allergies, '') ELSE '' END,
		       CASE WHEN co.consent_health_data THEN COALESCE(h.chronic_disease, '') ELSE '' END,
		       CASE WHEN co.consent_health_data THEN COALESCE(h.medications, '') ELSE '' END,
		       COALESCE(p.emergency_contact_name, ''), COALESCE(p.emergency_contact_phone, ''),
		       CASE WHEN co.consent_health_data         THEN 'yes' ELSE 'no' END,
		       CASE WHEN co.consent_emergency_treatment THEN 'yes' ELSE 'no' END,
		       CASE WHEN co.waiver_accepted             THEN 'yes' ELSE 'no' END,
		       to_char(u.created_at AT TIME ZONE 'Asia/Bangkok', 'YYYY-MM-DD HH24:MI')
		  FROM wbw_user u
		  LEFT JOIN participant_profile p ON p.user_id = u.user_id
		  LEFT JOIN school            s  ON s.school_id = p.school_id
		  LEFT JOIN participant_group g  ON g.group_id  = p.group_id
		  LEFT JOIN health_details    h  ON h.user_id   = u.user_id
		  LEFT JOIN consent           co ON co.user_id  = u.user_id
		 WHERE u.role = 'participant'
		 ORDER BY p.bib_number NULLS LAST, u.student_id`)
	if err != nil {
		return nil, err
	}
	return collect(rows, len(ParticipantHeader))
}

// StaffHeader — ลำดับต้องตรงกับ SELECT ใน Staff()
var StaffHeader = []string{
	"username", "display_name", "role", "status", "staff_role",
	"school", "major", "bases_assigned", "groups_assigned", "checkins_scanned", "created_at",
}

// Staff — เจ้าหน้าที่และผู้ดูแลทุกคน พร้อมภาระงานที่ถืออยู่จริง
//
// bases/groups/checkins ไม่ได้อยู่ในหน้ารายชื่อเจ้าหน้าที่ แต่เป็นสิ่งที่คนเปิด
// ไฟล์นี้อยากรู้: ใครรับผิดชอบอะไร และใครทำงานหน้างานจริงบ้าง
func (r *WBWExportRepository) Staff(ctx context.Context) ([][]string, error) {
	rows, err := r.db.Query(ctx, `
		SELECT u.username,
		       COALESCE(u.display_name, ''),
		       u.role::text,
		       u.status::text,
		       COALESCE(st.staff_role::text, ''),
		       COALESCE(s.name, ''),
		       COALESCE(st.major, ''),
		       (SELECT count(*)::text FROM checkpoint_staff cs WHERE cs.user_id = u.user_id),
		       (SELECT count(*)::text FROM group_staff gs      WHERE gs.user_id = u.user_id),
		       (SELECT count(*)::text FROM check_in ci         WHERE ci.staff_id = u.user_id),
		       to_char(u.created_at AT TIME ZONE 'Asia/Bangkok', 'YYYY-MM-DD HH24:MI')
		  FROM wbw_user u
		  LEFT JOIN wbw_staff st ON st.user_id = u.user_id
		  LEFT JOIN school    s  ON s.school_id = st.school_id
		 WHERE u.role IN ('staff', 'admin')
		 ORDER BY u.role, u.username`)
	if err != nil {
		return nil, err
	}
	return collect(rows, len(StaffHeader))
}

// collect — อ่านทุกแถวเป็น []string · width มาจากหัวตารางเพื่อให้ scan พังทันที
// ถ้ามีคนเพิ่มคอลัมน์ใน SELECT แล้วลืมเพิ่มในหัวตาราง (หรือกลับกัน)
func collect(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close()
}, width int) ([][]string, error) {
	defer rows.Close()

	out := [][]string{}
	for rows.Next() {
		cells := make([]string, width)
		targets := make([]any, width)
		for i := range cells {
			targets[i] = &cells[i]
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, err
		}
		out = append(out, cells)
	}
	return out, rows.Err()
}
