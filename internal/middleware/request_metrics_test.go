package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

// Percentiles are the reason to have this at all, and an off-by-one in the rank
// reports a p94 as a p95 forever without anything contradicting it.
func TestPercentileUsesNearestRank(t *testing.T) {
	// 1..100 milliseconds, in microseconds, ascending.
	samples := make([]int32, 100)
	for i := range samples {
		samples[i] = int32((i + 1) * 1000)
	}

	cases := []struct {
		p    float64
		want float64
	}{
		{50, 50},
		{95, 95},
		{99, 99},
		{100, 100},
	}
	for _, c := range cases {
		if got := percentileMS(samples, c.p); got != c.want {
			t.Errorf("p%v over 1..100ms: expected %vms, got %vms", c.p, c.want, got)
		}
	}
}

// A route nobody has called yet is the normal state for most of the table right
// after a deploy, and 0 is the only honest answer.
func TestPercentileOfNoSamplesIsZero(t *testing.T) {
	if got := percentileMS(nil, 99); got != 0 {
		t.Errorf("expected 0 for an empty window, got %v", got)
	}
}

// With one sample every percentile is that sample. The rank must not round to 0
// and index out of range.
func TestPercentileOfOneSample(t *testing.T) {
	for _, p := range []float64{50, 95, 99} {
		if got := percentileMS([]int32{7000}, p); got != 7 {
			t.Errorf("p%v of a single 7ms sample: expected 7, got %v", p, got)
		}
	}
}

// The window is what makes a percentile mean anything: cumulative-since-boot
// latency dilutes a bad afternoon with a quiet morning, which is exactly the
// problem this page exists to fix.
func TestOnlyTheLastSamplesCountTowardsPercentiles(t *testing.T) {
	m := NewRequestMetrics()

	// Fill the whole ring with 1ms, then overwrite all of it with 100ms.
	for range latencySamples {
		m.record("GET", "/x", 200, time.Millisecond)
	}
	for range latencySamples {
		m.record("GET", "/x", 200, 100*time.Millisecond)
	}

	row := onlyRoute(t, m)
	if row.Count != int64(latencySamples*2) {
		t.Errorf("count should be cumulative: expected %d, got %d", latencySamples*2, row.Count)
	}
	if row.Samples != latencySamples {
		t.Errorf("window should cap at %d, got %d", latencySamples, row.Samples)
	}
	if row.P50MS != 100 {
		t.Errorf("the old 1ms samples should have rolled out of the window, p50 is %v", row.P50MS)
	}
}

// Keyed by pattern, not URL: /booths/1 and /booths/2 are the same route. If they
// were not, the map would grow with traffic and every percentile would be taken
// over a single sample.
func TestPathParametersCollapseIntoOneRoute(t *testing.T) {
	m := NewRequestMetrics()
	r := chi.NewRouter()
	r.Use(RecordRequests(m))
	r.Get("/booths/{id}", func(w http.ResponseWriter, r *http.Request) {})

	for _, path := range []string{"/booths/1", "/booths/2", "/booths/9999"} {
		serve(r, http.MethodGet, path)
	}

	snap := m.Snapshot()
	if len(snap.Routes) != 1 {
		t.Fatalf("expected one row, got %d: %+v", len(snap.Routes), snap.Routes)
	}
	if snap.Routes[0].Pattern != "/booths/{id}" {
		t.Errorf("expected the pattern, got %q", snap.Routes[0].Pattern)
	}
	if snap.Routes[0].Count != 3 {
		t.Errorf("expected 3 requests on the one row, got %d", snap.Routes[0].Count)
	}
}

// A scanner walking random URLs must add rows to nothing.
func TestUnmatchedRequestsShareOneRow(t *testing.T) {
	m := NewRequestMetrics()
	r := chi.NewRouter()
	r.Use(RecordRequests(m))
	r.Get("/known", func(w http.ResponseWriter, r *http.Request) {})

	for _, path := range []string{"/wp-admin", "/.env", "/phpmyadmin"} {
		serve(r, http.MethodGet, path)
	}

	row := onlyRoute(t, m)
	if row.Pattern != unroutedKey {
		t.Fatalf("expected everything unrouted in one bucket, got %q", row.Pattern)
	}
	if row.Count != 3 || row.C4xx != 3 {
		t.Errorf("expected 3 requests all 4xx, got count=%d c4xx=%d", row.Count, row.C4xx)
	}
}

// Reading a page and writing one are different work at the same pattern.
// Merging them averages a cheap call with an expensive one and hides both.
func TestMethodIsPartOfTheKey(t *testing.T) {
	m := NewRequestMetrics()
	m.record("GET", "/wbw/groups/{groupId}/messages", 200, time.Millisecond)
	m.record("POST", "/wbw/groups/{groupId}/messages", 201, time.Millisecond)

	if got := len(m.Snapshot().Routes); got != 2 {
		t.Errorf("GET and POST on one pattern should be two rows, got %d", got)
	}
}

/*
The ordering trap, and the reason this middleware is mounted above Recoverer
rather than below it.

chi nests middleware outermost-first, so a handler panicking underneath
Recoverer unwinds through everything below it BEFORE the recover() runs. A
recorder mounted there records status 0 and never sees the 500 the client got.
Mounted above, Recoverer writes its 500 into the wrapped writer first.
*/
func TestAPanickingHandlerIsRecordedAsA500(t *testing.T) {
	// chi's Recoverer prints the panic and its stack to stderr. Silence it, or
	// a passing test looks like a failing one.
	realStderr := os.Stderr
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("could not open %s: %v", os.DevNull, err)
	}
	os.Stderr = devNull
	defer func() {
		os.Stderr = realStderr
		devNull.Close()
	}()

	m := NewRequestMetrics()
	r := chi.NewRouter()
	r.Use(RecordRequests(m))
	r.Use(chimw.Recoverer)
	r.Get("/boom", func(w http.ResponseWriter, r *http.Request) {
		panic("the handler this page exists to find")
	})

	rec := serve(r, http.MethodGet, "/boom")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("Recoverer should have answered 500, got %d", rec.Code)
	}

	row := onlyRoute(t, m)
	if row.C5xx != 1 {
		t.Errorf("expected the panic recorded as a 5xx, got c5xx=%d c2xx=%d", row.C5xx, row.C2xx)
	}
}

// A handler that returns without writing gets an implicit 200 from net/http,
// which the wrapper never sees. Counting that as "0xx" would only confuse.
func TestAHandlerThatWritesNothingCountsAs2xx(t *testing.T) {
	m := NewRequestMetrics()
	m.record("GET", "/quiet", 0, time.Millisecond)

	if row := onlyRoute(t, m); row.C2xx != 1 {
		t.Errorf("expected an unwritten response counted as 2xx, got %+v", row)
	}
}

// The long-poll flag is what stops a 25-second chat/sync reading as a fault.
func TestLongPollRoutesAreFlagged(t *testing.T) {
	const sync = "GET /wbw/groups/{groupId}/chat/sync"
	m := NewRequestMetrics(sync)

	m.record("GET", "/wbw/groups/{groupId}/chat/sync", 200, 25*time.Second)
	m.record("GET", "/wbw/capacity", 200, time.Millisecond)

	for _, row := range m.Snapshot().Routes {
		want := row.Pattern == "/wbw/groups/{groupId}/chat/sync"
		if row.LongPoll != want {
			t.Errorf("%s %s: long_poll=%v, expected %v", row.Method, row.Pattern, row.LongPoll, want)
		}
	}
}

// The pool is one pool and cannot be split by product, but request metrics can —
// and during an event "is this WBW or Club Fair" is the question being asked.
func TestRoutesAreLabelledByProduct(t *testing.T) {
	cases := map[string]string{
		"/wbw/me/progress":         "wbw",
		"/clubfair/progress":       "clubfair",
		"/su-server/admin/db-pool": "su",
		"/privacy":                 "other",
		unroutedKey:                "other",
	}
	for pattern, want := range cases {
		if got := productOf(pattern); got != want {
			t.Errorf("%s: expected product %q, got %q", pattern, want, got)
		}
	}
}

// A duration cannot legitimately be negative, but a percentile computed from a
// corrupted sample is a number nobody can tell is wrong.
func TestClampMicrosRefusesNegativeDurations(t *testing.T) {
	if got := clampMicros(-time.Second); got != 0 {
		t.Errorf("expected 0 for a negative duration, got %d", got)
	}
	if got := clampMicros(25 * time.Second); got != 25_000_000 {
		t.Errorf("a 25s long-poll must survive the conversion, got %d", got)
	}
}

/* ---------- helpers ---------- */

func serve(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func onlyRoute(t *testing.T, m *RequestMetrics) RouteSnapshot {
	t.Helper()
	snap := m.Snapshot()
	if len(snap.Routes) != 1 {
		t.Fatalf("expected exactly one route, got %d: %+v", len(snap.Routes), snap.Routes)
	}
	return snap.Routes[0]
}
