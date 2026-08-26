package repository

import (
	"context"

	"su-server/internal/model"

	"github.com/jackc/pgx/v5/pgxpool"
)

/* ============================================================
   Analytics — query สรุปสำหรับแท็บ "วิเคราะห์" ของแผงผู้ดูแล

   แยกไฟล์จาก wbw_admin_repository.go เพราะที่นั่นเป็น CRUD ของรายการที่แก้ได้
   ส่วนที่นี่อ่านอย่างเดียวและไม่มี query ไหนถูกเรียกจากที่อื่นเลย · ปนกันแล้ว
   ไฟล์เดิมจะยาวขึ้นเท่าตัวโดยไม่มีใครได้อะไร

   ทุก query อ่านตารางเล็ก (ผู้เข้าร่วมหลักพัน เคส SOS หลักสิบ) และ endpoint นี้
   เรียกได้เฉพาะแอดมิน จึงยิงเรียงกันไปตรง ๆ ไม่ต้อง goroutine — ความซับซ้อนของ
   การรวมผลกับ error หลายเส้นทางแพงกว่าเวลาที่ประหยัดได้จริงหลายเท่า
   ============================================================ */

// eventTZ — โซนเวลาที่ใช้ "ปัด" วันและชั่วโมงของทุกกราฟในไฟล์นี้
//
// คอลัมน์เป็น timestamptz และ Postgres คุยเป็น UTC · ถ้าปัดวันตรง ๆ โดยไม่แปลง
// โซน คนที่สมัครตอนหนึ่งทุ่มของวันที่ 5 (= 12:00Z) ยังอยู่ในถังวันที่ 5 ก็จริง
// แต่คนที่สมัครตอนตีหนึ่งของวันที่ 6 (= 18:00Z ของวันที่ 5) จะไปโผล่ในถังวันที่ 5
// ด้วย — แท่ง "ยอดสมัครรายวัน" จะเลื่อนไปเจ็ดชั่วโมงตลอดทั้งกราฟ และผิดหนักที่สุด
// ตรงกราฟรายชั่วโมงของวันงาน ซึ่งเป็นกราฟที่คนดูตอนงานกำลังเดินอยู่
const eventTZ = "Asia/Bangkok"

type WBWAnalyticsRepository struct {
	db *pgxpool.Pool
}

func NewWBWAnalyticsRepository(db *pgxpool.Pool) *WBWAnalyticsRepository {
	return &WBWAnalyticsRepository{db: db}
}

// countRows อ่านผลลัพธ์รูป (key, count) ซึ่งเป็นรูปของกราฟแท่ง/โดนัทเกือบทุกอัน
// ในไฟล์นี้ · key เป็น NULL ได้ (เช่น severity ที่ยังไม่ประเมิน) — คืนเป็น ""
// แล้วให้ฝั่งเว็บเลือกคำแปลเอง ห้ามใส่คำไทยลงใน SQL เพราะแผงนี้สองภาษา
func (r *WBWAnalyticsRepository) countRows(ctx context.Context, sql string, args ...any) ([]model.CountByKey, error) {
	rows, err := r.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.CountByKey{}
	for rows.Next() {
		var c model.CountByKey
		var key *string
		if err := rows.Scan(&key, &c.Count); err != nil {
			return nil, err
		}
		if key != nil {
			c.Key = *key
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// bucketRows อ่านผลลัพธ์รูป (ช่วงเวลา, count) สำหรับกราฟเส้นตามเวลา
func (r *WBWAnalyticsRepository) bucketRows(ctx context.Context, sql string) ([]model.TimeBucket, error) {
	rows, err := r.db.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.TimeBucket{}
	for rows.Next() {
		var b model.TimeBucket
		if err := rows.Scan(&b.Bucket, &b.Count); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

/* ---------- ที่นั่ง ---------- */

func (r *WBWAnalyticsRepository) capacity(ctx context.Context) (model.AnalyticsCapacity, error) {
	var c model.AnalyticsCapacity
	// wbw_capacity มีแถวเดียวเสมอ (PK เป็น boolean ที่ CHECK บังคับให้เป็น true)
	// แต่ถ้า seed หายไปก็ยังต้องตอบได้ — COALESCE กับ subquery ทำให้ไม่มีเคส ErrNoRows
	err := r.db.QueryRow(ctx, `
		SELECT COALESCE((SELECT max_participants FROM wbw_capacity), 0),
		       COALESCE((SELECT taken FROM wbw_capacity),
		                (SELECT count(*)::int FROM wbw_user WHERE role = 'participant')),
		       (SELECT count(*)::int FROM participant_profile WHERE checked_in)
	`).Scan(&c.Max, &c.Taken, &c.CheckedIn)
	c.SeatsLeft = c.Max - c.Taken
	if c.SeatsLeft < 0 {
		c.SeatsLeft = 0
	}
	return c, err
}

/* ---------- ยอดสมัครรายวัน ---------- */

func (r *WBWAnalyticsRepository) registration(ctx context.Context) ([]model.RegistrationDay, error) {
	// ยอดสะสมคำนวณด้วย window function บน aggregate — sum(count(*)) OVER (ORDER BY วัน)
	// ทำให้เส้นสะสมมาจากที่เดียวกับแท่งรายวันเสมอ แม้ฝั่งเว็บจะกรองอะไรทิ้งไป
	rows, err := r.db.Query(ctx, `
		SELECT day, n::int, sum(n) OVER (ORDER BY day)::int
		  FROM (
		    SELECT to_char(created_at AT TIME ZONE '`+eventTZ+`', 'YYYY-MM-DD') AS day,
		           count(*) AS n
		      FROM wbw_user
		     WHERE role = 'participant'
		     GROUP BY 1
		  ) t
		 ORDER BY day`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.RegistrationDay{}
	for rows.Next() {
		var d model.RegistrationDay
		if err := rows.Scan(&d.Day, &d.Count, &d.Cumulative); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

/* ---------- ประชากร ---------- */

func (r *WBWAnalyticsRepository) demographics(ctx context.Context) (model.Demographics, error) {
	var d model.Demographics

	// นับจาก participant_profile ไม่ใช่ wbw_user — คนที่สมัครค้างกลางคันยังไม่มี
	// แถว profile และไม่ควรถูกนับเป็น "เพศไม่ระบุ" หรือ "ไม่มีสำนักวิชา"
	err := r.db.QueryRow(ctx, `SELECT count(*)::int FROM participant_profile`).Scan(&d.Profiled)
	if err != nil {
		return d, err
	}

	if d.Sex, err = r.countRows(ctx, `
		SELECT sex::text, count(*)::int FROM participant_profile GROUP BY 1 ORDER BY 2 DESC`); err != nil {
		return d, err
	}
	// เรียงตามชั้นปี ไม่ใช่ตามจำนวน — แกนนี้มีลำดับในตัวเอง สลับที่แล้วอ่านไม่รู้เรื่อง
	if d.Year, err = r.countRows(ctx, `
		SELECT year::text, count(*)::int FROM participant_profile GROUP BY 1 ORDER BY year NULLS LAST`); err != nil {
		return d, err
	}
	// ผูกกับ participant_profile เพื่อให้ "ไม่ทราบกรุ๊ปเลือด" (ไม่มีแถว health_details)
	// ถูกนับด้วย — สำคัญกว่าค่าอื่นในบล็อกนี้ เพราะเป็นตัวเลขที่ทีมแพทย์ต้องรู้ก่อนวันงาน
	if d.Blood, err = r.countRows(ctx, `
		SELECT h.blood_type::text, count(*)::int
		  FROM participant_profile p
		  LEFT JOIN health_details h ON h.user_id = p.user_id
		 GROUP BY 1 ORDER BY 2 DESC`); err != nil {
		return d, err
	}

	rows, err := r.db.Query(ctx, `
		SELECT p.school_id, COALESCE(s.name, ''), count(*)::int,
		       count(*) FILTER (WHERE p.checked_in)::int
		  FROM participant_profile p
		  LEFT JOIN school s ON s.school_id = p.school_id
		 GROUP BY 1, 2 ORDER BY 3 DESC, 2`)
	if err != nil {
		return d, err
	}
	defer rows.Close()

	d.School = []model.SchoolBreakdown{}
	for rows.Next() {
		var s model.SchoolBreakdown
		if err := rows.Scan(&s.SchoolID, &s.Name, &s.Count, &s.CheckedIn); err != nil {
			return d, err
		}
		d.School = append(d.School, s)
	}
	return d, rows.Err()
}

/* ---------- กลุ่ม ---------- */

func (r *WBWAnalyticsRepository) groups(ctx context.Context) (model.GroupStats, error) {
	var g model.GroupStats
	err := r.db.QueryRow(ctx, `
		SELECT (SELECT count(*)::int FROM participant_group),
		       (SELECT count(*) FILTER (WHERE member_count >= capacity)::int FROM participant_group),
		       (SELECT count(*) FILTER (WHERE member_count = 0)::int FROM participant_group),
		       (SELECT COALESCE(sum(capacity), 0)::int FROM participant_group),
		       (SELECT count(*) FILTER (WHERE group_id IS NOT NULL)::int FROM participant_profile),
		       (SELECT count(*) FILTER (WHERE group_id IS NULL)::int FROM participant_profile)
	`).Scan(&g.Total, &g.Full, &g.Empty, &g.Seats, &g.Assigned, &g.Unassigned)
	if err != nil {
		return g, err
	}

	rows, err := r.db.Query(ctx, `
		SELECT g.group_id, g.group_number, g.capacity, g.member_count,
		       (SELECT count(*)::int FROM group_staff gs WHERE gs.group_id = g.group_id)
		  FROM participant_group g
		 ORDER BY g.group_number`)
	if err != nil {
		return g, err
	}
	defer rows.Close()

	g.Items = []model.GroupFill{}
	for rows.Next() {
		var f model.GroupFill
		if err := rows.Scan(&f.GroupID, &f.GroupNumber, &f.Capacity, &f.MemberCount, &f.StaffCount); err != nil {
			return g, err
		}
		g.Items = append(g.Items, f)
	}
	return g, rows.Err()
}

/* ---------- เช็คอิน ---------- */

func (r *WBWAnalyticsRepository) checkins(ctx context.Context) (model.CheckinStats, error) {
	var c model.CheckinStats
	err := r.db.QueryRow(ctx, `
		SELECT count(*)::int, count(DISTINCT participant_id)::int FROM check_in
	`).Scan(&c.Total, &c.Walkers)
	if err != nil {
		return c, err
	}

	// funnel — เฉพาะฐานที่ต้องเช็คอิน เรียงตามลำดับบนเส้นทาง (ฐานบริการอย่าง
	// เส้นชัย/ห้องน้ำไม่ใช่ขั้นของ funnel) · กรองเหมือน BasesOverview ไม่งั้น
	// ตัวเลขบนหน้าเดียวกันสองที่จะไม่ตรงกัน
	rows, err := r.db.Query(ctx, `
		SELECT c.checkpoint_id, c.sequence, c.name, c.name_en,
		       (SELECT count(*)::int FROM check_in ci WHERE ci.checkpoint_id = c.checkpoint_id)
		  FROM checkpoint c
		 WHERE c.requires_checkin
		 ORDER BY c.sequence NULLS LAST, c.checkpoint_id`)
	if err != nil {
		return c, err
	}
	c.Funnel = []model.CheckpointStep{}
	for rows.Next() {
		var s model.CheckpointStep
		if err := rows.Scan(&s.CheckpointID, &s.Sequence, &s.Name, &s.NameEn, &s.Count); err != nil {
			rows.Close()
			return c, err
		}
		c.Funnel = append(c.Funnel, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return c, err
	}

	if c.Timeline, err = r.bucketRows(ctx, `
		SELECT to_char(date_trunc('hour', server_received_at AT TIME ZONE '`+eventTZ+`'), 'YYYY-MM-DD HH24:00'),
		       count(*)::int
		  FROM check_in GROUP BY 1 ORDER BY 1`); err != nil {
		return c, err
	}

	// กี่คนเดินได้กี่ฐาน — ถัง 0 คือคนที่สมัครแล้วแต่ยังไม่เช็คอินฐานไหนเลย
	// ซึ่งเป็นถังที่ใหญ่ที่สุดก่อนวันงานและต้องเห็น ไม่ใช่ซ่อน
	crows, err := r.db.Query(ctx, `
		SELECT n::int, count(*)::int
		  FROM (
		    SELECT (SELECT count(*)
		              FROM check_in ci
		              JOIN checkpoint c ON c.checkpoint_id = ci.checkpoint_id AND c.requires_checkin
		             WHERE ci.participant_id = p.user_id) AS n
		      FROM participant_profile p
		  ) t
		 GROUP BY n ORDER BY n`)
	if err != nil {
		return c, err
	}
	c.Completion = []model.CompletionBucket{}
	for crows.Next() {
		var b model.CompletionBucket
		if err := crows.Scan(&b.BasesDone, &b.Participants); err != nil {
			crows.Close()
			return c, err
		}
		c.Completion = append(c.Completion, b)
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return c, err
	}

	if c.ByStaff, err = r.countRows(ctx, `
		SELECT COALESCE(u.display_name, u.username), count(*)::int
		  FROM check_in ci JOIN wbw_user u ON u.user_id = ci.staff_id
		 GROUP BY 1 ORDER BY 2 DESC LIMIT 12`); err != nil {
		return c, err
	}

	// เวลารวมบนเส้นทางต่อคน — เช็คอินครั้งสุดท้ายลบครั้งแรกของคนคนนั้น
	//
	// HAVING count(*) > 1 ไม่ใช่การกรองข้อมูลทิ้ง แต่เป็นการไม่นับสิ่งที่ไม่มีอยู่:
	// คนที่เช็คอินฐานเดียวไม่มี "ช่วงเวลา" ให้วัด ถ้าปล่อยเข้ามาจะเป็นศูนย์ทุกแถว
	// แล้วดึงค่ามัธยฐานของทั้งงานลงมาตามจำนวนคนที่เพิ่งเริ่มเดิน
	if err = r.db.QueryRow(ctx, `
		SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY span),
		       percentile_cont(0.9) WITHIN GROUP (ORDER BY span)
		  FROM (
		    SELECT extract(epoch FROM (max(ci.server_received_at) - min(ci.server_received_at))) AS span
		      FROM check_in ci
		      JOIN checkpoint c ON c.checkpoint_id = ci.checkpoint_id AND c.requires_checkin
		     GROUP BY ci.participant_id
		    HAVING count(*) > 1
		  ) t`).Scan(&c.TotalMedianSec, &c.TotalP90Sec); err != nil {
		return c, err
	}

	c.Pace, err = r.pace(ctx)
	return c, err
}

// pace — เวลาที่ใช้ระหว่างเช็คอินสองครั้งที่ติดกันของคนคนเดียวกัน
//
// lag() เรียงตาม server_received_at ไม่ใช่ตาม checkpoint.sequence · สองอย่างนี้ให้
// คำตอบต่างกันจริงและอันนี้คือคำถามที่ถูก: "เวลาระหว่างการเช็คอิน" คือช่วงที่คน
// เดินจริงจากจุดที่เพิ่งสแกนไปยังจุดที่สแกนถัดไป ถ้าเรียงตามลำดับบนแผนที่แทน
// คนที่เดินสลับฐาน (ซึ่งเกิดขึ้นจริงเมื่อฐานหนึ่งคิวยาว) จะได้ผลต่างติดลบ แล้ว
// ค่ามัธยฐานของช่วงนั้นจะเพี้ยนโดยไม่มีใครรู้ว่าทำไม · เรียงตามเวลาแล้วผลต่าง
// เป็นบวกเสมอตามนิยาม
//
// คู่ (from, to) จึงเป็น "เส้นทางที่คนเดินจริง" ไม่ใช่เส้นทางบนแผนที่ — คนที่ข้าม
// ฐาน 3 ไปฐาน 4 สร้างแถว 2→4 ของตัวเองแยกออกมา ซึ่งเป็นข้อมูลที่ผู้จัดใช้ได้
// (มีคนข้ามฐานนี้กี่คน และข้ามไปไหน) ไม่ใช่ของเสียที่ต้องกลบ
//
// เรียงผลลัพธ์ตาม sequence ของฐานต้นทาง เพื่อให้อ่านไล่ไปตามเส้นทางได้ ไม่ใช่
// เรียงตามความช้า — ตารางที่สลับลำดับตามข้อมูลเทียบข้ามรอบไม่ได้
func (r *WBWAnalyticsRepository) pace(ctx context.Context) ([]model.LegPace, error) {
	rows, err := r.db.Query(ctx, `
		WITH legs AS (
		  SELECT lag(ci.checkpoint_id) OVER w AS from_id,
		         ci.checkpoint_id              AS to_id,
		         extract(epoch FROM (ci.server_received_at - lag(ci.server_received_at) OVER w)) AS sec
		    FROM check_in ci
		    JOIN checkpoint c ON c.checkpoint_id = ci.checkpoint_id AND c.requires_checkin
		  WINDOW w AS (PARTITION BY ci.participant_id ORDER BY ci.server_received_at)
		)
		SELECT l.from_id, cf.name, cf.name_en,
		       l.to_id,   ct.name, ct.name_en,
		       count(*)::int,
		       percentile_cont(0.5) WITHIN GROUP (ORDER BY l.sec),
		       percentile_cont(0.9) WITHIN GROUP (ORDER BY l.sec),
		       min(l.sec), max(l.sec)
		  FROM legs l
		  JOIN checkpoint cf ON cf.checkpoint_id = l.from_id
		  JOIN checkpoint ct ON ct.checkpoint_id = l.to_id
		 WHERE l.from_id IS NOT NULL
		 GROUP BY l.from_id, cf.name, cf.name_en, cf.sequence,
		          l.to_id,   ct.name, ct.name_en, ct.sequence
		 ORDER BY cf.sequence NULLS LAST, ct.sequence NULLS LAST`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.LegPace{}
	for rows.Next() {
		var l model.LegPace
		if err := rows.Scan(&l.FromID, &l.FromName, &l.FromNameEn,
			&l.ToID, &l.ToName, &l.ToNameEn,
			&l.Walkers, &l.MedianSec, &l.P90Sec, &l.FastestSec, &l.SlowestSec); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

/* ---------- SOS ---------- */

func (r *WBWAnalyticsRepository) sos(ctx context.Context) (model.SOSStats, error) {
	var s model.SOSStats

	// เวลาตอบสนองนับจาก server_received_at ไม่ใช่ device_time — device_time มาจาก
	// นาฬิกาเครื่องผู้ใช้ซึ่งเพี้ยนได้เป็นนาที และเคยเห็นเครื่องที่ตั้งเวลาผิดวัน
	// ค่ามัธยฐานที่คำนวณจากมันจะกลายเป็นติดลบโดยไม่มีใครเข้าใจว่าทำไม
	//
	// FILTER บน percentile_cont ใส่ไว้ให้ชัดว่าตั้งใจนับเฉพาะเคสที่ถึงสถานะนั้นจริง
	// (ordered-set aggregate ทิ้ง NULL ให้อยู่แล้ว แต่เขียนไว้ไม่ให้ต้องเดา)
	err := r.db.QueryRow(ctx, `
		SELECT count(*)::int,
		       count(*) FILTER (WHERE NOT resolved)::int,
		       count(*) FILTER (WHERE resolved)::int,
		       count(*) FILTER (WHERE escalated)::int,
		       count(*) FILTER (WHERE for_other)::int,
		       count(*) FILTER (WHERE acked_at IS NOT NULL)::int,
		       count(*) FILTER (WHERE NOT resolved AND acked_at IS NULL)::int,
		       count(*) FILTER (WHERE lat IS NOT NULL AND lng IS NOT NULL)::int,
		       percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM (acked_at    - server_received_at)))
		         FILTER (WHERE acked_at    IS NOT NULL),
		       percentile_cont(0.9) WITHIN GROUP (ORDER BY extract(epoch FROM (acked_at    - server_received_at)))
		         FILTER (WHERE acked_at    IS NOT NULL),
		       percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM (resolved_at - server_received_at)))
		         FILTER (WHERE resolved_at IS NOT NULL),
		       percentile_cont(0.9) WITHIN GROUP (ORDER BY extract(epoch FROM (resolved_at - server_received_at)))
		         FILTER (WHERE resolved_at IS NOT NULL)
		  FROM sos_event`).Scan(
		&s.Total, &s.Open, &s.Resolved, &s.Escalated, &s.ForOther,
		&s.Acked, &s.OpenUnacked, &s.WithGPS,
		&s.AckMedianSec, &s.AckP90Sec, &s.ResolveMedianSec, &s.ResolveP90Sec)
	if err != nil {
		return s, err
	}

	if s.BySeverity, err = r.countRows(ctx, `
		SELECT severity, count(*)::int FROM sos_event GROUP BY 1 ORDER BY 2 DESC`); err != nil {
		return s, err
	}
	if s.ByReason, err = r.countRows(ctx, `
		SELECT NULLIF(btrim(resolve_reason), ''), count(*)::int
		  FROM sos_event WHERE resolved GROUP BY 1 ORDER BY 2 DESC`); err != nil {
		return s, err
	}
	// ฐานที่ผูกกับเคสคือฐานที่คนกดเช็คอินล่าสุด (ดู LastCheckinCheckpoint) —
	// อ่านว่า "กดตอนอยู่ช่วงไหนของเส้นทาง" ไม่ใช่พิกัดจริงตอนกด
	if s.ByBase, err = r.countRows(ctx, `
		SELECT c.name, count(*)::int
		  FROM sos_event e LEFT JOIN checkpoint c ON c.checkpoint_id = e.checkpoint_id
		 GROUP BY 1 ORDER BY 2 DESC`); err != nil {
		return s, err
	}
	s.Timeline, err = r.bucketRows(ctx, `
		SELECT to_char(date_trunc('hour', server_received_at AT TIME ZONE '`+eventTZ+`'), 'YYYY-MM-DD HH24:00'),
		       count(*)::int
		  FROM sos_event GROUP BY 1 ORDER BY 1`)
	return s, err
}

/* ---------- ความเห็นต่อฐาน ---------- */

func (r *WBWAnalyticsRepository) feedback(ctx context.Context) (model.FeedbackStats, error) {
	f := model.FeedbackStats{Distribution: make([]int, 5)}

	// JOIN requires_checkin เหมือน ListAll/SummaryByCheckpoint — สามที่นี้ต้องกรอง
	// เหมือนกันเสมอ ไม่งั้นยอดรวมบนหน้าเดียวกันจะไม่ตรงกันโดยไม่มีใครหาสาเหตุเจอ
	err := r.db.QueryRow(ctx, `
		SELECT count(*)::int, count(DISTINCT f.participant_id)::int, avg(f.rating)::float8,
		       count(*) FILTER (WHERE f.rating = 1)::int,
		       count(*) FILTER (WHERE f.rating = 2)::int,
		       count(*) FILTER (WHERE f.rating = 3)::int,
		       count(*) FILTER (WHERE f.rating = 4)::int,
		       count(*) FILTER (WHERE f.rating = 5)::int
		  FROM checkin_feedback f
		  JOIN checkpoint c ON c.checkpoint_id = f.checkpoint_id AND c.requires_checkin`).Scan(
		&f.Responses, &f.Respondents, &f.AvgOverall,
		&f.Distribution[0], &f.Distribution[1], &f.Distribution[2], &f.Distribution[3], &f.Distribution[4])
	if err != nil {
		return f, err
	}

	// LEFT JOIN — ฐานที่ยังไม่มีใครให้คะแนนต้องอยู่ในกราฟด้วย (แถวว่างพร้อม null)
	// ฐานที่หายไปจากกราฟดูเหมือนฐานที่ไม่มีปัญหา ทั้งที่แปลว่าไม่มีข้อมูลเลย
	rows, err := r.db.Query(ctx, `
		SELECT c.checkpoint_id, c.sequence, c.name, c.name_en, count(f.id)::int,
		       avg(f.rating)::float8, avg(f.rating_scenery)::float8,
		       avg(f.rating_activity)::float8, avg(f.rating_staff)::float8
		  FROM checkpoint c
		  LEFT JOIN checkin_feedback f ON f.checkpoint_id = c.checkpoint_id
		 WHERE c.requires_checkin
		 GROUP BY c.checkpoint_id
		 ORDER BY c.sequence NULLS LAST, c.checkpoint_id`)
	if err != nil {
		return f, err
	}
	f.ByCheckpoint = []model.FeedbackByBase{}
	for rows.Next() {
		var b model.FeedbackByBase
		if err := rows.Scan(&b.CheckpointID, &b.Sequence, &b.Name, &b.NameEn, &b.Responses,
			&b.AvgOverall, &b.AvgScenery, &b.AvgActivity, &b.AvgStaff); err != nil {
			rows.Close()
			return f, err
		}
		f.ByCheckpoint = append(f.ByCheckpoint, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return f, err
	}

	nrows, err := r.db.Query(ctx, `
		SELECT c.name, f.rating, btrim(f.comment), f.created_at::text
		  FROM checkin_feedback f
		  JOIN checkpoint c ON c.checkpoint_id = f.checkpoint_id AND c.requires_checkin
		 WHERE btrim(COALESCE(f.comment, '')) <> ''
		 ORDER BY f.created_at DESC LIMIT 20`)
	if err != nil {
		return f, err
	}
	defer nrows.Close()

	f.Recent = []model.FeedbackNote{}
	for nrows.Next() {
		var n model.FeedbackNote
		if err := nrows.Scan(&n.CheckpointName, &n.Rating, &n.Comment, &n.CreatedAt); err != nil {
			return f, err
		}
		f.Recent = append(f.Recent, n)
	}
	return f, nrows.Err()
}

/* ---------- กำลังคน ---------- */

func (r *WBWAnalyticsRepository) staff(ctx context.Context) (model.StaffCoverage, error) {
	var s model.StaffCoverage
	err := r.db.QueryRow(ctx, `
		SELECT (SELECT count(*)::int FROM wbw_user WHERE role = 'staff' AND status = 'approved'),
		       (SELECT count(*)::int FROM wbw_user WHERE status = 'pending'),
		       (SELECT count(*)::int FROM wbw_user WHERE role = 'admin'),
		       (SELECT count(*)::int FROM checkpoint WHERE requires_checkin),
		       (SELECT count(DISTINCT cs.checkpoint_id)::int
		          FROM checkpoint_staff cs
		          JOIN checkpoint c ON c.checkpoint_id = cs.checkpoint_id AND c.requires_checkin),
		       (SELECT count(*)::int FROM participant_group),
		       (SELECT count(DISTINCT group_id)::int FROM group_staff),
		       (SELECT count(*)::int FROM check_in WHERE staff_id IS NOT NULL)
	`).Scan(&s.Total, &s.Pending, &s.Admins, &s.BasesTotal, &s.BasesWithStaff,
		&s.GroupsTotal, &s.GroupsWithStaff, &s.CheckedInByStaff)
	if err != nil {
		return s, err
	}

	// staff_role อยู่ใน wbw_staff ซึ่งมีแถวเฉพาะคนที่สมัครผ่านหน้า /staff/register
	// บัญชีที่แอดมินสร้างให้เองไม่มีแถว — ผลรวมของกราฟนี้จึงน้อยกว่า Total ได้
	s.ByRole, err = r.countRows(ctx, `
		SELECT staff_role::text, count(*)::int FROM wbw_staff GROUP BY 1 ORDER BY 2 DESC`)
	return s, err
}

/* ---------- ประกาศ ---------- */

func (r *WBWAnalyticsRepository) notifications(ctx context.Context) (model.NotificationStats, error) {
	var n model.NotificationStats
	err := r.db.QueryRow(ctx, `
		SELECT count(*)::int,
		       count(*) FILTER (WHERE expires_at IS NULL OR expires_at > now())::int,
		       (SELECT count(*)::int FROM notification_read WHERE delivered_at IS NOT NULL),
		       (SELECT count(*)::int FROM notification_read WHERE read_at IS NOT NULL)
		  FROM notification`).Scan(&n.Total, &n.Active, &n.Delivered, &n.Read)
	if err != nil {
		return n, err
	}

	if n.ByLevel, err = r.countRows(ctx, `
		SELECT level::text, count(*)::int FROM notification GROUP BY 1 ORDER BY 2 DESC`); err != nil {
		return n, err
	}
	if n.ByAudience, err = r.countRows(ctx, `
		SELECT audience::text, count(*)::int FROM notification GROUP BY 1 ORDER BY 2 DESC`); err != nil {
		return n, err
	}
	n.Timeline, err = r.bucketRows(ctx, `
		SELECT to_char(created_at AT TIME ZONE '`+eventTZ+`', 'YYYY-MM-DD'), count(*)::int
		  FROM notification GROUP BY 1 ORDER BY 1`)
	return n, err
}

/* ---------- ประกอบร่าง ---------- */

// Analytics — รวมทุกบล็อกเป็นคำตอบเดียว
//
// error ตัวแรกที่เจอคือจบ ไม่ตอบครึ่งเดียว: หน้าที่ได้ก้อนไม่ครบจะวาดกราฟว่าง
// ให้ดูเหมือน "ไม่มีข้อมูล" ทั้งที่ความจริงคือ query พัง ซึ่งเป็นคนละเรื่องกัน
// และเป็นเรื่องที่คนดูแลระบบต้องรู้
func (r *WBWAnalyticsRepository) Analytics(ctx context.Context) (*model.Analytics, error) {
	var a model.Analytics
	var err error

	if a.Capacity, err = r.capacity(ctx); err != nil {
		return nil, err
	}
	if a.Registration, err = r.registration(ctx); err != nil {
		return nil, err
	}
	if a.Demographics, err = r.demographics(ctx); err != nil {
		return nil, err
	}
	if a.Groups, err = r.groups(ctx); err != nil {
		return nil, err
	}
	if a.Checkins, err = r.checkins(ctx); err != nil {
		return nil, err
	}
	if a.SOS, err = r.sos(ctx); err != nil {
		return nil, err
	}
	if a.Feedback, err = r.feedback(ctx); err != nil {
		return nil, err
	}
	if a.Staff, err = r.staff(ctx); err != nil {
		return nil, err
	}
	if a.Notifications, err = r.notifications(ctx); err != nil {
		return nil, err
	}
	return &a, nil
}
