// Package repository is for uh sql queries? i think
package repository

import (
	"context"
	"su-server/internal/model"

	"github.com/jackc/pgx/v5/pgxpool"
)

type UserRepository struct {
	db *pgxpool.Pool
}

func NewUserRepository(db *pgxpool.Pool) *UserRepository {
	return &UserRepository{db: db}
}

func (r *UserRepository) FindByEmail(ctx context.Context, email string) (*model.User, error) {
	var user model.User
	err := r.db.QueryRow(ctx,
		`SELECT id, user_type, name, email, avatar_url, student_id, major, school,
		        oauth_subject, is_flagged, created_at, updated_at
		 FROM users WHERE email = $1`,
		email,
	).Scan(
		&user.ID,
		&user.UserType,
		&user.Name,
		&user.Email,
		&user.AvatarURL,
		&user.StudentID,
		&user.Major,
		&user.School,
		&user.OAuthSubject,
		&user.IsFlagged,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *UserRepository) GetByID(ctx context.Context, id int) (*model.User, error) {
	var user model.User
	err := r.db.QueryRow(ctx,
		`SELECT id, user_type, name, email, avatar_url, student_id, major, school,
		        oauth_subject, is_flagged, created_at, updated_at
		 FROM users WHERE id = $1`,
		id,
	).Scan(
		&user.ID,
		&user.UserType,
		&user.Name,
		&user.Email,
		&user.AvatarURL,
		&user.StudentID,
		&user.Major,
		&user.School,
		&user.OAuthSubject,
		&user.IsFlagged,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// Insert is for manual/testing inserts with all fields
func (r *UserRepository) Insert(ctx context.Context, user model.User) (*model.User, error) {
	var result model.User
	err := r.db.QueryRow(ctx,
		`INSERT INTO users (user_type, name, email, avatar_url, student_id, major, school, oauth_subject)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id, user_type, name, email, avatar_url, student_id, major, school,
		           oauth_subject, is_flagged, created_at, updated_at`,
		user.UserType,
		user.Name,
		user.Email,
		user.AvatarURL,
		user.StudentID,
		user.Major,
		user.School,
		user.OAuthSubject,
	).Scan(
		&result.ID,
		&result.UserType,
		&result.Name,
		&result.Email,
		&result.AvatarURL,
		&result.StudentID,
		&result.Major,
		&result.School,
		&result.OAuthSubject,
		&result.IsFlagged,
		&result.CreatedAt,
		&result.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// Upsert is for OAuth login — only has fields Google provides
func (r *UserRepository) Upsert(ctx context.Context, user model.User) (*model.User, error) {
	var result model.User
	err := r.db.QueryRow(ctx,
		`INSERT INTO users (user_type, name, email, avatar_url, oauth_subject)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (oauth_subject) DO UPDATE
		 SET name       = EXCLUDED.name,
		     avatar_url = EXCLUDED.avatar_url,
		     updated_at = NOW()
		 RETURNING id, user_type, name, email, avatar_url, student_id, major, school,
		           oauth_subject, is_flagged, created_at, updated_at`,
		user.UserType,
		user.Name,
		user.Email,
		user.AvatarURL,
		user.OAuthSubject,
	).Scan(
		&result.ID,
		&result.UserType,
		&result.Name,
		&result.Email,
		&result.AvatarURL,
		&result.StudentID,
		&result.Major,
		&result.School,
		&result.OAuthSubject,
		&result.IsFlagged,
		&result.CreatedAt,
		&result.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (r *UserRepository) UpdateProfile(ctx context.Context, user model.User) (*model.User, error) {
	var result model.User
	err := r.db.QueryRow(ctx,
		`UPDATE users
		 SET major      = $1,
		     school     = $2,
		     student_id = $3,
		     updated_at = NOW()
		 WHERE id = $4
		 RETURNING id, user_type, name, email, avatar_url, student_id, major, school,
		           oauth_subject, is_flagged, created_at, updated_at`,
		user.Major,
		user.School,
		user.StudentID,
		user.ID,
	).Scan(
		&result.ID,
		&result.UserType,
		&result.Name,
		&result.Email,
		&result.AvatarURL,
		&result.StudentID,
		&result.Major,
		&result.School,
		&result.OAuthSubject,
		&result.IsFlagged,
		&result.CreatedAt,
		&result.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &result, nil
}
