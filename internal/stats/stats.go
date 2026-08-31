// Package stats aggregates usage records and renders terminal tables.
package stats

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Bruhadev45/tokentrace/internal/record"
)

// By selects the grouping dimension for aggregation.
type By string

// Grouping dimensions.
const (
	ByProject By = "project"
	ByModel   By = "model"
	ByDay     By = "day"
)

// ParseBy validates a --by flag value.
func ParseBy(s string) (By, error) {
	switch By(s) {
	case ByProject, ByModel, ByDay:
		return By(s), nil
	default:
		return "", fmt.Errorf("invalid --by %q (want model, project or day)", s)
	}
}

// Filter restricts which records are aggregated. Zero values mean "all".
type Filter struct {
	Project string
	Since   time.Time
}

// Row is one aggregated group of records.
type Row struct {
	Key        string
	Requests   int
	Input      int64 // non-cached input tokens
	Output     int64
	Cached     int64 // cache-read tokens
	CacheWrite int64 // cache-creation tokens
	Cost       float64
	Unpriced   int // requests with usage but no price for the model
	Unknown    int // requests with no parseable usage at all

	latencies []int64
}

func (r *Row) add(rec record.Record) {
	r.Requests++
	r.Input += rec.InputTokens
	r.Output += rec.OutputTokens
	r.Cached += rec.CachedTokens
	r.CacheWrite += rec.CacheCreationTokens
	r.Cost += rec.CostUSD
	switch {
	case rec.Unknown:
		r.Unknown++
	case !rec.Priced:
		r.Unpriced++
	}
	r.latencies = append(r.latencies, rec.LatencyMS)
}

// AvgLatencyMS is the mean latency of the group's requests.
func (r Row) AvgLatencyMS() int64 {
	if len(r.latencies) == 0 {
		return 0
	}
	var sum int64
	for _, l := range r.latencies {
		sum += l
	}
	return sum / int64(len(r.latencies))
}

// P95LatencyMS is the 95th-percentile latency of the group's requests.
func (r Row) P95LatencyMS() int64 {
	n := len(r.latencies)
	if n == 0 {
		return 0
	}
	sorted := append([]int64(nil), r.latencies...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(math.Ceil(0.95*float64(n))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	return sorted[idx]
}

// HitRate is the cache-hit rate cached/(input+cached). ok is false when
// the group has no input tokens at all.
func (r Row) HitRate() (rate float64, ok bool) {
	den := r.Input + r.Cached
	if den == 0 {
		return 0, false
	}
	return float64(r.Cached) / float64(den), true
}

// Aggregate scans the records file and groups matching records by the
// given dimension. It returns the sorted rows, a grand-total row, and the
// number of unparseable lines skipped while reading the file.
func Aggregate(path string, by By, f Filter) (rows []Row, total Row, skipped int, err error) {
	groups := map[string]*Row{}
	tot := &Row{Key: "TOTAL"}
	skipped, err = record.Scan(path, func(rec record.Record) error {
		if f.Project != "" && rec.Project != f.Project {
			return nil
		}
		if !f.Since.IsZero() && rec.Time.Before(f.Since) {
			return nil
		}
		key := keyFor(by, rec)
		g := groups[key]
		if g == nil {
			g = &Row{Key: key}
			groups[key] = g
		}
		g.add(rec)
		tot.add(rec)
		return nil
	})
	if err != nil {
		return nil, Row{}, skipped, fmt.Errorf("aggregate: %w", err)
	}
	rows = make([]Row, 0, len(groups))
	for _, g := range groups {
		rows = append(rows, *g)
	}
	sortRows(by, rows)
	return rows, *tot, skipped, nil
}

func keyFor(by By, rec record.Record) string {
	switch by {
	case ByModel:
		if rec.Model == "" {
			return "(unknown)"
		}
		return rec.Model
	case ByDay:
		return rec.Time.Local().Format("2006-01-02")
	default:
		if rec.Project == "" {
			return "(none)"
		}
		return rec.Project
	}
}

// sortRows orders days chronologically and everything else by cost
// (descending), tie-broken by key for stable output.
func sortRows(by By, rows []Row) {
	sort.Slice(rows, func(i, j int) bool {
		if by == ByDay {
			return rows[i].Key < rows[j].Key
		}
		if rows[i].Cost != rows[j].Cost {
			return rows[i].Cost > rows[j].Cost
		}
		return rows[i].Key < rows[j].Key
	})
}
