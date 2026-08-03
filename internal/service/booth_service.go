package service

import (
	"context"
	"su-server/internal/model"
	"su-server/internal/repository"
)

type BoothService struct {
	repo *repository.BoothRepository
}

func NewBoothService(repo *repository.BoothRepository) *BoothService {
	return &BoothService{repo: repo}
}

func (s *BoothService) GetAllBooths(ctx context.Context) ([]model.Booth, error) {
	return s.repo.GetAllBooths(ctx)
}
