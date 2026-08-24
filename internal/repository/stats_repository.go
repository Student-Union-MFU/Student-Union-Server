package repository

import (
	"context"
	"errors"

	"su-server/internal/model"

	"github.com/jackc/pgx/v5/pgxpool"
)

/*
Postgres' own view of itself, for the server stats dashboard.

Why this is a repository at all, when the runtime and request-metric endpoints
next to it deliberately have no layer under them: those read a counter that
lives in this process, and there is no rule to apply. These read SQL. CLAUDE.md
draws that line in one place and it holds here — a handler that reads a live
atomic needs nothing behind it, and a handler that opens a connection goes
through a repository like every other read in the server.

There is no service above this one, and that is the same decision the
DBPoolHandler comment argues for: a service that forwarded three calls and
decided nothing would be a layer for the shape of it.

⚠ Every call here borrows a pool connection like any other request. That is the
reason docs/stats-dashboard.md sets a floor of 5 seconds on the refresh: a stats
page polling hard takes connections away from the traffic it is measuring, and
then reports the queueing it caused.
*/

// ErrPGStatStatementsUnavailable means the extension is not answering — either
// it was never created, or it was created without being loaded through
// shared_preload_libraries, which is a postmaster-level setting and needs a
// Postgres RESTART rather than a reload.
//
// A sentinel rather than a 500, because "not enabled" is a normal state with a
// documented fix (migration 000027 plus the command: on the database service in
// docker-compose.yml) and the rest of the page must still render. Losing the
// whole dashboard because one optional section is missing would be the worst
// possible failure for a page whose only job is to be readable during an
// incident.
var ErrPGStatStatementsUnavailable = errors.New("stats: pg_stat_statements is not available")

type StatsRepository struct {
	db *pgxpool.Pool
}

func NewStatsRepository(db *pgxpool.Pool) *StatsRepository {
	return &StatsRepository{db: db}
}

/*
TopStatements returns the heaviest statements by TOTAL execution time.

Total, not mean, and that ordering is the whole value of this call. The query
that made the server slow at the Club Fair — ClubFairFairRepository.rank
scanning the whole of clubfair_checkin on every GET /clubfair/progress — was not
slow on its own; it was ordinary, and it ran on every poll from every student in
the building. Ranked by mean it sits in the middle of the list. Ranked by total
it is the top row and nothing else is close.

The text is truncated in SQL rather than in Go so the bytes never leave
Postgres; the whole statement is available from psql when a row turns out to
matter. Restricted to this database so a shared server's other databases do not
crowd out the rows we can act on.
*/
func (r *StatsRepository) TopStatements(ctx context.Context, limit int) ([]model.PGStatement, error) {
	if limit <= 0 {
		limit = 10
	}

	const q = `
		SELECT left(query, 400) AS query,
		       queryid,
		       calls,
		       total_exec_time,
		       mean_exec_time,
		       max_exec_time,
		       rows,
		       shared_blks_hit,
		       shared_blks_read
		FROM pg_stat_statements
		WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
		ORDER BY total_exec_time DESC
		LIMIT $1`

	rows, err := r.db.Query(ctx, q, limit)
	if err != nil {
		// 42P01 undefined_table — the extension was never created.
		// 55000 object_not_in_prerequisite_state — created, but not loaded via
		//       shared_preload_libraries, which is the mistake that looks like
		//       success until the first query.
		// 42501 insufficient_privilege — the role cannot read the view.
		if IsPGCode(err, "42P01") || IsPGCode(err, "55000") || IsPGCode(err, "42501") {
			return nil, ErrPGStatStatementsUnavailable
		}
		return nil, err
	}
	defer rows.Close()

	out := []model.PGStatement{}
	for rows.Next() {
		var s model.PGStatement
		if err := rows.Scan(
			&s.Query, &s.QueryID, &s.Calls,
			&s.TotalExecMS, &s.MeanExecMS, &s.MaxExecMS,
			&s.Rows, &s.SharedBlksHit, &s.SharedBlksRead,
		); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

/*
Activity counts the backends by what they are doing, and the ceiling they share.

Counted with FILTER in one pass rather than as several scalar subqueries, so
every number describes the same instant. pg_stat_activity is a snapshot of live
state, and three separate reads of it would disagree with each other under load —
which is precisely when someone is reading this.

backend_type = 'client backend' excludes autovacuum workers, the checkpointer
and the walwriter: they appear in this view but do not compete for
max_connections, so counting them would overstate how close we are to the wall.
*/
func (r *StatsRepository) Activity(ctx context.Context) (model.PGActivity, error) {
	const q = `
		SELECT
		  count(*) FILTER (WHERE backend_type = 'client backend' AND state = 'active'),
		  count(*) FILTER (WHERE backend_type = 'client backend' AND state = 'idle'),
		  count(*) FILTER (WHERE backend_type = 'client backend' AND state = 'idle in transaction'),
		  count(*) FILTER (WHERE backend_type = 'client backend' AND wait_event_type = 'Lock'),
		  count(*) FILTER (WHERE backend_type = 'client backend'),
		  (SELECT setting::int FROM pg_settings WHERE name = 'max_connections'),
		  COALESCE(EXTRACT(EPOCH FROM max(now() - state_change)
		           FILTER (WHERE state = 'idle in transaction')), 0)
		FROM pg_stat_activity`

	var a model.PGActivity
	err := r.db.QueryRow(ctx, q).Scan(
		&a.Active, &a.Idle, &a.IdleInTransaction, &a.WaitingOnLock,
		&a.Total, &a.MaxConnections, &a.LongestIdleInTransactionSeconds,
	)
	return a, err
}

// Database reads pg_stat_database for the database this pool is connected to.
//
// The cache hit ratio is derived in Go rather than in SQL so the divide-by-zero
// is guarded somewhere it can be unit-tested — see derivePGCacheHitPct and the
// same argument on derivePoolRates in db_pool_handler.go. A fresh database has
// zero blocks read of either kind, and encoding/json refuses to encode the NaN
// that 0/0 produces.
func (r *StatsRepository) Database(ctx context.Context) (model.PGDatabase, error) {
	const q = `
		SELECT xact_commit, xact_rollback, deadlocks,
		       blks_hit, blks_read, temp_files, temp_bytes, stats_reset
		FROM pg_stat_database
		WHERE datname = current_database()`

	var d model.PGDatabase
	err := r.db.QueryRow(ctx, q).Scan(
		&d.XactCommit, &d.XactRollback, &d.Deadlocks,
		&d.BlksHit, &d.BlksRead, &d.TempFiles, &d.TempBytes, &d.StatsReset,
	)
	if err != nil {
		return d, err
	}

	d.CacheHitPct = DerivePGCacheHitPct(d.BlksHit, d.BlksRead)
	return d, nil
}

// DerivePGCacheHitPct is blocks served from Postgres' shared buffers as a share
// of all block reads.
//
// Exported and separate for the same reason derivePoolRates is: a database that
// has served nothing gives 0/0, which is NaN, and encoding/json returns an
// UnsupportedValueError for NaN after WriteJSON has already sent the 200 and the
// headers — so the client gets a truncated body with no error it can read. The
// stats page would break exactly on a freshly restarted server, which is the
// moment somebody is most likely to open it.
func DerivePGCacheHitPct(blksHit, blksRead int64) float64 {
	total := blksHit + blksRead
	if total <= 0 {
		return 0
	}
	return float64(blksHit) / float64(total) * 100
}
