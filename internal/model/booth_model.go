package model

import "time"

// Booth is one club standing at the fair, as stored.
type Booth struct {
	ID       int    `json:"id"`
	EventID  *int   `json:"event_id"`
	Name     string `json:"name"`
	Category string `json:"category"`

	// Where on the floor, and what the sign says: zone 'A'/'B'/'C' and a code
	// like "B16". Both nullable — a booth can exist before the floor is laid
	// out — and both come from the real plan in migration 000016.
	Zone      *string `json:"zone"`
	BoothCode *string `json:"booth_code"`

	// `Name` is the Thai original; this is the translation, and null means the
	// client should fall back to Thai. Same convention as checkpoint.name_en.
	NameEN *string `json:"name_en"`

	// One line on what the club does. Null on every booth today — it needs copy
	// the Student Union has not written yet, and the clients hide the line
	// rather than showing an empty one.
	About *string `json:"about"`

	// A neutral icon token ('football', 'photo'), mapped to an asset by each
	// client. Null on six booths where nothing in the icon set is close enough.
	Icon *string `json:"icon"`

	// The HMAC key behind this booth's rotating check-in code. Tagged out of
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
	ID        int     `json:"id"`
	EventID   *int    `json:"event_id"`
	Name      string  `json:"name"`
	NameEN    *string `json:"name_en"`
	Category  string  `json:"category"`
	Zone      *string `json:"zone"`
	BoothCode *string `json:"booth_code"`
	About     *string `json:"about"`
	Icon      *string `json:"icon"`
}

func NewPublicBooth(b Booth) PublicBooth {
	return PublicBooth{
		ID:        b.ID,
		EventID:   b.EventID,
		Name:      b.Name,
		NameEN:    b.NameEN,
		Category:  b.Category,
		Zone:      b.Zone,
		BoothCode: b.BoothCode,
		About:     b.About,
		Icon:      b.Icon,
	}
}

// Zone is one of the three areas of the fair floor, from clubfair_zone.
//
// The names are the Student Union's own — โซนป่าดิบชื้น / Rainforest Zone — and
// the client groups its booth list by these rather than by category. `Code` is
// the letter on the signage and the prefix of every booth code in the area.
type Zone struct {
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	NameEN    *string `json:"name_en"`
	Intent    *string `json:"intent"`
	SortOrder int     `json:"sort_order"`
}
