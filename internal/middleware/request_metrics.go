package middleware

import (
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

/*
Per-route request metrics — count, latency and status classes, held in memory.

Why this exists. On 2026-08-22 the server slowed under ~3,000 students at the
Club Fair. Nothing errored and nothing crashed, so there was no log line to
follow and no way to ask the process which endpoint was eating the afternoon.
The answer (one query on GET /clubfair/progress scanning the whole of
clubfair_checkin) was found by reading code, which does not scale to the next
one. This middleware is the thing that would have pointed at it in a minute:
the offending route's p99 stands out even when its status codes are all 200.

Keyed by chi's ROUTE PATTERN, never the raw URL. /booths/1 and /booths/2 must
land in the same row or the map grows with traffic and every row has a sample
count of one, which is a memory leak and a useless percentile at the same time.
A request that matched no route has an empty pattern and is bucketed under
unroutedKey, so a scanner walking random paths adds rows to nothing.

The method is part of the key as well. GET /wbw/groups/{groupId}/messages reads
a page and POST to the same pattern writes a row and fans out a push; merging
them would average a cheap call with an expensive one and hide both.

No new dependency, no Prometheus: a mutex-guarded map is the house pattern for
shared state (see schoolsCache in wbw_admin_handler.go), and two more containers
to watch a memory-pressured single box is a poor bargain.
*/

// How many recent latencies to keep per route. Percentiles are computed over
// this window, NOT over all time, and that is the point: a p99 diluted by a
// quiet morning is exactly the number that made the last slowdown hard to see.
// 512 int32s is 2 KB per route — a few hundred KB across every route this
// server has, which is affordable in a way that keeping every sample is not.
const latencySamples = 512

// Where requests that matched no route are counted. One row, not one per URL.
const unroutedKey = "(no route)"

// RequestMetrics is the store. One instance, built in cmd/main.go and shared by
// the middleware that writes it and the stats handler that reads it.
type RequestMetrics struct {
	mu     sync.RWMutex
	routes map[string]*routeStat

	// Requests currently inside the middleware, across every route.
	//
	// ⚠ Global rather than per-route, and that is a limitation with a reason:
	// chi only fills RouteContext.RoutePattern DURING routing, which happens
	// below this middleware, so the pattern is knowable on the way out and not
	// on the way in. Resolving it early means calling Mux.Find with a second
	// route context — a whole extra tree walk and an allocation on every single
	// request, paid forever, so that a page nobody has open can show one more
	// column. The per-route half of the question is answered from the other
	// side anyway: the long-polls are what hold requests open, and ChatEvents
	// and SOSEvents report their waiter counts directly.
	inFlight atomic.Int64

	// Set at construction and never written again, so it needs no lock.
	longPoll map[string]struct{}
}

// A route's counters and its window of recent latencies.
//
// Each route carries its own mutex so recording contends only with the same
// route's own traffic, and the store's RWMutex is held just long enough to find
// the pointer. One lock over the whole map would serialise every request in the
// server behind whichever route is busiest.
type routeStat struct {
	mu sync.Mutex

	method  string
	pattern string

	count int64
	c2xx  int64
	c3xx  int64
	c4xx  int64
	c5xx  int64

	// Ring buffer of the last latencySamples durations, in MICROSECONDS.
	// int32 holds just over 35 minutes, and the longest request this server
	// can produce is a 25-second long-poll.
	samples [latencySamples]int32
	written int // how many slots hold a real sample, capped at len(samples)
	next    int // where the next sample goes
}

// NewRequestMetrics builds the store. longPollPatterns are route keys
// ("GET /wbw/groups/{groupId}/chat/sync") whose latency is SUPPOSED to be 25
// seconds — see docs/chat-v2-deploy.md. They are flagged rather than excluded:
// dropping them would hide a chat/sync that has started failing fast, and
// leaving them unmarked trains whoever reads the page to ignore a real p99.
func NewRequestMetrics(longPollPatterns ...string) *RequestMetrics {
	m := &RequestMetrics{
		routes:   make(map[string]*routeStat),
		longPoll: make(map[string]struct{}, len(longPollPatterns)),
	}
	for _, p := range longPollPatterns {
		m.longPoll[p] = struct{}{}
	}
	return m
}

/*
RecordRequests times every request and files it under its route pattern.

⚠ Mount this ABOVE middleware.Recoverer, not below it.

docs/stats-dashboard.md asks for it below, so that "a panicking handler still
records as a 500" — but below is what prevents that. chi's middlewares nest
outermost-first, so a handler panicking underneath Recoverer unwinds through
this one on its way UP to the recover(). Our deferred record would run while the
panic is still in flight, before Recoverer has written anything, and would file
the request as status 0. Mounted above, Recoverer catches the panic first,
writes its 500 into the wrapped writer below us, and we read the 500 that the
client actually received. The goal in the document is the right one; only the
ordering it prescribes gets in its way.
*/
func RecordRequests(m *RequestMetrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// chi's own wrapper, not a hand-rolled struct: it forwards
			// http.Flusher and http.Hijacker, and a wrapper that drops Flusher
			// silently breaks the long-polls rather than failing loudly.
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)

			m.inFlight.Add(1)
			started := time.Now()

			defer func() {
				m.inFlight.Add(-1)
				m.record(
					r.Method,
					chi.RouteContext(r.Context()).RoutePattern(),
					ww.Status(),
					time.Since(started),
				)
			}()

			next.ServeHTTP(ww, r)
		})
	}
}

func (m *RequestMetrics) record(method, pattern string, status int, took time.Duration) {
	if pattern == "" {
		pattern = unroutedKey
	}
	key := method + " " + pattern

	m.mu.RLock()
	st := m.routes[key]
	m.mu.RUnlock()

	if st == nil {
		m.mu.Lock()
		// Checked again under the write lock: two requests can both miss the
		// read above, and the loser must not throw away the winner's counters.
		if st = m.routes[key]; st == nil {
			st = &routeStat{method: method, pattern: pattern}
			m.routes[key] = st
		}
		m.mu.Unlock()
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	st.count++
	switch classOf(status) {
	case 2:
		st.c2xx++
	case 3:
		st.c3xx++
	case 4:
		st.c4xx++
	case 5:
		st.c5xx++
	}

	st.samples[st.next] = clampMicros(took)
	st.next = (st.next + 1) % latencySamples
	if st.written < latencySamples {
		st.written++
	}
}

// classOf maps a status to its leading digit.
//
// Status 0 means the handler returned without writing anything, and net/http
// then sends a 200 the wrapper never saw. Counting it as 2xx is what the client
// actually received; a "0xx" bucket would only ever confuse.
func classOf(status int) int {
	if status == 0 {
		return 2
	}
	return status / 100
}

// clampMicros converts to microseconds and pins the result inside int32.
// Nothing here can produce 35 minutes — the longest path is a 25-second
// long-poll — but a negative or absurd duration must not corrupt a percentile.
func clampMicros(d time.Duration) int32 {
	us := d.Microseconds()
	switch {
	case us < 0:
		return 0
	case us > 1<<31-1:
		return 1<<31 - 1
	}
	return int32(us)
}

/* ---------- reading ---------- */

// RouteSnapshot is the wire shape for one row of the table. snake_case like
// every other response this server sends.
type RouteSnapshot struct {
	Method  string `json:"method"`
	Pattern string `json:"pattern"`
	// Which of the three products the route belongs to, so the page can show
	// WBW and Club Fair traffic apart. The pool cannot be split this way — it
	// is one pool — but request metrics can, and during an event that is the
	// question being asked.
	Product string `json:"product"`

	Count int64 `json:"count"`
	C2xx  int64 `json:"c2xx"`
	C3xx  int64 `json:"c3xx"`
	C4xx  int64 `json:"c4xx"`
	C5xx  int64 `json:"c5xx"`

	// Over the last latencySamples requests to this route, not since boot.
	P50MS float64 `json:"p50_ms"`
	P95MS float64 `json:"p95_ms"`
	P99MS float64 `json:"p99_ms"`
	// How many samples the percentiles above were computed from. A p99 taken
	// over four requests is not a p99, and the page says so rather than
	// printing a number that looks like the others.
	Samples int `json:"samples"`

	// True for the routes that are MEANT to sit at 25 seconds.
	LongPoll bool `json:"long_poll"`
}

// RequestsSnapshot is what GET /su-server/admin/requests returns.
type RequestsSnapshot struct {
	InFlight int64           `json:"in_flight"`
	Total    int64           `json:"total"`
	Routes   []RouteSnapshot `json:"routes"`
}

// Snapshot copies every route's counters and computes its percentiles.
//
// Sorting a few hundred windows of 512 is trivial at the 10–15 second refresh
// this page is meant to poll at, and it keeps the recording path — which runs
// on every request — down to an increment and a ring write.
func (m *RequestMetrics) Snapshot() RequestsSnapshot {
	m.mu.RLock()
	stats := make([]*routeStat, 0, len(m.routes))
	for _, st := range m.routes {
		stats = append(stats, st)
	}
	m.mu.RUnlock()

	out := RequestsSnapshot{
		InFlight: m.inFlight.Load(),
		Routes:   make([]RouteSnapshot, 0, len(stats)),
	}

	buf := make([]int32, 0, latencySamples)
	for _, st := range stats {
		st.mu.Lock()
		row := RouteSnapshot{
			Method:  st.method,
			Pattern: st.pattern,
			Count:   st.count,
			C2xx:    st.c2xx,
			C3xx:    st.c3xx,
			C4xx:    st.c4xx,
			C5xx:    st.c5xx,
			Samples: st.written,
		}
		buf = append(buf[:0], st.samples[:st.written]...)
		st.mu.Unlock()

		row.Product = productOf(row.Pattern)
		_, row.LongPoll = m.longPoll[row.Method+" "+row.Pattern]

		sort.Slice(buf, func(i, j int) bool { return buf[i] < buf[j] })
		row.P50MS = percentileMS(buf, 50)
		row.P95MS = percentileMS(buf, 95)
		row.P99MS = percentileMS(buf, 99)

		out.Total += row.Count
		out.Routes = append(out.Routes, row)
	}

	// Busiest first: the row worth looking at during an event is almost always
	// the one carrying the traffic.
	sort.Slice(out.Routes, func(i, j int) bool {
		if out.Routes[i].Count != out.Routes[j].Count {
			return out.Routes[i].Count > out.Routes[j].Count
		}
		return out.Routes[i].Pattern < out.Routes[j].Pattern
	})
	return out
}

// percentileMS returns the p-th percentile of an ASCENDING slice of
// microsecond samples, in milliseconds.
//
// Nearest-rank, not interpolated: with 512 samples the difference is noise, and
// the rank is a real observed request rather than an average of two that never
// happened. Split out and unit-tested because arithmetic like this fails
// silently — an off-by-one here reports a p95 that is really a p94 and nothing
// ever contradicts it.
func percentileMS(ascending []int32, p float64) float64 {
	n := len(ascending)
	if n == 0 {
		return 0
	}
	rank := int(float64(n)*p/100 + 0.999999) // ceil, without importing math
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return float64(ascending[rank-1]) / 1000
}

// productOf names which of the three products a route belongs to. The prefixes
// are the ones cmd/main.go mounts; anything else ("/", "/privacy", the unrouted
// bucket) is "other".
func productOf(pattern string) string {
	switch {
	case strings.HasPrefix(pattern, "/wbw"):
		return "wbw"
	case strings.HasPrefix(pattern, "/clubfair"):
		return "clubfair"
	case strings.HasPrefix(pattern, "/su-server"):
		return "su"
	default:
		return "other"
	}
}
