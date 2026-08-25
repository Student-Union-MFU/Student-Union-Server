package repository

import (
	"context"
	"errors"

	"su-server/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotCheckedIn — ส่งความเห็นฐานที่ยังไม่ได้ไป
var ErrNotCheckedIn = errors.New("not checked in at this checkpoint")

// ErrAlreadyAnswered — ฐานนี้ตอบไปแล้วด้วย client_id อื่น
type ErrAlreadyAnswered struct{ Existing *model.CheckinFeedback }

func (e ErrAlreadyAnswered) Error() string { return "already answered" }

type WBWFeedbackRepository struct {
	db *pgxpool.Pool
}

func NewWBWFeedbackRepository(db *pgxpool.Pool) *WBWFeedbackRepository {
	return &WBWFeedbackRepository{db: db}
}

// created_at ต้อง ::text — pgx v5 โหมด binary คืน timestamptz เป็น time.Time
// scan ตรงเข้า *string ไม่ได้ (ทรงเดียวกับที่ repository ตัวอื่นในนี้ทำ)
const feedbackCols = `id, checkpoint_id, rating, rating_scenery, rating_activity, rating_staff, rating_area, comment, created_at::text`

func scanFeedback(row pgx.Row) (*model.CheckinFeedback, error) {
	var f model.CheckinFeedback
	if err := row.Scan(&f.ID, &f.CheckpointID, &f.Rating, &f.RatingScenery, &f.RatingActivity,
		&f.RatingStaff, &f.RatingArea, &f.Comment, &f.CreatedAt); err != nil {
		return nil, err
	}
	return &f, nil
}

// Submit — บันทึกความเห็น
//
// ลำดับสำคัญ: เช็ค client_id เดิมก่อน แล้วค่อยเช็คว่าเคยเช็คอินฐานที่ต้องเช็คอินจริงไหม
// แล้วค่อย insert — แต่ SELECT-then-INSERT แบบนี้กันซ้ำได้แค่ retry ที่มาไม่ชนกัน ถ้าสอง
// request client_id เดียวกันแข่งกันจริง (เช่น outbox flush ซ้อนกับ request เดิมที่ยังค้างอยู่)
// ทั้งคู่จะเห็น "ยังไม่มี" พร้อมกันแล้วไปชนกันตอน insert แทน ตัวที่ตัดสินผลให้ถูกจริง ๆ คือ
// conflict handler ใน insert ด้านล่าง — แยกว่า 23505 ชนที่ client_id ตัวเอง (retry คืน 200)
// หรือชนที่ (participant, checkpoint) ของคนอื่น (คืน 409)
func (r *WBWFeedbackRepository) Submit(ctx context.Context, participantID string, req model.FeedbackRequest) (*model.CheckinFeedback, bool, error) {
	// 1. client_id เดิม = ส่งซ้ำ คืนแถวเดิม ไม่ใช่ error
	existing, err := scanFeedback(r.db.QueryRow(ctx,
		`SELECT `+feedbackCols+` FROM checkin_feedback WHERE client_id = $1::uuid`, req.ClientID))
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}

	// 2. ต้องเคยเช็คอินฐานที่ต้องเช็คอินจริง (requires_checkin) — ฐานบริการ (เช่น จุดพัก/ห้องน้ำ)
	// ไม่รับความเห็นตาม spec (Non-Goals: "No feedback for service points") แม้จะมีแถว
	// check_in อยู่จริงก็ตาม จึง join กับ checkpoint แล้วกรองด้วย ไม่ใช่เช็คแค่ check_in เฉย ๆ
	var checkedIn bool
	if err := r.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM check_in ci
			  JOIN checkpoint c ON c.checkpoint_id = ci.checkpoint_id
			 WHERE ci.participant_id = $1::uuid AND ci.checkpoint_id = $2 AND c.requires_checkin
		)`,
		participantID, req.CheckpointID).Scan(&checkedIn); err != nil {
		return nil, false, err
	}
	if !checkedIn {
		return nil, false, ErrNotCheckedIn
	}

	// 3. insert · 23505 มีสองสาเหตุที่ต่างกัน ต้องแยกให้ถูก ไม่ใช่ฟันธงว่าเป็น "คนอื่นตอบไปแล้ว"
	// เสมอไป — ชน unique ได้สองทาง: client_id เดียวกัน (request อื่นที่ client_id เดียวกับ
	// เราเพิ่ง insert ไปก่อน = แข่งกับตัวเอง ไม่ใช่ error) หรือ (participant, checkpoint)
	// เดียวกัน (ตอบไปแล้วจริงด้วย client_id อื่น)
	created, err := scanFeedback(r.db.QueryRow(ctx, `
		INSERT INTO checkin_feedback (participant_id, checkpoint_id, rating,
		                              rating_scenery, rating_activity, rating_staff, rating_area,
		                              comment, client_id, device_time)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9::uuid, $10::timestamptz)
		RETURNING `+feedbackCols,
		participantID, req.CheckpointID, req.Rating,
		req.RatingScenery, req.RatingActivity, req.RatingStaff, req.RatingArea,
		req.Comment, req.ClientID, req.DeviceTime))
	if err != nil {
		if IsPGCode(err, "23505") {
			// เช็ค client_id ของเราเองก่อน — ถ้าเจอ แปลว่า request อื่นที่ client_id เดียวกัน
			// (ตัวเราเอง retry ซ้อนกัน) แทรกไปก่อนแล้ว คืนแถวนั้นเหมือน step 1 ไม่ใช่ 409
			own, oerr := scanFeedback(r.db.QueryRow(ctx,
				`SELECT `+feedbackCols+` FROM checkin_feedback WHERE client_id = $1::uuid`, req.ClientID))
			if oerr == nil {
				return own, false, nil
			}
			if !errors.Is(oerr, pgx.ErrNoRows) {
				return nil, false, oerr
			}

			// ไม่ใช่ client_id ตัวเอง แปลว่าชนที่ (participant, checkpoint) จริง — ตอบไปแล้วด้วย client_id อื่น
			prev, qerr := scanFeedback(r.db.QueryRow(ctx,
				`SELECT `+feedbackCols+` FROM checkin_feedback
				  WHERE participant_id = $1::uuid AND checkpoint_id = $2`,
				participantID, req.CheckpointID))
			if qerr != nil {
				return nil, false, qerr
			}
			return nil, false, ErrAlreadyAnswered{Existing: prev}
		}
		return nil, false, err
	}
	return created, true, nil
}

// ErrEventAlreadyAnswered — ตอบความเห็นต่อการเดินไปแล้วด้วย client_id อื่น
type ErrEventAlreadyAnswered struct{ Existing *model.EventFeedback }

func (e ErrEventAlreadyAnswered) Error() string { return "already answered" }

const eventFeedbackCols = `id, rating, rating_activity, comment, created_at::text`

func scanEventFeedback(row pgx.Row) (*model.EventFeedback, error) {
	var f model.EventFeedback
	if err := row.Scan(&f.ID, &f.Rating, &f.RatingActivity, &f.Comment, &f.CreatedAt); err != nil {
		return nil, err
	}
	return &f, nil
}

// SubmitEvent — บันทึกความเห็นต่อการเดินทั้งงาน
//
// ทรงเดียวกับ Submit ด้านบนโดยตั้งใจ — client_id เดิมคือ retry ไม่ใช่ error, และ 23505
// แยกสองสาเหตุด้วยการไล่ดู client_id ของตัวเองก่อน แต่ไม่มีขั้น "เคยเช็คอินไหม" เพราะ
// ความเห็นนี้ไม่ได้ผูกกับฐาน เงื่อนไข "เดินครบแล้วหรือยัง" อยู่ที่แอปซึ่งเป็นฝ่ายเลือกว่า
// จะถามเมื่อไร — ไม่บังคับซ้ำที่นี่ เพราะคนที่อยากบอกว่างานเป็นอย่างไรทั้งที่ยังเดินไม่ครบ
// (ถอนตัวกลางทาง เจ็บ ฝนตก) เป็นคนที่ผู้จัดอยากได้ยินมากที่สุด และ 403 จะปิดปากเขาพอดี
func (r *WBWFeedbackRepository) SubmitEvent(ctx context.Context, participantID string, req model.EventFeedbackRequest) (*model.EventFeedback, bool, error) {
	existing, err := scanEventFeedback(r.db.QueryRow(ctx,
		`SELECT `+eventFeedbackCols+` FROM event_feedback WHERE client_id = $1::uuid`, req.ClientID))
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}

	created, err := scanEventFeedback(r.db.QueryRow(ctx, `
		INSERT INTO event_feedback (participant_id, rating, rating_activity, comment, client_id, device_time)
		VALUES ($1::uuid, $2, $3, $4, $5::uuid, $6::timestamptz)
		RETURNING `+eventFeedbackCols,
		participantID, req.Rating, req.RatingActivity, req.Comment, req.ClientID, req.DeviceTime))
	if err != nil {
		if IsPGCode(err, "23505") {
			own, oerr := scanEventFeedback(r.db.QueryRow(ctx,
				`SELECT `+eventFeedbackCols+` FROM event_feedback WHERE client_id = $1::uuid`, req.ClientID))
			if oerr == nil {
				return own, false, nil
			}
			if !errors.Is(oerr, pgx.ErrNoRows) {
				return nil, false, oerr
			}

			prev, qerr := scanEventFeedback(r.db.QueryRow(ctx,
				`SELECT `+eventFeedbackCols+` FROM event_feedback WHERE participant_id = $1::uuid`, participantID))
			if qerr != nil {
				return nil, false, qerr
			}
			return nil, false, ErrEventAlreadyAnswered{Existing: prev}
		}
		return nil, false, err
	}
	return created, true, nil
}

// ListAll — ความเห็นทั้งหมด สำหรับแอดมิน
//
// กรอง requires_checkin เหมือนกับ SummaryByCheckpoint โดยตั้งใจ — ถ้าตัวหนึ่งกรองอีกตัวไม่กรอง
// รายการดิบกับยอดรวมจะไม่ตรงกัน (เช่น เจอแถวของฐานบริการที่หลุดเข้ามาจากก่อนแก้ Submit)
// Submit เองก็บล็อกฐานบริการไปแล้วตั้งแต่ทางเข้า แถวใหม่จะไม่มีทางหลุดผ่านมาถึงจุดนี้อีก
// แต่ endpoint อ่านสองตัวนี้ก็ยังต้องเห็นตรงกันเผื่อมีแถวเก่าหลงเหลืออยู่
func (r *WBWFeedbackRepository) ListAll(ctx context.Context) ([]model.AdminFeedbackRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT f.id, f.checkpoint_id, c.name, c.activity_name,
		       f.participant_id::text, COALESCE(p.first_name,''), COALESCE(p.last_name,''),
		       p.bib_number, f.rating, f.comment, f.created_at::text
		  FROM checkin_feedback f
		  JOIN checkpoint c ON c.checkpoint_id = f.checkpoint_id
		  LEFT JOIN participant_profile p ON p.user_id = f.participant_id
		 WHERE c.requires_checkin
		 ORDER BY f.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []model.AdminFeedbackRow{}
	for rows.Next() {
		var a model.AdminFeedbackRow
		if err := rows.Scan(&a.ID, &a.CheckpointID, &a.CheckpointName, &a.ActivityName,
			&a.ParticipantID, &a.FirstName, &a.LastName, &a.Bib,
			&a.Rating, &a.Comment, &a.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

// SummaryByCheckpoint — นับคะแนนต่อฐาน
func (r *WBWFeedbackRepository) SummaryByCheckpoint(ctx context.Context) ([]model.FeedbackSummary, error) {
	rows, err := r.db.Query(ctx, `
		SELECT c.checkpoint_id, c.name,
		       count(*) FILTER (WHERE f.rating = 1)::int,
		       count(*) FILTER (WHERE f.rating = 2)::int,
		       count(*) FILTER (WHERE f.rating = 3)::int
		  FROM checkpoint c
		  LEFT JOIN checkin_feedback f ON f.checkpoint_id = c.checkpoint_id
		 WHERE c.requires_checkin
		 GROUP BY c.checkpoint_id, c.name
		 ORDER BY c.sequence NULLS LAST, c.checkpoint_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []model.FeedbackSummary{}
	for rows.Next() {
		var s model.FeedbackSummary
		if err := rows.Scan(&s.CheckpointID, &s.Name, &s.Dislike, &s.Neutral, &s.Like); err != nil {
			return nil, err
		}
		list = append(list, s)
	}
	return list, rows.Err()
}
