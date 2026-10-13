package live

import (
	"testing"
	"time"

	"github.com/Bruhadev45/tokentrace/internal/record"
)

var t0 = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

// priced is a normally-priced record. Costs in these tests are exact
// binary fractions so sums compare with ==.
func priced(latencyMS int64, cost float64) record.Record {
	return record.Record{Model: "gpt-4o-mini", LatencyMS: latencyMS, CostUSD: cost, Priced: true}
}

type arrival struct {
	at  time.Duration // offset from t0
	rec record.Record
}

func TestTrackerSnapshot(t *testing.T) {
	tests := []struct {
		name     string
		arrivals []arrival
		at       time.Duration // snapshot time, offset from t0
		want     Snapshot
	}{
		{
			name: "empty tracker",
			want: Snapshot{},
		},
		{
			name:     "single request",
			arrivals: []arrival{{0, priced(800, 0.25)}},
			want:     Snapshot{PerMinute: 1, P50LatencyMS: 800, Requests: 1, CostUSD: 0.25},
		},
		{
			name: "odd count takes the middle latency",
			arrivals: []arrival{
				{0, priced(300, 0.125)}, {time.Second, priced(100, 0.125)}, {2 * time.Second, priced(200, 0.25)},
			},
			at:   2 * time.Second,
			want: Snapshot{PerMinute: 3, P50LatencyMS: 200, Requests: 3, CostUSD: 0.5},
		},
		{
			name: "even count takes the lower middle latency",
			arrivals: []arrival{
				{0, priced(100, 0)}, {0, priced(400, 0)}, {0, priced(200, 0)}, {0, priced(300, 0)},
			},
			want: Snapshot{PerMinute: 4, P50LatencyMS: 200, Requests: 4},
		},
		{
			name: "old arrivals leave the window but stay in the running totals",
			arrivals: []arrival{
				{0, priced(5000, 0.5)}, {10 * time.Second, priced(5000, 0.25)}, {30 * time.Second, priced(90, 0.125)},
			},
			at:   75 * time.Second,
			want: Snapshot{PerMinute: 1, P50LatencyMS: 90, Requests: 3, CostUSD: 0.875},
		},
		{
			name:     "an arrival exactly one window old is out",
			arrivals: []arrival{{0, priced(800, 0.25)}},
			at:       Window,
			want:     Snapshot{PerMinute: 0, P50LatencyMS: 0, Requests: 1, CostUSD: 0.25},
		},
		{
			name:     "an arrival just inside the window is in",
			arrivals: []arrival{{0, priced(800, 0.25)}},
			at:       Window - time.Millisecond,
			want:     Snapshot{PerMinute: 1, P50LatencyMS: 800, Requests: 1, CostUSD: 0.25},
		},
		{
			name:     "idle for minutes: rate and median drop to zero, cost does not",
			arrivals: []arrival{{0, priced(800, 0.25)}, {time.Second, priced(900, 0.25)}},
			at:       5 * time.Minute,
			want:     Snapshot{Requests: 2, CostUSD: 0.5},
		},
		{
			name: "unpriced is counted, unknown usage is not",
			arrivals: []arrival{
				{0, record.Record{Model: "mystery", LatencyMS: 100}},
				{0, record.Record{Unknown: true, LatencyMS: 300}},
				{0, priced(200, 0.25)},
			},
			want: Snapshot{PerMinute: 3, P50LatencyMS: 200, Requests: 3, CostUSD: 0.25, Unpriced: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tr Tracker
			for _, a := range tt.arrivals {
				tr.Add(a.rec, t0.Add(a.at))
			}
			if got := tr.Snapshot(t0.Add(tt.at)); got != tt.want {
				t.Errorf("Snapshot() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSnapshotDoesNotModifyTracker(t *testing.T) {
	var tr Tracker
	tr.Add(priced(800, 0.25), t0)
	tr.Snapshot(t0.Add(10 * time.Minute)) // far past the window
	if got := tr.Snapshot(t0); got.PerMinute != 1 {
		t.Errorf("after a later Snapshot, Snapshot(t0).PerMinute = %d, want 1", got.PerMinute)
	}
}

func TestAddForgetsArrivalsOutsideTheWindow(t *testing.T) {
	var tr Tracker
	for i := 0; i < 1000; i++ {
		tr.Add(priced(100, 0), t0.Add(time.Duration(i)*time.Second))
	}
	// One arrival per second: only the last 60 are still in the window.
	if got := len(tr.recent); got != 60 {
		t.Errorf("retained %d samples after 1000s at 1 req/s, want 60", got)
	}
}
