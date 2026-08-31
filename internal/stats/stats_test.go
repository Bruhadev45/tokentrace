package stats

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Bruhadev45/tokentrace/internal/record"
)

var (
	testNow       = time.Date(2026, 8, 31, 10, 0, 0, 0, time.Local)
	testYesterday = testNow.Add(-24 * time.Hour)
)

func writeRecords(t *testing.T, recs []record.Record) string {
	t.Helper()
	dir := t.TempDir()
	store, err := record.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, r := range recs {
		if err := store.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, record.FileName)
}

func testRecords() []record.Record {
	return []record.Record{
		{
			Time: testNow, Project: "p1", Provider: "openai", Model: "m1", Path: "/x",
			Status: 200, LatencyMS: 100, InputTokens: 100, OutputTokens: 10,
			CostUSD: 0.01, Priced: true,
		},
		{
			Time: testNow, Project: "p1", Provider: "openai", Model: "m1", Path: "/x",
			Status: 200, LatencyMS: 200, InputTokens: 300, OutputTokens: 30, CachedTokens: 100,
			CostUSD: 0.03, Priced: true,
		},
		{
			Time: testYesterday, Project: "p2", Provider: "anthropic", Model: "m2", Path: "/y",
			Status: 200, LatencyMS: 300, InputTokens: 50, OutputTokens: 5,
			CostUSD: 0, Priced: false, // usage known, model unpriced
		},
		{
			Time: testNow, Project: "p2", Provider: "anthropic", Model: "m2", Path: "/y",
			Status: 200, LatencyMS: 400, Unknown: true, // no usage at all
		},
	}
}

func findRow(t *testing.T, rows []Row, key string) Row {
	t.Helper()
	for _, r := range rows {
		if r.Key == key {
			return r
		}
	}
	t.Fatalf("no row with key %q in %+v", key, rows)
	return Row{}
}

func TestAggregate(t *testing.T) {
	path := writeRecords(t, testRecords())

	tests := []struct {
		name     string
		by       By
		filter   Filter
		wantKeys []string // in sorted output order
		check    func(t *testing.T, rows []Row, total Row)
	}{
		{
			name:     "by model sorts by cost desc",
			by:       ByModel,
			wantKeys: []string{"m1", "m2"},
			check: func(t *testing.T, rows []Row, total Row) {
				m1 := findRow(t, rows, "m1")
				if m1.Requests != 2 || m1.Input != 400 || m1.Output != 40 || m1.Cached != 100 {
					t.Errorf("m1 row = %+v", m1)
				}
				if m1.AvgLatencyMS() != 150 || m1.P95LatencyMS() != 200 {
					t.Errorf("m1 latency avg=%d p95=%d, want 150/200", m1.AvgLatencyMS(), m1.P95LatencyMS())
				}
				if rate, ok := m1.HitRate(); !ok || rate != 0.2 {
					t.Errorf("m1 hit rate = %v/%v, want 0.2/true", rate, ok)
				}
				m2 := findRow(t, rows, "m2")
				if m2.Unpriced != 1 || m2.Unknown != 1 {
					t.Errorf("m2 unpriced=%d unknown=%d, want 1/1", m2.Unpriced, m2.Unknown)
				}
				if total.Requests != 4 || total.Input != 450 {
					t.Errorf("total = %+v", total)
				}
			},
		},
		{
			name:     "by project",
			by:       ByProject,
			wantKeys: []string{"p1", "p2"},
		},
		{
			name:     "by day sorts chronologically",
			by:       ByDay,
			wantKeys: []string{testYesterday.Format("2006-01-02"), testNow.Format("2006-01-02")},
		},
		{
			name:     "project filter",
			by:       ByModel,
			filter:   Filter{Project: "p1"},
			wantKeys: []string{"m1"},
			check: func(t *testing.T, rows []Row, total Row) {
				if total.Requests != 2 {
					t.Errorf("filtered total requests = %d, want 2", total.Requests)
				}
			},
		},
		{
			name:     "since filter drops older records",
			by:       ByModel,
			filter:   Filter{Since: testNow.Add(-12 * time.Hour)},
			wantKeys: []string{"m1", "m2"},
			check: func(t *testing.T, rows []Row, total Row) {
				if total.Requests != 3 {
					t.Errorf("since-filtered total requests = %d, want 3", total.Requests)
				}
			},
		},
		{
			name:     "filters can match nothing",
			by:       ByModel,
			filter:   Filter{Project: "no-such-project"},
			wantKeys: []string{},
			check: func(t *testing.T, rows []Row, total Row) {
				if total.Requests != 0 {
					t.Errorf("total requests = %d, want 0", total.Requests)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, total, skipped, err := Aggregate(path, tt.by, tt.filter)
			if err != nil {
				t.Fatalf("Aggregate() error = %v", err)
			}
			if skipped != 0 {
				t.Errorf("skipped = %d, want 0", skipped)
			}
			if len(rows) != len(tt.wantKeys) {
				t.Fatalf("got %d rows (%+v), want keys %v", len(rows), rows, tt.wantKeys)
			}
			for i, k := range tt.wantKeys {
				if rows[i].Key != k {
					t.Errorf("rows[%d].Key = %q, want %q", i, rows[i].Key, k)
				}
			}
			if tt.check != nil {
				tt.check(t, rows, total)
			}
		})
	}
}

func TestParseBy(t *testing.T) {
	tests := []struct {
		in      string
		want    By
		wantErr bool
	}{
		{"model", ByModel, false},
		{"project", ByProject, false},
		{"day", ByDay, false},
		{"week", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		t.Run("in="+tt.in, func(t *testing.T) {
			got, err := ParseBy(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseBy(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ParseBy(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestRowEdgeCases(t *testing.T) {
	empty := Row{}
	if empty.AvgLatencyMS() != 0 || empty.P95LatencyMS() != 0 {
		t.Error("empty row latency should be 0")
	}
	if _, ok := empty.HitRate(); ok {
		t.Error("empty row hit rate should be undefined")
	}

	one := Row{latencies: []int64{42}}
	if one.P95LatencyMS() != 42 {
		t.Errorf("single-sample p95 = %d, want 42", one.P95LatencyMS())
	}
}
