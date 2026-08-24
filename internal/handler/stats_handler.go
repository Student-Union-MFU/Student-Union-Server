package handler

import (
	"context"
	_ "embed"
	"errors"
	"log/slog"
	"net/http"
	"runtime"
	"runtime/debug"
	"time"

	appmw "su-server/internal/middleware"
	"su-server/internal/model"
	"su-server/internal/repository"
	"su-server/internal/service"

	"github.com/jackc/pgx/v5/pgxpool"
)

/*
The server stats dashboard — see docs/stats-dashboard.md for why it exists.

The short version: on 2026-08-22 the server slowed under ~3,000 students at the
Club Fair, nothing crashed and nothing errored, and there was no way to ask the
process WHY from inside it. The cause was found by reading code, which will not
work a second time. Everything on this page is chosen so that the next slowdown
is attributable in a minute, and so that a claim like "this bought us 10x
headroom" can be checked rather than believed.

⚠ No service layer, deliberately, and it is the same argument the comment block
on DBPoolHandler makes: a runtime counter has no rule to apply and no decision
to make, so a service here would forward calls and decide nothing. The one part
that touches Postgres goes through StatsRepository like every other read in the
server — that is where the line is, and it is not "stats handlers may hold a
pool and write SQL".

The endpoints are staff-guarded. The PAGE is public and holds no numbers, for a
reason that is easy to get wrong: a browser navigating to a URL cannot send an
Authorization header, so putting auth middleware on the page would 401 every
visit forever. The gate belongs on the data, and it is there.
*/
type StatsHandler struct {
	pool     *pgxpool.Pool
	pg       *repository.StatsRepository
	requests *appmw.RequestMetrics

	// The two auth throttle groups. Independent quotas — see the comment on
	// the ThrottleBacklog call in cmd/main.go — so they are reported apart.
	throttles []*appmw.Throttle

	chat *service.ChatEvents
	sos  *service.SOSEvents
	push *service.WBWPushService

	startedAt time.Time
	build     buildStats
}

func NewStatsHandler(
	pool *pgxpool.Pool,
	pg *repository.StatsRepository,
	requests *appmw.RequestMetrics,
	chat *service.ChatEvents,
	sos *service.SOSEvents,
	push *service.WBWPushService,
	throttles ...*appmw.Throttle,
) *StatsHandler {
	return &StatsHandler{
		pool:      pool,
		pg:        pg,
		requests:  requests,
		throttles: throttles,
		chat:      chat,
		sos:       sos,
		push:      push,
		startedAt: time.Now(),
		build:     readBuildStats(),
	}
}

/* ---------- Phase 1: the Go runtime ---------- */

// buildStats is which commit is live, read once at startup.
//
// ⚠ Empty under `go run` and `make dev`: the toolchain only stamps VCS
// information into a binary it BUILDS. Blank here means a development process,
// not a broken read, and the page says so rather than showing an empty field.
type buildStats struct {
	GoVersion    string `json:"go_version"`
	Revision     string `json:"revision,omitempty"`
	RevisionTime string `json:"revision_time,omitempty"`
	// The working tree had uncommitted changes when this binary was built, so
	// Revision does not fully describe what is running.
	Modified bool `json:"modified"`
}

func readBuildStats() buildStats {
	out := buildStats{GoVersion: runtime.Version()}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return out
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			out.Revision = s.Value
		case "vcs.time":
			out.RevisionTime = s.Value
		case "vcs.modified":
			out.Modified = s.Value == "true"
		}
	}
	return out
}

type runtimeStats struct {
	/*
		The important one.

		Every long-poll parks a goroutine for up to 25 seconds, so this tracks
		concurrent pollers and rises and falls with them all day. What matters
		is the SHAPE: a number that climbs and never comes back down is a leak,
		and the likeliest cause is a ChatWatch whose Release() was never called
		— which would also show up as a waiter count that only grows, in the
		long_poll section of the composite response.
	*/
	Goroutines int `json:"goroutines"`

	// What the process actually got, which is NOT always the host's core
	// count: a container with a CPU limit still sees every core unless
	// GOMAXPROCS is set, and Go 1.25 onward reads the cgroup limit itself.
	// Printing both is how you find out which happened.
	GOMAXPROCS int `json:"gomaxprocs"`
	NumCPU     int `json:"num_cpu"`

	HeapInUseMB  float64 `json:"heap_in_use_mb"`
	HeapAllocMB  float64 `json:"heap_alloc_mb"`
	StackInUseMB float64 `json:"stack_in_use_mb"`
	// The heap size that will trigger the next collection. Heap in use sitting
	// just under it means the GC is about to run; far under means it just did.
	NextGCMB float64 `json:"next_gc_mb"`
	// Everything the runtime has obtained from the OS. This is the number to
	// compare against the box's memory, and the box already sits near 69%.
	SysMB float64 `json:"sys_mb"`

	GCCount        uint32     `json:"gc_count"`
	GCPauseTotalMS float64    `json:"gc_pause_total_ms"`
	LastGCPauseMS  float64    `json:"last_gc_pause_ms"`
	LastGCAt       *time.Time `json:"last_gc_at"`

	UptimeSeconds float64   `json:"uptime_seconds"`
	StartedAt     time.Time `json:"started_at"`

	Build buildStats `json:"build"`
}

/*
readRuntimeStats samples the Go runtime.

⚠ runtime.ReadMemStats STOPS THE WORLD. Every goroutine in the process is
paused for the duration, which is fine a few times a minute from a dashboard
and is not fine anywhere near a per-request middleware — a stats page that
pauses the server it is measuring reports its own cost as the server's. If this
ever needs sampling at any rate worth calling a rate, move to runtime/metrics,
which reads without a stop-the-world.
*/
func (h *StatsHandler) readRuntimeStats() runtimeStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	out := runtimeStats{
		Goroutines:     runtime.NumGoroutine(),
		GOMAXPROCS:     runtime.GOMAXPROCS(0),
		NumCPU:         runtime.NumCPU(),
		HeapInUseMB:    bytesToMB(m.HeapInuse),
		HeapAllocMB:    bytesToMB(m.HeapAlloc),
		StackInUseMB:   bytesToMB(m.StackInuse),
		NextGCMB:       bytesToMB(m.NextGC),
		SysMB:          bytesToMB(m.Sys),
		GCCount:        m.NumGC,
		GCPauseTotalMS: float64(m.PauseTotalNs) / 1e6,
		LastGCPauseMS:  lastGCPauseMS(m.NumGC, m.PauseNs),
		UptimeSeconds:  time.Since(h.startedAt).Seconds(),
		StartedAt:      h.startedAt,
		Build:          h.build,
	}

	// LastGC is nanoseconds since the epoch, and 0 means no collection has
	// happened yet — which is a real state for the first seconds of a process,
	// not a missing value to paper over with the zero time.
	if m.LastGC > 0 {
		t := time.Unix(0, int64(m.LastGC))
		out.LastGCAt = &t
	}
	return out
}

func bytesToMB(b uint64) float64 {
	return float64(b) / (1 << 20)
}

/*
lastGCPauseMS pulls the most recent pause out of MemStats' circular buffer.

PauseNs is a 256-entry ring written at index NumGC%256, so the most recent
completed pause is at (NumGC+255)%256 — one BEFORE where the next one will go.
The +255 is what makes the wrap work without a negative index when NumGC%256 is
0, and getting it wrong reads a pause from 255 collections ago and reports it as
current, which nothing would ever contradict. Split out and unit-tested for
exactly that reason.

NumGC == 0 means no collection has finished, so there is no last pause; the
whole array is zero at that point anyway, but returning early says so.
*/
func lastGCPauseMS(numGC uint32, pauseNs [256]uint64) float64 {
	if numGC == 0 {
		return 0
	}
	return float64(pauseNs[(numGC+255)%256]) / 1e6
}

// Runtime handles GET /su-server/admin/runtime — SU staff only.
func (h *StatsHandler) Runtime(w http.ResponseWriter, r *http.Request) {
	appmw.WriteJSON(w, http.StatusOK, h.readRuntimeStats())
}

/* ---------- Phase 2: request metrics ---------- */

// Requests handles GET /su-server/admin/requests — SU staff only.
func (h *StatsHandler) Requests(w http.ResponseWriter, r *http.Request) {
	appmw.WriteJSON(w, http.StatusOK, h.requests.Snapshot())
}

/* ---------- Phase 4: long-poll and fan-out internals ---------- */

type longPollStats struct {
	Chat service.ChatEventsStats `json:"chat"`
	SOS  service.SOSEventsStats  `json:"sos"`
}

func (h *StatsHandler) readLongPollStats() longPollStats {
	return longPollStats{Chat: h.chat.Stats(), SOS: h.sos.Stats()}
}

/* ---------- Phase 5: the Postgres side ---------- */

type postgresStats struct {
	// False when the extension is missing or was created without being loaded
	// through shared_preload_libraries. Everything else on this page still
	// renders — see ErrPGStatStatementsUnavailable.
	StatementsAvailable bool                `json:"statements_available"`
	Statements          []model.PGStatement `json:"statements"`

	Activity model.PGActivity `json:"activity"`
	Database model.PGDatabase `json:"database"`

	// Set when a read failed. Reported rather than raised, for the reason on
	// readPostgresStats.
	Error string `json:"error,omitempty"`
}

/*
readPostgresStats collects the three Postgres reads, and never fails the page.

An error here becomes a field on the response instead of a status code, and that
is a deliberate inversion of the usual handler rule. The page's entire purpose is
to be readable while something is wrong, and "something is wrong" very often
means the database. A 500 for the whole composite object would blank the
goroutine count, the pool counters and the request table — every number that
does not need Postgres to be healthy — at the one moment they matter most.

The deadline is here for the same reason. A database that has stopped answering
must make this section say so within a few seconds, not hold the connection open
until the browser gives up and shows nothing at all.
*/
func (h *StatsHandler) readPostgresStats(ctx context.Context) postgresStats {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var out postgresStats

	activity, err := h.pg.Activity(ctx)
	if err != nil {
		slog.Error("stats: pg_stat_activity read failed", "err", err)
		out.Error = err.Error()
	}
	out.Activity = activity

	database, err := h.pg.Database(ctx)
	if err != nil {
		slog.Error("stats: pg_stat_database read failed", "err", err)
		if out.Error == "" {
			out.Error = err.Error()
		}
	}
	out.Database = database

	statements, err := h.pg.TopStatements(ctx, 10)
	switch {
	case errors.Is(err, repository.ErrPGStatStatementsUnavailable):
		// Not an error worth logging on every refresh: it is a configuration
		// state with a documented fix, and the page explains it.
		out.Statements = []model.PGStatement{}
	case err != nil:
		slog.Error("stats: pg_stat_statements read failed", "err", err)
		if out.Error == "" {
			out.Error = err.Error()
		}
		out.Statements = []model.PGStatement{}
	default:
		out.StatementsAvailable = true
		out.Statements = statements
	}
	return out
}

// Postgres handles GET /su-server/admin/postgres — SU staff only.
func (h *StatsHandler) Postgres(w http.ResponseWriter, r *http.Request) {
	appmw.WriteJSON(w, http.StatusOK, h.readPostgresStats(r.Context()))
}

/* ---------- the composite ---------- */

type allStats struct {
	// The server's own clock at the moment of sampling. The page computes
	// per-second rates by differencing two responses, and doing that against
	// the BROWSER's clock would fold clock skew and network latency into every
	// rate it prints.
	Now time.Time `json:"now"`

	Runtime   runtimeStats             `json:"runtime"`
	Pool      dbPoolStats              `json:"pool"`
	Requests  appmw.RequestsSnapshot   `json:"requests"`
	Throttles []appmw.ThrottleSnapshot `json:"throttles"`
	LongPoll  longPollStats            `json:"long_poll"`
	Push      service.PushStats        `json:"push"`
	Postgres  postgresStats            `json:"postgres"`
}

/*
All handles GET /su-server/admin/stats — SU staff only. This is what the page
polls.

One endpoint rather than six parallel fetches, for the same reason
ClubFairProgress is one call: six requests are six chances to render half a
state, and on a page whose job is to describe a server under load, six is also
six pool acquisitions per refresh instead of one.

The per-phase endpoints beside it stay for reading with curl during an incident,
where fetching one section is the point.
*/
func (h *StatsHandler) All(w http.ResponseWriter, r *http.Request) {
	out := allStats{
		Now:      time.Now(),
		Runtime:  h.readRuntimeStats(),
		Pool:     readPoolStats(h.pool),
		Requests: h.requests.Snapshot(),
		LongPoll: h.readLongPollStats(),
		Push:     h.push.Stats(),
		Postgres: h.readPostgresStats(r.Context()),
	}

	out.Throttles = make([]appmw.ThrottleSnapshot, 0, len(h.throttles))
	for _, t := range h.throttles {
		out.Throttles = append(out.Throttles, t.Snapshot())
	}

	appmw.WriteJSON(w, http.StatusOK, out)
}

/* ---------- the page ---------- */

//go:embed stats_dashboard.html
var statsDashboardHTML []byte

/*
StatsDashboardPage handles GET /su-server/stats — PUBLIC, and holds no numbers.

Embedded in the binary rather than served from a directory, following
clubfair_dashboard.html and for the reason written on DashboardPage: the
Dockerfile's single-binary build stays true, there is no static path to get
wrong at runtime, and there is no npm step between a change and a deploy.

⚠ Do not add auth middleware here. A browser navigating to a URL cannot send an
Authorization header, so a guard on this route would 401 every visit and could
never be satisfied. The page is an empty shell; every number on it comes from
/su-server/admin/stats, which is where the SU staff gate lives.
*/
func (h *StatsHandler) StatsDashboardPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(statsDashboardHTML)
}
