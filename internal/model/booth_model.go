package model

import "time"

// Booth is one club standing at the fair, as stored.
type Booth struct {
	ID       int    `json:"id"`
	EventID  *int   `json:"event_id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	// The HMAC key behind this booth's rotating check-in QR. Tagged out of
	// JSON as a second lock: responses are built from PublicBooth, but a
	// direct marshal of this type must not leak it either.
	Secret    string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

// PublicBooth is what the API returns.
//
// It has no secret field at all, which is the point: a booth's key cannot
// escape by someone forgetting to blank it, because there is nothing to blank.
// The mistake would have to be adding a field.
type PublicBooth struct {
	ID       int    `json:"id"`
	EventID  *int   `json:"event_id"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

func NewPublicBooth(b Booth) PublicBooth {
	return PublicBooth{
		ID:       b.ID,
		EventID:  b.EventID,
		Name:     b.Name,
		Category: b.Category,
	}
}
