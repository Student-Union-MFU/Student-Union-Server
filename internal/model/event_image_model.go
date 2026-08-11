package model

// EventImage represents images that will be connected to Event instance
type EventImage struct {
	ID       int    `json:"id"`
	EventID  int    `json:"event_id"`
	URL      string `json:"url"`
	Position int    `json:"position"`
}
