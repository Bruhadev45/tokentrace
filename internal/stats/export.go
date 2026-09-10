package stats

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
)

// Format selects how a stats report is written.
type Format string

// Output formats.
const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	FormatCSV   Format = "csv"
)

// Fields are the machine-readable column/field names, in output order.
// They are part of tokentrace's documented interface (README) — treat any
// rename or reordering as a breaking change.
var Fields = []string{
	"key",
	"requests",
	"input_tokens",
	"output_tokens",
	"cached_tokens",
	"cache_creation_tokens",
	"cost_usd",
	"unpriced_requests",
	"unknown_requests",
	"avg_latency_ms",
	"p95_latency_ms",
	"cache_hit_rate",
}

// exportRow is the stable serialization shape of a Row. Field order here
// defines both the JSON key order and the CSV column order (see Fields).
type exportRow struct {
	Key                 string   `json:"key"`
	Requests            int      `json:"requests"`
	InputTokens         int64    `json:"input_tokens"`
	OutputTokens        int64    `json:"output_tokens"`
	CachedTokens        int64    `json:"cached_tokens"`
	CacheCreationTokens int64    `json:"cache_creation_tokens"`
	CostUSD             float64  `json:"cost_usd"`
	UnpricedRequests    int      `json:"unpriced_requests"`
	UnknownRequests     int      `json:"unknown_requests"`
	AvgLatencyMS        int64    `json:"avg_latency_ms"`
	P95LatencyMS        int64    `json:"p95_latency_ms"`
	CacheHitRate        *float64 `json:"cache_hit_rate"`
}

// report is the top-level JSON document written by RenderJSON.
type report struct {
	By    string      `json:"by"`
	Rows  []exportRow `json:"rows"`
	Total exportRow   `json:"total"`
}

// costPrecision and ratePrecision keep serialized floats free of binary
// rounding noise so output is byte-stable across runs and platforms.
const (
	costPrecision = 6
	ratePrecision = 4
)

func newExportRow(r Row) exportRow {
	out := exportRow{
		Key:                 r.Key,
		Requests:            r.Requests,
		InputTokens:         r.Input,
		OutputTokens:        r.Output,
		CachedTokens:        r.Cached,
		CacheCreationTokens: r.CacheWrite,
		CostUSD:             round(r.Cost, costPrecision),
		UnpricedRequests:    r.Unpriced,
		UnknownRequests:     r.Unknown,
		AvgLatencyMS:        r.AvgLatencyMS(),
		P95LatencyMS:        r.P95LatencyMS(),
	}
	if rate, ok := r.HitRate(); ok {
		rounded := round(rate, ratePrecision)
		out.CacheHitRate = &rounded
	}
	return out
}

// round returns v rounded to n decimal places.
func round(v float64, n int) float64 {
	pow := math.Pow(10, float64(n))
	return math.Round(v*pow) / pow
}

// RenderJSON writes the report as a single JSON object: the grouping
// dimension, one object per group, and a grand total.
func RenderJSON(w io.Writer, by By, rows []Row, total Row) error {
	doc := report{By: string(by), Rows: make([]exportRow, 0, len(rows)), Total: newExportRow(total)}
	for _, r := range rows {
		doc.Rows = append(doc.Rows, newExportRow(r))
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("render json: %w", err)
	}
	return nil
}

// RenderCSV writes one header row plus one row per group. The grand total
// is deliberately omitted so the output sums correctly when loaded as a
// dataframe; use the JSON format when you want the total.
func RenderCSV(w io.Writer, rows []Row) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(Fields); err != nil {
		return fmt.Errorf("render csv: %w", err)
	}
	for _, r := range rows {
		if err := cw.Write(csvFields(newExportRow(r))); err != nil {
			return fmt.Errorf("render csv: %w", err)
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return fmt.Errorf("render csv: %w", err)
	}
	return nil
}

// csvFields flattens a row in Fields order. An undefined cache-hit rate is
// written as an empty field rather than a misleading 0.
func csvFields(r exportRow) []string {
	rate := ""
	if r.CacheHitRate != nil {
		rate = strconv.FormatFloat(*r.CacheHitRate, 'f', ratePrecision, 64)
	}
	return []string{
		r.Key,
		strconv.Itoa(r.Requests),
		strconv.FormatInt(r.InputTokens, 10),
		strconv.FormatInt(r.OutputTokens, 10),
		strconv.FormatInt(r.CachedTokens, 10),
		strconv.FormatInt(r.CacheCreationTokens, 10),
		strconv.FormatFloat(r.CostUSD, 'f', costPrecision, 64),
		strconv.Itoa(r.UnpricedRequests),
		strconv.Itoa(r.UnknownRequests),
		strconv.FormatInt(r.AvgLatencyMS, 10),
		strconv.FormatInt(r.P95LatencyMS, 10),
		rate,
	}
}
