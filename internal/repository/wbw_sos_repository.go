package repository

import (
	"context"
	"errors"

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
