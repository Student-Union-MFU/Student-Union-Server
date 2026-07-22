package service

import (
	"context"
	"strings"

	"su-server/internal/model"
	"su-server/internal/repository"
)

type WBWNotificationService struct {
	repo *repository.WBWNotificationRepository
}

func NewWBWNotificationService(repo *repository.WBWNotificationRepository) *WBWNotificationService {
	return &WBWNotificationService{repo: repo}
}

func (s *WBWNotificationService) Create(ctx context.Context, req model.NotificationRequest, createdBy string) (*model.Notification, error) {
	if strings.TrimSpace(req.Title) == "" {
		return nil, ErrMissingFields
	}
	return s.repo.Create(ctx, req, createdBy)
}

func (s *WBWNotificationService) ListSent(ctx context.Context) ([]model.NotificationSent, error) {
	return s.repo.ListSent(ctx)
}

func (s *WBWNotificationService) ListForUser(ctx context.Context, userID string) ([]model.Notification, error) {
	return s.repo.ListForUser(ctx, userID)
}

func (s *WBWNotificationService) ListPublic(ctx context.Context) ([]model.NotificationPublic, error) {
	return s.repo.ListPublic(ctx)
}

func (s *WBWNotificationService) GetDraft(ctx context.Context, userID string) (*model.Preset, error) {
	return s.repo.GetDraft(ctx, userID)
}

func (s *WBWNotificationService) SaveDraft(ctx context.Context, userID string, req model.PresetRequest) (*model.Preset, error) {
	return s.repo.SaveDraft(ctx, userID, req)
}

func (s *WBWNotificationService) DeleteDraft(ctx context.Context, userID string) error {
	return s.repo.DeleteDraft(ctx, userID)
}

func (s *WBWNotificationService) ListPresets(ctx context.Context, userID string) ([]model.Preset, error) {
	return s.repo.ListPresets(ctx, userID)
}

func (s *WBWNotificationService) CreatePreset(ctx context.Context, userID string, req model.PresetRequest) (*model.Preset, error) {
	if req.Name == nil || strings.TrimSpace(*req.Name) == "" {
		return nil, ErrEmptyName
	}
	trimmed := strings.TrimSpace(*req.Name)
	req.Name = &trimmed
	return s.repo.CreatePreset(ctx, userID, req)
}

func (s *WBWNotificationService) DeletePreset(ctx context.Context, userID string, id int64) error {
	return s.repo.DeletePreset(ctx, userID, id)
}
