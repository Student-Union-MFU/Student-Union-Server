package repository

import (
	"context"
	"errors"
	"strconv"

	"su-server/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrSOSNotFound        = errors.New("sos case not found")
	ErrSOSAlreadyAcked    = errors.New("sos case already acknowledged")
	ErrSOSTooLateToCancel = errors.New("sos case too old to cancel")
)

// cancelWindowSeconds — ฝั่ง UI นับถอยหลัง 15 วิ แต่ server ยอมรับถึง 120
//
// เหตุผล: เคสที่ค้างอยู่ใน outbox 40 วิก่อนจะออกไปได้จริง ถ้าใช้ 15 วิเท่า UI
// การกดยกเลิกอย่างสมเหตุสมผลจะได้ 409 กลับมา และคนกดจะสรุปว่าระบบพัง
// ในนาทีที่แย่ที่สุดที่จะสรุปแบบนั้น
const cancelWindowSeconds = 120

type WBWSOSRepository struct {
	db *pgxpool.Pool
}

func NewWBWSOSRepository(db *pgxpool.Pool) *WBWSOSRepository {
	return &WBWSOSRepository{db: db}
}

// CheckpointGeo — ฐานพร้อมพิกัด · ชนิดของ repository เอง ไม่ใช่ service.CheckpointPoint
// เพราะ repository ห้าม import service (จะเป็น import cycle)
type CheckpointGeo struct {
	ID   int
	Name string
	Lat  float64
	Lng  float64
}

// Checkpoints — ฐานทุกจุดที่มีพิกัด รวมจุดบริการด้วย
// จุดบริการ (requires_checkin = false) นับเป็นฐานใกล้สุดได้ — คนเจ็บใกล้จุดห้องน้ำ
// ควรได้ทีมที่อยู่จุดห้องน้ำ ไม่ใช่ทีมที่อยู่ไกลกว่าเพียงเพราะฐานนั้นสแกน QR
func (r *WBWSOSRepository) Checkpoints(ctx context.Context) ([]CheckpointGeo, error) {
	rows, err := r.db.Query(ctx, `
		SELECT checkpoint_id, name, lat, lng
		  FROM checkpoint
		 WHERE lat IS NOT NULL AND lng IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []CheckpointGeo{}
	for rows.Next() {
		var c CheckpointGeo
		if err := rows.Scan(&c.ID, &c.Name, &c.Lat, &c.Lng); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// LastCheckinCheckpoint — ฐานล่าสุดที่คนนี้เช็คอิน · ใช้เมื่อไม่มีพิกัดจาก GPS
func (r *WBWSOSRepository) LastCheckinCheckpoint(ctx context.Context, participantID string) (*int, error) {
	var id int
	err := r.db.QueryRow(ctx, `
		SELECT checkpoint_id FROM check_in
		 WHERE participant_id = $1::uuid
		 ORDER BY server_received_at DESC LIMIT 1`, participantID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// Raise — เปิดเคสใหม่ หรืออัปเดตเคสที่เปิดอยู่
//
// ทั้งก้อนอยู่ในทรานแซกชันเดียวและล็อกแถวที่เปิดอยู่ด้วย FOR UPDATE ก่อน แทนที่จะ
// ยิง INSERT แล้วไปแก้ 23505 ทีหลัง — sos_one_open_per_user เป็น partial unique index
// การแข่งกันจึงเกิดได้จริงเมื่อ outbox flush ซ้อนกับ request เดิมที่ยังค้างอยู่
//
// คืน (เคส, สร้างใหม่ไหม, error) · created = false แปลว่าเป็นการย้ำหรือส่งซ้ำ
// ซึ่งผู้เรียกใช้ตัดสินใจว่าจะยิง push อีกรอบไหม (ดู rate limit ใน service)
//
// ข้อยกเว้นของ "ล็อกก่อนแล้วไม่มี 23505 ให้แก้ทีหลัง": การกดครั้งแรกสุดของคนคนหนึ่งยังไม่มีแถว
// ในตารางเลย จึงไม่มีอะไรให้ FOR UPDATE ล็อก — ดู raise() ด้านล่างสำหรับการดักจุดนี้
func (r *WBWSOSRepository) Raise(
	ctx context.Context, participantID string, req model.SOSRequest,
	checkpointID *int, locSource *string,
) (*model.SOSCase, bool, error) {
	return r.raise(ctx, participantID, req, checkpointID, locSource, true)
}

// raise — ตัวจริงของ Raise · retryOnConflict เปิดได้แค่ครั้งเดียว กันวนไม่รู้จบ
//
// ทำไมต้อง retry: การกดครั้งแรกสุดของคนคนหนึ่งยังไม่มีแถวให้ FOR UPDATE ล็อก สอง Raise ที่มา
// พร้อมกันจึงเห็น ErrNoRows เหมือนกันแล้ววิ่งเข้า branch INSERT ทั้งคู่ Postgres ปล่อยผ่านได้
// แค่ตัวเดียวผ่าน unique index (ชนได้ทั้ง sos_one_open_per_user หรือ client_id เดิม) ตัวที่แพ้
// ได้ 23505 กลับมา — ซึ่งแปลว่าตัวที่ชนะ commit ไปแล้วจริง (Postgres ล็อกระดับ index ตอน insert
// ตัวที่แพ้ต้อง block รอผลของตัวที่ชนะก่อนเสมอ ไม่มีทางเห็น 23505 ก่อนตัวชนะ commit) rollback
// แล้ววนกลับไปเดินตรรกะเดิมอีกครั้งเดียวจึงเจอแถวที่ชนะแน่นอน ไม่ว่าจะชนกันที่ constraint ไหน:
// ชนที่ participant → เจอผ่าน branch "เคสเปิดอยู่แล้ว" (case err == nil ด้านบน) · ชนที่ client_id
// → เจอผ่าน branch "client_id เดิม" ทั้งสอง branch ตอบ created = false เหมือนกัน ตรงกับที่
// ผู้เรียก (retry จาก outbox หรือกดซ้ำ) ต้องการอยู่แล้ว
func (r *WBWSOSRepository) raise(
	ctx context.Context, participantID string, req model.SOSRequest,
	checkpointID *int, locSource *string, retryOnConflict bool,
) (*model.SOSCase, bool, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)

	// 1. เคสที่ยังเปิดอยู่ของคนนี้ ล็อกไว้ก่อน
	var openID int64
	err = tx.QueryRow(ctx, `
		SELECT id FROM sos_event
		 WHERE participant_id = $1::uuid AND NOT resolved
		 FOR UPDATE`, participantID).Scan(&openID)
	switch {
	case err == nil:
		// อัปเดตของเดิม — พิกัดใหม่ทับได้ก็ต่อเมื่อส่งมาจริง (COALESCE)
		// for_other เป็น OR: ครั้งไหนบอกว่ามีคนอื่นเจ็บ เคสก็เป็นแบบนั้นตลอด
		if _, err := tx.Exec(ctx, `
			UPDATE sos_event
			   SET lat        = COALESCE($2, lat),
			       lng        = COALESCE($3, lng),
			       accuracy_m = COALESCE($4, accuracy_m),
			       loc_source = COALESCE($5, loc_source),
			       checkpoint_id = COALESCE($6, checkpoint_id),
			       message    = COALESCE($7, message),
			       for_other  = for_other OR $8,
			       updated_at = now()
			 WHERE id = $1`,
			openID, req.Lat, req.Lng, req.AccuracyM, locSource, checkpointID, req.Message, req.ForOther,
		); err != nil {
			return nil, false, err
		}
		c, err := r.getTx(ctx, tx, openID)
		if err != nil {
			return nil, false, err
		}
		return c, false, tx.Commit(ctx)

	case errors.Is(err, pgx.ErrNoRows):
		// ไม่มีเคสเปิด — แต่ client_id เดิมอาจเป็นของเคสที่ปิดไปแล้ว (แอป retry ช้า)
		var closedID int64
		cerr := tx.QueryRow(ctx,
			`SELECT id FROM sos_event WHERE client_id = $1::uuid`, req.ClientID).Scan(&closedID)
		if cerr == nil {
			c, err := r.getTx(ctx, tx, closedID)
			if err != nil {
				return nil, false, err
			}
			return c, false, tx.Commit(ctx)
		}
		if !errors.Is(cerr, pgx.ErrNoRows) {
			return nil, false, cerr
		}

		var newID int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO sos_event
			  (client_id, participant_id, device_time, lat, lng, accuracy_m,
			   loc_source, checkpoint_id, message, for_other, group_id)
			VALUES ($1::uuid, $2::uuid, $3::timestamptz, $4, $5, $6, $7, $8, $9, $10,
			        (SELECT group_id FROM participant_profile WHERE user_id = $2::uuid))
			RETURNING id`,
			req.ClientID, participantID, req.DeviceTime, req.Lat, req.Lng, req.AccuracyM,
			locSource, checkpointID, req.Message, req.ForOther,
		).Scan(&newID); err != nil {
			// การกดครั้งแรกสุดไม่มีแถวให้ FOR UPDATE ล็อก — แข่งกันได้จริงตรงนี้ (ดู doc ด้านบน)
			if retryOnConflict && IsPGCode(err, "23505") {
				_ = tx.Rollback(ctx)
				return r.raise(ctx, participantID, req, checkpointID, locSource, false)
			}
			return nil, false, err
		}
		c, err := r.getTx(ctx, tx, newID)
		if err != nil {
			return nil, false, err
		}
		return c, true, tx.Commit(ctx)

	default:
		return nil, false, err
	}
}

func (r *WBWSOSRepository) getTx(ctx context.Context, q pgx.Tx, id int64) (*model.SOSCase, error) {
	return scanSOSCase(q.QueryRow(ctx, sosSelect+` WHERE s.id = $1`, id))
}

func (r *WBWSOSRepository) Get(ctx context.Context, id int64) (*model.SOSCase, error) {
	c, err := scanSOSCase(r.db.QueryRow(ctx, sosSelect+` WHERE s.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSOSNotFound
	}
	return c, err
}

// sosSelect — created_at/acked_at ต้อง ::text เพราะ pgx v5 โหมด binary คืน timestamptz
// เป็น time.Time ซึ่ง scan เข้า *string ไม่ได้ (ทรงเดียวกับ repository ตัวอื่นในนี้)
const sosSelect = `
	SELECT s.id, s.for_other, s.lat, s.lng, s.accuracy_m, s.loc_source,
	       s.checkpoint_id, c.name, s.message, s.resolved, s.resolve_reason,
	       s.acked_at::text, ack.display_name, s.server_received_at::text
	  FROM sos_event s
	  LEFT JOIN checkpoint c ON c.checkpoint_id = s.checkpoint_id
	  LEFT JOIN wbw_user ack ON ack.user_id = s.acked_by`

func scanSOSCase(row pgx.Row) (*model.SOSCase, error) {
	var c model.SOSCase
	if err := row.Scan(&c.ID, &c.ForOther, &c.Lat, &c.Lng, &c.AccuracyM, &c.LocSource,
		&c.CheckpointID, &c.CheckpointName, &c.Message, &c.Resolved, &c.ResolveReason,
		&c.AckedAt, &c.AckedByName, &c.CreatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

// ActiveFor — เคสที่ยังเปิดอยู่ของคนนี้ หรือเคสที่เพิ่งปิดใน 5 นาที
//
// ทำไมต้องคืนเคสที่ปิดแล้วด้วย: คนกดต้องเห็นตอนจบ ไม่ใช่เห็นจอว่างเปล่า
// ซึ่งแยกไม่ออกจาก "เคสหายไปไหนไม่รู้"
func (r *WBWSOSRepository) ActiveFor(ctx context.Context, participantID string) (*model.SOSCase, error) {
	c, err := scanSOSCase(r.db.QueryRow(ctx, sosSelect+`
		 WHERE s.participant_id = $1::uuid
		   AND (NOT s.resolved OR s.resolved_at > now() - interval '5 minutes')
		 ORDER BY s.id DESC LIMIT 1`, participantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

// GetForViewer — คนกดเอง หรือสมาชิกกลุ่มเดียวกับเคสนั้น · คนอื่นได้ ErrSOSNotFound
func (r *WBWSOSRepository) GetForViewer(ctx context.Context, viewerID string, id int64) (*model.SOSCase, error) {
	c, err := scanSOSCase(r.db.QueryRow(ctx, sosSelect+`
		 WHERE s.id = $1
		   AND (s.participant_id = $2::uuid
		        OR s.group_id = (SELECT group_id FROM participant_profile WHERE user_id = $2::uuid))`,
		id, viewerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSOSNotFound
	}
	return c, err
}

// Cancel — คนกดยกเลิกเอง · ปิดเคสด้วย reason canceled_by_user เพื่อให้ open_sos
// ที่นับ resolved = FALSE ยังถูกต้อง
//
// เงื่อนไขทั้งหมด (เจ้าของเคส, ยังไม่ resolved, ยังไม่ acked, ยังไม่เลยเวลา) อยู่ใน WHERE ของ
// UPDATE ตัวเดียวกันตัวเดียว — เดิมเป็น SELECT เช็คก่อนแล้วค่อย UPDATE แยกทีหลัง ซึ่งเปิดช่องให้
// เจ้าหน้าที่ Ack หรือ Resolve แทรกเข้ามาระหว่างสองคำสั่งได้ (ไม่มีทรานแซกชัน ไม่มีล็อก) ผลคือ
// UPDATE ทับ resolve_reason ของเจ้าหน้าที่ทิ้งเงียบๆ กลายเป็น canceled_by_user ทั้งที่จริงมีคน
// ไปช่วยแล้ว — UPDATE เดียวอะตอมมิกตัดช่องนั้นทิ้งไปเลย โดยไม่ต้องเพิ่มทรานแซกชัน/ล็อก
func (r *WBWSOSRepository) Cancel(ctx context.Context, participantID string, id int64) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE sos_event
		   SET resolved = TRUE, resolve_reason = 'canceled_by_user',
		       resolved_at = now(), updated_at = now()
		 WHERE id = $1 AND participant_id = $2::uuid
		   AND NOT resolved AND acked_at IS NULL
		   AND now() - server_received_at <= make_interval(secs => $3)`,
		id, participantID, cancelWindowSeconds)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}

	// UPDATE ไม่โดนแถวไหนเลย — วินิจฉัยว่าเพราะอะไร (ไม่พบ / รับเรื่องแล้ว / เลยเวลา) ด้วย
	// SELECT แยกอีกที เป็นแค่การเลือกข้อความ error กลับไป ไม่ใช่การตัดสินใจเขียนอะไรเพิ่ม
	// จึงไม่เปิดช่อง race แบบเดิม
	var acked bool
	var tooOld bool
	err = r.db.QueryRow(ctx, `
		SELECT acked_at IS NOT NULL,
		       now() - server_received_at > make_interval(secs => $3)
		  FROM sos_event
		 WHERE id = $1 AND participant_id = $2::uuid AND NOT resolved`,
		id, participantID, cancelWindowSeconds).Scan(&acked, &tooOld)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSOSNotFound
	}
	if err != nil {
		return err
	}
	if acked {
		return ErrSOSAlreadyAcked
	}
	if tooOld {
		return ErrSOSTooLateToCancel
	}
	// ไม่ควรมาถึงจุดนี้ได้จริง (UPDATE พลาดไปแล้วแต่การวินิจฉัยบอกว่าน่าจะผ่าน) — คืนค่าที่ปลอดภัย
	// ที่สุดแทนการเดา ไม่ใช่ error ที่ปิดบังว่าเกิดอะไรขึ้น
	return ErrSOSNotFound
}

// Ack — เจ้าหน้าที่กด "กำลังไป" · คนที่สองไม่แย่งของคนแรก (WHERE acked_at IS NULL)
// และไม่ถือเป็น error — ผู้เรียกอ่านเคสกลับไปแสดงชื่อคนแรกแทน
func (r *WBWSOSRepository) Ack(ctx context.Context, id int64, staffID string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE sos_event
		   SET acked_at = now(), acked_by = $2::uuid, updated_at = now()
		 WHERE id = $1 AND acked_at IS NULL AND NOT resolved`, id, staffID)
	return err
}

func (r *WBWSOSRepository) Resolve(ctx context.Context, id int64, staffID, reason string) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE sos_event
		   SET resolved = TRUE, resolve_reason = $3,
		       resolved_by = $2::uuid, resolved_at = now(), updated_at = now()
		 WHERE id = $1 AND NOT resolved`, id, staffID, reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrSOSNotFound
	}
	return nil
}

// MarkPushed — จดว่าเพิ่งยิง push ไปเมื่อไหร่ · ตัว rate limit ของการ "ย้ำ"
func (r *WBWSOSRepository) MarkPushed(ctx context.Context, id int64) error {
	_, err := r.db.Exec(ctx, `UPDATE sos_event SET last_push_at = now() WHERE id = $1`, id)
	return err
}

// PushAllowed — ผ่านไปแล้วอย่างน้อย 60 วิจาก push ครั้งล่าสุดหรือยัง
func (r *WBWSOSRepository) PushAllowed(ctx context.Context, id int64) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx, `
		SELECT last_push_at IS NULL OR now() - last_push_at > interval '60 seconds'
		  FROM sos_event WHERE id = $1`, id).Scan(&ok)
	return ok, err
}

// staffVisibility — เงื่อนไข WHERE ที่ตัดสินว่าเจ้าหน้าที่คนหนึ่งเห็นเคสไหนได้บ้าง
//
// สามทางที่ทำให้เห็น เรียงตามความตั้งใจ:
//  1. เป็นฐานที่ตัวเองถูก assign
//  2. เคสไม่มีฐาน (ไม่มีพิกัดเลย) — ไม่มีใครเป็นเจ้าของ ทุกคนจึงต้องเห็น
//  3. ฐานนั้นไม่มีใครถูก assign เลย — "ไม่มีใครถูกมอบหมาย = ทุกคนเห็น" ปลอดภัยกว่า
//     "ไม่มีใครเห็น" และเป็นตาข่ายรองรับ checkpoint_staff ที่ยังว่างบน production
//
// admin กับ medical/security ไม่เข้าเงื่อนไขนี้เลย — เห็นทุกเคสอยู่แล้ว
const staffVisibility = `(
	  s.checkpoint_id IS NULL
	  OR s.checkpoint_id IN (SELECT checkpoint_id FROM checkpoint_staff WHERE user_id = $1::uuid)
	  OR NOT EXISTS (SELECT 1 FROM checkpoint_staff cs WHERE cs.checkpoint_id = s.checkpoint_id)
	  OR s.accuracy_m > 200
	)`

// seesEverything — บทบาทที่เห็นทุกเคสโดยไม่สนใจฐาน
func (r *WBWSOSRepository) seesEverything(ctx context.Context, userID, role string) (bool, error) {
	if role == "admin" {
		return true, nil
	}
	var central bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM wbw_staff
			 WHERE user_id = $1::uuid AND staff_role IN ('medical','security'))`,
		userID).Scan(&central)
	return central, err
}

// StaffFeed — เคสที่เปิดอยู่ทั้งหมด บวกเคสที่เพิ่งปิดใน 30 นาที
//
// ทำไมต้องมีเคสที่ปิดแล้ว: ฐานที่เพิ่งวิ่งไปช่วยต้องเห็นว่าเรื่องจบแล้ว ไม่ใช่เห็นเคส
// หายไปเฉยๆ ซึ่งแยกไม่ออกจาก "แอปโหลดไม่ขึ้น"
func (r *WBWSOSRepository) StaffFeed(ctx context.Context, staffID, role, since string) ([]model.SOSStaffCase, error) {
	all, err := r.seesEverything(ctx, staffID, role)
	if err != nil {
		return nil, err
	}
	// args กับตำแหน่ง $n ใน where ต้องสร้างคู่กันไปทีละขั้น — staffVisibility (ผู้ใช้ $1) ถูกใส่
	// ก็ต่อเมื่อ !all เท่านั้น ถ้า all = true (admin/medical/security) จะไม่มี $1 อยู่ใน SQL เลย
	// การยัด staffID เข้า args แบบไม่มีเงื่อนไข (เหมือนโค้ดตั้งต้น) ทำให้ pgx ปฏิเสธด้วย
	// "expected 0 arguments, got 1" ทันทีที่ all = true — เลขตำแหน่งของ since จึงคำนวณจาก
	// len(args) ปัจจุบัน แทนที่จะเป็น $2 ตายตัว
	where := `(NOT s.resolved OR s.resolved_at > now() - interval '30 minutes')`
	args := []any{}
	if !all {
		where += ` AND ` + staffVisibility
		args = append(args, staffID)
	}
	if since != "" {
		where += ` AND s.updated_at > $` + strconv.Itoa(len(args)+1) + `::timestamptz`
		args = append(args, since)
	}

	rows, err := r.db.Query(ctx, sosStaffSelect+` WHERE `+where+` ORDER BY s.updated_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []model.SOSStaffCase{}
	for rows.Next() {
		var c model.SOSStaffCase
		if err := rows.Scan(&c.ID, &c.ForOther, &c.Lat, &c.Lng, &c.AccuracyM, &c.LocSource,
			&c.CheckpointID, &c.CheckpointName, &c.Message, &c.Resolved, &c.ResolveReason,
			&c.AckedAt, &c.AckedByName, &c.CreatedAt, &c.UpdatedAt,
			&c.ParticipantID, &c.FirstName, &c.LastName, &c.Bib, &c.GroupNumber,
			&c.ContactPhone, &c.EmergencyName, &c.EmergencyPh,
			&c.BloodType, &c.HealthNotes); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// sosStaffSelect — ข้อมูลสุขภาพผูกเงื่อนไขไว้ใน SQL ไม่ใช่ในโค้ด Go
//
// สามข้อต้องจริงพร้อมกัน: ยินยอมให้ใช้ข้อมูลสุขภาพ · เคสยังเปิดอยู่ · เป็นตัวคนกดเอง
// (for_other = true แปลว่าคนเจ็บเป็นคนอื่น ประวัติของคนกดไม่เกี่ยวและไม่ควรถูกเปิด)
const sosStaffSelect = `
	SELECT s.id, s.for_other, s.lat, s.lng, s.accuracy_m, s.loc_source,
	       s.checkpoint_id, c.name, s.message, s.resolved, s.resolve_reason,
	       s.acked_at::text, ack.display_name, s.server_received_at::text, s.updated_at::text,
	       s.participant_id::text, COALESCE(p.first_name,''), COALESCE(p.last_name,''),
	       p.bib_number, g.group_number,
	       p.contact_phone, p.emergency_contact_name, p.emergency_contact_phone,
	       CASE WHEN co.consent_health_data AND NOT s.resolved AND NOT s.for_other
	            THEN h.blood_type::text END,
	       CASE WHEN co.consent_health_data AND NOT s.resolved AND NOT s.for_other
	            THEN concat_ws(E'\n',
	                 NULLIF(h.chronic_disease,''), NULLIF(h.food_allergies,''),
	                 NULLIF(h.medications,''), NULLIF(h.physical_limitations,'')) END
	  FROM sos_event s
	  LEFT JOIN checkpoint c        ON c.checkpoint_id = s.checkpoint_id
	  LEFT JOIN wbw_user ack        ON ack.user_id = s.acked_by
	  LEFT JOIN participant_profile p ON p.user_id = s.participant_id
	  LEFT JOIN participant_group g ON g.group_id = p.group_id
	  LEFT JOIN consent co          ON co.user_id = s.participant_id
	  LEFT JOIN health_details h    ON h.user_id = s.participant_id`

// SOSAudience — token ของทั้งสามกลุ่มผู้รับ แยกกันเพราะข้อความไม่เหมือนกัน
// และเพราะ "ย้ำ" ยิงซ้ำเฉพาะสองกลุ่มแรก กลุ่มเพื่อนได้แค่ตอนเปิดกับตอนปิดเท่านั้น
type SOSAudience struct {
	StaffTokens   []string
	CentralTokens []string
	GroupTokens   []string
}

func (r *WBWSOSRepository) PushAudience(ctx context.Context, caseID int64) (*SOSAudience, error) {
	var a SOSAudience

	// 1. เจ้าหน้าที่ประจำฐานของเคสนี้
	staff, err := r.tokens(ctx, `
		SELECT d.token FROM sos_event s
		  JOIN checkpoint_staff cs ON cs.checkpoint_id = s.checkpoint_id
		  JOIN device_token d ON d.user_id = cs.user_id
		 WHERE s.id = $1`, caseID)
	if err != nil {
		return nil, err
	}
	a.StaffTokens = staff

	// 2. ทีมกลาง — เสมอ ไม่ว่าฐานไหน
	central, err := r.tokens(ctx, `
		SELECT d.token FROM device_token d
		  JOIN wbw_user u ON u.user_id = d.user_id
		  LEFT JOIN wbw_staff ws ON ws.user_id = d.user_id
		 WHERE u.role = 'admin' OR ws.staff_role IN ('medical','security')`)
	if err != nil {
		return nil, err
	}
	a.CentralTokens = central

	// 3. กลุ่มของคนกด ยกเว้นตัวเขาเอง
	group, err := r.tokens(ctx, `
		SELECT d.token FROM sos_event s
		  JOIN participant_profile p ON p.group_id = s.group_id
		  JOIN device_token d ON d.user_id = p.user_id
		 WHERE s.id = $1 AND p.user_id <> s.participant_id`, caseID)
	if err != nil {
		return nil, err
	}
	a.GroupTokens = group

	return &a, nil
}

func (r *WBWSOSRepository) tokens(ctx context.Context, sql string, args ...any) ([]string, error) {
	rows, err := r.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CanStaffSee — ใช้กับ ack/resolve ซึ่งอ้าง id ตรงๆ ไม่ได้ผ่าน feed
func (r *WBWSOSRepository) CanStaffSee(ctx context.Context, staffID, role string, caseID int64) (bool, error) {
	all, err := r.seesEverything(ctx, staffID, role)
	if err != nil || all {
		return all, err
	}
	var ok bool
	err = r.db.QueryRow(ctx,
		`SELECT `+staffVisibility+` FROM sos_event s WHERE s.id = $2`, staffID, caseID).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrSOSNotFound
	}
	return ok, err
}
