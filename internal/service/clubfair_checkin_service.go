package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"su-server/internal/model"
	"su-server/internal/repository"
	"time"
)

/*
Rotating booth check-in codes.

Each booth has a 32-byte secret (migration 000009) that never leaves the server.
A device at the booth displays a QR that changes every [CheckInWindow]:

	clubfair://checkin?b=<booth id>&w=<window>&c=<code>

	window = unix / 30
	code   = HMAC-SHA256(secret, "<booth id>:<window>") truncated to 12 hex chars

The window travels in the payload in the clear. That is deliberate: it is only a
timestamp, and stating it means the server verifies one candidate exactly rather
than searching a range of them — so the accepted age becomes an explicit policy
instead of a side effect of how far the loop happens to run.

WHAT THIS STOPS: inventing a code. Without the secret a student cannot compute a
valid one for a booth they never stood in front of, which is the whole failure of
the previous scheme (the client accepted a bare booth number).

WHAT IT DOES NOT STOP, and this is worth being exact about: a screenshot shared
within the accepted age. TOTP cannot prevent that — anything a booth displays can
be photographed. The accepted age IS the sharing window, so it is deliberately
short and is the one number to tune if cheating shows up.

⚠ That is in direct tension with offline sync. A student who scans in a dead spot
and uploads twenty minutes later will have a code the server refuses, and the
honest answer is to make them re-scan rather than to widen the window to twenty
minutes for everyone. See [CheckInMaxAge].
*/

// CheckInWindow is how long one code is displayed. Short enough that a shared
// photo is stale quickly; long enough that a booth device with a slightly wrong
// clock still lands inside the tolerance below.
const CheckInWindow = 30 * time.Second

// The check-in QR's scheme and path, and the parameters it carries.
const checkInURLPrefix = "clubfair://checkin"

// Truncated to 12 hex characters — 48 bits. Not a signature anyone stores, just
// a value that has to be infeasible to guess inside a 30-second window; 48 bits
// against a rate-limited endpoint is far past that, and it keeps the QR small
// enough to read from a phone screen at arm's length.
const checkInCodeLength = 12

// ErrPrizeThresholdNotReached carries the two numbers the message needs, so the
// handler can phrase it in Thai without the service formatting user-facing copy.
type ErrPrizeThresholdNotReached struct {
	Collected int
	Needed    int
}

func (e ErrPrizeThresholdNotReached) Error() string {
	return fmt.Sprintf("clubfair: %d booths collected, %d needed", e.Collected, e.Needed)
}

var (
	ErrCheckInCodeMalformed = errors.New("clubfair: check-in code is not a fair code")
	ErrCheckInCodeInvalid   = errors.New("clubfair: check-in code did not verify")
	ErrCheckInCodeExpired   = errors.New("clubfair: check-in code has expired")
)

// CheckInMaxAge is how old a code may be when it arrives.
//
// Three minutes by default: enough for a slow network, a backgrounded app or a
// student who scans and then walks out of signal, and short enough that a
// screenshot passed to a friend is usually already dead. Tunable because the
// right value is a decision about the fair, not about the code —
// CLUBFAIR_CHECKIN_MAX_AGE_SECONDS.
func CheckInMaxAge() time.Duration {
	if raw := os.Getenv("CLUBFAIR_CHECKIN_MAX_AGE_SECONDS"); raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return 3 * time.Minute
}

// maxAgeWindows is [CheckInMaxAge] expressed in windows — how many rotations
// behind the current one a code may be and still verify.
//
// One definition, read by both Verify and CurrentCode. They have to agree
// exactly: the first computes whether a code is refused and the second tells a
// display when that will happen, and a booth screen whose idea of "still works"
// is a window out from the server's is worse than one with no idea at all.
func maxAgeWindows() int64 {
	return int64(CheckInMaxAge().Seconds()) / int64(CheckInWindow.Seconds())
}

// How far ahead of the server a booth device's clock may be. One window, for
// skew only — a code from the future is otherwise meaningless.
const checkInFutureTolerance = 1

// CheckInOutcome is what happened, because three results need three different
// things said to someone standing at a booth holding a phone up.
type CheckInOutcome string

const (
	// A new stamp.
	CheckInRecorded CheckInOutcome = "recorded"
	// A real booth this student already collected. Not an error.
	CheckInAlreadyScanned CheckInOutcome = "already_scanned"
	// The app replaying its own queue after a timeout. Idempotent success.
	CheckInDuplicateRequest CheckInOutcome = "duplicate_request"
)

type ClubFairCheckInService struct {
	repo *repository.ClubFairFairRepository
}

func NewClubFairCheckInService(repo *repository.ClubFairFairRepository) *ClubFairCheckInService {
	return &ClubFairCheckInService{repo: repo}
}

// windowAt is the code window a moment falls in.
func windowAt(t time.Time) int64 {
	return t.Unix() / int64(CheckInWindow.Seconds())
}

// computeCode derives a booth's code for one window.
func computeCode(secret string, boothID int, window int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d:%d", boothID, window)
	return hex.EncodeToString(mac.Sum(nil))[:checkInCodeLength]
}

// BoothCode is what the device at a booth displays right now.
type BoothCheckInCode struct {
	BoothID   int       `json:"booth_id"`
	Window    int64     `json:"window"`
	Code      string    `json:"code"`
	Payload   string    `json:"payload"`
	ExpiresAt time.Time `json:"expires_at"`

	// AcceptedUntil is when this code stops verifying — the end of its window
	// plus [CheckInMaxAge].
	//
	// Sent because it is the only one of the two instants a display can act on
	// and the only one it cannot work out for itself. ExpiresAt says when to ask
	// for the next code; this says when the one on screen becomes a lie. They
	// are three minutes apart by default, and a booth screen that showed a code
	// past this point would be inviting scans the server refuses.
	//
	// It is *computed from the server's own max age* rather than left for the
	// client to add on, because that value is tunable per deployment
	// (CLUBFAIR_CHECKIN_MAX_AGE_SECONDS). A client with its own copy of "180
	// seconds" is a client that starts lying the day somebody changes it, and
	// nothing would fail loudly enough to notice.
	AcceptedUntil time.Time `json:"accepted_until"`
}

// ErrBoothNotOwned is a booth owner asking for a booth that is not theirs.
//
// Distinct from "not found", and the handler answers 403 rather than 404: the
// booth exists and the caller is who they say they are, they simply do not run
// it. Collapsing the two would hide a misassignment behind what reads as a
// deleted booth — on setup day, when that difference is the whole of what the
// person holding the tablet needs to know.
var ErrBoothNotOwned = errors.New("clubfair: this booth is not assigned to you")

// CurrentCode builds the payload for a booth's display.
//
// The booth device asks the server for this rather than holding the secret and
// computing it locally, which is what keeps the key on one machine: a tablet left
// on a table at a fair is not somewhere to store something that mints valid
// check-ins for the rest of the event.
//
// ## Who may ask
//
// [userID] and [role] come off the caller's token, never off the request.
//
//   - **staff and admin** — any booth. They are the people walking the hall
//     fixing screens, and a screen that has gone dark should be fixable by
//     whoever reaches it rather than only by the volunteer assigned to it.
//   - **booth_owner** — only the booths in clubfair_booth_owner. This is the
//     entire authorisation the role carries: a booth's volunteers get a
//     credential that displays their own code and reaches nothing else.
//
// **The role is tested as well as the assignment, and the assignment is the half
// that can actually be revoked.** A role lives in a 30-day token this server
// cannot recall, so a demoted account keeps presenting one that says
// booth_owner; UpdateParticipant deletes its assignment rows for exactly that
// reason, which is what makes the refusal land on the very next poll.
func (s *ClubFairCheckInService) CurrentCode(
	ctx context.Context, boothID, userID int, role string,
) (*BoothCheckInCode, error) {
	if role != ClubFairRoleStaff && role != ClubFairRoleAdmin {
		if role != ClubFairRoleBoothOwner {
			return nil, ErrBoothNotOwned
		}
		owns, err := s.repo.BoothOwnedBy(ctx, userID, boothID)
		if err != nil {
			return nil, err
		}
		if !owns {
			return nil, ErrBoothNotOwned
		}
	}

	// Read after the check, never before. This is the one column in the schema
	// that must not leave the database for a caller with no business holding it,
	// and fetching it first would mean an unauthorised request had already
	// pulled a booth's key into memory before being refused.
	secret, err := s.repo.BoothSecret(ctx, boothID)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	window := windowAt(now)
	code := computeCode(secret, boothID, window)

	return &BoothCheckInCode{
		BoothID: boothID,
		Window:  window,
		Code:    code,
		Payload: fmt.Sprintf("%s?b=%d&w=%d&c=%s", checkInURLPrefix, boothID, window, code),
		// When this code stops being the current one, so the display knows when
		// to ask again.
		ExpiresAt: time.Unix((window+1)*int64(CheckInWindow.Seconds()), 0),
		// And when it stops verifying. Verify admits a code while
		// `current - window <= maxAgeWindows`, so the first instant it refuses
		// one is the start of window `window + maxAgeWindows + 1`. Derived from
		// the same expression Verify uses rather than restated, so the two
		// cannot drift.
		AcceptedUntil: time.Unix(
			(window+maxAgeWindows()+1)*int64(CheckInWindow.Seconds()), 0),
	}, nil
}

// parsedCheckIn is a payload that parsed, before it has been verified.
type parsedCheckIn struct {
	BoothID int
	Window  int64
	Code    string
}

// parseCheckInPayload reads the scanned string.
//
// Strict: the scheme, the path and all three parameters, or nothing. A fair hall
// is full of other QR codes — posters, Wi-Fi, price lists — and a lenient parse
// is how the previous scheme ended up crediting a booth for a phone number.
func parseCheckInPayload(payload string) (*parsedCheckIn, error) {
	trimmed := strings.TrimSpace(payload)
	if !strings.HasPrefix(strings.ToLower(trimmed), checkInURLPrefix) {
		return nil, ErrCheckInCodeMalformed
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, ErrCheckInCodeMalformed
	}
	q := parsed.Query()

	boothID, err := strconv.Atoi(q.Get("b"))
	if err != nil || boothID <= 0 {
		return nil, ErrCheckInCodeMalformed
	}
	window, err := strconv.ParseInt(q.Get("w"), 10, 64)
	if err != nil || window <= 0 {
		return nil, ErrCheckInCodeMalformed
	}
	code := strings.ToLower(strings.TrimSpace(q.Get("c")))
	if len(code) != checkInCodeLength {
		return nil, ErrCheckInCodeMalformed
	}

	return &parsedCheckIn{BoothID: boothID, Window: window, Code: code}, nil
}

// Verify checks a scanned payload without recording anything.
func (s *ClubFairCheckInService) Verify(ctx context.Context, payload string) (boothID int, err error) {
	scanned, err := parseCheckInPayload(payload)
	if err != nil {
		return 0, err
	}

	// Age is checked before the HMAC, so an ancient code costs a rejection
	// rather than a database read for the secret.
	current := windowAt(time.Now())
	if scanned.Window > current+checkInFutureTolerance {
		return 0, ErrCheckInCodeExpired
	}
	if current-scanned.Window > maxAgeWindows() {
		return 0, ErrCheckInCodeExpired
	}

	secret, err := s.repo.BoothSecret(ctx, scanned.BoothID)
	if err != nil {
		return 0, err
	}

	expected := computeCode(secret, scanned.BoothID, scanned.Window)
	// Constant time: this is a comparison against a secret-derived value, and
	// the endpoint is reachable by anyone with an account.
	if !hmac.Equal([]byte(expected), []byte(scanned.Code)) {
		return 0, ErrCheckInCodeInvalid
	}

	return scanned.BoothID, nil
}

// Record verifies a payload and writes the stamp.
//
// [clientID] is the app's idempotency key, minted before the request goes out, so
// a retry after a timeout returns the original row instead of failing or
// double-counting.
func (s *ClubFairCheckInService) Record(
	ctx context.Context,
	userID int,
	payload, clientID, deviceTime string,
) (*model.ClubFairCheckIn, CheckInOutcome, error) {
	boothID, err := s.Verify(ctx, payload)
	if err != nil {
		return nil, "", err
	}

	// An absent or unparseable device_time falls back to now rather than
	// rejecting: the client's clock is not something to fail a real check-in
	// over, and server_received_at is the column anything contested is settled
	// on anyway.
	if _, parseErr := time.Parse(time.RFC3339, deviceTime); parseErr != nil {
		deviceTime = time.Now().UTC().Format(time.RFC3339)
	}

	checkIn, created, err := s.repo.RecordCheckIn(ctx, clientID, userID, boothID, deviceTime)
	if err != nil {
		return nil, "", err
	}
	if created {
		return checkIn, CheckInRecorded, nil
	}
	// The row that blocked the insert: the app's own replay carries the same
	// client_id, anything else is this booth already being collected.
	if checkIn.ClientID == clientID {
		return checkIn, CheckInDuplicateRequest, nil
	}
	return checkIn, CheckInAlreadyScanned, nil
}

func (s *ClubFairCheckInService) List(ctx context.Context, userID int) ([]model.ClubFairCheckIn, error) {
	return s.repo.ListCheckIns(ctx, userID)
}

func (s *ClubFairCheckInService) Progress(ctx context.Context, userID int) (*model.ClubFairProgress, error) {
	return s.repo.Progress(ctx, userID)
}

func (s *ClubFairCheckInService) ListZones(ctx context.Context) ([]model.Zone, error) {
	return s.repo.ListZones(ctx)
}

// ClaimPrize records a prize handed to a student, refusing a tier they have not
// reached.
//
// The threshold is re-checked here against the server's own count rather than
// trusting whatever the staff device believes: it is the difference between a
// prize table and an honour system.
func (s *ClubFairCheckInService) ClaimPrize(
	ctx context.Context,
	userID, tierID, issuedBy int,
) (claimed bool, err error) {
	threshold, err := s.repo.TierThreshold(ctx, tierID)
	if err != nil {
		return false, err
	}

	progress, err := s.repo.Progress(ctx, userID)
	if err != nil {
		return false, err
	}
	if progress.Visited < threshold {
		return false, ErrPrizeThresholdNotReached{Collected: progress.Visited, Needed: threshold}
	}

	return s.repo.ClaimPrize(ctx, userID, tierID, issuedBy)
}
