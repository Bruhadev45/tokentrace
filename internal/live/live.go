// Package live keeps the rolling numbers behind the `tokentrace tail
// --stats` footer: request rate and median latency over the last minute,
// plus running totals since tracking started. It is pure — the caller
// passes the clock in — so the window math is testable without sleeping.
package live

import (
	"sort"
	"time"

	"github.com/Bruhadev45/tokentrace/internal/record"
)

// Window is how far back the rate and the median latency look.
const Window = 60 * time.Second

// Snapshot is the footer's view of a Tracker at one instant.
type Snapshot struct {
	// PerMinute counts requests that arrived within the last Window. The
	// window is exactly one minute, so the count is the per-minute rate.
	PerMinute int
	// P50LatencyMS is the nearest-rank median latency of those requests,
	// or 0 when the window is empty.
	P50LatencyMS int64

	// Requests and CostUSD are running totals since tracking started.
	Requests int
	CostUSD  float64
	// Unpriced counts requests with usage but no price-table entry, whose
	// cost was counted as zero.
	Unpriced int
}

// Tracker accumulates records as they arrive. The zero value is ready to
// use. It is not safe for concurrent use.
type Tracker struct {
	recent   []sample // arrivals still inside the window
	requests int
	cost     float64
	unpriced int
}

type sample struct {
	at        time.Time
	latencyMS int64
}

// Add counts r as arriving at now and forgets arrivals that have left the
// window, so memory stays bounded by the request rate, not the session.
// Arrival time is used rather than the record's own timestamp: a long
// stream is stamped when it started but completes much later.
func (t *Tracker) Add(r record.Record, now time.Time) {
	t.requests++
	t.cost += r.CostUSD
	if !r.Unknown && !r.Priced {
		t.unpriced++
	}
	t.recent = append(inWindow(t.recent, now), sample{at: now, latencyMS: r.LatencyMS})
}

// Snapshot summarises the tracker as of now without modifying it.
func (t *Tracker) Snapshot(now time.Time) Snapshot {
	recent := inWindow(t.recent, now)
	return Snapshot{
		PerMinute:    len(recent),
		P50LatencyMS: median(recent),
		Requests:     t.requests,
		CostUSD:      t.cost,
		Unpriced:     t.unpriced,
	}
}

// inWindow returns a new slice holding the samples that arrived after
// now-Window. A sample exactly Window old has left the window.
func inWindow(samples []sample, now time.Time) []sample {
	cutoff := now.Add(-Window)
	kept := make([]sample, 0, len(samples))
	for _, s := range samples {
		if s.at.After(cutoff) {
			kept = append(kept, s)
		}
	}
	return kept
}

// median is the nearest-rank 50th percentile (the lower middle value for
// an even count), matching how stats computes its p95.
func median(samples []sample) int64 {
	if len(samples) == 0 {
		return 0
	}
	lat := make([]int64, len(samples))
	for i, s := range samples {
		lat[i] = s.latencyMS
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	return lat[(len(lat)+1)/2-1]
}
