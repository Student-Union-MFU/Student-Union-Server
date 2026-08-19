package service

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const probeSecret = "c3VwZXItc2VjcmV0LWtleS1tYXRlcmlhbA=="

// A code must depend on both the booth and the window, or one booth's code would
// work at another's table, or yesterday's would work today.
func TestCodeDependsOnBoothAndWindow(t *testing.T) {
	base := computeCode(probeSecret, 5, 59544784)

	if same := computeCode(probeSecret, 5, 59544784); same != base {
		t.Errorf("same inputs gave different codes: %s vs %s", base, same)
	}
	if other := computeCode(probeSecret, 6, 59544784); other == base {
		t.Error("booth 6 produced booth 5's code")
	}
	if later := computeCode(probeSecret, 5, 59544785); later == base {
		t.Error("the next window produced the same code")
	}
	if elsewhere := computeCode("a-different-secret", 5, 59544784); elsewhere == base {
		t.Error("a different secret produced the same code")
	}
}

func TestCodeLength(t *testing.T) {
	code := computeCode(probeSecret, 5, 59544784)
	if len(code) != checkInCodeLength {
		t.Errorf("code is %d chars, want %d: %s", len(code), checkInCodeLength, code)
	}
}

// The window arithmetic has to be stable across the boundary, since a code is
// generated on one request and verified on another.
func TestWindowBoundary(t *testing.T) {
	step := int64(CheckInWindow.Seconds())

	start := time.Unix(59544784*step, 0)
	if got := windowAt(start); got != 59544784 {
		t.Errorf("start of window: got %d", got)
	}
	if got := windowAt(start.Add(CheckInWindow - time.Second)); got != 59544784 {
		t.Errorf("last second of window: got %d", got)
	}
	if got := windowAt(start.Add(CheckInWindow)); got != 59544785 {
		t.Errorf("first second of next window: got %d", got)
	}
}

// The parse is the boundary against a hall full of other QR codes. Each of these
// is something a camera genuinely sweeps past.
func TestParseRejectsAnythingThatIsNotAFairCode(t *testing.T) {
	valid := computeCode(probeSecret, 5, 59544784)

	cases := map[string]string{
		"empty":                   "",
		"a booth number":          "7",
		"the old static scheme":   "clubfair://booth/7",
		"a phone number":          "0683150329",
		"a room sign":             "Room B12",
		"someone else's url":      "https://example.com/fair/booth/7",
		"a wifi qr":               "WIFI:S:ClubFair;T:WPA;P:letmein12;;",
		"no window":               "clubfair://checkin?b=5&c=" + valid,
		"no code":                 "clubfair://checkin?b=5&w=59544784",
		"no booth":                "clubfair://checkin?w=59544784&c=" + valid,
		"booth zero":              "clubfair://checkin?b=0&w=59544784&c=" + valid,
		"negative booth":          "clubfair://checkin?b=-5&w=59544784&c=" + valid,
		"non-numeric booth":       "clubfair://checkin?b=five&w=59544784&c=" + valid,
		"short code":              "clubfair://checkin?b=5&w=59544784&c=abc",
		"long code":               "clubfair://checkin?b=5&w=59544784&c=" + valid + "extra",
		"right params, no scheme": "checkin?b=5&w=59544784&c=" + valid,
	}

	for name, payload := range cases {
		if _, err := parseCheckInPayload(payload); err == nil {
			t.Errorf("%s should not parse: %q", name, payload)
		}
	}
}

func TestParseAcceptsAGenuinePayload(t *testing.T) {
	code := computeCode(probeSecret, 5, 59544784)
	payload := fmt.Sprintf("clubfair://checkin?b=5&w=59544784&c=%s", code)

	parsed, err := parseCheckInPayload(payload)
	if err != nil {
		t.Fatalf("genuine payload did not parse: %v", err)
	}
	if parsed.BoothID != 5 || parsed.Window != 59544784 || parsed.Code != code {
		t.Errorf("parsed wrong: %+v", parsed)
	}
}

// Whitespace round a scanned string and an upper-cased scheme are both things a
// real scanner produces.
func TestParseToleratesWhitespaceAndCase(t *testing.T) {
	code := computeCode(probeSecret, 5, 59544784)

	for _, payload := range []string{
		"  clubfair://checkin?b=5&w=59544784&c=" + code + "  ",
		"\nclubfair://checkin?b=5&w=59544784&c=" + code + "\n",
		"CLUBFAIR://checkin?b=5&w=59544784&c=" + strings.ToUpper(code),
	} {
		parsed, err := parseCheckInPayload(payload)
		if err != nil {
			t.Errorf("should have parsed %q: %v", payload, err)
			continue
		}
		if parsed.Code != code {
			t.Errorf("code came back as %q, want %q", parsed.Code, code)
		}
	}
}

// The default is the number that decides how long a shared screenshot works, so
// it is worth asserting rather than assuming.
func TestCheckInMaxAgeDefault(t *testing.T) {
	t.Setenv("CLUBFAIR_CHECKIN_MAX_AGE_SECONDS", "")
	if got := CheckInMaxAge(); got != 3*time.Minute {
		t.Errorf("default max age is %v, want 3m", got)
	}

	t.Setenv("CLUBFAIR_CHECKIN_MAX_AGE_SECONDS", "60")
	if got := CheckInMaxAge(); got != time.Minute {
		t.Errorf("configured max age is %v, want 1m", got)
	}

	// Nonsense falls back rather than becoming zero, which would reject every
	// code the instant its window ticked over.
	t.Setenv("CLUBFAIR_CHECKIN_MAX_AGE_SECONDS", "not-a-number")
	if got := CheckInMaxAge(); got != 3*time.Minute {
		t.Errorf("unparseable max age gave %v, want the 3m default", got)
	}
	t.Setenv("CLUBFAIR_CHECKIN_MAX_AGE_SECONDS", "-30")
	if got := CheckInMaxAge(); got != 3*time.Minute {
		t.Errorf("negative max age gave %v, want the 3m default", got)
	}
}

// The ordinary rule, with no exemption configured: a code is good for
// CheckInMaxAge and then it is not.
func TestCodeAgeWithoutAnExemption(t *testing.T) {
	t.Setenv("CLUBFAIR_CHECKIN_MAX_AGE_SECONDS", "180")
	t.Setenv(reviewBoothEnv, "")
	t.Setenv(reviewUntilEnv, "")

	now := time.Unix(59544784*int64(CheckInWindow.Seconds()), 0)
	current := windowAt(now)

	if err := checkCodeAge(1, current, now); err != nil {
		t.Errorf("the current window should verify: %v", err)
	}
	// 3 minutes is 6 windows; the sixth is still inside the rule.
	if err := checkCodeAge(1, current-6, now); err != nil {
		t.Errorf("a code 3 minutes old should verify: %v", err)
	}
	if err := checkCodeAge(1, current-7, now); err != ErrCheckInCodeExpired {
		t.Errorf("a code older than 3 minutes gave %v, want expired", err)
	}
	if err := checkCodeAge(1, current+2, now); err != ErrCheckInCodeExpired {
		t.Errorf("a code from the future gave %v, want expired", err)
	}
}

// The exemption App Review needs: one booth, and only until the date it carries.
func TestReviewBoothExemption(t *testing.T) {
	t.Setenv("CLUBFAIR_CHECKIN_MAX_AGE_SECONDS", "180")
	t.Setenv(reviewBoothEnv, "1")
	t.Setenv(reviewUntilEnv, "2026-08-21T23:59:00+07:00")

	// Days after the code was minted, which is the case the PNG in the
	// submission has to survive.
	now := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	stale := windowAt(now.Add(-72 * time.Hour))

	if err := checkCodeAge(1, stale, now); err != nil {
		t.Errorf("the exempt booth should accept a three-day-old code: %v", err)
	}

	// The narrowness is the point: every other booth keeps the ordinary rule.
	if err := checkCodeAge(2, stale, now); err != ErrCheckInCodeExpired {
		t.Errorf("booth 2 got the exemption: %v", err)
	}

	// Still not a way round the future check.
	if err := checkCodeAge(1, windowAt(now)+2, now); err != ErrCheckInCodeExpired {
		t.Errorf("the exempt booth accepted a future code: %v", err)
	}

	// After the date, the exempt booth is an ordinary booth again — the whole
	// reason the date exists, since the fair opens the next morning.
	afterFairOpens := time.Date(2026, 8, 22, 9, 0, 0, 0, time.FixedZone("ICT", 7*3600))
	if err := checkCodeAge(1, stale, afterFairOpens); err != ErrCheckInCodeExpired {
		t.Errorf("the exemption outlived its date: %v", err)
	}
}

// Fails closed. A half-set or misspelt exemption must not quietly widen the
// window for a booth, and must not widen it for everyone either.
func TestReviewBoothExemptionFailsClosed(t *testing.T) {
	t.Setenv("CLUBFAIR_CHECKIN_MAX_AGE_SECONDS", "180")

	now := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	stale := windowAt(now.Add(-72 * time.Hour))

	for name, env := range map[string][2]string{
		"both unset":         {"", ""},
		"booth only":         {"1", ""},
		"date only":          {"", "2026-08-21T23:59:00+07:00"},
		"booth not a number": {"one", "2026-08-21T23:59:00+07:00"},
		"booth zero":         {"0", "2026-08-21T23:59:00+07:00"},
		"date not RFC3339":   {"1", "21 Aug 2026"},
		"date with no zone":  {"1", "2026-08-21T23:59:00"},
	} {
		t.Setenv(reviewBoothEnv, env[0])
		t.Setenv(reviewUntilEnv, env[1])

		if err := checkCodeAge(1, stale, now); err != ErrCheckInCodeExpired {
			t.Errorf("%s: stale code accepted, want expired", name)
		}
	}
}

func TestNormalisePhone(t *testing.T) {
	for raw, want := range map[string]string{
		"0683150329":      "0683150329",
		"068 315 0329":    "0683150329",
		"068-315-0329":    "0683150329",
		"(068) 315 0329":  "0683150329",
		"+66 68 315 0329": "0683150329",
		"+66683150329":    "0683150329",
		"66683150329":     "0683150329",
	} {
		if got := NormalisePhone(raw); got != want {
			t.Errorf("NormalisePhone(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestIsValidThaiMobile(t *testing.T) {
	valid := []string{"0683150329", "0812345678", "0912345678", "+66 68 315 0329"}
	for _, phone := range valid {
		if !IsValidThaiMobile(phone) {
			t.Errorf("%q should be valid", phone)
		}
	}

	// Landline, too short, too long, wrong prefix, empty.
	invalid := []string{"053916000", "068315032", "06831503299", "0783150329", ""}
	for _, phone := range invalid {
		if IsValidThaiMobile(phone) {
			t.Errorf("%q should not be valid", phone)
		}
	}
}

func TestPasswordPolicy(t *testing.T) {
	if err := checkPasswordPolicy("clubfair1"); err != nil {
		t.Errorf("a letter, a digit and 9 chars should pass: %v", err)
	}
	for _, weak := range []string{"", "short1", "12345678", "clubfairs"} {
		if err := checkPasswordPolicy(weak); err == nil {
			t.Errorf("%q should have been rejected", weak)
		}
	}
}
