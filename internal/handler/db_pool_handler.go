package handler

import (
	"net/http"
	appmw "su-server/internal/middleware"

	"github.com/jackc/pgx/v5/pgxpool"
)

/*
Why this endpoint exists.

Every product in this binary — SU, WBW and Club Fair — shares the one pool built
in config.ConnectPool, and DB_MAX_CONNS (default 20) is the ceiling on how much
database work the server can have in flight at once. That number was picked by
reasoning, never by measurement, because nothing in the process ever reported
what the pool was actually doing. Raising it on a hunch is the wrong move in both
directions: too low and requests queue behind connections that exist; too high
and Postgres runs more backends than the box has cores, so throughput FALLS while
every one of them holds work_mem. Neither failure announces itself — both just
look like "the server felt slow at peak".

EmptyAcquireCount is the number that settles it, and it is the reason to read
this page at all: it counts the times a caller found the pool empty and had to
WAIT for a connection. While it stays flat, the pool is not the constraint and
raising DB_MAX_CONNS cannot help — the queue is somewhere else (CPU inside
Postgres, a slow query, the bcrypt throttle on the auth routes). Only when it
climbs during real load is a bigger pool the answer, and then MeanAcquireMS says
how much the waiting is costing.

This handler holds the pool directly and has no service behind it, which is the
one place in this codebase that is right: there is no rule to apply and no
decision to make, only a counter to read. A service layer here would be an empty
pass-through. LegalPrivacyPage/LegalSupportPage are service-less for the same
reason.

Staff-only. The numbers describe the shape of our infrastructure and would tell
someone probing exactly how much headroom is left before the pool starves.
*/
type DBPoolHandler struct {
	pool *pgxpool.Pool
}

func NewDBPoolHandler(pool *pgxpool.Pool) *DBPoolHandler {
	return &DBPoolHandler{pool: pool}
}

// dbPoolStats is the wire shape. Field names are snake_case to match every other
// response this server sends, and the two derived numbers at the bottom exist so
// the answer can be read off the page without doing arithmetic during an event.
type dbPoolStats struct {
	// Capacity. MaxConns is DB_MAX_CONNS; TotalConns is how many exist right
	// now (idle + acquired + being built), which sits below the max until load
	// has ever needed them.
	MaxConns          int32 `json:"max_conns"`
	TotalConns        int32 `json:"total_conns"`
	AcquiredConns     int32 `json:"acquired_conns"`
	IdleConns         int32 `json:"idle_conns"`
	ConstructingConns int32 `json:"constructing_conns"`

	// Lifetime counters, since process start.
	AcquireCount int64 `json:"acquire_count"`

	// The one that decides whether DB_MAX_CONNS is too small. Times a caller
	// found no free connection and blocked until one was returned. Flat under
	// load = the pool is not your bottleneck.
	EmptyAcquireCount int64 `json:"empty_acquire_count"`

	// Callers that gave up waiting — a client disconnecting mid-request, or a
	// context deadline. A rising count next to a rising EmptyAcquireCount means
	// the wait got long enough that requests died in the queue.
	CanceledAcquireCount int64 `json:"canceled_acquire_count"`

	NewConnsCount           int64 `json:"new_conns_count"`
	MaxLifetimeDestroyCount int64 `json:"max_lifetime_destroy_count"`
	MaxIdleDestroyCount     int64 `json:"max_idle_destroy_count"`

	// Cumulative time spent waiting for a connection, and that time divided by
	// AcquireCount. The mean is the honest headline: a handful of slow waits
	// hidden inside millions of instant ones is not a problem worth reconfiguring
	// the pool over.
	AcquireDurationMS float64 `json:"acquire_duration_ms"`
	MeanAcquireMS     float64 `json:"mean_acquire_ms"`

	// EmptyAcquireCount as a share of all acquires. Reading "0.004%" takes no
	// arithmetic at 3am; comparing two ten-digit counters does.
	EmptyAcquirePct float64 `json:"empty_acquire_pct"`
}

// GET /su-server/admin/db-pool — SU staff only.
func (h *DBPoolHandler) Stats(w http.ResponseWriter, r *http.Request) {
	appmw.WriteJSON(w, http.StatusOK, readPoolStats(h.pool))
}

// readPoolStats is the whole of the reading, split out from the handler so the
// composite /su-server/admin/stats can carry the same numbers without a second
// copy of the field list. Two copies would drift, and the one on the page
// people actually read is the one that would go stale.
func readPoolStats(pool *pgxpool.Pool) dbPoolStats {
	s := pool.Stat()

	out := dbPoolStats{
		MaxConns:                s.MaxConns(),
		TotalConns:              s.TotalConns(),
		AcquiredConns:           s.AcquiredConns(),
		IdleConns:               s.IdleConns(),
		ConstructingConns:       s.ConstructingConns(),
		AcquireCount:            s.AcquireCount(),
		EmptyAcquireCount:       s.EmptyAcquireCount(),
		CanceledAcquireCount:    s.CanceledAcquireCount(),
		NewConnsCount:           s.NewConnsCount(),
		MaxLifetimeDestroyCount: s.MaxLifetimeDestroyCount(),
		MaxIdleDestroyCount:     s.MaxIdleDestroyCount(),
		AcquireDurationMS:       float64(s.AcquireDuration().Microseconds()) / 1000,
	}

	out.MeanAcquireMS, out.EmptyAcquirePct = derivePoolRates(
		s.AcquireCount(), s.EmptyAcquireCount(), out.AcquireDurationMS)

	return out
}

// derivePoolRates turns the raw counters into the two numbers worth reading.
//
// Split out from Stats so the zero case can be tested without a live pool, and
// it is the case that matters: a pool that has served nothing yet is the normal
// state for the first seconds after a deploy, and 0/0 in Go is NaN rather than a
// panic. encoding/json REFUSES to encode NaN — it returns UnsupportedValueError
// — and WriteJSON has already written the 200 and the headers by then, so the
// client would receive a truncated body with no error it could read. The guard
// is the difference between "the stats page works" and "the stats page breaks
// exactly when you first look at it".
func derivePoolRates(acquireCount, emptyAcquireCount int64, acquireDurationMS float64) (meanMS, emptyPct float64) {
	if acquireCount <= 0 {
		return 0, 0
	}
	return acquireDurationMS / float64(acquireCount),
		float64(emptyAcquireCount) / float64(acquireCount) * 100
}
