// Package model defines the data structures used throughout the application.
package model

import "time"

// Event represents a student union event.
type Event struct {
	ID        int          `json:"id"`
	Title     string       `json:"title"`
	Content   string       `json:"content"`
	Location  string       `json:"location"`
	Date      string       `json:"date"`
	Time      string       `json:"time"`
	Link      string       `json:"link"`
	CreatedAt time.Time    `json:"created_at"`
	Images    []EventImage `json:"images"`
}
