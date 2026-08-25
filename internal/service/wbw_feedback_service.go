package service

import (
	"context"
	"errors"
	"strings"

	"su-server/internal/model"
)

var (
	ErrBadRating = errors.New("rating must be 1..5")
	// ErrFeedbackMissingClientID — ตั้งชื่อแยกจาก wbw_chat_service.ErrMissingClientID
	// (ข้อความเดียวกันแต่คนละ error value) เพราะสอง var ชื่อซ้ำใน package เดียวกันคอมไพล์ไม่ผ่าน
	ErrFeedbackMissingClientID = errors.New("missing client_id")
)

// feedbackRepo — หน้าตาที่ service ใช้จริงจาก repository ประกาศไว้ฝั่งผู้ใช้ตามธรรมเนียม Go
// *repository.WBWFeedbackRepository เข้าได้เองโดยไม่ต้องประกาศอะไรเพิ่ม cmd/main.go ไม่ต้องแก้
//
// มีไว้ให้เทสของ handler เดินครบทั้งห้าเคสของ POST /wbw/me/feedback ตามที่ spec สั่ง
// (201 สร้างใหม่ / 200 retry client_id เดิม / 400 rating ผิด / 403 ยังไม่เช็คอิน / 409 ตอบแล้ว)
// — เดิมทั้งเส้น handler → service → repository ผูกกับ *pgxpool.Pool ทำให้ต้องมี DB จริงถึงจะ
// แตะได้เลยแม้แต่เคส 400 ที่ไม่เคยไปถึงฐานข้อมูลด้วยซ้ำ
//
// **ขอบเขต**: seam นี้ครอบการ "แปลง error เป็น HTTP status" เท่านั้น การแยก 23505 สองสาเหตุ
// กับตัวกรอง requires_checkin เป็นพฤติกรรมของ SQL ล้วนๆ ปลอมไม่ได้ ต้องมี Postgres จริง
// (ดู wbw_feedback_repository_test.go)
type feedbackRepo interface {
	Submit(ctx context.Context, participantID string, req model.FeedbackRequest) (*model.CheckinFeedback, bool, error)
	SubmitEvent(ctx context.Context, participantID string, req model.EventFeedbackRequest) (*model.EventFeedback, bool, error)
	ListAll(ctx context.Context) ([]model.AdminFeedbackRow, error)
	SummaryByCheckpoint(ctx context.Context) ([]model.FeedbackSummary, error)
}

type WBWFeedbackService struct {
	repo feedbackRepo
}

func NewWBWFeedbackService(repo feedbackRepo) *WBWFeedbackService {
	return &WBWFeedbackService{repo: repo}
}

// Submit — ตรวจค่าที่รับได้ก่อนแตะฐานข้อมูล · คืน (แถว, สร้างใหม่ไหม, error)
func (s *WBWFeedbackService) Submit(ctx context.Context, participantID string, req model.FeedbackRequest) (*model.CheckinFeedback, bool, error) {
	if strings.TrimSpace(req.ClientID) == "" {
		return nil, false, ErrFeedbackMissingClientID
	}
	if req.Rating < 1 || req.Rating > 5 {
		return nil, false, ErrBadRating
	}
	// ข้อย่อยตรวจเฉพาะที่ส่งมา — ไม่ส่งคือไม่ตอบ ซึ่งต่างจากตอบผิดช่วง
	for _, r := range []*int{req.RatingScenery, req.RatingActivity, req.RatingStaff, req.RatingArea} {
		if r != nil && (*r < 1 || *r > 5) {
			return nil, false, ErrBadRating
		}
	}
	if req.Comment != nil {
		trimmed := strings.TrimSpace(*req.Comment)
		if trimmed == "" {
			req.Comment = nil // ช่องว่างล้วน = ไม่ได้เขียนอะไร
		} else {
			req.Comment = &trimmed
		}
	}
	return s.repo.Submit(ctx, participantID, req)
}

// SubmitEvent — ความเห็นต่อการเดินทั้งงาน · ตรวจเหมือน Submit ทุกอย่างที่ตรวจได้
//
// ไม่ตรวจว่าเดินครบทุกฐานหรือยัง ทั้งที่แอปถามตอนครบเท่านั้น — เงื่อนไข "เมื่อไรจึงถาม"
// เป็นเรื่องของแอป ส่วนตรงนี้เป็นเรื่องของ "รับได้ไหม" คนที่ถอนตัวกลางทางแล้วอยากบอกว่า
// ทำไม คือคนที่ผู้จัดอยากได้ยินที่สุด และการบังคับให้ครบก่อนคือการปิดปากเขาพอดี
func (s *WBWFeedbackService) SubmitEvent(ctx context.Context, participantID string, req model.EventFeedbackRequest) (*model.EventFeedback, bool, error) {
	if strings.TrimSpace(req.ClientID) == "" {
		return nil, false, ErrFeedbackMissingClientID
	}
	if req.Rating < 1 || req.Rating > 5 {
		return nil, false, ErrBadRating
	}
	if req.RatingActivity != nil && (*req.RatingActivity < 1 || *req.RatingActivity > 5) {
		return nil, false, ErrBadRating
	}
	if req.Comment != nil {
		trimmed := strings.TrimSpace(*req.Comment)
		if trimmed == "" {
			req.Comment = nil
		} else {
			req.Comment = &trimmed
		}
	}
	return s.repo.SubmitEvent(ctx, participantID, req)
}

func (s *WBWFeedbackService) AdminList(ctx context.Context) (*model.AdminFeedbackResponse, error) {
	items, err := s.repo.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	summary, err := s.repo.SummaryByCheckpoint(ctx)
	if err != nil {
		return nil, err
	}
	return &model.AdminFeedbackResponse{Items: items, Summary: summary}, nil
}
