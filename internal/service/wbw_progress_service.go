package service

import (
	"context"

	"su-server/internal/model"
	"su-server/internal/repository"
)

// WBWProgressService — ความคืบหน้าเช็คอินของผู้เข้าร่วมที่เรียกเอง
type WBWProgressService struct {
	repo *repository.WBWCheckpointRepository
}

func NewWBWProgressService(repo *repository.WBWCheckpointRepository) *WBWProgressService {
	return &WBWProgressService{repo: repo}
}

func (s *WBWProgressService) MyProgress(ctx context.Context, participantID string) (*model.CheckinProgress, error) {
	return s.repo.Progress(ctx, participantID)
}
