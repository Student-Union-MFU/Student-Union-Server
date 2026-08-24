package middleware

import (
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"sync/atomic"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
)

/*
The auth throttle, wrapped so a refused caller gets JSON it can read and a
Retry-After it can obey — and so the refusals are counted by REASON.

chi's ThrottleBacklog does the queueing correctly and this keeps every bit of
that: the limiter underneath is chi's own, unchanged. What it does badly is the
refusal. It answers with http.Error, which is plain text, and CLAUDE.md forbids
that for one concrete reason — the apps print body.error verbatim, so a throttled
student sees nothing at all where an error should be. It also sends no
Retry-After unless one is configured, and a client with no backoff hint retries
at once, which adds load to the exact moment the server had none to spare.

The other half is the counting. chi refuses with three different messages that
mean opposite things, and a single "429s" number is unreadable because the two
real cases call for opposite responses:

  capacity exceeded  the backlog is FULL, refused with no queue slot at all
                     → raise AUTH_THROTTLE_BACKLOG
  timed out          queued, then waited the whole backlog timeout
                     → raise THROUGHPUT; a bigger backlog makes this worse,
                       because the students at the back of a longer queue wait
                       the full timeout and are refused anyway
  client canceled    the caller gave up mid-wait — usually not actionable

⚠ The split is read off chi's message text, because the reason is not exposed in
any other way: ThrottleOpts.RetryAfterFn is handed only a ctxDone bool, which
separates the cancelled case from the other two but not the two that matter.
If a future chi reworded those strings the count would land in "unknown" rather
than in the wrong bucket, and TestThrottleRefusesAtCapacityWithJSON drives a
real throttler to its limit so the rewording fails a test rather than a fair.
*/

// Why a caller was refused. The strings are the JSON field values the page
// groups by, so they are part of the wire shape.
const (
	ThrottleCapacityExceeded = "capacity_exceeded"
	ThrottleTimedOut         = "timed_out"
	ThrottleClientCanceled   = "client_canceled"
	ThrottleUnknown          = "unknown"
)

// chi's own refusal bodies, from middleware/throttle.go. Unexported over there,
// so they are matched by value here. fmt.Fprintln adds the newline.
const (
	chiCapacityExceeded = "Server capacity exceeded.\n"
	chiTimedOut         = "Timed out while waiting for a pending request to complete.\n"
	chiContextCanceled  = "Context was canceled.\n"
)

// Thai, because both throttled groups are Thai-facing: /wbw/auth serves the
// เดินรอบดอย app and /clubfair/auth the Club Fair one, and each prints
// body.error straight onto the screen.
var throttleMessages = map[string]string{
	ThrottleCapacityExceeded: "ระบบกำลังมีผู้ใช้งานหนาแน่นมาก กรุณารอสักครู่แล้วลองใหม่",
	ThrottleTimedOut:         "รอคิวนานเกินไป กรุณาลองใหม่อีกครั้ง",
	ThrottleClientCanceled:   "การเชื่อมต่อถูกยกเลิกระหว่างรอคิว",
	ThrottleUnknown:          "ระบบกำลังมีผู้ใช้งานหนาแน่นมาก กรุณารอสักครู่แล้วลองใหม่",
}

// Throttle is one throttled group — /wbw/auth and /clubfair/auth each build
// their own, and the quota is per-group rather than server-wide.
type Throttle struct {
	name    string
	limit   int
	backlog int
	timeout time.Duration

	inner func(http.Handler) http.Handler

	admitted         atomic.Int64
	capacityExceeded atomic.Int64
	timedOut         atomic.Int64
	clientCanceled   atomic.Int64
	unknown          atomic.Int64
}

// NewThrottle builds the group. limit is how many auth requests are processed
// at once (bcrypt at cost 10 is ~80ms of CPU each), backlog how many more may
// queue, and timeout how long one may sit in that queue before it is refused.
//
// Real capacity is limit + backlog held at once — chi sizes its backlogTokens
// channel that way — and anything past it is refused instantly with no queue
// slot, which is the capacity_exceeded case above.
func NewThrottle(name string, limit, backlog int, timeout time.Duration) *Throttle {
	t := &Throttle{name: name, limit: limit, backlog: backlog, timeout: timeout}

	t.inner = chimw.ThrottleWithOpts(chimw.ThrottleOpts{
		Limit:          limit,
		BacklogLimit:   backlog,
		BacklogTimeout: timeout,
		RetryAfterFn:   t.retryAfter,
	})
	return t
}

/*
retryAfter is how long we ask a refused caller to wait, and it is deliberately
NOT a constant.

chi calls this once per refusal, so the value can differ per caller — and it
must. A fixed number sends every one of up to 2,540 refused students back at the
same instant, which is the queue that just overflowed arriving again as one
spike. Spreading them over the timeout and half again turns a second wall into
a ramp.

The floor is the full backlog timeout rather than something eager: a caller was
refused because the server had nothing spare, and coming back in three seconds
is how a throttle becomes a retry storm.

ctxDone means the caller had already disconnected, so nothing will read the
header. Same value, because branching on it would only add a case with no
observer.
*/
func (t *Throttle) retryAfter(ctxDone bool) time.Duration {
	if t.timeout <= 0 {
		return time.Second
	}
	return t.timeout + rand.N(t.timeout/2)
}

// Handler is the middleware to hand to r.Use.
func (t *Throttle) Handler() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		// chi calls this only for a request it ADMITTED. Note that it is
		// handed tw.ResponseWriter and not tw: past this point the throttle is
		// out of the way entirely, so nothing downstream — Flusher, Hijacker,
		// a handler writing its own status — is looking at a wrapper. The
		// wrapper below therefore only ever sees chi's own refusal.
		admitted := t.inner(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tw := w.(*throttleWriter)
			t.admitted.Add(1)
			next.ServeHTTP(tw.ResponseWriter, r)
		}))

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			admitted.ServeHTTP(&throttleWriter{ResponseWriter: w, throttle: t}, r)
		})
	}
}

// throttleWriter turns chi's plain-text refusal into {"error": "..."}.
//
// It holds the status back at WriteHeader, because the reason arrives with the
// BODY on the following Write and the counter has to be picked before anything
// is committed to the client.
type throttleWriter struct {
	http.ResponseWriter
	throttle *Throttle
	status   int
	done     bool
}

func (tw *throttleWriter) WriteHeader(status int) {
	tw.status = status
}

func (tw *throttleWriter) Write(p []byte) (int, error) {
	if tw.done {
		// http.Error writes once, but a second write must never append a
		// second body to a response already sent.
		return len(p), nil
	}
	tw.done = true

	reason := classifyThrottle(string(p))
	tw.throttle.count(reason)

	status := tw.status
	if status == 0 {
		status = http.StatusTooManyRequests
	}

	// Retry-After was already set on this header map by chi before http.Error,
	// so it survives — only the content type and the body are replaced.
	h := tw.ResponseWriter.Header()
	h.Set("Content-Type", "application/json")
	h.Del("X-Content-Type-Options")

	tw.ResponseWriter.WriteHeader(status)
	_ = json.NewEncoder(tw.ResponseWriter).Encode(map[string]string{
		"error": throttleMessages[reason],
	})

	// The caller (chi, via http.Error) is told its whole message was written.
	// Reporting the JSON length instead would look like a short write.
	return len(p), nil
}

func classifyThrottle(body string) string {
	switch body {
	case chiCapacityExceeded:
		return ThrottleCapacityExceeded
	case chiTimedOut:
		return ThrottleTimedOut
	case chiContextCanceled:
		return ThrottleClientCanceled
	default:
		return ThrottleUnknown
	}
}

func (t *Throttle) count(reason string) {
	switch reason {
	case ThrottleCapacityExceeded:
		t.capacityExceeded.Add(1)
	case ThrottleTimedOut:
		t.timedOut.Add(1)
	case ThrottleClientCanceled:
		t.clientCanceled.Add(1)
	default:
		t.unknown.Add(1)
	}
}

// ThrottleSnapshot is the wire shape. The configured numbers travel with the
// counters on purpose: "1,204 refused" means nothing without "out of 2,540
// that fit", and the settings live in env vars nobody reads during an event.
type ThrottleSnapshot struct {
	Name string `json:"name"`

	Limit    int     `json:"limit"`
	Backlog  int     `json:"backlog"`
	Capacity int     `json:"capacity"` // limit + backlog, held at once
	TimeoutS float64 `json:"timeout_seconds"`

	Admitted int64 `json:"admitted"`

	// Backlog full, refused instantly. Raise AUTH_THROTTLE_BACKLOG.
	CapacityExceeded int64 `json:"capacity_exceeded"`
	// Queued, then waited the whole timeout. Raise throughput — a bigger
	// backlog makes this one WORSE.
	TimedOut int64 `json:"timed_out"`
	// Caller gave up mid-wait. Usually not actionable.
	ClientCanceled int64 `json:"client_canceled"`
	// A refusal whose wording this build did not recognise; see the note at
	// the top of the file.
	Unknown int64 `json:"unknown"`
}

func (t *Throttle) Snapshot() ThrottleSnapshot {
	return ThrottleSnapshot{
		Name:             t.name,
		Limit:            t.limit,
		Backlog:          t.backlog,
		Capacity:         t.limit + t.backlog,
		TimeoutS:         t.timeout.Seconds(),
		Admitted:         t.admitted.Load(),
		CapacityExceeded: t.capacityExceeded.Load(),
		TimedOut:         t.timedOut.Load(),
		ClientCanceled:   t.clientCanceled.Load(),
		Unknown:          t.unknown.Load(),
	}
}
