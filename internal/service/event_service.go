// Package service represents all the services of the application
package service

import (
	"context"
	"su-server/internal/model"
	"su-server/internal/repository"
)

type EventService struct {
	repo *repository.EventRepository
}

func NewEventService(repo *repository.EventRepository) *EventService {
	return &EventService{repo: repo}
}

// GetOneEvent and GetAllEvents is for GET Requests
func (s *EventService) GetOneEvent(ctx context.Context, id int) (*model.Event, error) {
	return s.repo.GetOneEvent(id, ctx)
}

func (s *EventService) GetAllEvents(ctx context.Context) ([]model.Event, error) {
	return s.repo.GetAllEvents(ctx)
}

// CreateOneEvent and CreateMultipleEvent is for POST Requests
func (s *EventService) CreateOneEvent(ctx context.Context, event model.Event) (bool, error) {
	return s.repo.InsertOneEvent(ctx, event)
}

func (s *EventService) CreateMultipleEvent(ctx context.Context, event []model.Event) (bool, error) {
	return s.repo.InsertMultipleEvents(ctx, event)
}

// UpdateOneEvent is for PATCH Request
func (s *EventService) UpdateOneEvent(ctx context.Context, id int, event model.Event) (*model.Event, error) {
	return s.repo.UpdateOneEvent(ctx, id, event)
}

// DeleteEvent is for DELETE Request
func (s *EventService) DeleteEvent(ctx context.Context, id int) (bool, error) {
	return s.repo.DeletetOneEvent(id, ctx)
}
