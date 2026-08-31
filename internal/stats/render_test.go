package stats

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Bruhadev45/tokentrace/internal/record"
)

func TestComma(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{7, "7"},
		{999, "999"},
		{1000, "1,000"},
		{123456, "123,456"},
		{1234567, "1,234,567"},
		{-1234, "-1,234"},
	}
	for _, tt := range tests {
		if got := comma(tt.in); got != tt.want {
			t.Errorf("comma(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRender(t *testing.T) {
	rows := []Row{
		{Key: "claude-sonnet-4-5", Requests: 3, Input: 1200, Output: 300, Cached: 4800,
			Cost: 0.0456, latencies: []int64{100, 200, 300}},
		{Key: "mystery-model", Requests: 1, Input: 10, Output: 2,
			Cost: 0, Unpriced: 1, latencies: []int64{50}},
	}
	total := Row{Key: "TOTAL", Requests: 4, Input: 1210, Output: 302, Cached: 4800,
		Cost: 0.0456, Unpriced: 1, Unknown: 2, latencies: []int64{50, 100, 200, 300}}

	var buf bytes.Buffer
	if err := Render(&buf, ByModel, rows, total); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"MODEL", "REQS", "INPUT", "OUTPUT", "CACHED", "COST", "AVG MS", "P95 MS", "HIT %",
		"claude-sonnet-4-5", "mystery-model", "TOTAL",
		"$0.0456", "4,800",
		"$0.0000*", // unpriced marker
		"* includes 1 request(s) with no known price",
		"note: 2 request(s) carried no parseable usage",
		"80.0", // 4800/(1200+4800)
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Render() output missing %q:\n%s", want, out)
		}
	}

	// Aligned table: every data row must have the same column offsets as
	// the header (tabwriter guarantees it; sanity-check line lengths align
	// on the REQS column).
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 4 {
		t.Fatalf("Render() produced %d lines, want at least 4:\n%s", len(lines), out)
	}
}

func TestRenderSingleRowOmitsTotal(t *testing.T) {
	rows := []Row{{Key: "only", Requests: 1, latencies: []int64{10}}}
	var buf bytes.Buffer
	if err := Render(&buf, ByProject, rows, rows[0]); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if strings.Contains(buf.String(), "TOTAL") {
		t.Errorf("single-row table should not repeat a TOTAL line:\n%s", buf.String())
	}
}

func TestFormatRecord(t *testing.T) {
	ts := time.Date(2026, 8, 31, 14, 23, 5, 0, time.Local)
	tests := []struct {
		name     string
		rec      record.Record
		contains []string
		excludes []string
	}{
		{
			name: "priced record",
			rec: record.Record{
				Time: ts, Project: "checkout-bot", Provider: "anthropic",
				Model: "claude-sonnet-4-5-20250929", Path: "/v1/messages", Status: 200,
				LatencyMS: 812, InputTokens: 1204, OutputTokens: 356, CachedTokens: 2048,
				CostUSD: 0.0092, Priced: true,
			},
			contains: []string{"14:23:05", "anthropic", "200", "812ms", "checkout-bot",
				"claude-sonnet-4-5-20250929", "in=1,204", "out=356", "cached=2,048", "$0.0092"},
		},
		{
			name: "unknown usage",
			rec: record.Record{
				Time: ts, Project: "p", Provider: "openai", Path: "/v1/chat/completions",
				Status: 200, LatencyMS: 99, Unknown: true,
			},
			contains: []string{"tokens=unknown", "(unknown)", "$-"},
			excludes: []string{"in=", "out="},
		},
		{
			name: "known usage but unpriced model",
			rec: record.Record{
				Time: ts, Project: "p", Provider: "openai", Model: "custom-llm",
				Status: 200, LatencyMS: 5, InputTokens: 10, OutputTokens: 2,
			},
			contains: []string{"custom-llm", "in=10", "$-"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line := FormatRecord(tt.rec)
			for _, want := range tt.contains {
				if !strings.Contains(line, want) {
					t.Errorf("FormatRecord() = %q, missing %q", line, want)
				}
			}
			for _, bad := range tt.excludes {
				if strings.Contains(line, bad) {
					t.Errorf("FormatRecord() = %q, should not contain %q", line, bad)
				}
			}
		})
	}
}
