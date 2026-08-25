package handler

import (
	"math"
	"runtime"
	"testing"
)

/*
The GC pause ring is the one piece of arithmetic here that can be wrong forever
without anyone noticing: a bad index reads a real pause from a real collection,
just the wrong one, so the number stays plausible in every case.
*/

// The most recent completed pause sits one slot BEFORE where the next one will
// be written, so with NumGC=1 the pause lives at index 0.
func TestLastGCPauseReadsTheSlotBeforeTheWritePoint(t *testing.T) {
	var ring [256]uint64
	ring[0] = 3_000_000 // 3ms

	if got := lastGCPauseMS(1, ring); got != 3 {
		t.Errorf("expected the pause at index 0 for NumGC=1, got %vms", got)
	}
}

// The wrap is the whole reason for the +255. When NumGC%256 is 0 the next write
// goes to index 0, so the last completed pause is at 255 — the naive NumGC-1
// would index -1 here, and a %256 of the raw NumGC would read index 0, which is
// the pause that is about to be OVERWRITTEN, not the newest one.
func TestLastGCPauseWrapsToTheEndOfTheRing(t *testing.T) {
	var ring [256]uint64
	ring[255] = 7_000_000 // 7ms — the newest
	ring[0] = 99_000_000  // the oldest, next to be overwritten

	if got := lastGCPauseMS(256, ring); got != 7 {
		t.Errorf("expected the pause at index 255 after a full wrap, got %vms", got)
	}
}

// The same wrap, a lap later, to catch an off-by-one that only shows up once
// NumGC exceeds the ring twice.
func TestLastGCPauseWrapsOnEveryLap(t *testing.T) {
	var ring [256]uint64
	ring[100] = 12_000_000

	if got := lastGCPauseMS(256+101, ring); got != 12 {
		t.Errorf("expected the pause at index 100, got %vms", got)
	}
}

// No collection has finished yet. Real state for the first seconds of a
// process, not a missing value.
func TestLastGCPauseIsZeroBeforeTheFirstCollection(t *testing.T) {
	var ring [256]uint64
	ring[255] = 5_000_000 // must not be read

	if got := lastGCPauseMS(0, ring); got != 0 {
		t.Errorf("expected 0 before the first GC, got %vms", got)
	}
}

// Nanoseconds in, milliseconds out. A factor of 1e6 wrong reads as a healthy
// server during a stall, or a stalling one when it is fine.
func TestLastGCPauseConvertsNanosecondsToMilliseconds(t *testing.T) {
	var ring [256]uint64
	ring[0] = 1_500_000 // 1.5ms

	if got := lastGCPauseMS(1, ring); got != 1.5 {
		t.Errorf("expected 1.5ms, got %vms", got)
	}
}

// MB on the page means 1<<20, not 1e6. The two differ by ~5%, which is small
// enough to look like drift and large enough to matter when reading against a
// container memory limit.
func TestBytesToMBUsesBinaryMegabytes(t *testing.T) {
	if got := bytesToMB(1 << 20); got != 1 {
		t.Errorf("one binary megabyte should be 1.0, got %v", got)
	}
	if got := bytesToMB(3 << 20); got != 3 {
		t.Errorf("three binary megabytes should be 3.0, got %v", got)
	}
}

func TestBytesToMBOfNothingIsZero(t *testing.T) {
	if got := bytesToMB(0); got != 0 {
		t.Errorf("expected 0, got %v", got)
	}
}

// Sub-megabyte values are the normal case for stack in use, and truncating them
// to an integer would report a busy server as using none.
func TestBytesToMBKeepsTheFraction(t *testing.T) {
	if got := bytesToMB(1 << 19); got != 0.5 {
		t.Errorf("half a megabyte should be 0.5, got %v", got)
	}
}

/*
The guard that matters at the top of this file's package: nothing derived from a
live runtime may be NaN or Inf, because encoding/json refuses both and WriteJSON
has already sent the 200 by the time it finds out — the client gets a truncated
body with no error in it. Same failure derivePoolRates is guarded against.
*/
func TestRuntimeStatsFromARealProcessAreAllFinite(t *testing.T) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	for name, v := range map[string]float64{
		"heap in use":  bytesToMB(m.HeapInuse),
		"stack in use": bytesToMB(m.StackInuse),
		"next GC":      bytesToMB(m.NextGC),
		"last pause":   lastGCPauseMS(m.NumGC, m.PauseNs),
	} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Errorf("%s is not a finite number (%v) and will not encode as JSON", name, v)
		}
	}
}
