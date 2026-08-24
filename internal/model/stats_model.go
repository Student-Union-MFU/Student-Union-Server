package model

import "time"

/*
What Postgres says about itself, for the server stats dashboard.

These are the numbers the 2026-08-22 slowdown needed and nobody had. The pool
counters in db_pool_handler.go answer "are we queueing for a connection"; when
that says no — and it did — the cost is INSIDE Postgres and none of it is
visible from the Go process. TopStatements is the whole answer to "which query",
ranked by the time it has actually consumed on real traffic, with no load test
and no guessing which endpoint to model.
*/

// PGStatement is one row of pg_stat_statements, normalised so the page can sort
// on it without arithmetic.
type PGStatement struct {
	// The normalised query text, truncated by the query itself. Constants are
	// already replaced with $1, $2 … by the extension, so two calls of the same
	// statement with different ids share one row — which is the entire point.
	Query string `json:"query"`
	// Postgres' own identifier for the normalised statement. Nullable in
	// principle (a statement can be tracked without one), so a pointer.
	QueryID *int64 `json:"query_id"`

	Calls int64 `json:"calls"`
	// The ranking column. Total execution time is what makes a cheap query
	// called ten thousand times outrank an expensive one called twice — and
	// the query that ate the fair was exactly the first kind.
	TotalExecMS float64 `json:"total_exec_ms"`
	MeanExecMS  float64 `json:"mean_exec_ms"`
	MaxExecMS   float64 `json:"max_exec_ms"`
	Rows        int64   `json:"rows"`

	// Blocks found in Postgres' own cache versus read from the filesystem. A
	// statement with a high read count next to a high call count is the shape
	// of a sequential scan over a table that no longer fits in memory.
	SharedBlksHit  int64 `json:"shared_blks_hit"`
	SharedBlksRead int64 `json:"shared_blks_read"`
}

// PGActivity counts what the backends are doing right now, against the ceiling
// they share.
type PGActivity struct {
	Active            int `json:"active"`
	Idle              int `json:"idle"`
	IdleInTransaction int `json:"idle_in_transaction"`
	// Backends blocked on a lock. Nonzero for more than a moment means writers
	// are queueing behind each other — wbw_capacity and participant_group both
	// serialise every writer on one row by design, so this is where that shows.
	WaitingOnLock int `json:"waiting_on_lock"`

	// Client backends only: background workers and autovacuum do not compete
	// for the same ceiling.
	Total int `json:"total"`
	// The ceiling, read from pg_settings rather than assumed. Nothing in this
	// repo overrides it, so it is the Postgres default of 100 — against which
	// the app takes at most DB_MAX_CONNS (20) plus the two dedicated LISTEN
	// connections.
	MaxConnections int `json:"max_connections"`

	// How long the oldest idle-in-transaction backend has been sitting there.
	// One of these holds its locks and stops vacuum from cleaning up rows for
	// the whole database, and it is invisible in every other number here.
	LongestIdleInTransactionSeconds float64 `json:"longest_idle_in_transaction_seconds"`
}

// PGDatabase is pg_stat_database for the database we are connected to.
type PGDatabase struct {
	XactCommit   int64 `json:"xact_commit"`
	XactRollback int64 `json:"xact_rollback"`
	// Two transactions that deadlocked and one of them was killed by Postgres.
	// Should be 0. Anything else is a lock ordering bug, not load.
	Deadlocks int64 `json:"deadlocks"`

	BlksHit  int64 `json:"blks_hit"`
	BlksRead int64 `json:"blks_read"`
	// BlksHit as a share of all block reads. Below ~95% under load means
	// Postgres is going to disk for data it should be holding.
	CacheHitPct float64 `json:"cache_hit_pct"`

	// Queries that spilled to disk because work_mem was not enough. Each one is
	// a sort or a hash that would have been in memory with a bigger work_mem —
	// and the reason a too-large pool hurts, since every backend can hold one.
	TempFiles int64 `json:"temp_files"`
	TempBytes int64 `json:"temp_bytes"`

	// When these counters were last zeroed. Everything above is cumulative from
	// this instant, and without it "48 rollbacks" has no denominator in time.
	StatsReset *time.Time `json:"stats_reset"`
}
