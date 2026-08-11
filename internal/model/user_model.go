// Package model defines the data structures used throughout the application.
package model

import "time"

// These are the types of user

const (
	UserTypeStudent = "student"
	UserTypeStaff   = "staff"
	UserTypeAdmin   = "admin"
)

// User represents a MFU student union user.
type User struct {
	ID           int       `json:"id"`
	Name         string    `json:"name"`
	Email        string    `json:"email"`
	UserType     string    `json:"usertype"`
	StudentID    *string   `json:"student_id"`
	Major        *string   `json:"major"`
	School       *string   `json:"school"`
	AvatarURL    *string   `json:"avatar_url"`
	OAuthSubject string    `json:"-"`
	IsFlagged    bool      `json:"is_flagged"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
