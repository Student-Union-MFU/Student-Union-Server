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
const feedbackCols = `id, checkpoint_id, rating, comment, created_at::text`

func scanFeedback(row pgx.Row) (*model.CheckinFeedback, error) {
	var f model.CheckinFeedback
	if err := row.Scan(&f.ID, &f.CheckpointID, &f.Rating, &f.Comment, &f.CreatedAt); err != nil {
		return nil, err
	}
	return &f, nil
}

// Submit — บันทึกความเห็น
//
// ลำดับสำคัญ: เช็ค client_id เดิมก่อน (retry ตอนเน็ตหลุด ต้องได้แถวเดิมไม่ใช่ 409)
// แล้วค่อยเช็คว่าเคยเช็คอินฐานนี้จริงไหม แล้วค่อย insert
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

	// 2. ต้องเคยเช็คอินฐานนี้จริง
	var checkedIn bool
	if err := r.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM check_in WHERE participant_id = $1::uuid AND checkpoint_id = $2)`,
		participantID, req.CheckpointID).Scan(&checkedIn); err != nil {
		return nil, false, err
	}
	if !checkedIn {
		return nil, false, ErrNotCheckedIn
	}

	// 3. insert · ชนคู่ (participant, checkpoint) = ตอบไปแล้วด้วย client_id อื่น
	created, err := scanFeedback(r.db.QueryRow(ctx, `
		INSERT INTO checkin_feedback (participant_id, checkpoint_id, rating, comment, client_id, device_time)
		VALUES ($1::uuid, $2, $3, $4, $5::uuid, $6::timestamptz)
		RETURNING `+feedbackCols,
		participantID, req.CheckpointID, req.Rating, req.Comment, req.ClientID, req.DeviceTime))
	if err != nil {
		if IsPGCode(err, "23505") {
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

// ListAll — ความเห็นทั้งหมด สำหรับแอดมิน
func (r *WBWFeedbackRepository) ListAll(ctx context.Context) ([]model.AdminFeedbackRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT f.id, f.checkpoint_id, c.name, c.activity_name,
		       f.participant_id::text, COALESCE(p.first_name,''), COALESCE(p.last_name,''),
		       p.bib_number, f.rating, f.comment, f.created_at::text
		  FROM checkin_feedback f
		  JOIN checkpoint c ON c.checkpoint_id = f.checkpoint_id
		  LEFT JOIN participant_profile p ON p.user_id = f.participant_id
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
