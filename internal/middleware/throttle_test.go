package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

/*
The refusal is the whole point of this wrapper, and it is tested by driving a
REAL chi throttler to its limit rather than by calling classifyThrottle with a
string.

Two things would otherwise break silently. chi's refusal messages are unexported
constants matched here by value, so a reworded message in a future chi would
quietly send every refusal into the "unknown" bucket — and the split between
"backlog full" and "waited the whole timeout" is the split that decides whether
to raise the backlog or raise throughput, which are opposite actions. Second, the
body has to be JSON: CLAUDE.md forbids plain text because the apps print
body.error verbatim, so a throttled student sees a blank error otherwise.
*/
func TestThrottleRefusesAtCapacityWithJSON(t *testing.T) {
	// Limit 1, no backlog: the second concurrent caller finds no queue slot at
	// all and is refused instantly. That is the capacity_exceeded case.
	throttle := NewThrottle("test", 1, 0, time.Second)

	release := make(chan struct{})
	entered := make(chan struct{})
	handler := throttle.Handler()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	}))

	go handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/auth/login", nil))
	<-entered // the one processing token is now held

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/login", nil))
	close(release)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected JSON, got Content-Type %q", ct)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("refusal body must be JSON the apps can decode, got %q: %v", rec.Body.String(), err)
	}
	if body["error"] == "" {
		t.Errorf(`refusal body must carry {"error": "..."}, got %v`, body)
	}

	// Without a backoff hint a refused client retries at once, which adds load
	// at the exact moment there was none to spare.
	retryAfter := rec.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("expected a Retry-After header on a refusal")
	}
	if seconds, err := strconv.Atoi(retryAfter); err != nil || seconds < 1 {
		t.Errorf("Retry-After should be a positive number of seconds, got %q", retryAfter)
	}

	snap := throttle.Snapshot()
	if snap.CapacityExceeded != 1 {
		t.Errorf("expected the refusal counted as capacity_exceeded, got %+v", snap)
	}
	if snap.Unknown != 0 {
		t.Errorf("chi's refusal wording was not recognised — the message constants in "+
			"throttle.go have drifted from chi's own: %+v", snap)
	}
}

// The other real case, and the one that means the OPPOSITE thing: the caller
// did get a queue slot and waited the whole timeout in it. Raising the backlog
// makes this one worse, so it must never be counted with the one above.
func TestThrottleCountsAQueueTimeoutSeparately(t *testing.T) {
	throttle := NewThrottle("test", 1, 1, 30*time.Millisecond)

	release := make(chan struct{})
	entered := make(chan struct{})
	handler := throttle.Handler()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
	}))

	go handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/auth/login", nil))
	<-entered

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/login", nil))
	close(release)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	snap := throttle.Snapshot()
	if snap.TimedOut != 1 || snap.CapacityExceeded != 0 {
		t.Errorf("expected exactly one timed_out and no capacity_exceeded, got %+v", snap)
	}
}

// An admitted request must not be able to tell the throttle is there. The
// wrapper is handed the real ResponseWriter on this path precisely so that a
// handler's own status, its Flusher and its Hijacker are untouched.
func TestAnAdmittedRequestIsUntouched(t *testing.T) {
	throttle := NewThrottle("test", 4, 4, time.Second)

	handler := throttle.Handler()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, wrapped := w.(*throttleWriter); wrapped {
			t.Error("an admitted handler should be writing to the real ResponseWriter")
		}
		if _, ok := w.(http.Flusher); !ok {
			t.Error("http.Flusher was lost — this is what breaks the long-polls")
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"ok":true}`))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/register", nil))

	if rec.Code != http.StatusCreated {
		t.Errorf("expected the handler's own 201, got %d", rec.Code)
	}
	if rec.Body.String() != `{"ok":true}` {
		t.Errorf("expected the handler's own body, got %q", rec.Body.String())
	}
	if snap := throttle.Snapshot(); snap.Admitted != 1 {
		t.Errorf("expected one admitted request, got %+v", snap)
	}
}

/*
Retry-After is deliberately not a constant.

A fixed value sends every one of up to 2,540 refused students back at the same
instant, which is the queue that just overflowed arriving again as one spike.
The floor is the full timeout — coming back sooner is how a throttle becomes a
retry storm — and the spread above it is what turns a second wall into a ramp.
*/
func TestRetryAfterIsSpreadOutAboveTheTimeout(t *testing.T) {
	const timeout = 20 * time.Second
	throttle := NewThrottle("test", 1, 0, timeout)

	seen := map[time.Duration]bool{}
	for range 200 {
		d := throttle.retryAfter(false)
		if d < timeout {
			t.Fatalf("Retry-After must never ask a caller back sooner than the queue timeout: %v", d)
		}
		if d > timeout+timeout/2 {
			t.Fatalf("Retry-After spread further than intended: %v", d)
		}
		seen[d] = true
	}
	if len(seen) < 2 {
		t.Error("every refused caller was given the same Retry-After — they will all come back at once")
	}
}
