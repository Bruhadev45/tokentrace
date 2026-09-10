package stats

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the testdata golden files")

// fixtureRows are the rows every golden case renders from, chosen to cover
// the interesting shapes: a fully priced group, an unpriced one, a group
// with no input tokens at all (undefined hit rate), and unknown usage.
func fixtureRows() (rows []Row, total Row) {
	rows = []Row{
		{Key: "claude-sonnet-4-5-20250929", Requests: 3, Input: 1200, Output: 300,
			Cached: 4800, CacheWrite: 640, Cost: 0.045612345, latencies: []int64{100, 200, 300}},
		{Key: "mystery-model", Requests: 2, Input: 10, Output: 2,
			Unpriced: 2, latencies: []int64{50, 70}},
		{Key: "(unknown)", Requests: 1, Unknown: 1, latencies: []int64{40}},
	}
	total = Row{Key: "TOTAL", Requests: 6, Input: 1210, Output: 302, Cached: 4800,
		CacheWrite: 640, Cost: 0.045612345, Unpriced: 2, Unknown: 1,
		latencies: []int64{40, 50, 70, 100, 200, 300}}
	return rows, total
}

func TestRenderGolden(t *testing.T) {
	rows, total := fixtureRows()
	tests := []struct {
		name   string
		golden string
		render func(w *bytes.Buffer) error
	}{
		{
			name:   "json by model",
			golden: "model.json",
			render: func(w *bytes.Buffer) error { return RenderJSON(w, ByModel, rows, total) },
		},
		{
			name:   "csv by model",
			golden: "model.csv",
			render: func(w *bytes.Buffer) error { return RenderCSV(w, rows) },
		},
		{
			name:   "json with no rows",
			golden: "empty.json",
			render: func(w *bytes.Buffer) error { return RenderJSON(w, ByDay, nil, Row{Key: "TOTAL"}) },
		},
		{
			name:   "csv with no rows",
			golden: "empty.csv",
			render: func(w *bytes.Buffer) error { return RenderCSV(w, nil) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tt.render(&buf); err != nil {
				t.Fatalf("render error = %v", err)
			}
			assertGolden(t, tt.golden, buf.Bytes())
		})
	}
}

func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("update %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run `go test ./internal/stats -update`)", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("output does not match %s\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

// TestRenderJSONIsStable guards the documented field set: every name in
// Fields must appear as a JSON key, and nothing else may.
func TestRenderJSONFieldNames(t *testing.T) {
	rows, total := fixtureRows()
	var buf bytes.Buffer
	if err := RenderJSON(&buf, ByModel, rows, total); err != nil {
		t.Fatalf("RenderJSON() error = %v", err)
	}
	var doc struct {
		By    string                     `json:"by"`
		Rows  []map[string]any           `json:"rows"`
		Total map[string]json.RawMessage `json:"total"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if doc.By != string(ByModel) {
		t.Errorf(`"by" = %q, want %q`, doc.By, ByModel)
	}
	if len(doc.Rows) != len(rows) {
		t.Fatalf("got %d rows, want %d", len(doc.Rows), len(rows))
	}
	for i, got := range doc.Rows {
		if len(got) != len(Fields) {
			t.Errorf("row %d has %d fields, want %d: %v", i, len(got), len(Fields), got)
		}
		for _, f := range Fields {
			if _, ok := got[f]; !ok {
				t.Errorf("row %d missing documented field %q", i, f)
			}
		}
	}
	if len(doc.Total) != len(Fields) {
		t.Errorf("total has %d fields, want %d", len(doc.Total), len(Fields))
	}
}

// TestCacheHitRateOmitted pins the "undefined, not zero" contract for a
// group that never saw an input token.
func TestCacheHitRateUndefined(t *testing.T) {
	rows := []Row{{Key: "(unknown)", Requests: 1, Unknown: 1, latencies: []int64{40}}}

	var jsonBuf bytes.Buffer
	if err := RenderJSON(&jsonBuf, ByModel, rows, rows[0]); err != nil {
		t.Fatalf("RenderJSON() error = %v", err)
	}
	var doc struct {
		Rows []struct {
			CacheHitRate *float64 `json:"cache_hit_rate"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(jsonBuf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Rows[0].CacheHitRate != nil {
		t.Errorf("cache_hit_rate = %v, want null", *doc.Rows[0].CacheHitRate)
	}

	var csvBuf bytes.Buffer
	if err := RenderCSV(&csvBuf, rows); err != nil {
		t.Fatalf("RenderCSV() error = %v", err)
	}
	if got := csvBuf.String(); !bytes.HasSuffix(bytes.TrimRight(csvBuf.Bytes(), "\n"), []byte(",")) {
		t.Errorf("csv hit-rate field should be empty, got:\n%s", got)
	}
}

// TestCSVQuotesSeparators makes sure a group key containing a comma (a
// project label is free-form) round-trips instead of shifting columns.
func TestCSVEscapesKeys(t *testing.T) {
	rows := []Row{{Key: `a,b "c"`, Requests: 1, latencies: []int64{1}}}
	var buf bytes.Buffer
	if err := RenderCSV(&buf, rows); err != nil {
		t.Fatalf("RenderCSV() error = %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"a,b ""c"""`)) {
		t.Errorf("key not CSV-escaped:\n%s", buf.String())
	}
}
