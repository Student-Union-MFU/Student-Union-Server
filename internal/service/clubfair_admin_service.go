package service

import (
	"context"

	"su-server/internal/model"
	"su-server/internal/repository"
)

type ClubFairAdminService struct {
	admin *repository.ClubFairAdminRepository
}

func NewClubFairAdminService(admin *repository.ClubFairAdminRepository) *ClubFairAdminService {
	return &ClubFairAdminService{admin: admin}
}

func (s *ClubFairAdminService) Dashboard(ctx context.Context) (*model.ClubFairDashboardStats, error) {
	return s.admin.Dashboard(ctx)
}
