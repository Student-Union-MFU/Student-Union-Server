package handler

import (
	"encoding/json"
	"math"
	"testing"
)

// A pool that has served nothing is what every deploy looks like for its first
// seconds, and it is the moment somebody is most likely to open the stats page
// to check the deploy worked. 0/0 is NaN in Go, and encoding/json refuses NaN.
func TestPoolRatesOnAnUnusedPoolAreZeroRatherThanNaN(t *testing.T) {
	mean, pct := derivePoolRates(0, 0, 0)

	if math.IsNaN(mean) || math.IsNaN(pct) {
		t.Fatalf("NaN leaked out: mean=%v pct=%v", mean, pct)
	}
	if mean != 0 || pct != 0 {
		t.Errorf("expected 0 and 0 for an unused pool, got mean=%v pct=%v", mean, pct)
	}
}

// The guard is only worth anything if the value it protects can actually reach
// the client, so encode it the way WriteJSON would.
func TestUnusedPoolStatsStillEncodeAsJSON(t *testing.T) {
	var out dbPoolStats
	out.MeanAcquireMS, out.EmptyAcquirePct = derivePoolRates(0, 0, 0)

	if _, err := json.Marshal(out); err != nil {
		t.Fatalf("stats for a fresh pool must encode, got: %v", err)
	}
}

func TestPoolRatesReportMeanWaitAndEmptyShare(t *testing.T) {
	// 200 acquires, 5 of which had to wait, 100ms of waiting in total.
	mean, pct := derivePoolRates(200, 5, 100)

	if mean != 0.5 {
		t.Errorf("mean acquire: expected 0.5ms, got %v", mean)
	}
	if pct != 2.5 {
		t.Errorf("empty acquire share: expected 2.5%%, got %v", pct)
	}
}

// A negative count cannot come from pgx, but the guard is a <= rather than a ==
// and that should stay true if anyone rewrites it.
func TestPoolRatesRefuseToDivideByANonPositiveCount(t *testing.T) {
	mean, pct := derivePoolRates(-1, 3, 50)

	if mean != 0 || pct != 0 {
		t.Errorf("expected 0 and 0 for a non-positive count, got mean=%v pct=%v", mean, pct)
	}
}
