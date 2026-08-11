package service

import (
	"context"
	"su-server/internal/model"
	"su-server/internal/repository"
)

type UserService struct {
	repo *repository.UserRepository
}

func NewUserService(repo *repository.UserRepository) *UserService {
	return &UserService{repo: repo}
}

// GetUserByID is a GET Request to get user information by id
func (s *UserService) GetUserByID(ctx context.Context, id int) (*model.User, error) {
	return s.repo.GetByID(ctx, id)
}

// GetUserByEmail is a GET Request to get user information by email
func (s *UserService) GetUserByEmail(ctx context.Context, email string) (*model.User, error) {
	return s.repo.FindByEmail(ctx, email)
}

// InsertUser is a POST Request to add a new user
func (s *UserService) InsertUser(ctx context.Context, user model.User) (*model.User, error) {
	return s.repo.Insert(ctx, user)
}

// UpsertUser is a POST Request to add a new user
func (s *UserService) UpsertUser(ctx context.Context, user model.User) (*model.User, error) {
	return s.repo.Upsert(ctx, user)
}

// UpdateUser is a PATCH Request to change user profile data
func (s *UserService) UpdateUser(ctx context.Context, user model.User) (*model.User, error) {
	return s.repo.UpdateProfile(ctx, user)
}
