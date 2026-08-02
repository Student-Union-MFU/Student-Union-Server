package service

import (
	"context"
	"errors"
	"strings"

	"su-server/internal/model"
	"su-server/internal/repository"
)

var (
	ErrBadRating = errors.New("rating must be 1..3")
	// ErrFeedbackMissingClientID — ตั้งชื่อแยกจาก wbw_chat_service.ErrMissingClientID
	// (ข้อความเดียวกันแต่คนละ error value) เพราะสอง var ชื่อซ้ำใน package เดียวกันคอมไพล์ไม่ผ่าน
	ErrFeedbackMissingClientID = errors.New("missing client_id")
)

type WBWFeedbackService struct {
	repo *repository.WBWFeedbackRepository
}

func NewWBWFeedbackService(repo *repository.WBWFeedbackRepository) *WBWFeedbackService {
	return &WBWFeedbackService{repo: repo}
}

// Submit — ตรวจค่าที่รับได้ก่อนแตะฐานข้อมูล · คืน (แถว, สร้างใหม่ไหม, error)
func (s *WBWFeedbackService) Submit(ctx context.Context, participantID string, req model.FeedbackRequest) (*model.CheckinFeedback, bool, error) {
	if strings.TrimSpace(req.ClientID) == "" {
		return nil, false, ErrFeedbackMissingClientID
	}
	if req.Rating < 1 || req.Rating > 3 {
		return nil, false, ErrBadRating
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
