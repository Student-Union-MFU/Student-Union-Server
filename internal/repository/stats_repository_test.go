package repository

import (
	"encoding/json"
	"math"
	"testing"

	"su-server/internal/model"
)

// A database that has served no blocks yet gives 0/0, which is NaN, and
// encoding/json returns an UnsupportedValueError for NaN — after WriteJSON has
// already sent the 200 and the headers. The client gets a truncated body it
// cannot read an error out of, and it happens on a freshly restarted server,
// which is exactly when somebody opens the stats page to check the deploy.
func TestCacheHitPctOnAFreshDatabaseIsZeroRatherThanNaN(t *testing.T) {
	got := DerivePGCacheHitPct(0, 0)

	if math.IsNaN(got) {
		t.Fatalf("NaN leaked out of the cache hit ratio")
	}
	if got != 0 {
		t.Errorf("expected 0 for a database that has read nothing, got %v", got)
	}
}

// The guard is only worth anything if the value it protects can reach the
// client, so encode it the way WriteJSON would.
func TestFreshDatabaseStatsStillEncodeAsJSON(t *testing.T) {
	d := model.PGDatabase{CacheHitPct: DerivePGCacheHitPct(0, 0)}

	if _, err := json.Marshal(d); err != nil {
		t.Fatalf("stats for a fresh database must encode, got: %v", err)
	}
}

func TestCacheHitPctIsHitsOverAllBlockReads(t *testing.T) {
	// 900 served from shared buffers, 100 that went to disk.
	if got := DerivePGCacheHitPct(900, 100); got != 90 {
		t.Errorf("expected 90%%, got %v", got)
	}
}

// The denominator is hits PLUS reads, not reads alone. Dividing by reads only
// would put this above 100% on any healthy server, where hits vastly outnumber
// disk reads — plausible enough on a dashboard to go unquestioned.
func TestCacheHitPctCannotExceedOneHundred(t *testing.T) {
	if got := DerivePGCacheHitPct(1_000_000, 1); got > 100 {
		t.Errorf("cache hit ratio went above 100%%: %v", got)
	}
}

// Everything from disk, nothing from cache. The number that should have people
// looking at shared_buffers.
func TestCacheHitPctOfAnAllDiskWorkloadIsZero(t *testing.T) {
	if got := DerivePGCacheHitPct(0, 500); got != 0 {
		t.Errorf("expected 0%%, got %v", got)
	}
}

// pg_stat_database counters are bigint and cannot go negative, but the guard is
// a <= rather than an == and that should stay true if anyone rewrites it.
func TestCacheHitPctRefusesToDivideByANonPositiveTotal(t *testing.T) {
	if got := DerivePGCacheHitPct(5, -5); got != 0 {
		t.Errorf("expected 0 for a non-positive total, got %v", got)
	}
}
